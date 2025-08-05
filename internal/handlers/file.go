package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/utils"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/3Eeeecho/go-clouddisk/internal/services/explorer"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// @Summary 获取用户文件列表
// @Description 获取当前用户指定文件夹下的文件和文件夹列表
// @Tags 文件
// @Produce json
// @Security BearerAuth
// @Param parent_id query int false "父文件夹ID"
// @Success 200 {object} xerr.Response "文件列表"
// @Failure 400 {object} xerr.Response "参数错误"
// @Router /api/v1/files/ [get]
func GetSpecificFile(fileService explorer.FileService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		currentUserID, ok := utils.GetUserIDFromContext(c)
		if !ok {
			return
		}

		fileIDStr := c.Param("file_id")
		fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
		if err != nil {
			xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid file ID format")
			return
		}

		files, err := fileService.GetFileByID(currentUserID, fileID)
		if err != nil {
			if err.Error() == "not found file" {
				xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, err.Error())
				return
			}
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("Failed to list files: %v", err))
			return
		}

		xerr.Success(c, http.StatusOK, "Files listed successfully", files)
	}
}

// @Summary 获取用户文件列表
// @Description 获取当前用户指定文件夹下的文件和文件夹列表
// @Tags 文件
// @Produce json
// @Security BearerAuth
// @Param parent_id query int false "父文件夹ID"
// @Success 200 {object} xerr.Response "文件列表"
// @Failure 400 {object} xerr.Response "参数错误"
// @Router /api/v1/files/ [get]
func ListUserFiles(fileService explorer.FileService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		currentUserID, ok := utils.GetUserIDFromContext(c)
		if !ok {
			return
		}

		//获取父文件夹ID (可选)，如果未提供或无效，则默认为根目录 (0)
		parentFolderIDStr := c.Query("parent_id")
		var parentFolderID *uint64
		if parentFolderIDStr != "" {
			parsedID, err := strconv.ParseUint(parentFolderIDStr, 10, 64)
			if err != nil {
				xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid parent_folder_id")
				return
			}
			parentFolderID = &parsedID
		}

		files, err := fileService.GetFilesByUserID(currentUserID, parentFolderID)
		if err != nil {
			if err.Error() == "parent folder not found" || err.Error() == "invalid parent folder or not a folder" {
				xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, err.Error())
				return
			}
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("Failed to list files: %v", err))
			return
		}

		xerr.Success(c, http.StatusOK, "Files listed successfully", files)
	}
}

// @Summary 上传文件
// @Description 上传文件到指定文件夹
// @Tags 文件
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param file formData file true "文件内容"
// @Param parent_folder_id formData int false "父文件夹ID"
// @Success 201 {object} xerr.Response "上传成功"
// @Failure 400 {object} xerr.Response "参数错误"
// @Router /api/v1/files/upload [post]
func UploadFile(fileService explorer.FileService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取用户ID
		currentUserID, ok := utils.GetUserIDFromContext(c)
		if !ok {
			return
		}

		// 1. 解析文件表单
		fileHeader, err := c.FormFile("file")
		if err != nil {
			xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, fmt.Sprintf("Failed to get file from form: %v", err))
			return
		}

		//获取其他表单字段
		originalName := fileHeader.Filename
		filesize := fileHeader.Size
		mimeType := fileHeader.Header.Get("Content-Type")
		//如果客户端未设置Content-Type，则回退
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		// 获取 parent_folder_id (可选)
		parentFolderIDStr := c.PostForm("parent_folder_id")
		var parentFolderID *uint64 = nil // 使用指针类型，默认为 nil
		if parentFolderIDStr != "" {
			parsedID, err := strconv.ParseUint(parentFolderIDStr, 10, 64)
			if err != nil {
				xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid parent_folder_id format")
				return
			}
			parentFolderID = &parsedID //  赋值时取地址
		}

		// 2. 将文件内容写入临时文件，以便服务层可以多次读取
		tempFile, err := os.CreateTemp("", "upload-*.tmp")
		if err != nil {
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("Failed to create temporary file: %v", err))
			return
		}
		//defer 先进后出，先执行close再remove
		defer os.Remove(tempFile.Name())
		defer tempFile.Close()

		fileStream, err := fileHeader.Open()
		if err != nil {
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("Failed to open uploaded file stream: %v", err))
			return
		}
		defer fileStream.Close()

		// 将上传文件流写入临时文件
		_, err = io.Copy(tempFile, fileStream)
		if err != nil {
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("Failed to write to temporary file: %v", err))
			return
		}
		tempFile.Seek(0, 0) // 将临时文件指针重置到开头，以便 AddFile 可以读取

		// 3. 调用文件服务处理文件
		uploadedFile, err := fileService.UploadFile(currentUserID, originalName, mimeType, uint64(filesize), parentFolderID, tempFile)
		if err != nil {
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("Failed to upload file: %v", err))
			return
		}

		xerr.Success(c, http.StatusCreated, "File uploaded successfully", gin.H{
			"id":               uploadedFile.ID,
			"uuid":             uploadedFile.UUID,
			"filename":         uploadedFile.FileName, // 使用 FileName
			"path":             uploadedFile.Path,
			"is_folder":        uploadedFile.IsFolder,
			"size":             uploadedFile.Size,
			"mime_type":        uploadedFile.MimeType,
			"oss_bucket":       uploadedFile.OssBucket,
			"oss_key":          uploadedFile.OssKey,
			"md5_hash":         uploadedFile.MD5Hash,
			"parent_folder_id": uploadedFile.ParentFolderID,
		})
	}
}

