package main

import (
	"context"
	"flag"
	"time"
	_ "time/tzdata"

	"short-url/internal/config"
	"short-url/internal/repository"
	"short-url/pkg/logger"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	configPath := flag.String("config", "config/config.yaml", "配置文件路径")
	batchSize := flag.Int64("batch", 100, "每批次同步数量")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Init(logger.LogConfig{Level: "info", Filename: "logs/sync.log", MaxSize: 10, MaxBackups: 3, MaxAge: 7})
		logger.Fatal("加载配置失败", logger.WithError(err))
	}

	logger.Init(logger.LogConfig{
		Level:      cfg.Log.Level,
		Filename:   cfg.Log.Filename,
		MaxSize:    cfg.Log.MaxSize,
		MaxBackups: cfg.Log.MaxBackups,
		MaxAge:     cfg.Log.MaxAge,
		Compress:   cfg.Log.Compress,
		Console:    true,
	})
	defer logger.Sync()

	ctx, cancel := context.WithTimeout(context.Background(), cfg.MongoDB.Timeout)
	defer cancel()

	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoDB.URI))
	if err != nil {
		logger.Fatal("连接 MongoDB 失败", logger.WithError(err))
	}
	defer mongoClient.Disconnect(context.Background())

	if err := mongoClient.Ping(ctx, nil); err != nil {
		logger.Fatal("MongoDB Ping 失败", logger.WithError(err))
	}

	redisClient := newRedisClient(cfg)
	defer redisClient.Close()

	if _, err := redisClient.Ping(ctx).Result(); err != nil {
		logger.Fatal("连接 Redis 失败", logger.WithError(err))
	}

	mongoColl := mongoClient.Database(cfg.MongoDB.Database).Collection(cfg.MongoDB.Collection)
	mongoRepo := repository.NewMongoRepository(mongoColl)
	redisRepo := repository.NewRedisRepository(redisClient, cfg.Cache.ShortTTL, cfg.Cache.EmptyTTL)

	logger.Info("开始同步统计数据", logger.WithField("batch", *batchSize))

	syncCtx, syncCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer syncCancel()

	entries, err := redisRepo.PopPendingStats(syncCtx, *batchSize)
	if err != nil {
		logger.Fatal("读取待同步数据失败", logger.WithError(err))
	}

	if len(entries) == 0 {
		logger.Info("无待同步数据")
		return
	}

	synced := 0
	for _, e := range entries {
		if e.Count == 0 {
			continue
		}
		if err := mongoRepo.IncrClickCount(syncCtx, e.Code, e.Count); err != nil {
			logger.Error("同步点击统计失败",
				logger.String("code", e.Code),
				logger.Int64("count", e.Count),
				logger.WithError(err),
			)
			_ = redisRepo.RollbackStat(syncCtx, e.Code, e.Count)
			continue
		}
		_ = redisRepo.InvalidateCachedURL(syncCtx, e.Code)
		synced++
	}

	logger.Info("同步完成", logger.WithField("synced", synced), logger.WithField("batch", len(entries)))
}

// newRedisClient 根据配置创建 Redis 客户端（单机或集群模式）
func newRedisClient(cfg *config.Config) redis.UniversalClient {
	if cfg.Redis.Mode == "cluster" {
		return redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:    cfg.Redis.Addrs,
			Password: cfg.Redis.Password,
			PoolSize: cfg.Redis.PoolSize,
		})
	}
	return redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		PoolSize: cfg.Redis.PoolSize,
	})
}
