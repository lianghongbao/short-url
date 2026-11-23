package handler

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"short-url/internal/repository"
	"short-url/internal/service"
	"short-url/pkg/logger"
	"short-url/pkg/response"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	shortenSvc   *service.ShortenService
	statSvc      *service.StatService
	idSegmentSvc *service.IDSegmentService
	healthCheck  func(context.Context) error
	ready        atomic.Bool
}

func NewHandler(shortenSvc *service.ShortenService, statSvc *service.StatService, idSegmentSvc *service.IDSegmentService) *Handler {
	return &Handler{
		shortenSvc:   shortenSvc,
		statSvc:      statSvc,
		idSegmentSvc: idSegmentSvc,
	}
}

// SetHealthCheck 设置健康检查函数（检查 MongoDB、Redis 等依赖）
func (h *Handler) SetHealthCheck(fn func(context.Context) error) {
	h.healthCheck = fn
}

// SetReady 标记服务已就绪
func (h *Handler) SetReady() {
	h.ready.Store(true)
}

type ShortenRequest struct {
	LongURL string `json:"long_url" binding:"required,url"`
}

// Shorten 处理短链生成请求
func (h *Handler) Shorten(c *gin.Context) {
	var req ShortenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, 1001, "无效的URL: "+err.Error())
		return
	}

	result, err := h.shortenSvc.Shorten(c.Request.Context(), req.LongURL)
	if err != nil {
		if errors.Is(err, service.ErrInvalidURL) {
			response.Error(c, http.StatusBadRequest, 1001, "无效的URL")
			return
		}
		if errors.Is(err, service.ErrURLDisabled) {
			response.Error(c, http.StatusForbidden, 1004, "短链已被禁用")
			return
		}
		if errors.Is(err, service.ErrURLNotExist) {
			response.Error(c, http.StatusNotFound, 1002, "短链不存在")
			return
		}
		logger.Error("短链生成失败",
			logger.String("long_url", req.LongURL),
			logger.WithError(err))
		response.Error(c, http.StatusInternalServerError, 1003, "服务器内部错误")
		return
	}

	response.Success(c, gin.H{
		"short_url": h.shortenSvc.BuildShortURL(result.ShortCode),
		"code":      result.ShortCode,
	})
}

// Redirect 处理短链重定向请求
func (h *Handler) Redirect(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		response.Error(c, http.StatusBadRequest, 1001, "缺少短链码")
		return
	}

	longURL, err := h.statSvc.Redirect(c.Request.Context(), code)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.Error(c, http.StatusNotFound, 1002, "短链码不存在")
			return
		}
		if errors.Is(err, service.ErrURLDisabled) {
			response.Error(c, http.StatusForbidden, 1004, "短链已被禁用")
			return
		}
		logger.Error("短链重定向失败",
			logger.String("code", code),
			logger.WithError(err))
		response.Error(c, http.StatusInternalServerError, 1003, "服务器内部错误")
		return
	}

	c.Redirect(http.StatusFound, longURL)
}

type StatResponse struct {
	ShortCode   string `json:"short_code"`
	LongURL     string `json:"long_url"`
	ClickCount  int64  `json:"click_count"`
	CreatedAt   string `json:"created_at"`
	LastVisited string `json:"last_visited"`
}

// localZone 本地时区加载缓存
var localZone = loadLocalZone()

// loadLocalZone 加载本地时区，加载失败时回退到 UTC
func loadLocalZone() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.UTC
	}
	return loc
}

// toLocalTime 将 UTC 时间转换为本地时间字符串
func toLocalTime(t time.Time) string {
	return t.In(localZone).Format("2006-01-02 15:04:05")
}

// Stat 处理统计查询请求
func (h *Handler) Stat(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		response.Error(c, http.StatusBadRequest, 1001, "缺少短链码")
		return
	}

	url, err := h.statSvc.GetByCode(c.Request.Context(), code)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.Error(c, http.StatusNotFound, 1002, "短链码不存在")
			return
		}
		logger.Error("查询短链统计失败",
			logger.String("code", code),
			logger.WithError(err))
		response.Error(c, http.StatusInternalServerError, 1003, "服务器内部错误")
		return
	}

	response.Success(c, StatResponse{
		ShortCode:   url.ShortCode,
		LongURL:     url.LongURL,
		ClickCount:  url.ClickCount,
		CreatedAt:   toLocalTime(url.CreatedAt),
		LastVisited: toLocalTime(url.LastVisited),
	})
}

// Health 健康检查，返回依赖状态
func (h *Handler) Health(c *gin.Context) {
	if h.healthCheck != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := h.healthCheck(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "unhealthy",
				"error":  err.Error(),
			})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"time":   time.Now().Format("2006-01-02 15:04:05"),
	})
}

// Ready 就绪检查，用于 K8s readiness probe
func (h *Handler) Ready(c *gin.Context) {
	if h.ready.Load() {
		c.JSON(http.StatusOK, gin.H{
			"status": "ready",
			"time":   time.Now().Format("2006-01-02 15:04:05"),
		})
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"status": "not ready",
		"time":   time.Now().Format("2006-01-02 15:04:05"),
	})
}

// IDSegment 查询ID号段使用情况
func (h *Handler) IDSegment(c *gin.Context) {
	info, err := h.idSegmentSvc.GetSegmentInfo(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusInternalServerError, 1003, "查询ID号段失败: "+err.Error())
		return
	}
	response.Success(c, info)
}

// RegisterRoutes 注册全部 HTTP 路由
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	r.POST("/api/shorten", h.Shorten)
	r.GET("/api/stat/:code", h.Stat)
	r.GET("/api/id-segment", h.IDSegment)
	r.GET("/:code", h.Redirect)
	r.GET("/health", h.Health)
	r.GET("/ready", h.Ready)
}
