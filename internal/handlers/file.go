package handlers

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/ginutils"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/3Eeeecho/go-clouddisk/internal/services"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type CreateFolderRequest struct {
	FolderName     string  `json:"folder_name" binding:"required"`
	ParentFolderID *uint64 `json:"parent_folder_id"` // 可选，根目录为 null
}

// 定义 RenameFileRequest 结构体
type RenameFileRequest struct {
	NewFileName string `json:"new_file_name" binding:"required"`
}

// @Summary 获取用户文件列表
// @Description 获取当前用户指定文件夹下的文件和文件夹列表
// @Tags 文件
// @Produce json
// @Param parent_id query int false "父文件夹ID"
// @Success 200 {object} map[string]interface{} "文件列表"
// @Failure 400 {object} map[string]interface{} "参数错误"
// @Router /api/v1/files/ [get]
func ListUserFiles(fileService services.FileService, cfg *config.Config) gin.HandlerFunc {
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

// @Summary 上传文件
// @Description 上传文件到指定文件夹
// @Tags 文件
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "文件内容"
// @Param parent_folder_id formData int false "父文件夹ID"
// @Success 201 {object} map[string]interface{} "上传成功"
// @Failure 400 {object} map[string]interface{} "参数错误"
// @Router /api/v1/files/upload [post]
func UploadFile(fileService services.FileService, cfg *config.Config) gin.HandlerFunc {
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
		uploadedFile, err := fileService.UploadFile(currentUserID, originalName, mimeType, uint64(filesize), parentFolderID, tempFile)
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

// @Summary 创建文件夹
// @Description 在指定目录下创建文件夹
// @Tags 文件
// @Accept json
// @Produce json
// @Param data body CreateFolderRequest true "文件夹信息"
// @Success 201 {object} map[string]interface{} "创建成功"
// @Failure 400 {object} map[string]interface{} "参数错误"
// @Router /api/v1/files/folder [post]
func CreateFolder(fileService services.FileService, cfg *config.Config) gin.HandlerFunc {
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

// @Summary 下载文件
// @Description 下载指定ID的文件
// @Tags 文件
// @Produce application/octet-stream
// @Param file_id path int true "文件ID"
// @Success 200 {file} file "文件内容"
// @Failure 400 {object} map[string]interface{} "参数错误"
// @Router /api/v1/files/download/{file_id} [get]
func DownloadFile(fileService services.FileService, cfg *config.Config) gin.HandlerFunc {
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
	}
}

// @Summary 删除文件或文件夹（软删除）
// @Description 将文件或文件夹移动到回收站
// @Tags 文件
// @Param file_id path int true "文件ID"
// @Success 200 {object} map[string]interface{} "删除成功"
// @Failure 400 {object} map[string]interface{} "参数错误"
// @Router /api/v1/files/softdelete/{file_id} [delete]
func SoftDeleteFile(fileService services.FileService, cfg *config.Config) gin.HandlerFunc {
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

// @Summary 彻底删除文件或文件夹（永久删除）
// @Description 将文件或文件夹彻底删除
// @Tags 文件
// @Param file_id path int true "文件ID"
// @Success 200 {object} map[string]interface{} "删除成功"
// @Failure 400 {object} map[string]interface{} "参数错误"
// @Router /api/v1/files/permanentdelete/{file_id} [delete]
func PermanentDeleteFile(fileService services.FileService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		currentUserID, ok := ginutils.GetUserIDFromContext(c)
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
// @Param
// @Success 200 {object} map[string]interface{} "获取成功"
// @Failure 500 {object} map[string]interface{} "内部错误"
// @Router /api/v1/files/recyclebin [get]
func ListRecycleBinFiles(fileService services.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		currentUserID, ok := ginutils.GetUserIDFromContext(c)
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

func RestoreFile(fileService services.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		currentUserID, ok := ginutils.GetUserIDFromContext(c)
		if !ok {
			return
		}

		fileIDStr := c.Param("file_id")
		fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
		if err != nil {
			xerr.Error(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid file ID format")
			return
		}

		// 不再需要解析请求体

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

// RenameFile 处理文件/文件夹的改名请求
// PUT /api/v1/files/rename/:id
func RenameFile(fileService services.FileService) gin.HandlerFunc {
	return func(c *gin.Context) {
		fileIDStr := c.Param("id")
		fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
		if err != nil {
			logger.Warn("RenameFile: Invalid file ID format", zap.String("fileIDStr", fileIDStr), zap.Error(err))
			xerr.AbortWithError(c, http.StatusBadRequest, xerr.CodeInvalidParams, "Invalid file ID")
			return
		}

		currentUserID, ok := ginutils.GetUserIDFromContext(c)
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
