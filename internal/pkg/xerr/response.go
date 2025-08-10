package xerr

import (
	"errors"

	"github.com/gin-gonic/gin"
)

// CodeError 结构体用于在服务层传递带有业务码的错误
// 它实现了 error 接口
type CodeError struct {
	Code int   // 业务错误码
	Err  error // 被包裹的底层错误
}

// Error 实现 error 接口
func (e *CodeError) Error() string {
	return e.Err.Error()
}

// Unwrap 返回被包裹的底层错误，支持 errors.Unwrap
func (e *CodeError) Unwrap() error {
	return e.Err
}

// NewCodeError 创建一个 CodeError 实例
func NewCodeError(code int, err error) *CodeError {
	return &CodeError{Code: code, Err: err}
}

// Is 判断错误是否为指定的错误类型
// 如果 err 是 *CodeError，则会解包后与 target 比较
func Is(err, target error) bool {
	return errors.Is(err, target)
}

// Response 是通用 JSON 响应结构
type Response struct {
	Code    int    `json:"code"`    // 业务状态码
	Message string `json:"message"` // 消息
	Data    any    `json:"data"`    // 响应数据
}

// JSONResponse 发送标准 JSON 响应
func JSONResponse(c *gin.Context, httpStatus int, code int, message string, data any) {
	c.JSON(httpStatus, Response{
		Code:    code,
		Message: message,
		Data:    data,
	})
}

// Success 成功响应
func Success(c *gin.Context, httpStatus int, message string, data any) {
	JSONResponse(c, httpStatus, 20000, message, data) // 20000 表示业务成功码
}

// Error 错误响应
func Error(c *gin.Context, httpStatus int, code int, message string) {
	JSONResponse(c, httpStatus, code, message, nil)
}

// AbortWithError 终止请求并发送错误响应
func AbortWithError(c *gin.Context, httpStatus int, code int, message string) {
	Error(c, httpStatus, code, message)
	c.Abort() // 终止后续的 HandlerFunc
}
