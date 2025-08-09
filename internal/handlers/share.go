package handlers

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
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
// @Failure 401 {object} xerr.Response "未授权"
// @Failure 403 {object} xerr.Response "无权操作或文件状态异常"
// @Failure 409 {object} xerr.Response "文件已存在有效分享链接"
// @Failure 500 {object} xerr.Response "内部服务器错误"
// @Router /api/v1/shares [post]
func (h *ShareHandler) CreateShare(c *gin.Context) {

	var req CreateShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "请求参数解析失败: "+err.Error())
		return
	}

	userID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return // 错误已在 GetUserIDFromContext 中处理
	}

	share, err := h.shareService.CreateShare(c.Request.Context(), userID, req.FileID, req.Password, req.ExpiresInMinutes)
	if err != nil {
		if strings.Contains(err.Error(), "文件或文件夹不存在") || strings.Contains(err.Error(), "无权分享") || strings.Contains(err.Error(), "文件或文件夹状态异常") {
			xerr.Error(c, http.StatusForbidden, xerr.CodeForbidden, err.Error())
		} else if strings.Contains(err.Error(), "已存在有效分享链接") {
			xerr.Error(c, http.StatusConflict, xerr.CodeConflict, err.Error()) // 409 Conflict
		} else {
			logger.Error("CreateShare: 创建分享链接失败", zap.Error(err))
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("创建分享链接失败: %v", err))
		}
		return
	}

	// 可以返回一个包含完整分享链接URL的响应
	// TODO 当前URL仅作为测试用
	shareURL := fmt.Sprintf("%s/share/%s", h.cfg.Storage.LocalBasePath, share.UUID)
	xerr.Success(c, http.StatusOK, "分享链接创建成功", gin.H{
		"share":     share,
		"share_url": shareURL,
	})
}

// GetShareDetails handles retrieving details of a share link.
// This API is for unauthenticated access (public access to share link meta-data)
// @Summary 获取分享链接详情
// @Description 根据分享 UUID 获取分享链接的详细信息（不包括文件内容），用于展示给下载者
// @Tags 分享
// @Produce json
// @Param share_uuid path string true "分享链接 UUID"
// @Success 200 {object} xerr.Response "分享链接详情"
// @Failure 400 {object} xerr.Response "请求参数无效"
// @Failure 404 {object} xerr.Response "分享链接不存在或已失效"
// @Failure 403 {object} xerr.Response "分享链接需要密码"
// @Failure 500 {object} xerr.Response "内部服务器错误"
// @Router /share/{share_uuid}/details [get]
func (h *ShareHandler) GetShareDetails(c *gin.Context) {

	shareUUID := c.Param("share_uuid")
	if shareUUID == "" {
		xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "分享UUID不能为空")
		return
	}
	password := c.Query("password")
	var providedPassword *string
	if password != "" {
		providedPassword = &password
	}

	share, err := h.shareService.GetShareByUUID(c.Request.Context(), shareUUID, providedPassword) // 初始不带密码尝试
	if err != nil {
		if strings.Contains(err.Error(), "不存在") || strings.Contains(err.Error(), "已失效") || strings.Contains(err.Error(), "已过期") {
			xerr.Error(c, http.StatusNotFound, xerr.CodeNotFound, err.Error())
		} else if strings.Contains(err.Error(), "需要密码") {
			xerr.Error(c, http.StatusForbidden, xerr.CodeForbidden, err.Error()) // 返回 403 提示需要密码
		} else {
			logger.Error("GetShareDetails: 获取分享详情失败", zap.String("uuid", shareUUID), zap.Error(err))
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("获取分享详情失败: %v", err))
		}
		return
	}

	// 返回脱敏后的分享信息（例如不返回加密的密码）
	share.Password = nil // 清除密码信息，不返回给前端
	xerr.Success(c, http.StatusOK, "获取链接详情成功", gin.H{
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
// @Failure 400 {object} xerr.Response "请求参数无效"
// @Failure 404 {object} xerr.Response "分享链接不存在或已失效"
// @Failure 403 {object} xerr.Response "密码不正确或链接已过期"
// @Failure 500 {object} xerr.Response "内部服务器错误"
// @Router /share/{share_uuid}/verify [post]
func (h *ShareHandler) VerifySharePassword(c *gin.Context) {
	shareUUID := c.Param("share_uuid")
	if shareUUID == "" {
		xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "分享UUID不能为空")
		return
	}

	var req ShareCheckPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "请求参数解析失败: "+err.Error())
		return
	}

	// 调用服务层验证密码
	_, err := h.shareService.GetShareByUUID(c.Request.Context(), shareUUID, req.Password)
	if err != nil {
		if strings.Contains(err.Error(), "不存在") || strings.Contains(err.Error(), "已失效") || strings.Contains(err.Error(), "已过期") {
			xerr.Error(c, http.StatusNotFound, xerr.CodeNotFound, err.Error())
		} else if strings.Contains(err.Error(), "密码不正确") {
			xerr.Error(c, http.StatusForbidden, xerr.CodeForbidden, err.Error()) // 403 Forbidden for incorrect password
		} else {
			logger.Error("VerifySharePassword: 验证分享密码失败", zap.String("uuid", shareUUID), zap.Error(err))
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("验证分享密码失败: %v", err))
		}
		return
	}
	xerr.Success(c, http.StatusOK, "密码验证成功", nil)
}

