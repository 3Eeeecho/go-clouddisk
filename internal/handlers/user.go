package handlers

import (
	"net/http" // 如果从URL参数中获取ID
	"strings"  // 用于检查错误消息

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger" // 确保你的日志包路径正确
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/utils"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/3Eeeecho/go-clouddisk/internal/services/admin"
)

type UserHandler struct {
	userService admin.UserService
}

func NewUserHandler(userService admin.UserService) *UserHandler {
	return &UserHandler{
		userService: userService,
	}
}

// GetUserInfo 处理获取已认证用户资料的请求。
// @Summary 获取当前用户资料
// @Description 检索已认证用户的资料详情。
// @Tags User
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} xerr.Response "用户资料检索成功"
// @Failure 401 {object} xerr.Response "未授权"
// @Failure 404 {object} xerr.Response "用户未找到"
// @Failure 500 {object} xerr.Response "内部服务器错误"
// @Router /api/v1/user/me [get]
func (h *UserHandler) GetUserProfile(c *gin.Context) {
	currentUserID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	user, err := h.userService.GetUserProfile(currentUserID)
	if err != nil {
		if strings.Contains(err.Error(), "用户未找到") {
			xerr.AbortWithError(c, http.StatusNotFound, xerr.CodeNotFound, "未找到用户资料")
		} else {
			logger.Error("GetMyProfile: 获取用户资料失败",
				zap.Uint64("userID", currentUserID),
				zap.Error(err))
			xerr.AbortWithError(c, http.StatusInternalServerError, xerr.CodeInternalServerError, "检索用户资料失败")
		}
		return
	}

	// 成功响应，GORM 的 json:"-" 标签会确保密码不被序列化
	xerr.Success(c, http.StatusOK, "成功获取用户资料", user)
}

// 如果你想允许通过ID获取其他用户资料（例如供管理员使用）
/*
// GetUserProfileByID 处理通过ID获取用户资料的请求。
// @Summary 通过ID获取用户资料
// @Description 根据用户ID检索特定用户的资料详情。需要管理员权限。
// @Tags User
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "用户ID"
// @Success 200 {object} xerr.Response "用户资料检索成功"
// @Failure 401 {object} xerr.Response "未授权"
// @Failure 403 {object} xerr.Response "禁止访问"
// @Failure 404 {object} xerr.Response "用户未找到"
// @Failure 500 {object} xerr.Response "内部服务器错误"
// @Router /api/v1/user/{id} [get]
func (h *userHandler) GetUserProfileByID(c *gin.Context) {
	// 获取当前用户ID（如果需要进行授权检查）
	currentUserID, exists := c.Get("userID")
	if !exists {
		logger.Error("GetUserProfileByID: Context中未找到UserID (中间件错误)")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权"})
		return
	}

	// 通常在这里添加授权检查，
	// 例如，判断当前用户是否为管理员。
	// 为了简化，目前假设可以直接访问，但在生产环境中，请检查 isAdmin。
	// if !currentUserID.(xerr.Response).IsAdmin { // 这需要在Context中存储用户对象，而不仅仅是ID
	//    c.JSON(http.StatusForbidden, gin.H{"error": "禁止访问"})
	//    return
	// }

	targetUserIDStr := c.Param("id")
	targetUserID, err := strconv.ParseUint(targetUserIDStr, 10, 64)
	if err != nil {
		logger.Warn("GetUserProfileByID: 用户ID格式无效", zap.String("userIDStr", targetUserIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户ID无效"})
		return
	}

	user, err := h.userService.GetUserProfile(targetUserID)
	if err != nil {
		if strings.Contains(err.Error(), "用户未找到") {
			c.JSON(http.StatusNotFound, gin.H{"error": "未找到用户资料"})
		} else {
			logger.Error("GetUserProfileByID: 获取用户资料失败",
				zap.Uint64("userID", targetUserID),
				zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "检索用户资料失败"})
		}
		return
	}

	c.JSON(http.StatusOK, user)
}
*/
