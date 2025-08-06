package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/utils"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/3Eeeecho/go-clouddisk/internal/services/explorer"
	"github.com/gin-gonic/gin"
)

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
// @Failure 500 {object} xerr.Response "内部服务器错误"
// @Router /api/v1/upload/init [post]
func InitUploadHandler(uploadService explorer.UploadService) gin.HandlerFunc {
	return func(c *gin.Context) {
		currentUserID, ok := utils.GetUserIDFromContext(c)
		if !ok {
			return
		}
		var req models.UploadInitRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			xerr.AbortWithError(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid request body")
			return
		}

		resp, err := uploadService.UploadInit(c, currentUserID, &req)
		if err != nil {
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, "Failed to initialize upload")
			return
		}
		xerr.Success(c, http.StatusOK, "Upload initialized successfully", resp)
	}
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
// @Failure 500 {object} xerr.Response "内部服务器错误"
// @Router /api/v1/upload/chunk [post]
func UploadChunkHandler(uploadService explorer.UploadService) gin.HandlerFunc {
	return func(c *gin.Context) {
		currentUserID, ok := utils.GetUserIDFromContext(c)
		if !ok {
			return
		}

		file, err := c.FormFile("chunk")
		if err != nil {
			xerr.AbortWithError(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Chunk file not found")
			return
		}

		fileContent, err := file.Open()
		if err != nil {
			xerr.AbortWithError(c, http.StatusInternalServerError, xerr.CodeInternalServerError, "Failed to open chunk file")
			return
		}
		defer fileContent.Close()

		// 手动从表单中获取其他字段
		fileHash := c.PostForm("file_hash")
		chunkIndexStr := c.PostForm("chunk_index")

		// 验证字段是否缺失
		if fileHash == "" || chunkIndexStr == "" {
			xerr.AbortWithError(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid form data: missing required fields")
			return
		}

		// 将字符串转换为整数
		chunkIndex, err := strconv.Atoi(chunkIndexStr)
		if err != nil {
			xerr.AbortWithError(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid chunk_index format")
			return
		}

		// 构造请求体并调用服务
		req := models.UploadChunkRequest{
			FileHash:   fileHash,
			ChunkIndex: chunkIndex,
		}

		if err := uploadService.UploadChunk(c, currentUserID, &req, fileContent); err != nil {
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("Failed to upload chunk: %v", err))
			return
		}

		xerr.Success(c, http.StatusOK, "Chunk uploaded successfully", nil)
	}
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
// @Failure 500 {object} xerr.Response "内部服务器错误"
// @Router /api/v1/upload/complete [post]
func CompleteUploadHandler(uploadService explorer.UploadService) gin.HandlerFunc {
	return func(c *gin.Context) {
		currentUserID, ok := utils.GetUserIDFromContext(c)
		if !ok {
			return
		}
		var req models.UploadCompleteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			xerr.AbortWithError(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid request body")
			return
		}

		newFile, err := uploadService.UploadComplete(c, currentUserID, &req)
		if err != nil {
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("Failed to complete upload: %v", err))
			return
		}

		xerr.Success(c, http.StatusOK, "File uploaded and merged successfully", newFile)
	}
}
