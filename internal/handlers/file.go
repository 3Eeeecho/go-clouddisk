package handlers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
	"github.com/3Eeeecho/go-clouddisk/internal/services"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ListUserFiles 获取用户文件列表
func ListUserFiles(db *gorm.DB, cfg *config.Config) gin.HandlerFunc {
	fileRepo := repositories.NewFileRepository(db)
	userRepo := repositories.NewUserRepository(db)
	fileService := services.NewFileService(fileRepo, userRepo, cfg)
	return func(c *gin.Context) {
		userID, exists := c.Get("userID")
		if !exists {
			xerr.AbortWithError(c, http.StatusInternalServerError, xerr.CodeInternalServerError, "User ID not found in context")
			return
		}

		currentUserID, ok := userID.(uint64)
		if !ok {
			xerr.AbortWithError(c, http.StatusInternalServerError, xerr.CodeInternalServerError, "Invalid user ID type in context")
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

// UploadFile 处理文件上传请求 (占位符)
func UploadFile(db *gorm.DB, cfg *config.Config) gin.HandlerFunc {
	fileRepo := repositories.NewFileRepository(db)
	userRepo := repositories.NewUserRepository(db)
	fileService := services.NewFileService(fileRepo, userRepo, cfg)
	return func(c *gin.Context) {
		// 从 AuthMiddleware 中获取用户ID
		userID, exists := c.Get("userID")
		if !exists {
			xerr.AbortWithError(c, http.StatusInternalServerError, xerr.CodeInternalServerError, "User ID not found in context")
			return
		}
		currentUserID, ok := userID.(uint64)
		if !ok {
			xerr.AbortWithError(c, http.StatusInternalServerError, xerr.CodeInternalServerError, "Invalid user ID type in context")
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
		uploadedFile, err := fileService.AddFile(currentUserID, originalName, mimeType, uint64(filesize), parentFolderID, tempFile)
		if err != nil {
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("Failed to upload file: %v", err))
			return
		}

		xerr.Success(c, http.StatusCreated, "File uploaded successfully", gin.H{
			"id":               uploadedFile.ID,
			"uuid":             uploadedFile.UUID,
			"filename":         uploadedFile.FileName, // 使用 FileName
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

// DownloadFile 处理文件下载请求 (占位符)
func DownloadFile(db *gorm.DB, cfg *config.Config) gin.HandlerFunc {
	// fileRepo := repositories.NewFileRepository(db)
	// userRepo := repositories.NewUserRepository(db)
	// fileService := services.NewFileService(fileRepo, userRepo)
	return func(c *gin.Context) {
		xerr.Success(c, http.StatusOK, "File download endpoint - To be implemented", nil)
	}
}

// DeleteFile 处理文件删除请求 (占位符)
func DeleteFile(db *gorm.DB, cfg *config.Config) gin.HandlerFunc {
	// fileRepo := repositories.NewFileRepository(db)
	// userRepo := repositories.NewUserRepository(db)
	// fileService := services.NewFileService(fileRepo, userRepo)
	return func(c *gin.Context) {
		xerr.Success(c, http.StatusOK, "File delete endpoint - To be implemented", nil)
	}
}
