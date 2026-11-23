package idgen

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"short-url/internal/codec"
	"short-url/pkg/logger"
)

type Generator struct {
	mongoColl         *mongo.Collection
	batchSize         int64
	mask              int64
	prefetchThreshold int64

	ch      chan int64
	mu      sync.Mutex
	started bool
}

// NewGenerator 创建一个新的ID生成器实例
func NewGenerator(mongoColl *mongo.Collection, batchSize, mask, prefetchThreshold int64) *Generator {
	return &Generator{
		mongoColl:         mongoColl,
		batchSize:         batchSize,
		mask:              mask,
		prefetchThreshold: prefetchThreshold,
		ch:                make(chan int64, batchSize),
	}
}

// Start 启动ID生成器，预取第一批ID并在后台定期检查和预取新ID
func (g *Generator) Start(ctx context.Context) error {
	g.mu.Lock()
	if g.started {
		g.mu.Unlock()
		return nil
	}
	g.started = true
	g.mu.Unlock()

	if err := g.prefetch(ctx); err != nil {
		return err
	}

	go g.backgroundPrefetch(ctx)
	return nil
}

// Next 返回下一个ID，阻塞直到有ID可用或上下文超时
func (g *Generator) Next(ctx context.Context) (int64, error) {
	select {
	case id := <-g.ch:
		return id, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// NextRawAndXor 一次性获取原始ID和异或后ID，避免两次取号导致原始ID与编码用ID不匹配
func (g *Generator) NextRawAndXor(ctx context.Context) (rawID, xorID int64, err error) {
	id, err := g.Next(ctx)
	if err != nil {
		return 0, 0, err
	}
	return id, g.xorMask(id), nil
}

// prefetch 从MongoDB预取一批ID，并放入通道
func (g *Generator) prefetch(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	filter := bson.M{"_id": "global_seq"}
	update := bson.M{"$inc": bson.M{"max": g.batchSize}}
	// SetUpsert: 序列文档不存在时创建；SetReturnDocument: 返回更新后的文档
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)

	var result struct {
		Max int64 `bson:"max"`
	}
	err := g.mongoColl.FindOneAndUpdate(ctx, filter, update, opts).Decode(&result)
	if err != nil {
		return err
	}

	startID := result.Max - g.batchSize + 1

	// ID 超出安全范围检查，确保不会生成无法编码的 ID
	if startID > codec.MaxCodeValue || startID+g.batchSize-1 > codec.MaxCodeValue {
		logger.Error("ID耗尽",
			logger.Int64("startID", startID),
			logger.Int64("batchSize", g.batchSize),
			logger.Int64("maxCodeValue", codec.MaxCodeValue),
		)
		return errors.New("ID耗尽: 序列已超过最大编码值，需要重置")
	}

	for i := int64(0); i < g.batchSize; i++ {
		g.ch <- startID + i
	}

	return nil
}

// backgroundPrefetch 在后台定期检查通道中的ID数量，低于阈值时触发预取
func (g *Generator) backgroundPrefetch(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if len(g.ch) < int(g.prefetchThreshold) {
				if err := g.prefetch(ctx); err != nil {
					logger.Error("后台预取ID失败", logger.WithError(err))
				}
			}
		}
	}
}

// maskValue 计算异或掩码，取模保证异或结果落在可编码范围内
func (g *Generator) maskValue() int64 {
	return g.mask % codec.MaxCodeValue
}

// xorMask 对ID进行异或掩码处理，增加ID的随机性和安全性
func (g *Generator) xorMask(id int64) int64 {
	return id ^ g.maskValue()
}

// UnXorMask 还原异或前的原始ID
func (g *Generator) UnXorMask(xoredID int64) int64 {
	return xoredID ^ g.maskValue()
}
