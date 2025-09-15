package handlers

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/handlers/response"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/utils"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/3Eeeecho/go-clouddisk/internal/services/share"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type ShareHandler struct {
	shareService share.ShareService
	cfg          *config.Config
}

func NewShareHandler(shareService share.ShareService, cfg *config.Config) *ShareHandler {
	return &ShareHandler{
		shareService: shareService,
		cfg:          cfg,
	}
}

type CreateShareRequest struct {
	FileID           uint64  `json:"file_id" binding:"required"`
	Password         *string `json:"password"`
	ExpiresInMinutes *int    `json:"expires_in_minutes"` // 以分钟为单位
}

type ShareCheckPasswordRequest struct {
	Password *string `json:"password" binding:"required"`
}

// CreateShare handles creation of a new share link.
// @Summary 创建分享链接
// @Description 为指定文件或文件夹创建可分享链接，可设置密码和有效期
// @Tags 分享
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateShareRequest true "分享链接信息"
// @Success 200 {object} xerr.Response "分享链接创建成功"
// @Failure 400 {object} xerr.Response "请求参数无效"
// @Failure 403 {object} xerr.Response "无权操作或文件状态异常"
// @Failure 404 {object} xerr.Response "文件未找到"
// @Failure 409 {object} xerr.Response "文件已存在有效分享链接"
// @Router /api/v1/shares [post]
func (h *ShareHandler) CreateShare(c *gin.Context) {
	var req CreateShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "请求参数解析失败: "+err.Error())
		return
	}

	userID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	share, err := h.shareService.CreateShare(c.Request.Context(), userID, req.FileID, req.Password, req.ExpiresInMinutes)
	if err != nil {
		if errors.Is(err, xerr.ErrFileNotFound) {
			response.Error(c, http.StatusNotFound, xerr.FileNotFoundCode, err.Error())
		} else if errors.Is(err, xerr.ErrPermissionDenied) {
			response.Error(c, http.StatusForbidden, xerr.PermissionDeniedCode, err.Error())
		} else if errors.Is(err, xerr.ErrFileStatusInvalid) {
			response.Error(c, http.StatusBadRequest, xerr.FileStatusInvalidCode, err.Error())
		} else if errors.Is(err, xerr.ErrShareAlreadyExists) {
			response.Error(c, http.StatusConflict, xerr.ShareAlreadyExistsCode, err.Error())
		} else {
			logger.Error("CreateShare: 创建分享链接失败", zap.Error(err))
			response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "创建分享链接失败")
		}
		return
	}

	shareURL := fmt.Sprintf("%s/share/%s", h.cfg.Storage.LocalBasePath, share.UUID)
	response.Success(c, http.StatusOK, "分享链接创建成功", gin.H{
		"share":     share,
		"share_url": shareURL,
	})
}

// GetShareDetails handles retrieving details of a share link.
// @Summary 获取分享链接详情
// @Description 根据分享 UUID 获取分享链接的详细信息（不包括文件内容），用于展示给下载者
// @Tags 分享
// @Produce json
// @Param share_uuid path string true "分享链接 UUID"
// @Success 200 {object} xerr.Response "分享链接详情"
// @Failure 403 {object} xerr.Response "分享链接需要密码"
// @Failure 404 {object} xerr.Response "分享链接不存在或已失效"
// @Router /share/{share_uuid}/details [get]
func (h *ShareHandler) GetShareDetails(c *gin.Context) {
	shareUUID := c.Param("share_uuid")
	if shareUUID == "" {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "分享UUID不能为空")
		return
	}
	password := c.Query("password")
	var providedPassword *string
	if password != "" {
		providedPassword = &password
	}

	share, err := h.shareService.GetShareByUUID(c.Request.Context(), shareUUID, providedPassword)
	if err != nil {
		if errors.Is(err, xerr.ErrShareNotFound) {
			response.Error(c, http.StatusNotFound, xerr.ShareNotFoundCode, err.Error())
		} else if errors.Is(err, xerr.ErrSharePasswordRequired) {
			response.Error(c, http.StatusForbidden, xerr.SharePasswordRequiredCode, err.Error())
		} else {
			logger.Error("GetShareDetails: 获取分享详情失败", zap.String("uuid", shareUUID), zap.Error(err))
			response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "获取分享详情失败")
		}
		return
	}

	share.Password = nil
	response.Success(c, http.StatusOK, "获取链接详情成功", gin.H{
		"share": share,
	})
}

