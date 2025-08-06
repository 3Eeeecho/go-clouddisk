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
