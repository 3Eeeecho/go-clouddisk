package handlers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/ginutils"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
	"github.com/3Eeeecho/go-clouddisk/internal/services"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type CreateFolderRequest struct {
	FolderName     string  `json:"folder_name" binding:"required"`
	ParentFolderID *uint64 `json:"parent_folder_id"` // 可选，根目录为 null
}

// ListUserFiles 获取用户文件列表
func ListUserFiles(db *gorm.DB, cfg *config.Config) gin.HandlerFunc {
	fileRepo := repositories.NewFileRepository(db)
	userRepo := repositories.NewUserRepository(db)
	fileService := services.NewFileService(fileRepo, userRepo, cfg)
	return func(c *gin.Context) {
		currentUserID, ok := ginutils.GetUserIDFromContext(c)
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

// UploadFile 处理文件上传请求 (占位符)
func UploadFile(db *gorm.DB, cfg *config.Config) gin.HandlerFunc {
	fileRepo := repositories.NewFileRepository(db)
	userRepo := repositories.NewUserRepository(db)
	fileService := services.NewFileService(fileRepo, userRepo, cfg)
	return func(c *gin.Context) {
		// 获取用户ID
		currentUserID, ok := ginutils.GetUserIDFromContext(c)
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

func CreateFolder(db *gorm.DB, cfg *config.Config) gin.HandlerFunc {
	fileRepo := repositories.NewFileRepository(db)
	userRepo := repositories.NewUserRepository(db)
	fileService := services.NewFileService(fileRepo, userRepo, cfg)

	return func(c *gin.Context) {
		currentUserID, ok := ginutils.GetUserIDFromContext(c)
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
			"parent_folder_id": folder.ParentFolderID,
			"is_folder":        folder.IsFolder,
			"created_at":       folder.CreatedAt,
		})
	}
}

// DownloadFile 处理文件下载请求 (占位符)
func DownloadFile(db *gorm.DB, cfg *config.Config) gin.HandlerFunc {
	fileRepo := repositories.NewFileRepository(db)
	userRepo := repositories.NewUserRepository(db)
	fileService := services.NewFileService(fileRepo, userRepo, cfg)
	return func(c *gin.Context) {
		fileIDStr := c.Param("file_id")
		fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
		if err != nil {
			xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid file ID format")
			return
		}

		currentUserID, ok := ginutils.GetUserIDFromContext(c)
		if !ok {
			return
		}

		fileModel, fileReader, err := fileService.GetFileForDownload(currentUserID, fileID)
		if err != nil {
			if err.Error() == "file not found" || err.Error() == "physical file not found on disk" {
				xerr.Error(c, http.StatusNotFound, xerr.CodeNotFound, err.Error())
				return
			}
			if err.Error() == "access denied: file does not belong to user" {
				xerr.Error(c, http.StatusForbidden, xerr.CodeForbidden, err.Error())
				return
			}
			if err.Error() == "cannot download a folder" {
				xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, err.Error())
				return
			}
			if err.Error() == "file is not available for download" {
				xerr.Error(c, http.StatusGone, xerr.CodeFileNotAvailable, err.Error()) // 例如，文件已在回收站
				return
			}
			xerr.Error(c, http.StatusInternalServerError, xerr.CodeInternalServerError, fmt.Sprintf("Failed to prepare file for download: %v", err))
			return
		}
		defer fileReader.Close() // 确保文件读取器关闭

		// 尝试将 fileReader 转换为 *os.File，以便利用其 Stat() 方法和 ReadSeekCloser 接口
		// http.ServeContent 最好接收一个 io.ReadSeeker 接口
		var seekableReader io.ReadSeeker
		var lastModified time.Time
		if osFile, ok := fileReader.(*os.File); ok {
			fileInfo, statErr := osFile.Stat()
			if statErr == nil {
				seekableReader = osFile
				lastModified = fileInfo.ModTime()
			} else {
				// 如果 Stat 失败，仍然可以使用普通的 io.Reader，但 ServeContent 功能会受限
				// 对于这种情况，http.ServeContent 内部会进行 io.Copy
				seekableReader = fileReader.(io.ReadSeeker) // 这里强制转换，因为它必须是 ReadSeeker
				lastModified = fileModel.UpdatedAt          // 使用数据库中的更新时间作为 Last-Modified
			}
		} else {
			// 如果不是 *os.File，则假设它已经是 io.ReadSeeker 并且没有更具体的 FileInfo
			seekableReader = fileReader.(io.ReadSeeker)
			lastModified = fileModel.UpdatedAt
		}

		// 设置文件名
		fileName := fileModel.FileName

		// 使用 http.ServeContent 进行下载
		// ServeContent 会自动处理 Content-Length, Content-Type, Content-Disposition, Range 请求等
		http.ServeContent(c.Writer, c.Request, fileName, lastModified, seekableReader)

		// 注意：io.Copy(c.Writer, fileReader) 这行代码不再需要，
		// 因为 http.ServeContent 已经处理了文件内容的写入。
	}
}

// DeleteFile 处理文件删除请求 (占位符)
func DeleteFile(db *gorm.DB, cfg *config.Config) gin.HandlerFunc {
	fileRepo := repositories.NewFileRepository(db)
	userRepo := repositories.NewUserRepository(db)
	fileService := services.NewFileService(fileRepo, userRepo, cfg)
	return func(c *gin.Context) {
		fileIDStr := c.Param("file_id")
		fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
		if err != nil {
			xerr.AbortWithError(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid file ID format")
		}

		currentUserID, ok := ginutils.GetUserIDFromContext(c)
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