type CreateFolderRequest struct {
	FolderName     string  `json:"folder_name" binding:"required"`
	ParentFolderID *uint64 `json:"parent_folder_id"` // 可选，根目录为 null
}

// @Summary 创建文件夹
// @Description 在指定目录下创建文件夹
// @Tags 文件
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param data body CreateFolderRequest true "文件夹信息"
// @Success 201 {object} xerr.Response "创建成功"
// @Failure 400 {object} xerr.Response "参数错误"
// @Router /api/v1/files/folder [post]
func CreateFolder(fileService explorer.FileService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		currentUserID, ok := utils.GetUserIDFromContext(c)
		if !ok {
			return
		}

		var req CreateFolderRequest
		if err := c.ShouldBindBodyWithJSON(&req); err != nil {
			xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, fmt.Sprintf("Invalid request payload: %v", err))
			return
		}

		folder, err := fileService.CreateFolder(currentUserID, req.FolderName, req.ParentFolderID)
		if err != nil {
			if err.Error() == "parent folder not found" || err.Error() == "invalid parent folder or not a folder" {
				xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, err.Error())
				return
			}
			if err.Error() == "folder with this name already exists in the current directory" {
				xerr.Error(c, http.StatusConflict, xerr.CodeResourceAlreadyExists, err.Error())
				return
			}
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("Failed to create folder: %v", err))
			return
		}

		xerr.Success(c, http.StatusCreated, "Folder created successfully", gin.H{
			"id":               folder.ID,
			"uuid":             folder.UUID,
			"folder_name":      folder.FileName,
			"path":             folder.Path,
			"parent_folder_id": folder.ParentFolderID,
			"is_folder":        folder.IsFolder,
			"created_at":       folder.CreatedAt,
		})
	}
}

