package service

import (
	"context"
	"errors"
	"fmt"

	"short-url/internal/model"
	"short-url/internal/repository"
)

type StatService struct {
	mongo *repository.MongoRepository
	redis *repository.RedisRepository
}

func NewStatService(
	mongo *repository.MongoRepository,
	redis *repository.RedisRepository,
) *StatService {
	return &StatService{
		mongo: mongo,
		redis: redis,
	}
}

// GetByCode 根据短链code获取URL信息
func (s *StatService) GetByCode(ctx context.Context, code string) (*model.URL, error) {
	if cached, err := s.redis.GetURL(ctx, code); err == nil && cached != nil {
		return cached, nil
	}

	if empty, _ := s.redis.IsEmpty(ctx, code); empty {
		return nil, repository.ErrNotFound
	}

	urlModel, err := s.mongo.FindByCode(ctx, code)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			_ = s.redis.SetEmpty(ctx, code)
		}
		return nil, err
	}

	redisStat, _ := s.redis.GetClickCount(ctx, code)
	urlModel.ClickCount += redisStat

	_ = s.redis.SetURL(ctx, urlModel)
	return urlModel, nil
}

// Redirect 根据短链code获取原始URL，并增加点击统计
func (s *StatService) Redirect(ctx context.Context, code string) (string, error) {
	cached, err := s.redis.GetShort(ctx, code)
	if err == nil && cached != nil {
		if cached.Status != model.StatusNormal {
			return "", ErrURLDisabled
		}
		s.redis.IncrClick(ctx, code)
		return cached.LongURL, nil
	}

	if empty, _ := s.redis.IsEmpty(ctx, code); empty {
		return "", repository.ErrNotFound
	}

	urlModel, err := s.mongo.FindByCode(ctx, code)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			_ = s.redis.SetEmpty(ctx, code)
			return "", repository.ErrNotFound
		}
		return "", fmt.Errorf("查询短链码失败: %w", err)
	}

	if urlModel.Status != model.StatusNormal {
		return "", ErrURLDisabled
	}

	cached = &model.CachedURL{
		ShortCode: urlModel.ShortCode,
		LongURL:   urlModel.LongURL,
		Status:    urlModel.Status,
	}
	_ = s.redis.SetShort(ctx, cached)

	s.redis.IncrClick(ctx, code)

	return urlModel.LongURL, nil
}
