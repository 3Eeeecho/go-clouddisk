package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// collectAllChildren 辅助函数：收集所有子项文件以及文件夹
// 这里使用 BFS (广度优先搜索) 遍历，避免深层递归导致的栈溢出
// 返回一个切片，包含所有需要处理的 models.File 记录
func (s *fileService) collectAllChildren(rootFileID uint64, userID uint64) ([]models.File, error) {
	logger.Debug("collectFilesForDeletion called", zap.Uint64("rootFileID", rootFileID), zap.Uint64("userID", userID))
	rootFile, err := s.fileRepo.FindByID(rootFileID)
	if err != nil {
		logger.Error("collectFilesForDeletion: root file not found", zap.Uint64("rootFileID", rootFileID), zap.Error(err))
		return nil, err // 错误会在调用方处理
	}
	filesToDelete := []models.File{*rootFile} // 包含根文件/文件夹自身

	// 如果是文件夹，查找所有子项
	if rootFile.IsFolder == 1 {
		// 使用队列进行 BFS
		queue := []uint64{rootFileID}
		processedIDs := make(map[uint64]bool) // 避免重复处理
		processedIDs[rootFileID] = true

		for len(queue) > 0 {
			currentFolderID := queue[0]
			queue = queue[1:]

			logger.Debug("collectFilesForDeletion: processing folder", zap.Uint64("currentFolderID", currentFolderID))
			var children []models.File
			// 注意：这里需要确保能够查到所有状态的子项，包括 Status 为 0 的，因为它们也应该被软删除或更新 deleted_at
			// FindByUserIDAndParentFolderID 默认只查 status=1，这里需要更灵活的查询
			childQuery := s.db.Unscoped().Where("user_id = ?", userID)
			if currentFolderID == 0 {
				childQuery = childQuery.Where("parent_folder_id IS NULL")
			} else {
				childQuery = childQuery.Where("parent_folder_id = ?", currentFolderID)
			}

			err = childQuery.Find(&children).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				logger.Error("collectFilesForDeletion: failed to retrieve children", zap.Uint64("folderID", currentFolderID), zap.Error(err))
				return nil, fmt.Errorf("failed to retrieve folder children: %w", err)
			}

			for _, child := range children {
				if !processedIDs[child.ID] {
					logger.Debug("collectFilesForDeletion: found child", zap.Uint64("childID", child.ID), zap.String("childName", child.FileName))
					filesToDelete = append(filesToDelete, child)
					processedIDs[child.ID] = true
					if child.IsFolder == 1 {
						queue = append(queue, child.ID)
					}
				}
			}
		}
	}
	logger.Info("collectFilesForDeletion finished", zap.Int("totalFiles", len(filesToDelete)), zap.Uint64("rootFileID", rootFileID))
	return filesToDelete, nil
}