// VerifySharePassword handles password verification for a share link.
// @Summary 验证分享链接密码
// @Description 验证分享链接的访问密码
// @Tags 分享
// @Accept json
// @Produce json
// @Param share_uuid path string true "分享链接 UUID"
// @Param request body ShareCheckPasswordRequest true "密码"
// @Success 200 {object} map[string]string "密码验证成功"
// @Failure 403 {object} xerr.Response "密码不正确或链接已过期"
// @Failure 404 {object} xerr.Response "分享链接不存在或已失效"
// @Router /share/{share_uuid}/verify [post]
func (h *ShareHandler) VerifySharePassword(c *gin.Context) {
	shareUUID := c.Param("share_uuid")
	if shareUUID == "" {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "分享UUID不能为空")
		return
	}

	var req ShareCheckPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "请求参数解析失败: "+err.Error())
		return
	}

	_, err := h.shareService.GetShareByUUID(c.Request.Context(), shareUUID, req.Password)
	if err != nil {
		if errors.Is(err, xerr.ErrShareNotFound) {
			response.Error(c, http.StatusNotFound, xerr.ShareNotFoundCode, err.Error())
		} else if errors.Is(err, xerr.ErrSharePasswordIncorrect) {
			response.Error(c, http.StatusForbidden, xerr.SharePasswordIncorrectCode, err.Error())
		} else {
			logger.Error("VerifySharePassword: 验证分享密码失败", zap.String("uuid", shareUUID), zap.Error(err))
			response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "验证分享密码失败")
		}
		return
	}
	response.Success(c, http.StatusOK, "密码验证成功", nil)
}

// DownloadSharedContent handles downloading the content of a shared file/folder.
// @Summary 下载分享内容
// @Description 根据分享 UUID 下载文件或文件夹（如果为文件夹则打包为 ZIP）
// @Tags 分享
// @Produce octet-stream
// @Param share_uuid path string true "分享链接 UUID"
// @Param password query string false "分享密码（如果需要）"
// @Success 200 {file} file "文件/文件夹下载成功"
// @Failure 403 {object} xerr.Response "分享链接需要密码或密码不正确"
// @Failure 404 {object} xerr.Response "分享链接不存在或已失效"
// @Router /share/{share_uuid}/download [get]
func (h *ShareHandler) DownloadSharedContent(c *gin.Context) {
	shareUUID := c.Param("share_uuid")
	if shareUUID == "" {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "分享UUID不能为空")
		return
	}

	password := c.Query("password")
	var providedPassword *string
	if password != "" {
		providedPassword = &password
	}

	share, err := h.shareService.GetShareByUUID(c.Request.Context(), shareUUID, providedPassword)
	if err != nil {
		if errors.Is(err, xerr.ErrShareNotFound) {
			response.Error(c, http.StatusNotFound, xerr.ShareNotFoundCode, err.Error())
		} else if errors.Is(err, xerr.ErrSharePasswordRequired) {
			response.Error(c, http.StatusForbidden, xerr.SharePasswordRequiredCode, err.Error())
		} else if errors.Is(err, xerr.ErrSharePasswordIncorrect) {
			response.Error(c, http.StatusForbidden, xerr.SharePasswordIncorrectCode, err.Error())
		} else {
			logger.Error("DownloadSharedContent: 验证分享链接失败", zap.String("uuid", shareUUID), zap.Error(err))
			response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "下载分享内容失败")
		}
		return
	}

	// 如果是文件夹，保持服务器端压缩并流式传输
	if share.File.IsFolder == 1 {
		reader, err := h.shareService.GetSharedFolderContent(c.Request.Context(), share)
		if err != nil {
			logger.Error("DownloadSharedContent: 打包分享文件夹内容失败", zap.String("uuid", shareUUID), zap.Error(err))
			response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "打包分享文件夹内容失败")
			return
		}
		defer reader.Close()

		fileName := fmt.Sprintf("%s.zip", share.File.FileName)
		encodedFileName := url.PathEscape(fileName)
		contentDisposition := fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, encodedFileName, encodedFileName)

		c.Header("Content-Disposition", contentDisposition)
		c.Header("Content-Type", "application/zip")

		_, err = io.Copy(c.Writer, reader)
		if err != nil {
			logger.Error("DownloadSharedContent: 流式传输文件夹ZIP内容失败", zap.String("uuid", shareUUID), zap.Error(err))
		}
		return
	}

	// 如果是单个文件，则生成预签名URL并重定向
	presignedURL, err := h.shareService.GetSharedFilePresignedURL(c.Request.Context(), share)
	if err != nil {
		logger.Error("DownloadSharedContent: 生成预签名URL失败", zap.String("uuid", shareUUID), zap.Error(err))
		response.Error(c, http.StatusInternalServerError, xerr.StorageErrorCode, "获取文件下载链接失败")
		return
	}

	c.Redirect(http.StatusFound, presignedURL)
}

