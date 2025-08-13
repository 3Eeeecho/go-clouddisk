package handlers

import (
	"context"
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
	"github.com/3Eeeecho/go-clouddisk/internal/services/explorer"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type FileHandler struct {
	fileService explorer.FileService
	cfg         *config.Config
}

func NewFileHandler(fileService explorer.FileService, cfg *config.Config) *FileHandler {
	return &FileHandler{
		fileService: fileService,
		cfg:         cfg,
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
func (h *FileHandler) GetSpecificFile(c *gin.Context) {
	currentUserID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	fileIDStr := c.Param("file_id")
	fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "Invalid file ID format")
		return
	}

	files, err := h.fileService.GetFileByID(currentUserID, fileID)
	if err != nil {
		if errors.Is(err, xerr.ErrFileNotFound) {
			response.Error(c, http.StatusNotFound, xerr.FileNotFoundCode, err.Error())
			return
		}
		response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "Failed to get file info")
		return
	}
	response.Success(c, http.StatusOK, "File info retrieved successfully", files)
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
func (h *FileHandler) ListUserFiles(c *gin.Context) {
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
			response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "Invalid parent_folder_id")
			return
		}
		parentFolderID = &parsedID
	}

	files, err := h.fileService.GetFilesByUserID(currentUserID, parentFolderID)
	if err != nil {
		if errors.Is(err, xerr.ErrDirectoryNotFound) {
			response.Error(c, http.StatusBadRequest, xerr.DirectoryNotFoundCode, err.Error())
			return
		}
		response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "Failed to list files")
		return
	}

	response.Success(c, http.StatusOK, "Files listed successfully", files)
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
func (h *FileHandler) CreateFolder(c *gin.Context) {
	currentUserID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	var req CreateFolderRequest
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "Invalid request payload")
		return
	}

	folder, err := h.fileService.CreateFolder(currentUserID, req.FolderName, req.ParentFolderID)
	if err != nil {
		if errors.Is(err, xerr.ErrDirectoryNotFound) {
			response.Error(c, http.StatusBadRequest, xerr.DirectoryNotFoundCode, err.Error())
			return
		}
		if errors.Is(err, xerr.ErrFileAlreadyExists) {
			response.Error(c, http.StatusConflict, xerr.FileAlreadyExistsCode, err.Error())
			return
		}
		response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "Failed to create folder")
		return
	}

	response.Success(c, http.StatusCreated, "Folder created successfully", gin.H{
		"id":               folder.ID,
		"uuid":             folder.UUID,
		"folder_name":      folder.FileName,
		"path":             folder.Path,
		"parent_folder_id": folder.ParentFolderID,
		"is_folder":        folder.IsFolder,
		"created_at":       folder.CreatedAt,
	})
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
func (h *FileHandler) DownloadFile(c *gin.Context) {
	fileIDStr := c.Param("file_id")
	fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "Invalid file ID format")
		return
	}

	currentUserID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	file, reader, err := h.fileService.Download(c.Request.Context(), currentUserID, fileID)
	if err != nil {
		if errors.Is(err, xerr.ErrFileNotFound) {
			response.Error(c, http.StatusNotFound, xerr.FileNotFoundCode, err.Error())
		} else if errors.Is(err, xerr.ErrPermissionDenied) {
			response.Error(c, http.StatusForbidden, xerr.PermissionDeniedCode, err.Error())
		} else if errors.Is(err, xerr.ErrCannotDownloadFolder) {
			response.Error(c, http.StatusBadRequest, xerr.CannotDownloadFolderCode, err.Error())
		} else {
			logger.Error("DownloadFile: Failed to download file", zap.Uint64("fileID", fileID), zap.Error(err))
			response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "Failed to download file")
		}
		return
	}
	defer reader.Close()

	contentDisposition := fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		url.PathEscape(file.FileName),
		url.PathEscape(file.FileName),
	)
	c.Header("Content-Disposition", contentDisposition)
	c.Header("Content-Type", *file.MimeType)
	c.Header("Content-Length", fmt.Sprintf("%d", file.Size))

	_, err = io.Copy(c.Writer, reader)
	if err != nil {
		logger.Error("DownloadFile: Failed to stream file content", zap.Uint64("fileID", fileID), zap.Error(err))
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
func (h *FileHandler) DownloadFolder(c *gin.Context) {
	currentUserID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	folderIDStr := c.Param("id")
	folderID, err := strconv.ParseUint(folderIDStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "invalid folder ID")
		return
	}

	folder, zipReader, err := h.fileService.Download(context.Background(), currentUserID, folderID)
	if err != nil {
		if errors.Is(err, xerr.ErrFileNotFound) {
			response.Error(c, http.StatusNotFound, xerr.FileNotFoundCode, err.Error())
		} else if errors.Is(err, xerr.ErrPermissionDenied) {
			response.Error(c, http.StatusForbidden, xerr.PermissionDeniedCode, err.Error())
		} else if errors.Is(err, xerr.ErrTargetNotFolder) {
			response.Error(c, http.StatusBadRequest, xerr.TargetNotFolderCode, "Cannot download a file using folder download endpoint")
		} else {
			logger.Error("DownloadFolder: Failed to get folder for download", zap.Uint64("folderID", folderID), zap.Uint64("userID", currentUserID), zap.Error(err))
			response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "failed to prepare folder for download")
		}
		return
	}
	defer zipReader.Close()

	downloadFileName := fmt.Sprintf("%s.zip", folder.FileName)
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", downloadFileName))
	c.Header("Content-Transfer-Encoding", "binary")

	_, err = io.Copy(c.Writer, zipReader)
	if err != nil {
		logger.Error("DownloadFolder: Failed to write ZIP stream to HTTP response", zap.Uint64("folderID", folderID), zap.Uint64("userID", currentUserID), zap.Error(err))
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
func (h *FileHandler) SoftDeleteFile(c *gin.Context) {
	fileIDStr := c.Param("file_id")
	fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "Invalid file ID format")
		return
	}

	currentUserID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	err = h.fileService.SoftDelete(currentUserID, fileID)
	if err != nil {
		if errors.Is(err, xerr.ErrFileNotFound) {
			response.Error(c, http.StatusNotFound, xerr.FileNotFoundCode, err.Error())
			return
		}
		if errors.Is(err, xerr.ErrPermissionDenied) {
			response.Error(c, http.StatusForbidden, xerr.PermissionDeniedCode, err.Error())
			return
		}
		response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "Failed to delete file")
		return
	}
	response.Success(c, http.StatusOK, fmt.Sprintf("File/Folder %d soft-deleted successfully", fileID), nil)
}

