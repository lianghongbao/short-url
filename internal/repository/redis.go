package repository

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"short-url/internal/model"

	"github.com/redis/go-redis/v9"
)

const (
	shortKeyPrefix   = "short:"
	longURLKeyPrefix = "longurl:"
	urlKeyPrefix     = "url:"
	emptyKeyPrefix   = "empty:"
	idSegmentKey     = "id_segment:info"
)

// 点击计数相关的 key 用 {} 包住，保证集群模式下落在同一个 slot
const (
	clickCountersKey = "{click}:counters"
	clickPendingKey  = "{click}:pending"
)

type RedisRepository struct {
	client   redis.UniversalClient
	shortTTL time.Duration
	emptyTTL time.Duration
}

func NewRedisRepository(client redis.UniversalClient, shortTTL, emptyTTL time.Duration) *RedisRepository {
	return &RedisRepository{
		client:   client,
		shortTTL: shortTTL,
		emptyTTL: emptyTTL,
	}
}

// HashKey 计算字符串的哈希，用作 Redis key 的一部分
func HashKey(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// cacheTTL 缓存过期时间 = 基础 TTL + 0~1 小时的随机偏移
// 随机偏移用于打散过期时间，避免大量 key 同时失效造成缓存雪崩
func (r *RedisRepository) cacheTTL() time.Duration {
	return r.shortTTL + time.Duration(rand.Intn(3600))*time.Second
}

func (r *RedisRepository) GetShort(ctx context.Context, code string) (*model.CachedURL, error) {
	key := shortKeyPrefix + code
	data, err := r.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis查询缓存失败: %w", err)
	}

	var url model.CachedURL
	if err := json.Unmarshal(data, &url); err != nil {
		return nil, fmt.Errorf("缓存数据解析失败: %w", err)
	}
	return &url, nil
}

func (r *RedisRepository) SetShort(ctx context.Context, url *model.CachedURL) error {
	key := shortKeyPrefix + url.ShortCode
	data, err := json.Marshal(url)
	if err != nil {
		return fmt.Errorf("缓存数据序列化失败: %w", err)
	}

	return r.client.Set(ctx, key, data, r.cacheTTL()).Err()
}

func (r *RedisRepository) GetByLongURL(ctx context.Context, longURL string) (*model.CachedURL, error) {
	key := longURLKeyPrefix + HashKey(longURL)
	data, err := r.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis长链接缓存查询失败: %w", err)
	}

	var url model.CachedURL
	if err := json.Unmarshal(data, &url); err != nil {
		return nil, fmt.Errorf("长链接缓存数据解析失败: %w", err)
	}
	return &url, nil
}

func (r *RedisRepository) SetByLongURL(ctx context.Context, url *model.CachedURL) error {
	key := longURLKeyPrefix + HashKey(url.LongURL)
	data, err := json.Marshal(url)
	if err != nil {
		return fmt.Errorf("长链接缓存数据序列化失败: %w", err)
	}

	return r.client.Set(ctx, key, data, r.cacheTTL()).Err()
}

func (r *RedisRepository) SetEmpty(ctx context.Context, key string) error {
	return r.client.Set(ctx, emptyKeyPrefix+key, "1", r.emptyTTL).Err()
}

func (r *RedisRepository) IsEmpty(ctx context.Context, key string) (bool, error) {
	exists, err := r.client.Exists(ctx, emptyKeyPrefix+key).Result()
	return exists > 0, err
}

var incrClickScript = redis.NewScript(`
	redis.call("HINCRBY", KEYS[1], ARGV[1], 1)
	redis.call("SADD", KEYS[2], ARGV[1])
	return 1
`)

// IncrClick 原子增加点击计数并标记 code 待同步
func (r *RedisRepository) IncrClick(ctx context.Context, code string) error {
	return incrClickScript.Run(ctx, r.client, []string{clickCountersKey, clickPendingKey}, code).Err()
}

// GetClickCount 获取指定 code 的当前点击计数（未同步部分）
func (r *RedisRepository) GetClickCount(ctx context.Context, code string) (int64, error) {
	val, err := r.client.HGet(ctx, clickCountersKey, code).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return val, err
}

// InvalidateCachedURL 删除 URL 缓存，下次统计查询时从 MongoDB 重新加载
func (r *RedisRepository) InvalidateCachedURL(ctx context.Context, code string) error {
	return r.client.Del(ctx, urlKeyPrefix+code).Err()
}

// StatEntry 单条同步记录
type StatEntry struct {
	Code  string
	Count int64
}