// @Summary 下载文件
// @Description 下载指定ID的文件
// @Tags 文件
// @Produce application/octet-stream
// @Security BearerAuth
// @Param file_id path int true "文件ID"
// @Success 200 {file} file "文件内容"
// @Failure 400 {object} xerr.Response "参数错误"
// @Router /api/v1/files/download/{file_id} [get]
func DownloadFile(fileService explorer.FileService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		fileIDStr := c.Param("file_id")
		fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
		if err != nil {
			xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid file ID format")
			return
		}

		currentUserID, ok := utils.GetUserIDFromContext(c)
		if !ok {
			return
		}

		// 调用服务层，并传入 context
		file, reader, err := fileService.Download(c.Request.Context(), currentUserID, fileID) // <--- 传入 c.Request.Context()
		if err != nil {
			if strings.Contains(err.Error(), "文件未找到") || strings.Contains(err.Error(), "物理文件在云存储中未找到") {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			} else if strings.Contains(err.Error(), "无权") || strings.Contains(err.Error(), "无法下载文件夹") { // 增加文件夹错误判断
				c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			} else {
				logger.Error("DownloadFile: Failed to download file", zap.Uint64("fileID", fileID), zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("下载文件失败: %v", err)})
			}
			return
		}
		defer reader.Close() // 确保 reader 在请求结束后关闭

		// 设置响应头，强制浏览器下载文件

		// 构建 Content-Disposition 头，使用 RFC 6266 推荐的 filename* 参数支持 UTF-8
		// filename* 是标准解决方案，filename 用于向后兼容，但最好不要直接包含非ASCII字符
		contentDisposition := fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
			url.PathEscape(file.FileName), // 对于不支持 filename* 的旧浏览器，提供一个 URL 编码的兼容版本
			url.PathEscape(file.FileName), // 规范的 UTF-8 编码文件名 (这是首选)
		)
		c.Header("Content-Disposition", contentDisposition)
		c.Header("Content-Type", *file.MimeType) // 使用文件元数据中的 MimeType
		c.Header("Content-Length", fmt.Sprintf("%d", file.Size))
		// 将文件内容流式传输给客户端
		_, err = io.Copy(c.Writer, reader)
		if err != nil {
			logger.Error("DownloadFile: Failed to stream file content", zap.Uint64("fileID", fileID), zap.Error(err))
			// 注意：这里可能无法发送新的HTTP状态码，因为头部已经发送，但可以记录错误
			// c.JSON(http.StatusInternalServerError, gin.H{"error": "文件传输失败"}) // 不要在这里再发JSON响应
		}
	}
}

// @Summary 下载文件夹
// @Description 下载指定ID的文件夹，打包为ZIP格式
// @Tags 文件
// @Produce application/zip
// @Security BearerAuth
// @Param id path int true "文件夹ID"
// @Success 200 {file} file "文件夹ZIP包"
// @Failure 400 {object} xerr.Response "参数错误"
// @Failure 404 {object} xerr.Response "文件夹未找到"
// @Router /api/v1/files/download/folder/{id} [get]
func DownloadFolder(fileService explorer.FileService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := c.Get("userID") // 从 AuthMiddleware 获取 userID
		if !exists {
			logger.Error("DownloadFolder: UserID not found in context")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "user not authenticated"})
			return
		}
		currentUserID := userID.(uint64)

		folderIDStr := c.Param("id")
		folderID, err := strconv.ParseUint(folderIDStr, 10, 64)
		if err != nil {
			logger.Warn("DownloadFolder: Invalid folder ID format", zap.String("folderIDStr", folderIDStr), zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid folder ID"})
			return
		}

		folder, zipReader, err := fileService.Download(context.Background(), currentUserID, folderID)
		if err != nil {
			if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "access denied") {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			} else if strings.Contains(err.Error(), "cannot download a file") {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			} else {
				logger.Error("DownloadFolder: Failed to get folder for download",
					zap.Uint64("folderID", folderID),
					zap.Uint64("userID", currentUserID),
					zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare folder for download"})
			}
			return
		}
		defer zipReader.Close() // 确保在请求结束时关闭 zipReader

		// 设置响应头
		c.Header("Content-Type", "application/zip")
		// 建议使用 folder.FileName + ".zip" 作为下载文件名
		downloadFileName := fmt.Sprintf("%s.zip", folder.FileName)
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", downloadFileName))
		c.Header("Content-Transfer-Encoding", "binary")
		c.Header("Expires", "0")
		c.Header("Cache-Control", "must-revalidate")
		c.Header("Pragma", "public")

		// 流式传输 ZIP 数据
		_, err = io.Copy(c.Writer, zipReader)
		if err != nil {
			// io.Copy 错误通常表示客户端连接中断，或在写入过程中发生其他网络错误。
			// 这不一定是服务器内部错误，但应该记录。
			logger.Error("DownloadFolder: Failed to write ZIP stream to HTTP response",
				zap.Uint64("folderID", folderID),
				zap.Uint64("userID", currentUserID),
				zap.Error(err))
			// 通常不需要再次 c.JSON，因为头部可能已经发送
		}
		logger.Info("DownloadFolder: Folder ZIP stream sent successfully",
			zap.Uint64("folderID", folderID),
			zap.Uint64("userID", currentUserID))
	}
}