// resolveFileNameConflict 检查指定父文件夹下是否有命名冲突，并返回一个不冲突的文件名。
// 如果存在冲突，它会自动在文件名后添加 (1), (2) 等后缀。
// currentFileID: 当前正在操作的文件/文件夹的 ID。在重命名自身时，需要排除它与自身名称的冲突检查。
// isFolder: 指示待检查的项是文件夹 (1) 还是文件 (0)。
func (s *fileService) resolveFileNameConflict(userID uint64, parentFolderID *uint64, originalProposedName string, currentFileID uint64, isFolder uint8) (string, error) {
	logger.Debug("resolveFileNameConflict called", zap.Uint64("userID", userID), zap.Any("parentFolderID", parentFolderID), zap.String("originalProposedName", originalProposedName), zap.Uint64("currentFileID", currentFileID), zap.Uint8("isFolder", isFolder))
	existingFiles, err := s.fileRepo.FindByUserIDAndParentFolderID(userID, parentFolderID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.Error("resolveFileNameConflict: Failed to check existing files for naming conflict",
			zap.Uint64("userID", userID),
			zap.Any("parentFolderID", parentFolderID),
			zap.Error(err))
		return "", fmt.Errorf("failed to check folder contents for naming conflict: %w", err)
	}

	proposedFileName := originalProposedName
	for counter := 1; ; counter++ {
		isConflict := false
		for _, existingFile := range existingFiles {
			// 冲突判断条件：
			// 1. 文件名相同 (proposedFileName)
			// 2. 文件类型相同 (都是文件或都是文件夹)
			// 3. 不是当前正在操作的文件/文件夹本身 (如果 currentFileID 为 0 表示是新创建，则无需排除)
			if existingFile.FileName == proposedFileName &&
				existingFile.IsFolder == isFolder &&
				(currentFileID == 0 || existingFile.ID != currentFileID) {
				isConflict = true
				break
			}
		}
		if !isConflict {
			break // 没有冲突，找到可用名称
		}

		// 存在冲突，生成新文件名
		ext := filepath.Ext(originalProposedName) // 总是基于原始提议的名称提取扩展名
		nameWithoutExt := originalProposedName[:len(originalProposedName)-len(ext)]
		proposedFileName = fmt.Sprintf("%s(%d)%s", nameWithoutExt, counter, ext)
		logger.Debug("resolveFileNameConflict: conflict found, trying new name", zap.String("proposedFileName", proposedFileName))
	}

	// 如果文件名被修改了，记录日志
	if proposedFileName != originalProposedName {
		logger.Info("FileNameConflictResolved",
			zap.String("originalProposedName", originalProposedName),
			zap.String("finalName", proposedFileName),
			zap.Uint64("userID", userID),
			zap.Any("parentFolderID", parentFolderID))
	}
	logger.Debug("resolveFileNameConflict finished", zap.String("finalName", proposedFileName))
	return proposedFileName, nil
}

// 检查目标文件是否处于正常的可操作状态
func (s *fileService) checkFile(fileToCheck *models.File, userID uint64) error {
	if fileToCheck == nil {
		return xerr.ErrFileNotFound
	}

	// 权限检查：确保文件属于当前用户
	if fileToCheck.UserID != userID {
		logger.Warn("Access denied for file", zap.Uint64("fileID", fileToCheck.ID), zap.Uint64("userID", userID), zap.Uint64("ownerID", fileToCheck.UserID))
		return errors.New("access denied: file does not belong to user")
	}

	if fileToCheck.DeletedAt.Valid {
		return fmt.Errorf("%w: file %d is deleted", xerr.ErrFileUnavailable, fileToCheck.ID)
	}

	return nil
}

// CheckDirectory 是一个辅助函数，用于检查一个文件是否是有效的目录
func (s *fileService) checkDirectory(dirToCheck *models.File, userID uint64) error {
	if err := s.checkFile(dirToCheck, userID); err != nil {
		return err // 如果文件本身不可用，直接返回
	}
	// 根据你的 models.File.IsFolder 定义：1 为文件夹
	if dirToCheck.IsFolder == 0 { // Changed from !dirToCheck.IsFolder to dirToCheck.IsFolder == 0
		return fmt.Errorf("file %d is not a directory", dirToCheck.ID)
	}
	return nil
}

func (s *fileService) ValidateParentFolder(userID uint64, parentFolderID *uint64) (*models.File, error) {
	if parentFolderID == nil {
		// 如果 parentFolderID 为 nil，表示是根目录，根目录无需验证（或在其他地方处理根目录逻辑）
		// 在这种情况下，可以返回nil，表示没有特定的父文件夹对象需要返回，但验证通过
		return nil, nil
	}

	parentFolder, err := s.fileRepo.FindByID(*parentFolderID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("ValidateParentFolder: Parent folder not found",
				zap.Uint64("parentFolderID", *parentFolderID),
				zap.Error(err))
			return nil, errors.New("parent folder not found")
		}
		logger.Error("ValidateParentFolder: Failed to check parent folder",
			zap.Uint64("parentFolderID", *parentFolderID),
			zap.Error(err))
		return nil, fmt.Errorf("failed to check parent folder: %w", err)
	}

	// 确保父文件夹属于当前用户
	if parentFolder.UserID != userID {
		logger.Warn("ValidateParentFolder: Access denied - Parent folder does not belong to user",
			zap.Uint64("attemptedUserID", userID),
			zap.Uint64("parentFolderID", parentFolder.ID),
			zap.Uint64("parentFolderOwnerID", parentFolder.UserID))
		return nil, errors.New("access denied: parent folder does not belong to you")
	}

	// 确保目标是文件夹
	if parentFolder.IsFolder != 1 {
		logger.Warn("ValidateParentFolder: Invalid parent - Not a folder",
			zap.Uint64("parentFolderID", parentFolder.ID),
			zap.Uint8("isFolderValue", parentFolder.IsFolder))
		return nil, errors.New("invalid parent: target ID is not a folder")
	}

	return parentFolder, nil
}

