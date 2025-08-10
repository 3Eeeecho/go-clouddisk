package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
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
		if errors.Is(err, xerr.ErrUserNotFound) {
			xerr.AbortWithError(c, http.StatusNotFound, xerr.UserNotFoundCode, "未找到用户资料")
		} else {
			logger.Error("GetMyProfile: 获取用户资料失败",
				zap.Uint64("userID", currentUserID),
				zap.Error(err))
			xerr.AbortWithError(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "检索用户资料失败")
		}
		return
	}

	xerr.Success(c, http.StatusOK, "成功获取用户资料", user)
}