// @Summary 删除文件或文件夹（软删除）
// @Description 将文件或文件夹移动到回收站
// @Tags 文件
// @Security BearerAuth
// @Param file_id path int true "文件ID"
// @Success 200 {object} xerr.Response "删除成功"
// @Failure 400 {object} xerr.Response "参数错误"
// @Router /api/v1/files/softdelete/{file_id} [delete]
func SoftDeleteFile(fileService explorer.FileService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		fileIDStr := c.Param("file_id")
		fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
		if err != nil {
			xerr.AbortWithError(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid file ID format")
		}

		currentUserID, ok := utils.GetUserIDFromContext(c)
		if !ok {
			return
		}

		err = fileService.SoftDeleteFile(currentUserID, fileID)
		if err != nil {
			if err.Error() == "file or folder not found" {
				xerr.Error(c, http.StatusNotFound, xerr.CodeNotFound, err.Error())
				return
			}
			if err.Error() == "access denied: file or folder does not belong to user" {
				xerr.Error(c, http.StatusForbidden, xerr.CodeForbidden, err.Error())
				return
			}
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("Failed to delete file: %v", err))
			return
		}
		xerr.Success(c, http.StatusOK, fmt.Sprintf("File/Folder %d soft-deleted successfully", fileID), nil)
	}
}

// @Summary 彻底删除文件或文件夹（永久删除）
// @Description 将文件或文件夹彻底删除
// @Tags 文件
// @Security BearerAuth
// @Param file_id path int true "文件ID"
// @Success 200 {object} xerr.Response "删除成功"
// @Failure 400 {object} xerr.Response "参数错误"
// @Router /api/v1/files/permanentdelete/{file_id} [delete]
func PermanentDeleteFile(fileService explorer.FileService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		currentUserID, ok := utils.GetUserIDFromContext(c)
		if !ok {
			return // 辅助函数已经处理了错误响应
		}

		fileIDStr := c.Param("file_id")
		fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
		if err != nil {
			xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid file ID format")
			return
		}

		err = fileService.PermanentDeleteFile(currentUserID, fileID)
		if err != nil {
			if err.Error() == "file or folder not found" {
				xerr.Error(c, http.StatusNotFound, xerr.CodeNotFound, err.Error())
				return
			}
			if err.Error() == "access denied: file or folder does not belong to user" {
				xerr.Error(c, http.StatusForbidden, xerr.CodeForbidden, err.Error())
				return
			}
			if err.Error() == "folder is not empty, cannot permanently delete" {
				xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, err.Error())
				return
			}
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("Failed to permanently delete file: %v", err))
			return
		}

		xerr.Success(c, http.StatusOK, fmt.Sprintf("File/Folder %d permanently deleted successfully", fileID), nil)
	}
}

// @Summary 列出回收站中的文件
// @Description 列出用户回收站中的所有文件
// @Tags 文件
// @Security BearerAuth
// @Success 200 {object} xerr.Response "获取成功"
// @Failure 500 {object} xerr.Response "内部错误"
// @Router /api/v1/files/recyclebin [get]
func ListRecycleBinFiles(fileService explorer.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		currentUserID, ok := utils.GetUserIDFromContext(c)
		if !ok {
			return
		}

		files, err := fileService.ListRecycleBinFiles(currentUserID)
		if err != nil {
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("Failed to list recycle bin files: %v", err))
			return
		}

		xerr.Success(c, http.StatusOK, "Recycle bin files listed successfully", files)
	}
}

