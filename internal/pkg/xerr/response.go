package xerr

import "github.com/gin-gonic/gin"

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
	JSONResponse(c, httpStatus, 0, message, data) // 0 表示业务成功码
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