// DownloadSharedContent handles downloading the content of a shared file/folder.
// @Summary 下载分享内容
// @Description 根据分享 UUID 下载文件或文件夹（如果为文件夹则打包为 ZIP）
// @Tags 分享
// @Produce octet-stream
// @Param share_uuid path string true "分享链接 UUID"
// @Param password query string false "分享密码（如果需要）"
// @Success 200 {file} file "文件/文件夹下载成功"
// @Failure 400 {object} xerr.Response "请求参数无效"
// @Failure 404 {object} xerr.Response "分享链接不存在或已失效"
// @Failure 403 {object} xerr.Response "分享链接需要密码或密码不正确"
// @Failure 500 {object} xerr.Response "内部服务器错误"
// @Router /share/{share_uuid}/download [get]
func (h *ShareHandler) DownloadSharedContent(c *gin.Context) {

	shareUUID := c.Param("share_uuid")
	if shareUUID == "" {
		xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "分享UUID不能为空")
		return
	}

	// 从查询参数获取密码 (GET 请求通常在 Query 中携带密码)
	password := c.Query("password")
	var providedPassword *string
	if password != "" {
		providedPassword = &password
	}

	// 1. 验证分享链接及密码
	share, err := h.shareService.GetShareByUUID(c.Request.Context(), shareUUID, providedPassword)
	if err != nil {
		if strings.Contains(err.Error(), "不存在") || strings.Contains(err.Error(), "已失效") || strings.Contains(err.Error(), "已过期") {
			xerr.Error(c, http.StatusNotFound, xerr.CodeNotFound, err.Error())
		} else if strings.Contains(err.Error(), "需要密码") || strings.Contains(err.Error(), "密码不正确") {
			xerr.Error(c, http.StatusForbidden, xerr.CodeForbidden, err.Error())
		} else {
			logger.Error("DownloadSharedContent: 验证分享链接失败", zap.String("uuid", shareUUID), zap.Error(err))
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("下载分享内容失败: %v", err))
		}
		return
	}

	// 根据是文件还是文件夹，调用不同的下载逻辑
	var reader io.ReadCloser
	var fileName string
	var contentType string
	var fileSize int64 // 用于 Content-Length

	if share.File.IsFolder == 0 { // 是文件
		reader, err = h.shareService.GetSharedFileContent(c.Request.Context(), share)
		if err != nil {
			logger.Error("DownloadSharedContent: 获取分享文件内容失败", zap.String("uuid", shareUUID), zap.Error(err))
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("获取分享文件内容失败: %v", err))
			return
		}
		fileName = share.File.FileName
		contentType = *share.File.MimeType
		fileSize = int64(share.File.Size)
	} else { // 是文件夹
		reader, err = h.shareService.GetSharedFolderContent(c.Request.Context(), share)
		if err != nil {
			logger.Error("DownloadSharedContent: 打包分享文件夹内容失败", zap.String("uuid", shareUUID), zap.Error(err))
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("打包分享文件夹内容失败: %v", err))
			return
		}
		fileName = fmt.Sprintf("%s.zip", share.File.FileName) // 文件夹下载通常打包为zip
		contentType = "application/zip"
		fileSize = 0 // 流式ZIP压缩，Content-Length通常未知，可以省略或设置为0
	}
	defer reader.Close()

	// 设置响应头，支持中文文件名
	encodedFileName := url.PathEscape(fileName)
	contentDisposition := fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		encodedFileName, // filename="" 这部分也进行 URL 编码
		encodedFileName, // filename*="" 这部分进行 URL 编码
	)
	c.Header("Content-Disposition", contentDisposition)
	c.Header("Content-Type", contentType)
	if fileSize > 0 { // 只有文件才设置 Content-Length
		c.Header("Content-Length", fmt.Sprintf("%d", fileSize))
	}

	_, err = io.Copy(c.Writer, reader)
	if err != nil {
		logger.Error("DownloadSharedContent: 流式传输文件内容失败", zap.String("uuid", shareUUID), zap.Error(err))
		// 此时通常无法再返回HTTP错误，连接可能会中断
	}
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
// @Failure 400 {object} xerr.Response "请求参数无效"
// @Failure 401 {object} xerr.Response "未授权"
// @Failure 500 {object} xerr.Response "内部服务器错误"
// @Router /api/v1/shares/my [get]
func (h *ShareHandler) ListUserShares(c *gin.Context) {
	pageStr := c.DefaultQuery("page", "1")
	pageSizeStr := c.DefaultQuery("pageSize", "10")

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}
	pageSize, err := strconv.Atoi(pageSizeStr)
	if err != nil || pageSize < 1 || pageSize > 100 { // 限制 pageSize 最大值
		pageSize = 10
	}

	userID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	shares, total, err := h.shareService.ListUserShares(userID, page, pageSize)
	if err != nil {
		logger.Error("ListUserShares: 获取用户分享列表失败", zap.Uint64("userID", userID), zap.Error(err))
		xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("获取分享列表失败: %v", err))
		return
	}
	xerr.Success(c, http.StatusOK, "成功获取所有有效分享链接", gin.H{
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
// @Failure 400 {object} xerr.Response "请求参数无效"
// @Failure 401 {object} xerr.Response "未授权"
// @Failure 403 {object} xerr.Response "无权操作或链接已失效"
// @Failure 404 {object} xerr.Response "分享链接不存在"
// @Failure 500 {object} xerr.Response "内部服务器错误"
// @Router /api/v1/shares/{share_id} [delete]
func (h *ShareHandler) RevokeShare(c *gin.Context) {

	shareIDStr := c.Param("share_id")
	shareID, err := strconv.ParseUint(shareIDStr, 10, 64)
	if err != nil {
		xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "分享ID格式无效")
		return
	}

	userID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	err = h.shareService.RevokeShare(userID, shareID)
	if err != nil {
		if strings.Contains(err.Error(), "不存在") {
			xerr.Error(c, http.StatusNotFound, xerr.CodeNotFound, err.Error())
		} else if strings.Contains(err.Error(), "无权") || strings.Contains(err.Error(), "已失效") {
			xerr.Error(c, http.StatusForbidden, xerr.CodeForbidden, err.Error())
		} else {
			logger.Error("RevokeShare: 撤销分享链接失败", zap.Uint64("shareID", shareID), zap.Error(err))
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("撤销分享链接失败: %v", err))
		}
		return
	}

	c.Status(http.StatusNoContent) // 204 No Content for successful deletion/revocation
}
