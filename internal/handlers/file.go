package handlers

import (
	"fmt"
	"net/http"
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
	fileService := services.NewFileService(fileRepo, userRepo)
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
		var parentFolderID uint64 = 0
		if parentFolderIDStr != "" {
			parsedID, err := strconv.ParseUint(parentFolderIDStr, 10, 64)
			if err != nil {
				xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid parent_folder_id")
				return
			}
			parentFolderID = parsedID
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
	// fileRepo := repositories.NewFileRepository(db)
	// userRepo := repositories.NewUserRepository(db)
	// fileService := services.NewFileService(fileRepo, userRepo)
	return func(c *gin.Context) {
		xerr.Success(c, http.StatusOK, "File upload endpoint - To be implemented", nil)
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
