package logger

import (
	"os"
	"path"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	Log  *zap.Logger
	once sync.Once
)

// LogConfig 日志配置
type LogConfig struct {
	Level      string // 日志级别: debug, info, warn, error
	Filename   string // 日志文件路径
	MaxSize    int    // 单个日志文件最大大小(MB)
	MaxBackups int    // 保留的旧日志文件数量
	MaxAge     int    // 保留的旧日志文件天数
	Compress   bool   // 是否压缩旧日志
	Console    bool   // 是否同时输出到控制台（release 模式建议关闭）
}

var defaultLogConfig = LogConfig{
	Level:      "info",
	Filename:   "logs/app.log",
	MaxSize:    100,
	MaxBackups: 30,
	MaxAge:     30,
	Compress:   true,
}

// Init 初始化日志系统：文件 + 可选控制台输出
func Init(cfg ...LogConfig) {
	once.Do(func() {
		config := defaultLogConfig
		if len(cfg) > 0 {
			config = cfg[0]
		}

		level := parseLevel(config.Level)
		if level == zapcore.InvalidLevel {
			level = zapcore.InfoLevel
		}

		encoderConfig := zapcore.EncoderConfig{
			TimeKey:        "time",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.CapitalColorLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.MillisDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		}

		encoder := zapcore.NewConsoleEncoder(encoderConfig)
		writeSyncer := getWriteSyncer(config)

		core := zapcore.NewCore(encoder, writeSyncer, level)

		Log = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1), zap.AddStacktrace(zapcore.ErrorLevel))
	})
}

// getWriteSyncer 根据配置获取写入器
func getWriteSyncer(config LogConfig) zapcore.WriteSyncer {
	// 确保目录存在
	if config.Filename != "" {
		dir := path.Dir(config.Filename)
		if dir != "" {
			os.MkdirAll(dir, 0755)
		}
	}

	// 使用 lumberjack 做日志轮转
	lumberjackLogger := &lumberjack.Logger{
		Filename:   config.Filename,
		MaxSize:    config.MaxSize,
		MaxBackups: config.MaxBackups,
		MaxAge:     config.MaxAge,
		Compress:   config.Compress,
	}

	var writers []zapcore.WriteSyncer

	// 控制台输出：仅当没有文件、或明确开启 Console 时
	if config.Filename == "" || config.Console {
		writers = append(writers, zapcore.AddSync(os.Stdout))
	}

	// 文件输出
	if config.Filename != "" {
		writers = append(writers, zapcore.AddSync(lumberjackLogger))
	}

	return zapcore.NewMultiWriteSyncer(writers...)
}

// parseLevel 解析日志级别
func parseLevel(level string) zapcore.Level {
	switch level {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	case "fatal":
		return zapcore.FatalLevel
	default:
		return zapcore.InvalidLevel
	}
}

// Debug 调试日志
func Debug(msg string, fields ...zap.Field) {
	Log.Debug(msg, fields...)
}

// Info 信息日志
func Info(msg string, fields ...zap.Field) {
	Log.Info(msg, fields...)
}

// Warn 警告日志
func Warn(msg string, fields ...zap.Field) {
	Log.Warn(msg, fields...)
}

// Error 错误日志
func Error(msg string, fields ...zap.Field) {
	Log.Error(msg, fields...)
}

// Fatal 致命错误日志，调用后进程退出
func Fatal(msg string, fields ...zap.Field) {
	Log.Fatal(msg, fields...)
}

// Sync 刷新日志缓冲区
func Sync() {
	if Log != nil {
		Log.Sync()
	}
}

// WithError 添加错误字段
func WithError(err error) zap.Field {
	return zap.Error(err)
}

// WithField 添加单个字段
func WithField(key string, value interface{}) zap.Field {
	return zap.Any(key, value)
}

// String 添加字符串字段
func String(key string, val string) zap.Field {
	return zap.String(key, val)
}

// Int64 添加int64字段
func Int64(key string, val int64) zap.Field {
	return zap.Int64(key, val)
}

// Int 添加int字段
func Int(key string, val int) zap.Field {
	return zap.Int(key, val)
}