// getRelativePathInZip 计算文件或文件夹在 ZIP 包中的相对路径。
// rootFolder: 用户选择下载的根文件夹的 models.File 对象
// fileRecord: 当前正在处理的文件或文件夹的 models.File 对象
func (s *fileService) getRelativePathInZip(rootFolder *models.File, fileRecord *models.File) string {
	// 1. 获取根文件夹在ZIP中应作为前缀的名称 (例如 "我的文档/")
	zipRootPrefix := rootFolder.FileName
	if rootFolder.IsFolder == 1 && !strings.HasSuffix(zipRootPrefix, "/") {
		zipRootPrefix += "/" // 确保是目录
	}

	// 2. 如果当前 fileRecord 就是要下载的根文件夹本身，直接返回其 ZIP 前缀
	if fileRecord.ID == rootFolder.ID {
		return zipRootPrefix
	}

	// 3. 构建 fileRecord 在数据库中的完整路径（例如 "/users/user1/MyDocs/SubFolder/File.txt"）
	// 确保 Path 以 / 结尾，FileName 不含 /
	fileRecordDbPath := fileRecord.Path
	if !strings.HasSuffix(fileRecordDbPath, "/") {
		fileRecordDbPath += "/"
	}
	// 拼接文件名或文件夹名
	fullDbPathWithFileName := fileRecordDbPath + fileRecord.FileName

	// 4. 构建 rootFolder 在数据库中的完整路径，用于精确匹配和修剪
	// 例如："/users/user1/MyDocs/"
	rootFolderFullDbPath := rootFolder.Path
	if !strings.HasSuffix(rootFolderFullDbPath, "/") {
		rootFolderFullDbPath += "/"
	}
	rootFolderFullDbPath += rootFolder.FileName // 拼接根文件夹的文件名
	if rootFolder.IsFolder == 1 && !strings.HasSuffix(rootFolderFullDbPath, "/") {
		rootFolderFullDbPath += "/" // 如果是文件夹，确保以 / 结尾
	}

	// 5. 从 fileRecord 的完整路径中移除 rootFolder 的完整路径前缀
	// TrimPrefix 会处理前导斜杠的问题，只要前缀匹配
	relativePath := strings.TrimPrefix(fullDbPathWithFileName, rootFolderFullDbPath)

	// 6. 移除可能留下的任何前导斜杠（如果 rootFolderFullDbPath 没有完全匹配到文件路径开头）
	relativePath = strings.TrimPrefix(relativePath, "/")

	// 7. 如果是子文件夹，确保其在 ZIP 中的路径以斜杠结尾
	if fileRecord.IsFolder == 1 && !strings.HasSuffix(relativePath, "/") {
		relativePath += "/"
	}

	// 最终拼接 zipRootPrefix
	return zipRootPrefix + relativePath
}

