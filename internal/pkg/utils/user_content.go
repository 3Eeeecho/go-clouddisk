package utils

import (
	"net/http"

	"github.com/3Eeeecho/go-clouddisk/internal/handlers/response"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/gin-gonic/gin"
)

// GetUserIDFromContext 从 Gin 上下文中获取并验证用户ID
// 如果获取失败或类型不正确，会中止请求并返回错误
func GetUserIDFromContext(c *gin.Context) (uint64, bool) {
	userID, exists := c.Get("userID")
	if !exists {
		response.AbortWithError(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "User ID not found in context")
		return 0, false
	}
	currentUserID, ok := userID.(uint64)
	if !ok {
		response.AbortWithError(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "Invalid user ID type in context")
		return 0, false
	}
	return currentUserID, true
}
