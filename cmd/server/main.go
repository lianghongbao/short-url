package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	_ "time/tzdata"

	"short-url/internal/config"
	"short-url/internal/handler"
	"short-url/internal/idgen"
	"short-url/internal/repository"
	"short-url/internal/service"
	"short-url/pkg/logger"
	"short-url/pkg/middleware"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	configPath := flag.String("config", "config/config.yaml", "配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Init(logger.LogConfig{Level: "info", Filename: "logs/app.log", MaxSize: 10, MaxBackups: 3, MaxAge: 7})
		logger.Fatal("加载配置失败", logger.WithError(err))
	}

	logger.Init(logger.LogConfig{
		Level:      cfg.Log.Level,
		Filename:   cfg.Log.Filename,
		MaxSize:    cfg.Log.MaxSize,
		MaxBackups: cfg.Log.MaxBackups,
		MaxAge:     cfg.Log.MaxAge,
		Compress:   cfg.Log.Compress,
		Console:    cfg.App.Mode != "release",
	})

	logger.Info("启动短链服务",
		logger.WithField("日志级别", cfg.Log.Level),
		logger.WithField("运行模式", cfg.App.Mode),
	)

	// 设置MongoDB连接超时
	ctx, cancel := context.WithTimeout(context.Background(), cfg.MongoDB.Timeout)
	defer cancel()

	// 初始化MongoDB连接
	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoDB.URI))
	if err != nil {
		logger.Fatal("连接 MongoDB 失败", logger.WithError(err))
	}
	defer func() {
		if err := mongoClient.Disconnect(context.Background()); err != nil {
			logger.Error("MongoDB 断开连接失败", logger.WithError(err))
		}
	}()

	if err := mongoClient.Ping(ctx, nil); err != nil {
		logger.Fatal("MongoDB Ping 失败", logger.WithError(err))
	}
	logger.Info("已连接 MongoDB")

	// 获取 MongoDB 短链数据、序列生成器集合
	mongoColl := mongoClient.Database(cfg.MongoDB.Database).Collection(cfg.MongoDB.Collection)
	mongoSeqColl := mongoClient.Database(cfg.MongoDB.Database).Collection(cfg.MongoDB.SequenceCollection)

	// 初始化 Redis 连接（根据配置选择单机或集群模式）
	redisClient := newRedisClient(cfg)

	if _, err := redisClient.Ping(ctx).Result(); err != nil {
		logger.Fatal("连接 Redis 失败", logger.WithError(err))
	}
	logger.Info("已连接 Redis")
	defer redisClient.Close()

	// 初始化仓库、服务和处理器
	mongoRepo := repository.NewMongoRepository(mongoColl)
	if err := mongoRepo.EnsureIndexes(ctx); err != nil {
		logger.Fatal("创建索引失败", logger.WithError(err))
	}

	// 初始化 Redis 仓库
	redisRepo := repository.NewRedisRepository(
		redisClient,
		cfg.Cache.ShortTTL,
		cfg.Cache.EmptyTTL,
	)

	// 初始化 ID 生成器，使用可取消的 context 控制后台 goroutine 生命周期
	idGenCtx, idGenCancel := context.WithCancel(context.Background())
	defer idGenCancel()

	idGenerator := idgen.NewGenerator(
		mongoSeqColl,
		cfg.IDGen.BatchSize,
		cfg.IDGen.Mask,
		cfg.IDGen.PrefetchThreshold,
	)

	if err := idGenerator.Start(idGenCtx); err != nil {
		logger.Fatal("启动 ID 生成器失败", logger.WithError(err))
	}
	logger.Info("ID 生成器已启动")

	if cfg.App.BaseURL == "" {
		cfg.App.BaseURL = "http://" + cfg.App.Addr()
	}
	baseURL := strings.TrimRight(cfg.App.BaseURL, "/")
	// 初始化短链服务
	shortenSvc := service.NewShortenService(mongoRepo, redisRepo, idGenerator, baseURL)
	// 统计服务
	statSvc := service.NewStatService(mongoRepo, redisRepo)
	// ID号段查询服务
	idSegmentSvc := service.NewIDSegmentService(mongoSeqColl, redisRepo, cfg.IDGen.BatchSize, cfg.Cache.IDSegmentTTL)
	// 初始化 HTTP 处理器
	h := handler.NewHandler(shortenSvc, statSvc, idSegmentSvc)

	// 设置健康检查：检查 MongoDB 和 Redis 连接
	h.SetHealthCheck(func(ctx context.Context) error {
		if err := mongoClient.Ping(ctx, nil); err != nil {
			return fmt.Errorf("MongoDB 健康检查失败: %w", err)
		}
		if _, err := redisClient.Ping(ctx).Result(); err != nil {
			return fmt.Errorf("Redis 健康检查失败: %w", err)
		}
		return nil
	})
	// 标记服务已就绪
	h.SetReady()

	// 设置 Gin 模式
	gin.SetMode(cfg.App.Mode)

	// 创建 Gin 引擎
	r := gin.New()

	// 使用自定义中间件: 请求日志 + 请求ID + 指标收集
	r.Use(middleware.RequestIDMiddleware())
	r.Use(middleware.GinLogger())
	r.Use(middleware.MetricsCollector())
	r.Use(gin.Recovery())

	h.RegisterRoutes(r)
	r.GET("/metrics", middleware.MetricsHandler())

	srv := &http.Server{
		Addr:    cfg.App.Addr(),
		Handler: r,
	}

	go func() {
		logger.Info("服务监听中", logger.WithField("地址", cfg.App.Addr()))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("监听失败", logger.WithError(err))
		}
	}()

	// 优雅退出
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("正在关闭服务...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Fatal("服务关闭失败", logger.WithError(err))
	}

	// 刷新日志
	logger.Sync()
	logger.Info("服务已退出")
}

// newRedisClient 根据配置创建 Redis 客户端（单机或集群模式）
func newRedisClient(cfg *config.Config) redis.UniversalClient {
	if cfg.Redis.Mode == "cluster" {
		logger.Info("使用 Redis 集群模式",
			logger.WithField("addrs", cfg.Redis.Addrs),
		)
		return redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:    cfg.Redis.Addrs,
			Password: cfg.Redis.Password,
			PoolSize: cfg.Redis.PoolSize,
		})
	}

	logger.Info("使用 Redis 单机模式",
		logger.WithField("addr", cfg.Redis.Addr),
	)
	return redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		PoolSize: cfg.Redis.PoolSize,
	})
}