// @Summary 彻底删除文件或文件夹（永久删除）
// @Description 将文件或文件夹彻底删除
// @Tags 文件
// @Security BearerAuth
// @Param file_id path int true "文件ID"
// @Success 200 {object} xerr.Response "删除成功"
// @Failure 400 {object} xerr.Response "参数错误"
// @Router /api/v1/files/permanentdelete/{file_id} [delete]
func (h *FileHandler) PermanentDeleteFile(c *gin.Context) {
	currentUserID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	fileIDStr := c.Param("file_id")
	fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "Invalid file ID format")
		return
	}

	err = h.fileService.PermanentDelete(currentUserID, fileID)
	if err != nil {
		if errors.Is(err, xerr.ErrFileNotFound) {
			response.Error(c, http.StatusNotFound, xerr.FileNotFoundCode, err.Error())
			return
		}
		if errors.Is(err, xerr.ErrPermissionDenied) {
			response.Error(c, http.StatusForbidden, xerr.PermissionDeniedCode, err.Error())
			return
		}
		if errors.Is(err, xerr.ErrDirNotEmpty) {
			response.Error(c, http.StatusBadRequest, xerr.DirNotEmptyCode, err.Error())
			return
		}
		response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "Failed to permanently delete file")
		return
	}

	response.Success(c, http.StatusOK, fmt.Sprintf("File/Folder %d permanently deleted successfully", fileID), nil)
}