// @Summary 恢复文件/文件夹
// @Description 从回收站恢复文件或文件夹到原位置
// @Tags 文件
// @Security BearerAuth
// @Param file_id path int true "文件ID"
// @Success 200 {object} xerr.Response "恢复成功"
// @Failure 400 {object} xerr.Response "参数错误"
// @Failure 403 {object} xerr.Response "权限不足"
// @Failure 409 {object} xerr.Response "原位置已存在同名文件"
// @Router /api/v1/files/restore/{file_id} [post]
func RestoreFile(fileService explorer.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		currentUserID, ok := utils.GetUserIDFromContext(c)
		if !ok {
			return
		}

		fileIDStr := c.Param("file_id")
		fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
		if err != nil {
			xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid file ID format")
			return
		}

		err = fileService.RestoreFile(currentUserID, fileID)
		if err != nil {
			if err.Error() == "file or folder not found" || err.Error() == "file or folder is not in the recycle bin" {
				xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, err.Error())
				return
			}
			if err.Error() == "access denied: file or folder does not belong to user" {
				xerr.Error(c, http.StatusForbidden, xerr.CodeForbidden, err.Error())
				return
			}
			// 命名冲突的错误
			if err.Error() == "a file with the same name already exists in the original location" ||
				err.Error() == "a folder with the same name already exists in the original location" {
				xerr.Error(c, http.StatusConflict, xerr.CodeConflict, err.Error())
				return
			}
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("Failed to restore file: %v", err))
			return
		}

		xerr.Success(c, http.StatusOK, fmt.Sprintf("File/Folder %d restored successfully to its original location", fileID), nil)
	}
}

// 定义 RenameFileRequest 结构体
type RenameFileRequest struct {
	NewFileName string `json:"new_file_name" binding:"required"`
}

// @Summary 重命名文件/文件夹
// @Description 重命名指定的文件或文件夹
// @Tags 文件
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "文件ID"
// @Param data body RenameFileRequest true "重命名信息"
// @Success 200 {object} xerr.Response "重命名成功"
// @Failure 400 {object} xerr.Response "参数错误"
// @Failure 403 {object} xerr.Response "权限不足"
// @Failure 404 {object} xerr.Response "文件未找到"
// @Router /api/v1/files/rename/{id} [put]
func RenameFile(fileService explorer.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		fileIDStr := c.Param("id")
		fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
		if err != nil {
			logger.Warn("RenameFile: Invalid file ID format", zap.String("fileIDStr", fileIDStr), zap.Error(err))
			xerr.AbortWithError(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid file ID")
			return
		}

		currentUserID, ok := utils.GetUserIDFromContext(c)
		if !ok {
			return
		}

		var req RenameFileRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			logger.Warn("RenameFile: Invalid request body", zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
			return
		}

		// 业务逻辑调用
		renamedFile, err := fileService.RenameFile(currentUserID, fileID, req.NewFileName)
		if err != nil {
			// 记录详细错误日志
			logger.Error("RenameFile: Failed to rename file",
				zap.Uint64("fileID", fileID),
				zap.Uint64("userID", currentUserID),
				zap.String("newFileName", req.NewFileName),
				zap.Error(err))

			if errors.Is(err, errors.New("file or folder not found")) {
				xerr.AbortWithError(c, http.StatusNotFound, xerr.CodeNotFound, err.Error())
			} else if errors.Is(err, errors.New("access denied: file or folder does not belong to user")) {
				xerr.AbortWithError(c, http.StatusForbidden, xerr.CodeAccessDenied, err.Error()) // 403 Forbidden 更合适
			} else if errors.Is(err, errors.New("cannot rename a deleted or abnormal file/folder")) {
				xerr.AbortWithError(c, http.StatusBadRequest, xerr.CodeInvalidStatus, err.Error()) // 400 Bad Request
			} else if errors.Is(err, errors.New("invalid request body")) { // 这个错误应该在 ShouldBindJSON 处处理了
				xerr.AbortWithError(c, http.StatusBadRequest, xerr.CodeInvalidParams, err.Error())
			} else {
				// 对于其他未知的内部错误，返回 Internal Server Error
				xerr.AbortWithError(c, http.StatusInternalServerError, xerr.CodeInternalServerError, "Failed to rename file: "+err.Error())
			}
			return
		}

		logger.Info("RenameFile: File renamed successfully",
			zap.Uint64("fileID", fileID),
			zap.String("oldName", c.Param("id")), // 这里其实不是老名字，只是用于日志记录
			zap.String("newName", renamedFile.FileName))

		xerr.Success(c, http.StatusOK, "File/folder renamed successfully", gin.H{
			"file_info": renamedFile,
		})
	}
}

