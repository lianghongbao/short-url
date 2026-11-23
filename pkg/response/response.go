package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Response struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg,omitempty"`
	Data interface{} `json:"data,omitempty"`
}

// 业务错误码
const (
	CodeSuccess       = 0
	CodeInvalidURL    = 1001
	CodeShortNotFound = 1002
	CodeInternalError = 1003
	CodeShortDisabled = 1004
)

// Success 返回成功响应
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code: CodeSuccess,
		Msg:  "成功",
		Data: data,
	})
}

// Error 返回错误响应
func Error(c *gin.Context, httpStatus int, code int, msg string) {
	c.JSON(httpStatus, Response{
		Code: code,
		Msg:  msg,
	})
}