// getFileContentReader 是一个辅助函数，用于根据存储类型获取文件内容 Reader
// 这个函数与 DownloadFile 逻辑类似，但返回 io.ReadCloser
func (s *fileService) getFileContentReader(ctx context.Context, file models.File) (io.ReadCloser, error) { // <--- 增加 ctx 参数
	if file.OssKey == nil || *file.OssKey == "" {
		logger.Error("getFileContentReader: File record has no OssKey", zap.Uint64("fileID", file.ID))
		return nil, errors.New("文件记录缺少存储键(OssKey)")
	}

	var bucketName string
	// 根据文件记录中实际存储的 OssBucket 来决定
	if file.OssBucket != nil && *file.OssBucket != "" {
		bucketName = *file.OssBucket
	} else {
		// 如果文件记录中没有指定 OssBucket，根据当前配置的默认存储类型来获取默认桶名
		// 这是为了兼容，如果你的业务逻辑保证 OssBucket 总是存在于文件记录中，则此 else 分支可移除
		switch s.cfg.Storage.Type {
		case "minio":
			bucketName = s.cfg.MinIO.BucketName
		case "aliyun_oss":
			bucketName = s.cfg.AliyunOSS.BucketName
		// case "qiniu_kodo":
		// 	// 七牛云通常不直接通过桶名访问，而是通过绑定的域名，但为了统一接口，此处仍保留
		// 	bucketName = s.cfg.QiniuKodo.BucketName
		default:
			logger.Error("getFileContentReader: Unsupported default storage type for getting bucket name",
				zap.String("storageType", s.cfg.Storage.Type))
			return nil, errors.New("不支持的存储类型配置，无法获取桶名")
		}
		logger.Warn("getFileContentReader: OssBucket is missing in file record, using default bucket name",
			zap.Uint64("fileID", file.ID), zap.String("defaultBucket", bucketName))
	}

	switch s.cfg.Storage.Type {
	case "local":
		// 如果你的 FileService 确实支持本地存储，需要确保相关配置 s.cfg.Storage.LocalBasePath 存在
		if s.cfg.Storage.LocalBasePath == "" {
			logger.Error("getFileContentReader: Local storage base path not configured", zap.String("type", s.cfg.Storage.Type))
			return nil, errors.New("本地存储路径未配置")
		}
		localFilePath := filepath.Join(s.cfg.Storage.LocalBasePath, *file.OssKey)
		logger.Info("getFileContentReader: Opening local physical file", zap.String("path", localFilePath))

		f, err := os.Open(localFilePath)
		if err != nil {
			if os.IsNotExist(err) {
				logger.Warn("getFileContentReader: Physical file not found on local disk",
					zap.Uint64("fileID", file.ID), zap.String("path", localFilePath), zap.Error(err))
				return nil, errors.New("物理文件在本地磁盘未找到")
			}
			logger.Error("getFileContentReader: Failed to open local file", zap.String("path", localFilePath), zap.Error(err))
			return nil, fmt.Errorf("打开本地文件失败 %s: %w", localFilePath, err)
		}
		return f, nil
	case "minio", "aliyun_oss", "qiniu_kodo": // <--- 将所有云存储类型统一处理
		logger.Info("getFileContentReader: Attempting to get object from cloud storage",
			zap.String("storageType", s.cfg.Storage.Type),
			zap.String("bucket", bucketName),
			zap.String("ossKey", *file.OssKey))

		// 调用抽象的 StorageService 接口
		objResult, err := s.fileStorageService.GetObject(ctx, bucketName, *file.OssKey)
		if err != nil {
			// 这里可以尝试更精确地判断“对象不存在”的错误，如果你的 StorageService 接口能返回统一的 ErrObjectNotFound
			// 例如：if errors.Is(err, storage.ErrObjectNotFound) { ... }
			if strings.Contains(err.Error(), "key does not exist") ||
				strings.Contains(err.Error(), "NoSuchKey") ||
				strings.Contains(err.Error(), "not found") { // 适用于 MinIO 和部分 S3 兼容服务
				logger.Warn("getFileContentReader: Physical file not found in cloud storage",
					zap.Uint64("fileID", file.ID), zap.String("ossKey", *file.OssKey), zap.Error(err))
				return nil, errors.New("物理文件在云存储中未找到")
			}

			logger.Error("getFileContentReader: Failed to get object from cloud storage",
				zap.String("storageType", s.cfg.Storage.Type),
				zap.String("bucket", bucketName),
				zap.String("ossKey", *file.OssKey),
				zap.Error(err))
			return nil, fmt.Errorf("从云存储获取对象失败 %s/%s: %w", bucketName, *file.OssKey, err)
		}
		// 从 GetObjectResult 中提取 Reader
		return objResult.Reader, nil // <--- 返回 objResult.Reader
	default:
		logger.Error("getFileContentReader: Unsupported storage type configured", zap.String("type", s.cfg.Storage.Type))
		return nil, errors.New("配置了不支持的存储类型")
	}
}
