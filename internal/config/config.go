package config

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	App     AppConfig     `mapstructure:"app"`
	MongoDB MongoDBConfig `mapstructure:"mongodb"`
	Redis   RedisConfig   `mapstructure:"redis"`
	IDGen   IDGenConfig   `mapstructure:"idgen"`
	Cache   CacheConfig   `mapstructure:"cache"`
	Log     LogConfig     `mapstructure:"log"`
}

type AppConfig struct {
	Host            string        `mapstructure:"host"`
	Port            int           `mapstructure:"port"`
	Mode            string        `mapstructure:"mode"`
	BaseURL         string        `mapstructure:"base_url"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

type LogConfig struct {
	Level      string `mapstructure:"level"`
	Filename   string `mapstructure:"filename"`
	MaxSize    int    `mapstructure:"max_size"`
	MaxBackups int    `mapstructure:"max_backups"`
	MaxAge     int    `mapstructure:"max_age"`
	Compress   bool   `mapstructure:"compress"`
}

type MongoDBConfig struct {
	URI                string        `mapstructure:"uri"`
	Database           string        `mapstructure:"database"`
	Collection         string        `mapstructure:"collection"`
	SequenceCollection string        `mapstructure:"sequence_collection"`
	Timeout            time.Duration `mapstructure:"timeout"`
}

type RedisConfig struct {
	Mode     string   `mapstructure:"mode"`  // "single" 或 "cluster"
	Addr     string   `mapstructure:"addr"`  // 单机地址，仅 mode=single 有效
	Addrs    []string `mapstructure:"addrs"` // 集群地址列表，仅 mode=cluster 有效
	Password string   `mapstructure:"password"`
	DB       int      `mapstructure:"db"` // 仅 mode=single 有效，集群模式不支持多 DB
	PoolSize int      `mapstructure:"pool_size"`
}

type IDGenConfig struct {
	BatchSize         int64 `mapstructure:"batch_size"`
	Mask              int64 `mapstructure:"mask"`
	PrefetchThreshold int64 `mapstructure:"prefetch_threshold"`
}

type CacheConfig struct {
	ShortTTL     time.Duration `mapstructure:"short_ttl"`
	EmptyTTL     time.Duration `mapstructure:"empty_ttl"`
	IDSegmentTTL time.Duration `mapstructure:"id_segment_ttl"`
}

func Load(path string) (*Config, error) {
	viper.SetConfigFile(path)
	viper.SetConfigType("yaml")

	// 环境变量自动映射到配置（优先级高于配置文件）
	bindEnvVars()

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}

	// 环境变量再次覆盖，确保生产环境优先级最高
	overrideFromEnv(&cfg)

	return &cfg, nil
}

// bindEnvVars 将环境变量绑定到配置路径（使用 . 作为分隔符）
func bindEnvVars() {
	// MongoDB
	viper.BindEnv("mongodb.uri", "MONGODB_URI")
	viper.BindEnv("mongodb.database", "MONGODB_DATABASE")
	viper.BindEnv("mongodb.collection", "MONGODB_COLLECTION")

	// Redis
	viper.BindEnv("redis.addr", "REDIS_ADDR")
	viper.BindEnv("redis.password", "REDIS_PASSWORD")
	viper.BindEnv("redis.db", "REDIS_DB")

	// App
	viper.BindEnv("app.base_url", "APP_BASE_URL")
}

// overrideFromEnv 环境变量直接覆盖配置值（兜底，确保最高优先级）
func overrideFromEnv(cfg *Config) {
	if uri := os.Getenv("MONGODB_URI"); uri != "" {
		cfg.MongoDB.URI = uri
	}
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		cfg.Redis.Addr = addr
	}
	if pwd := os.Getenv("REDIS_PASSWORD"); pwd != "" {
		cfg.Redis.Password = pwd
	}
}

func (c *AppConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}
