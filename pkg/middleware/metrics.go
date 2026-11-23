package middleware

import (
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

var (
	requestsTotal uint64
	requests2xx   uint64
	requests4xx   uint64
	requests5xx   uint64
)

func MetricsCollector() gin.HandlerFunc {
	return func(c *gin.Context) {
		atomic.AddUint64(&requestsTotal, 1)
		c.Next()
		switch {
		case c.Writer.Status() >= 500:
			atomic.AddUint64(&requests5xx, 1)
		case c.Writer.Status() >= 400:
			atomic.AddUint64(&requests4xx, 1)
		default:
			atomic.AddUint64(&requests2xx, 1)
		}
	}
}

func MetricsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(200, gin.H{
			"requests_total": atomic.LoadUint64(&requestsTotal),
			"requests_2xx":   atomic.LoadUint64(&requests2xx),
			"requests_4xx":   atomic.LoadUint64(&requests4xx),
			"requests_5xx":   atomic.LoadUint64(&requests5xx),
		})
	}
}