var popPendingScript = redis.NewScript(`
	local codes = redis.call("SPOP", KEYS[1], ARGV[1])
	local result = {}
	for i, code in ipairs(codes) do
		result[i * 2 - 1] = code
		local count = redis.call("HGET", KEYS[2], code)
		redis.call("HDEL", KEYS[2], code)
		result[i * 2] = tonumber(count) or 0
	end
	return result
`)

// PopPendingStats 原子地弹出最多 count 个待同步 code 并取出其计数
func (r *RedisRepository) PopPendingStats(ctx context.Context, count int64) ([]StatEntry, error) {
	result, err := popPendingScript.Run(ctx, r.client, []string{clickPendingKey, clickCountersKey}, count).Result()
	if err != nil {
		return nil, fmt.Errorf("弹出待同步统计数据失败: %w", err)
	}
	elems, ok := result.([]interface{})
	if !ok || len(elems) == 0 {
		return nil, nil
	}
	entries := make([]StatEntry, 0, len(elems)/2)
	for i := 0; i+1 < len(elems); i += 2 {
		code := elems[i].(string)
		count, _ := elems[i+1].(int64)
		entries = append(entries, StatEntry{Code: code, Count: count})
	}
	return entries, nil
}

var rollbackStatScript = redis.NewScript(`
	redis.call("HINCRBY", KEYS[1], ARGV[1], ARGV[2])
	redis.call("SADD", KEYS[2], ARGV[1])
`)

// RollbackStat MongoDB 写入失败时回滚计数到 Redis
func (r *RedisRepository) RollbackStat(ctx context.Context, code string, count int64) error {
	return rollbackStatScript.Run(ctx, r.client, []string{clickCountersKey, clickPendingKey}, code, count).Err()
}

func (r *RedisRepository) GetURL(ctx context.Context, code string) (*model.URL, error) {
	key := urlKeyPrefix + code
	data, err := r.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis查询URL失败: %w", err)
	}

	var url model.URL
	if err := json.Unmarshal(data, &url); err != nil {
		return nil, fmt.Errorf("URL缓存数据解析失败: %w", err)
	}
	return &url, nil
}

func (r *RedisRepository) SetURL(ctx context.Context, url *model.URL) error {
	key := urlKeyPrefix + url.ShortCode
	data, err := json.Marshal(url)
	if err != nil {
		return fmt.Errorf("URL缓存数据序列化失败: %w", err)
	}

	return r.client.Set(ctx, key, data, r.cacheTTL()).Err()
}

// TryLock 尝试获取分布式锁，返回 token 和是否获取成功。token 用于安全释放锁
func (r *RedisRepository) TryLock(ctx context.Context, key string, ttl time.Duration) (string, bool, error) {
	token := make([]byte, 16)
	if _, err := cryptorand.Read(token); err != nil {
		return "", false, fmt.Errorf("生成锁令牌失败: %w", err)
	}
	tokenStr := hex.EncodeToString(token)

	ok, err := r.client.SetNX(ctx, key, tokenStr, ttl).Result()
	if err != nil {
		return "", false, fmt.Errorf("获取分布式锁失败: %w", err)
	}
	return tokenStr, ok, nil
}

var unlockScript = redis.NewScript(`
	if redis.call("GET", KEYS[1]) == ARGV[1] then
		return redis.call("DEL", KEYS[1])
	else
		return 0
	end
`)

// Unlock 安全释放分布式锁，仅当 token 匹配时才删除
func (r *RedisRepository) Unlock(ctx context.Context, key string, token string) error {
	return unlockScript.Run(ctx, r.client, []string{key}, token).Err()
}

// IDSegmentInfo ID号段信息缓存结构
type IDSegmentInfo struct {
	CurrentMax   int64   `json:"current_max"`
	BatchSize    int64   `json:"batch_size"`
	MaxCodeValue int64   `json:"max_code_value"`
	Remaining    int64   `json:"remaining"`
	UsagePercent float64 `json:"usage_percent"`
}

// GetIDSegment 从缓存获取ID号段信息
func (r *RedisRepository) GetIDSegment(ctx context.Context) (*IDSegmentInfo, error) {
	data, err := r.client.Get(ctx, idSegmentKey).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis查询ID号段缓存失败: %w", err)
	}
	var info IDSegmentInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("ID号段缓存数据解析失败: %w", err)
	}
	return &info, nil
}

// SetIDSegment 缓存ID号段信息
func (r *RedisRepository) SetIDSegment(ctx context.Context, info *IDSegmentInfo, ttl time.Duration) error {
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("ID号段缓存数据序列化失败: %w", err)
	}
	return r.client.Set(ctx, idSegmentKey, data, ttl).Err()
}
