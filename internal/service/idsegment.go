package service

import (
	"context"
	"fmt"
	"time"

	"short-url/internal/codec"
	"short-url/internal/repository"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type IDSegmentService struct {
	mongoSeqColl *mongo.Collection
	redis        *repository.RedisRepository
	batchSize    int64
	cacheTTL     time.Duration
}

func NewIDSegmentService(
	mongoSeqColl *mongo.Collection,
	redis *repository.RedisRepository,
	batchSize int64,
	cacheTTL time.Duration,
) *IDSegmentService {
	return &IDSegmentService{
		mongoSeqColl: mongoSeqColl,
		redis:        redis,
		batchSize:    batchSize,
		cacheTTL:     cacheTTL,
	}
}

func (s *IDSegmentService) GetSegmentInfo(ctx context.Context) (*repository.IDSegmentInfo, error) {
	if cached, err := s.redis.GetIDSegment(ctx); err == nil && cached != nil {
		return cached, nil
	}

	info, err := s.fetchFromMongo(ctx)
	if err != nil {
		return nil, err
	}

	_ = s.redis.SetIDSegment(ctx, info, s.cacheTTL)
	return info, nil
}

func (s *IDSegmentService) fetchFromMongo(ctx context.Context) (*repository.IDSegmentInfo, error) {
	var result struct {
		Max int64 `bson:"max"`
	}
	err := s.mongoSeqColl.FindOne(ctx, bson.M{"_id": "global_seq"}).Decode(&result)
	if err == mongo.ErrNoDocuments {
		return &repository.IDSegmentInfo{
			CurrentMax:   0,
			BatchSize:    s.batchSize,
			MaxCodeValue: codec.MaxCodeValue,
			Remaining:    codec.MaxCodeValue,
			UsagePercent: 0,
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询ID号段失败: %w", err)
	}

	maxCode := codec.MaxCodeValue
	remaining := maxCode - result.Max
	usagePercent := float64(result.Max) / float64(maxCode) * 100

	return &repository.IDSegmentInfo{
		CurrentMax:   result.Max,
		BatchSize:    s.batchSize,
		MaxCodeValue: maxCode,
		Remaining:    remaining,
		UsagePercent: usagePercent,
	}, nil
}
