package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"short-url/internal/codec"
	"short-url/internal/idgen"
	"short-url/internal/model"
	"short-url/internal/repository"
	"short-url/pkg/logger"
)

var (
	ErrInvalidURL  = errors.New("无效的URL")
	ErrURLNotExist = errors.New("短链不存在")
	ErrURLDisabled = errors.New("短链已被禁用")
)

const (
	lockTTL       = 5 * time.Second
	lockKeyPrefix = "lock:shorten:"
)

type ShortenService struct {
	mongo   *repository.MongoRepository
	redis   *repository.RedisRepository
	idGen   *idgen.Generator
	baseURL string
}

func NewShortenService(
	mongo *repository.MongoRepository,
	redis *repository.RedisRepository,
	idGen *idgen.Generator,
	baseURL string,
) *ShortenService {
	return &ShortenService{
		mongo:   mongo,
		redis:   redis,
		idGen:   idGen,
		baseURL: baseURL,
	}
}

// cacheURL 把短链写入 Redis 缓存：短码索引 + 长链反查索引
func (s *ShortenService) cacheURL(ctx context.Context, code, longURL string, status int) *model.CachedURL {
	cached := &model.CachedURL{ShortCode: code, LongURL: longURL, Status: status}
	_ = s.redis.SetShort(ctx, cached)
	_ = s.redis.SetByLongURL(ctx, cached)
	return cached
}

// Shorten 生成短链
func (s *ShortenService) Shorten(ctx context.Context, longURL string) (*model.CachedURL, error) {
	// 验证URL格式
	parsed, err := url.Parse(longURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, ErrInvalidURL
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, ErrInvalidURL
	}

	// 查缓存
	if cached, err := s.redis.GetByLongURL(ctx, longURL); err == nil && cached != nil {
		return cached, nil
	}

	// 查库
	existing, err := s.mongo.FindByLongURL(ctx, longURL)
	if err == nil && existing != nil {
		// 禁用或过期
		if existing.Status != model.StatusNormal {
			return nil, ErrURLDisabled
		}
		// 写缓存
		return s.cacheURL(ctx, existing.ShortCode, existing.LongURL, existing.Status), nil
	}
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		logger.Error("查询已存在URL失败", logger.String("longURL", longURL), logger.WithError(err))
		return nil, fmt.Errorf("检查已存在URL失败: %w", err)
	}

	// 获取锁（用哈希避免长URL导致Redis key过长）
	lockKey := lockKeyPrefix + repository.HashKey(longURL)
	token, acquired, err := s.redis.TryLock(ctx, lockKey, lockTTL)
	if err != nil {
		logger.Error("获取锁失败", logger.String("longURL", longURL), logger.WithError(err))
	}

	// 拿不到锁，轮询等待结果
	if !acquired {
		if cached := s.waitForCache(ctx, longURL); cached != nil {
			return cached, nil
		}
		token, acquired, err = s.redis.TryLock(ctx, lockKey, lockTTL)
		if err != nil {
			logger.Error("重试获取锁失败", logger.String("longURL", longURL), logger.WithError(err))
		}
		if !acquired {
			return nil, fmt.Errorf("创建短链超时，请重试")
		}
	}

	defer func() {
		_ = s.redis.Unlock(ctx, lockKey, token)
	}()

	// 获取锁后再次检查，防止并发下重复创建同一条短链
	existing, err = s.mongo.FindByLongURL(ctx, longURL)
	if err == nil && existing != nil {
		if existing.Status != model.StatusNormal {
			return nil, ErrURLDisabled
		}
		return s.cacheURL(ctx, existing.ShortCode, existing.LongURL, existing.Status), nil
	}
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		logger.Error("查询已存在URL失败", logger.String("longURL", longURL), logger.WithError(err))
		return nil, fmt.Errorf("检查已存在URL失败: %w", err)
	}

	// 生成ID
	rawID, xorID, err := s.idGen.NextRawAndXor(ctx)
	if err != nil {
		logger.Error("生成ID失败", logger.String("longURL", longURL), logger.WithError(err))
		return nil, fmt.Errorf("生成自增ID失败: %w", err)
	}

	shortCode := codec.Encode(xorID)

	// 插入数据库
	urlModel := &model.URL{
		AutoID:    rawID,
		XorID:     xorID,
		ShortCode: shortCode,
		LongURL:   longURL,
	}

	if err := s.mongo.Create(ctx, urlModel); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			if byLong, err := s.mongo.FindByLongURL(ctx, longURL); err == nil && byLong != nil {
				return s.cacheURL(ctx, byLong.ShortCode, byLong.LongURL, byLong.Status), nil
			}
			if byCode, err := s.mongo.FindByCode(ctx, shortCode); err == nil && byCode != nil {
				return s.cacheURL(ctx, byCode.ShortCode, byCode.LongURL, byCode.Status), nil
			}
		}

		logger.Error("创建短链失败",
			logger.String("longURL", longURL),
			logger.String("shortCode", shortCode),
			logger.Int64("rawID", rawID),
			logger.WithError(err),
		)
		return nil, fmt.Errorf("创建短链记录失败: %w", err)
	}

	// 写入缓存
	return s.cacheURL(ctx, shortCode, longURL, model.StatusNormal), nil
}

// waitForCache 轮询等待写入缓存结果
func (s *ShortenService) waitForCache(ctx context.Context, longURL string) *model.CachedURL {
	for i := 0; i < 10; i++ {
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if cached, err := s.redis.GetByLongURL(ctx, longURL); err == nil && cached != nil {
			return cached
		}
	}
	return nil
}

// BuildShortURL 构建完整短链URL
func (s *ShortenService) BuildShortURL(code string) string {
	return s.baseURL + "/" + code
}
