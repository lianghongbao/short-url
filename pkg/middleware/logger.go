package middleware

import (
	"fmt"
	"runtime"
	"time"

	"short-url/pkg/logger"

	"github.com/sony/sonyflake"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var sf = sonyflake.NewSonyflake(sonyflake.Settings{
	StartTime: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
})

/*
GinLogger 请求日志中间件
*/
func GinLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery
		requestID := c.GetHeader("X-Request-ID")

		// 客户端信息
		clientIP := c.ClientIP()
		userAgent := c.Request.UserAgent()
		referer := c.Request.Referer()
		accept := c.GetHeader("Accept")
		acceptLanguage := c.GetHeader("Accept-Language")
		acceptEncoding := c.GetHeader("Accept-Encoding")
		contentType := c.GetHeader("Content-Type")

		host := c.Request.Host
		method := c.Request.Method

		c.Next()

		durationMs := time.Since(start).Milliseconds()

		// 获取状态码
		status := c.Writer.Status()

		// 获取请求ID (如果中间件没设置，从 context 获取)
		if requestID == "" {
			if rid, exists := c.Get("request_id"); exists {
				requestID = rid.(string)
			}
		}

		sessionID := getSessionID(c)

		// 构建详细日志字段
		fields := []zap.Field{
			// 请求基本信息
			zap.String("request_id", requestID),
			zap.String("session_id", sessionID),
			zap.String("method", method),
			zap.String("path", path),
			zap.String("query", query),
			zap.Int("status", status),
			zap.Int64("latency_ms", durationMs),

			// 服务器信息
			zap.String("server_host", host),
			zap.Int("server_goroutines", runtime.NumGoroutine()),
			zap.Int("server_cpu", runtime.NumCPU()),

			// 客户端信息
			zap.String("client_ip", clientIP),
			zap.String("client_user_agent", userAgent),
			zap.String("client_referer", referer),
			zap.String("client_accept", accept),
			zap.String("client_accept_language", acceptLanguage),
			zap.String("client_accept_encoding", acceptEncoding),
			zap.String("client_content_type", contentType),

			// 响应信息
			zap.Int("response_size", c.Writer.Size()),
			zap.String("response_type", c.Writer.Header().Get("Content-Type")),
		}

		// 如果有gin错误，记录错误信息
		if len(c.Errors) > 0 {
			fields = append(fields, zap.String("gin_errors", c.Errors.String()))
		}

		// 根据状态码选择日志级别
		switch {
		case status >= 500:
			logger.Error("请求服务端错误", fields...)
		case status >= 400:
			logger.Warn("请求客户端错误", fields...)
		case status >= 300:
			logger.Info("请求重定向", fields...)
		default:
			logger.Info("请求成功", fields...)
		}
	}
}

/*
RequestIDMiddleware 请求ID中间件
为每个请求生成唯一的请求ID，便于链路追踪
*/
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从请求头获取请求ID，如果没有则生成
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}

		// 设置到 context 和响应头
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)

		c.Next()
	}
}

/*
generateRequestID 生成唯一请求ID
使用雪花算法（Sonyflake）生成 64 位唯一 ID
*/
func generateRequestID() string {
	id, err := sf.NextID()
	if err != nil {
		return time.Now().Format("20060102150405.000000")
	}
	return fmt.Sprintf("%d", id)
}

/*
getSessionID 获取会话ID
从多个来源按优先级获取: Authorization -> X-Session-ID -> Cookie:session_id
*/
func getSessionID(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if auth != "" {
		return auth
	}

	sessionID := c.GetHeader("X-Session-ID")
	if sessionID != "" {
		return sessionID
	}

	sessionID, _ = c.Cookie("session_id")
	if sessionID != "" {
		return sessionID
	}

	return ""
}