// ListUserShares handles listing all share links created by the authenticated user.
// @Summary 列出用户创建的分享链接
// @Description 列出当前用户创建的所有有效分享链接
// @Tags 分享
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码，默认为1" default(1)
// @Param pageSize query int false "每页数量，默认为10" default(10)
// @Success 200 {object} object{data=[]xerr.Response,total=int} "分享链接列表"
// @Router /api/v1/shares/my [get]
func (h *ShareHandler) ListUserShares(c *gin.Context) {
	pageStr := c.DefaultQuery("page", "1")
	pageSizeStr := c.DefaultQuery("pageSize", "10")

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}
	pageSize, err := strconv.Atoi(pageSizeStr)
	if err != nil || pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}

	userID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	shares, total, err := h.shareService.ListUserShares(userID, page, pageSize)
	if err != nil {
		logger.Error("ListUserShares: 获取用户分享列表失败", zap.Uint64("userID", userID), zap.Error(err))
		response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "获取分享列表失败")
		return
	}
	response.Success(c, http.StatusOK, "成功获取所有有效分享链接", gin.H{
		"shares": shares,
		"total":  total,
	})
}

// RevokeShare handles revoking a share link.
// @Summary 撤销分享链接
// @Description 根据分享 ID 撤销用户创建的分享链接
// @Tags 分享
// @Security BearerAuth
// @Param share_id path int true "分享链接 ID"
// @Success 204 "分享链接撤销成功"
// @Failure 403 {object} xerr.Response "无权操作或链接已失效"
// @Failure 404 {object} xerr.Response "分享链接不存在"
// @Router /api/v1/shares/{share_id} [delete]
func (h *ShareHandler) RevokeShare(c *gin.Context) {
	shareIDStr := c.Param("share_id")
	shareID, err := strconv.ParseUint(shareIDStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "分享ID格式无效")
		return
	}

	userID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	err = h.shareService.RevokeShare(userID, shareID)
	if err != nil {
		if errors.Is(err, xerr.ErrShareNotFound) {
			response.Error(c, http.StatusNotFound, xerr.ShareNotFoundCode, err.Error())
		} else if errors.Is(err, xerr.ErrPermissionDenied) {
			response.Error(c, http.StatusForbidden, xerr.PermissionDeniedCode, err.Error())
		} else {
			logger.Error("RevokeShare: 撤销分享链接失败", zap.Uint64("shareID", shareID), zap.Error(err))
			response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "撤销分享链接失败")
		}
		return
	}

	c.Status(http.StatusNoContent)
}