// @Summary 列出回收站中的文件
// @Description 列出用户回收站中的所有文件
// @Tags 文件
// @Security BearerAuth
// @Success 200 {object} xerr.Response "获取成功"
// @Failure 500 {object} xerr.Response "内部错误"
// @Router /api/v1/files/recyclebin [get]
func (h *FileHandler) ListRecycleBinFiles(c *gin.Context) {
	currentUserID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	files, err := h.fileService.ListRecycleBinFiles(currentUserID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "Failed to list recycle bin files")
		return
	}

	response.Success(c, http.StatusOK, "Recycle bin files listed successfully", files)
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
func (h *FileHandler) RestoreFile(c *gin.Context) {
	currentUserID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	fileIDStr := c.Param("file_id")
	fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "Invalid file ID format")
		return
	}

	err = h.fileService.RestoreFile(currentUserID, fileID)
	if err != nil {
		if errors.Is(err, xerr.ErrFileNotInRecycleBin) {
			response.Error(c, http.StatusBadRequest, xerr.FileNotInRecycleBinCode, err.Error())
			return
		}
		if errors.Is(err, xerr.ErrPermissionDenied) {
			response.Error(c, http.StatusForbidden, xerr.PermissionDeniedCode, err.Error())
			return
		}
		if errors.Is(err, xerr.ErrFileAlreadyExists) {
			response.Error(c, http.StatusConflict, xerr.FileAlreadyExistsCode, err.Error())
			return
		}
		response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "Failed to restore file")
		return
	}

	response.Success(c, http.StatusOK, fmt.Sprintf("File/Folder %d restored successfully", fileID), nil)
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
func (h *FileHandler) RenameFile(c *gin.Context) {
	fileIDStr := c.Param("id")
	fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "Invalid file ID")
		return
	}

	currentUserID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	var req RenameFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "Invalid request body: "+err.Error())
		return
	}

	renamedFile, err := h.fileService.RenameFile(currentUserID, fileID, req.NewFileName)
	if err != nil {
		if errors.Is(err, xerr.ErrFileNotFound) {
			response.Error(c, http.StatusNotFound, xerr.FileNotFoundCode, err.Error())
		} else if errors.Is(err, xerr.ErrPermissionDenied) {
			response.Error(c, http.StatusForbidden, xerr.PermissionDeniedCode, err.Error())
		} else if errors.Is(err, xerr.ErrFileStatusInvalid) {
			response.Error(c, http.StatusBadRequest, xerr.FileStatusInvalidCode, err.Error())
		} else if errors.Is(err, xerr.ErrFileAlreadyExists) {
			response.Error(c, http.StatusConflict, xerr.FileAlreadyExistsCode, err.Error())
		} else {
			response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "Failed to rename file")
		}
		return
	}

	response.Success(c, http.StatusOK, "File/folder renamed successfully", gin.H{
		"file_info": renamedFile,
	})
}

// MoveFileRequest 移动文件的请求体
type MoveFileRequest struct {
	FileID               uint64  `json:"file_id" binding:"required"`
	TargetParentFolderID *uint64 `json:"target_parent_folder_id"`
}