// MoveFileRequest 移动文件的请求体
type MoveFileRequest struct {
	FileID               uint64  `json:"file_id" binding:"required"` // 要移动的文件或文件夹的ID
	TargetParentFolderID *uint64 `json:"target_parent_folder_id"`    // 目标父文件夹的ID，nil表示移动到根目录
}

// @Summary 移动文件/文件夹
// @Description 移动指定文件或文件夹到新的父文件夹下
// @Tags 文件
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body MoveFileRequest true "移动文件请求体"
// @Success 200 {object} xerr.Response "成功移动后的文件/文件夹信息"
// @Failure 400 {object} xerr.Response "参数错误，例如文件ID或目标父文件夹ID无效，或目标不是文件夹"
// @Failure 403 {object} xerr.Response "权限不足，例如文件不属于当前用户，或无权访问目标文件夹"
// @Failure 404 {object} xerr.Response "文件或目标文件夹未找到"
// @Failure 409 {object} xerr.Response "目标位置已存在同名文件/文件夹"
// @Failure 500 {object} xerr.Response "内部服务器错误"
// @Router /api/v1/files/move [post]
func MoveFile(fileService explorer.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req MoveFileRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			logger.Error("MoveFile: Invalid request body", zap.Error(err))
			xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid request body format")
			return
		}

		currentUserID, ok := utils.GetUserIDFromContext(c)
		if !ok {
			// GetUserIDFromContext 应该会返回一个错误响应，这里只是双重检查
			return
		}

		logger.Debug("MoveFile: attempting to move file",
			zap.Uint64("userID", currentUserID),
			zap.Uint64("fileID", req.FileID),
			zap.Any("targetParentFolderID", req.TargetParentFolderID))

		// 调用 Service 层进行文件移动操作
		movedFile, err := fileService.MoveFile(currentUserID, req.FileID, req.TargetParentFolderID)
		if err != nil {
			logger.Error("MoveFile: Service call failed",
				zap.Uint64("userID", currentUserID),
				zap.Uint64("fileID", req.FileID),
				zap.Any("targetParentFolderID", req.TargetParentFolderID),
				zap.Error(err))

			// 根据 service 层返回的错误类型，返回不同的 HTTP 状态码
			switch err.Error() {
			case "file not found":
				xerr.Error(c, http.StatusNotFound, xerr.CodeNotFound, "File or folder to move not found")
			case "target folder not found":
				xerr.Error(c, http.StatusNotFound, xerr.CodeNotFound, "Target parent folder not found")
			case "access denied: file does not belong to you":
				xerr.Error(c, http.StatusForbidden, xerr.CodeForbidden, "Access denied: file/folder does not belong to you")
			case "access denied: target folder does not belong to you": // 或者更通用的权限不足
				xerr.Error(c, http.StatusForbidden, xerr.CodeForbidden, "Access denied: target folder is not accessible")
			case "cannot move root directory":
				xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Cannot move root directory")
			case "cannot move folder into its own subtree":
				xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Cannot move folder into its own subdirectory")
			case "target is not a folder":
				xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Target ID is not a folder")
			case "name conflict: file or folder with the same name already exists in target location":
				xerr.Error(c, http.StatusConflict, xerr.CodeConflict, "Name conflict: an item with the same name already exists in the target location")
			default:
				xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("Failed to move file/folder: %v", err))
			}
			return
		}

		logger.Info("MoveFile: File/folder moved successfully",
			zap.Uint64("userID", currentUserID),
			zap.Uint64("movedFileID", movedFile.ID),
			zap.String("newPath", movedFile.Path+movedFile.FileName)) // 记录新路径

		xerr.Success(c, http.StatusOK, "File/folder moved successfully", gin.H{
			"file_info": movedFile,
		})
	}
}
