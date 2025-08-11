package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/utils"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/3Eeeecho/go-clouddisk/internal/services/explorer"
	"github.com/gin-gonic/gin"
)

// UploadHandler 结构体持有其服务依赖
type UploadHandler struct {
	uploadService explorer.UploadService
}

// NewUploadHandler 创建一个新的 UploadHandler 实例
func NewUploadHandler(uploadService explorer.UploadService) *UploadHandler {
	return &UploadHandler{
		uploadService: uploadService,
	}
}

// InitUploadHandler 处理上传初始化请求
// @Summary 初始化文件上传
// @Description 创建上传会话并返回上传参数
// @Tags 文件上传
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body models.UploadInitRequest true "上传初始化参数"
// @Success 200 {object} xerr.Response "上传初始化成功"
// @Failure 400 {object} xerr.Response "参数错误"
// @Failure 409 {object} xerr.Response "文件已存在"
// @Failure 500 {object} xerr.Response "内部服务器错误"
// @Router /api/v1/uploads/init [post]
func (h *UploadHandler) InitUploadHandler(c *gin.Context) {
	currentUserID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}
	var req models.UploadInitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		xerr.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "Invalid request body")
		return
	}

	resp, err := h.uploadService.UploadInit(c, currentUserID, &req)
	if err != nil {
		if errors.Is(err, xerr.ErrDirectoryNotFound) {
			xerr.Error(c, http.StatusBadRequest, xerr.DirectoryNotFoundCode, err.Error())
			return
		}
		if errors.Is(err, xerr.ErrFileAlreadyExists) {
			xerr.Error(c, http.StatusConflict, xerr.FileAlreadyExistsCode, err.Error())
			return
		}
		xerr.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "Failed to initialize upload")
		return
	}
	xerr.Success(c, http.StatusOK, "Upload initialized successfully", resp)
}

// UploadChunkHandler 处理分片上传请求
// @Summary 上传文件分片
// @Description 上传文件的一个分片
// @Tags 文件上传
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param chunk formData file true "文件分片内容"
// @Param file_hash formData string true "文件哈希值"
// @Param chunk_index formData int true "分片索引"
// @Success 200 {object} xerr.Response "分片上传成功"
// @Failure 400 {object} xerr.Response "参数错误"
// @Failure 404 {object} xerr.Response "上传会话未找到"
// @Failure 500 {object} xerr.Response "内部服务器错误"
// @Router /api/v1/uploads/chunk [post]
func (h *UploadHandler) UploadChunkHandler(c *gin.Context) {
	currentUserID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	// 从 multipart form 中获取块数据
	file, err := c.FormFile("chunk")
	if err != nil {
		xerr.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "Chunk file not found in form")
		return
	}

	fileContent, err := file.Open()
	if err != nil {
		xerr.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "Failed to open chunk file")
		return
	}
	defer fileContent.Close()

	// 从 form 中解析其他参数
	var req models.UploadChunkRequest
	if err := c.ShouldBind(&req); err != nil {
		xerr.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, fmt.Sprintf("Invalid form data: %v", err))
		return
	}

	// 调用 service 层处理块上传
	if err := h.uploadService.UploadChunk(c, currentUserID, &req, fileContent); err != nil {
		if errors.Is(err, xerr.ErrUploadSessionNotFound) {
			xerr.Error(c, http.StatusNotFound, xerr.UploadSessionNotFoundCode, err.Error())
			return
		}
		xerr.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, fmt.Sprintf("Failed to upload chunk: %v", err))
		return
	}

	xerr.Success(c, http.StatusOK, "Chunk uploaded successfully", nil)
}

// CompleteUploadHandler 处理分片合并请求
// @Summary 完成文件上传
// @Description 合并所有分片完成文件上传
// @Tags 文件上传
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body models.UploadCompleteRequest true "上传完成参数"
// @Success 200 {object} xerr.Response "文件上传完成"
// @Failure 400 {object} xerr.Response "参数错误"
// @Failure 404 {object} xerr.Response "上传会话未找到"
// @Failure 500 {object} xerr.Response "内部服务器错误"
// @Router /api/v1/uploads/complete [post]
func (h *UploadHandler) CompleteUploadHandler(c *gin.Context) {
	currentUserID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}
	var req models.UploadCompleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		xerr.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "Invalid request body")
		return
	}

	newFile, err := h.uploadService.UploadComplete(c, currentUserID, &req)
	if err != nil {
		if errors.Is(err, xerr.ErrUploadSessionNotFound) {
			xerr.Error(c, http.StatusNotFound, xerr.UploadSessionNotFoundCode, err.Error())
			return
		}
		if errors.Is(err, xerr.ErrChunkMissing) {
			xerr.Error(c, http.StatusBadRequest, xerr.ChunkMissingCode, err.Error())
			return
		}
		if errors.Is(err, xerr.ErrHashMismatch) {
			xerr.Error(c, http.StatusBadRequest, xerr.HashMismatchCode, err.Error())
			return
		}
		xerr.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, fmt.Sprintf("Failed to complete upload: %v", err))
		return
	}

	xerr.Success(c, http.StatusOK, "File uploaded and merged successfully", newFile)
}

// ListPartsHandler 处理查询已上传分块的请求
// @Summary 查询已上传的分块
// @Description 根据 upload_id 查询已经成功上传的分块列表
// @Tags 文件上传
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body models.ListPartsRequest true "查询参数"
// @Success 200 {object} models.ListPartsResponse "查询成功"
// @Failure 400 {object} xerr.Response "参数错误"
// @Failure 500 {object} xerr.Response "内部服务器错误"
// @Router /api/v1/uploads/parts [post]
func (h *UploadHandler) ListPartsHandler(c *gin.Context) {
	var req models.ListPartsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		xerr.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "Invalid request body")
		return
	}

	resp, err := h.uploadService.ListUploadedParts(c, &req)
	if err != nil {
		xerr.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "Failed to list uploaded parts")
		return
	}

	xerr.Success(c, http.StatusOK, "Successfully listed uploaded parts", resp)
}
