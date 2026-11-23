package model

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// URL 是 short_urls 集合的文档结构
type URL struct {
	ID          primitive.ObjectID `bson:"_id,omitempty"`       // MongoDB 主键
	AutoID      int64              `bson:"auto_id"`             // 全局自增ID，62进制编码来源
	XorID       int64              `bson:"xor_id"`              // AutoID异或掩码后的ID，编码用
	ShortCode   string             `bson:"short_code"`          // 5位短链码，唯一索引
	LongURL     string             `bson:"long_url"`            // 原始长链接，唯一索引
	CreatedAt   time.Time          `bson:"created_at"`          // 创建时间
	ClickCount  int64              `bson:"click_count"`         // 累计点击次数
	LastVisited time.Time          `bson:"last_visited"`        // 最后访问时间
	ExpireAt    *time.Time         `bson:"expire_at,omitempty"` // 过期时间
	Status      int                `bson:"status"`              // 状态：0 正常，1 禁用，2 已过期
}

// CachedURL Redis缓存结构
type CachedURL struct {
	ShortCode string `json:"short_code"` // 短链码
	LongURL   string `json:"long_url"`   // 长链接
	Status    int    `json:"status"`     // 状态
}

// 短链状态。服务只写入正常状态，禁用和过期由外部在数据库里标记
const (
	StatusNormal  = 0 // 正常
	StatusDisable = 1 // 禁用
	StatusExpired = 2 // 已过期
)