// @Summary 移动文件/文件夹
// @Description 移动指定文件或文件夹到新的父文件夹下
// @Tags 文件
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body MoveFileRequest true "移动文件请求体"
// @Success 200 {object} xerr.Response "成功移动后的文件/文件夹信息"
// @Failure 400 {object} xerr.Response "参数错误"
// @Failure 403 {object} xerr.Response "权限不足"
// @Failure 404 {object} xerr.Response "文件或目标文件夹未找到"
// @Failure 409 {object} xerr.Response "目标位置已存在同名文件/文件夹"
// @Router /api/v1/files/move [post]
func (h *FileHandler) MoveFile(c *gin.Context) {
	var req MoveFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "Invalid request body format")
		return
	}

	currentUserID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	movedFile, err := h.fileService.MoveFile(currentUserID, req.FileID, req.TargetParentFolderID)
	if err != nil {
		if errors.Is(err, xerr.ErrFileNotFound) {
			response.Error(c, http.StatusNotFound, xerr.FileNotFoundCode, "File or folder to move not found")
		} else if errors.Is(err, xerr.ErrDirectoryNotFound) {
			response.Error(c, http.StatusNotFound, xerr.DirectoryNotFoundCode, "Target parent folder not found")
		} else if errors.Is(err, xerr.ErrPermissionDenied) {
			response.Error(c, http.StatusForbidden, xerr.PermissionDeniedCode, err.Error())
		} else if errors.Is(err, xerr.ErrCannotMoveRoot) {
			response.Error(c, http.StatusBadRequest, xerr.CannotMoveRootCode, err.Error())
		} else if errors.Is(err, xerr.ErrCannotMoveIntoSubtree) {
			response.Error(c, http.StatusBadRequest, xerr.CannotMoveIntoSubtreeCode, err.Error())
		} else if errors.Is(err, xerr.ErrTargetNotFolder) {
			response.Error(c, http.StatusBadRequest, xerr.TargetNotFolderCode, err.Error())
		} else if errors.Is(err, xerr.ErrFileAlreadyExists) {
			response.Error(c, http.StatusConflict, xerr.FileAlreadyExistsCode, "Name conflict in target location")
		} else {
			response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "Failed to move file/folder")
		}
		return
	}

	response.Success(c, http.StatusOK, "File/folder moved successfully", gin.H{
		"file_info": movedFile,
	})
}

// @Summary 删除文件版本
// @Description 删除指定文件的指定版本
// @Tags 文件
// @Security BearerAuth
// @Param file_id path int true "文件ID"
// @Param version_id path int true "版本ID"
// @Success 200 {object} xerr.Response "删除成功"
// @Failure 400 {object} xerr.Response "参数错误"
// @Router /api/v1/files/{file_id}/versions/{version_id} [delete]
func (h *FileHandler) DeleteFileVersion(c *gin.Context) {
	currentUserID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	fileIDStr := c.Param("file_id")
	fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "Invalid file ID format")
		return
	}

	versionID := c.Param("version_id")

	err = h.fileService.DeleteFileVersion(currentUserID, fileID, versionID)
	if err != nil {
		if errors.Is(err, xerr.ErrFileNotFound) {
			response.Error(c, http.StatusNotFound, xerr.FileNotFoundCode, err.Error())
		} else if errors.Is(err, xerr.ErrPermissionDenied) {
			response.Error(c, http.StatusForbidden, xerr.PermissionDeniedCode, err.Error())
		} else {
			logger.Error("DeleteFileVersion: Failed to delete file version", zap.Uint64("fileID", fileID), zap.String("versionID", versionID), zap.Error(err))
			response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "Failed to delete file version")
		}
		return
	}

	response.Success(c, http.StatusOK, "File version deleted successfully", nil)
}

// @Summary 列举文件版本
// @Description 列举指定文件的所有版本记录
// @Tags 文件
// @Security BearerAuth
// @Param file_id path int true "文件ID"
// @Success 200 {object} xerr.Response "列举成功"
// @Failure 400 {object} xerr.Response "参数错误"
// @Router /api/v1/files/versions/{file_id} [get]
func (h *FileHandler) ListFileVersions(c *gin.Context) {
	currentUserID, ok := utils.GetUserIDFromContext(c)
	if !ok {
		return
	}

	fileIDStr := c.Param("file_id")
	fileID, err := strconv.ParseUint(fileIDStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, xerr.InvalidParamsCode, "Invalid file ID format")
		return
	}

	versions, err := h.fileService.ListFileVersions(currentUserID, fileID)
	if err != nil {
		if errors.Is(err, xerr.ErrFileNotFound) {
			response.Error(c, http.StatusNotFound, xerr.FileNotFoundCode, err.Error())
		} else if errors.Is(err, xerr.ErrPermissionDenied) {
			response.Error(c, http.StatusForbidden, xerr.PermissionDeniedCode, err.Error())
		} else {
			logger.Error("ListFileVersions: Failed to list file versions", zap.Uint64("fileID", fileID), zap.Error(err))
			response.Error(c, http.StatusInternalServerError, xerr.InternalServerErrorCode, "Failed to list file versions")
		}
		return
	}

	response.Success(c, http.StatusOK, "File versions list successfully", gin.H{
		"file_versions": versions,
	})
}
