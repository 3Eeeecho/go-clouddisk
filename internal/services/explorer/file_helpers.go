package explorer

import (
	"archive/zip"
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

// 删除文件相关辅助函数
// performSoftDelete 执行软删除
func (s *fileService) performSoftDelete(userID uint64, filesToDelete []models.File) error {
	for _, fileToDelete := range filesToDelete {
		// 双重检查权限
		if fileToDelete.UserID != userID {
			return errors.New("access denied: file does not belong to user")
		}

		// 执行软删除
		if err := s.fileRepo.SoftDelete(fileToDelete.ID); err != nil {
			return fmt.Errorf("failed to soft delete file %d: %w", fileToDelete.ID, err)
		}

		logger.Info("File soft deleted",
			zap.Uint64("fileID", fileToDelete.ID),
			zap.String("fileName", fileToDelete.FileName))
	}

	return nil
}

// performPermanentDelete 执行永久删除
func (s *fileService) performPermanentDelete(filesToDelete []models.File, userID uint64) error {
	// 对要删除的文件进行排序，确保先删除子项的物理文件，再删除父文件夹的物理文件
	for i := len(filesToDelete) - 1; i >= 0; i-- {
		fileToDelete := filesToDelete[i]

		// 确保文件属于当前用户 (双重检查)
		if fileToDelete.UserID != userID {
			return errors.New("internal error: file ownership mismatch during batch permanent deletion")
		}

		// 删除物理文件（如果是文件且有物理存储）
		if err := s.deletePhysicalFile(&fileToDelete); err != nil {
			return err
		}

		// 删除数据库记录
		if err := s.fileRepo.PermanentDelete(fileToDelete.ID); err != nil {
			logger.Error("PermanentDeleteFile: Failed to delete file record from DB",
				zap.Uint64("fileID", fileToDelete.ID), zap.Error(err))
			return fmt.Errorf("failed to delete file record: %w", err)
		}

		logger.Info("File permanently deleted",
			zap.Uint64("fileID", fileToDelete.ID),
			zap.String("fileName", fileToDelete.FileName))
	}

	return nil
}

// deletePhysicalFile 删除物理文件
func (s *fileService) deletePhysicalFile(file *models.File) error {
	// 如果是文件且有 OssKey (即有对应的物理文件)，则删除物理文件
	if file.IsFolder == 0 && file.OssKey != nil && *file.OssKey != "" && file.MD5Hash != nil && *file.MD5Hash != "" {
		// 检查是否有其他文件引用这个物理文件
		referencesCount, err := s.fileRepo.CountFilesInStorage(*file.OssKey, *file.MD5Hash, file.ID)
		if err != nil {
			return fmt.Errorf("failed to check file references before deleting physical file: %w", err)
		}

		if referencesCount == 0 {
			// 没有其他文件记录引用这个物理文件了，可以安全删除物理文件
			logger.Info("No other references to physical file, proceeding with physical deletion.",
				zap.String("ossKey", *file.OssKey),
				zap.Uint64("fileID", file.ID))

			switch s.cfg.Storage.Type {
			case "local":
				return s.deleteLocalFile(*file.OssKey)
			case "minio":
				return s.deleteMinioFile(file)
			default:
				return errors.New("unsupported storage type configured for permanent deletion")
			}
		} else {
			// 存在其他用户的文件引用,就不删除物理文件
			logger.Info("Physical file has other references, skipping physical deletion.",
				zap.String("ossKey", *file.OssKey),
				zap.Uint64("fileID", file.ID),
				zap.Int64("referencesCount", referencesCount))
		}
	}

	return nil
}

// deleteLocalFile 删除本地文件
func (s *fileService) deleteLocalFile(ossKey string) error {
	localFilePath := filepath.Join(s.cfg.Storage.LocalBasePath, ossKey)
	err := os.Remove(localFilePath)
	if err != nil {
		logger.Error("Failed to delete physical file",
			zap.String("path", localFilePath), zap.Error(err))
		return fmt.Errorf("failed to delete physical file: %w", err)
	}
	logger.Info("Physical file deleted", zap.String("path", localFilePath))
	return nil
}

// deleteMinioFile 删除MinIO文件
func (s *fileService) deleteMinioFile(file *models.File) error {
	bucketName := s.cfg.MinIO.BucketName
	if file.OssBucket != nil && *file.OssBucket != "" {
		bucketName = *file.OssBucket
	}

	logger.Info("Attempting to delete object from MinIO",
		zap.String("bucket", bucketName),
		zap.String("ossKey", *file.OssKey),
		zap.Uint64("fileID", file.ID))

	err := s.StorageService.RemoveObject(context.Background(), bucketName, *file.OssKey)
	if err != nil {
		logger.Error("Failed to delete object from MinIO",
			zap.String("bucket", bucketName),
			zap.String("ossKey", *file.OssKey),
			zap.Uint64("fileID", file.ID),
			zap.Error(err))
		return fmt.Errorf("failed to delete object from cloud storage: %w", err)
	}

	logger.Info("Object deleted from MinIO",
		zap.String("bucket", bucketName),
		zap.String("ossKey", *file.OssKey),
		zap.Uint64("fileID", file.ID))
	return nil
}

// 下载文件相关辅助函数
func (s *fileService) downloadFile(ctx context.Context, file *models.File) (*models.File, io.ReadCloser, error) {
	// 检查 OssKey 是否存在
	if file.OssKey == nil || *file.OssKey == "" {
		logger.Error("DownloadFile: File record has no OssKey, cannot retrieve physical file", zap.Uint64("fileID", file.ID))
		return nil, nil, errors.New("文件数据不可用（缺少存储键）")
	}

	// getFileContentReader 成为一个通用的辅助函数，用于获取文件内容读取器
	fileContentReader, err := s.GetFileContentReader(ctx, file)
	if err != nil {
		return nil, nil, fmt.Errorf("获取文件内容失败: %w", err)
	}

	return file, fileContentReader, nil // 返回文件元数据和读取器
}

func (s *fileService) downloadFolder(ctx context.Context, userID uint64, rootFolder *models.File) (*models.File, io.ReadCloser, error) {
	// CollectAllNormalFiles 返回一个扁平化的列表,它能递归地获取一个文件夹下的所有文件和子文件夹,包括文件自身
	filesToCompress, err := s.domainService.CollectAllNormalFiles(rootFolder.ID, userID)
	if err != nil {
		logger.Error("DownloadFolder: Failed to collect children for folder", zap.Uint64("folderID", rootFolder.ID), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to collect folder children: %w", err)
	}

	// 使用 pipe 来实现流式 ZIP 压缩
	// reader 用于从 pipe 读取 ZIP 数据，writer 用于向 pipe 写入 ZIP 数据
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		zipWriter := zip.NewWriter(pw)
		defer func() {
			if err := zipWriter.Close(); err != nil {
				logger.Error("DownloadFolder: 关闭 ZIP 写入器失败", zap.Error(err))
				// 如果关闭 zipWriter 失败，也通过 pipe writer 传递错误
				pw.CloseWithError(fmt.Errorf("关闭 ZIP 写入器失败: %w", err))
			}
		}()

		for _, fileRecord := range filesToCompress {
			relativePath := s.domainService.GetRelativePathInZip(rootFolder, &fileRecord)

			// 如果是文件夹，则在 ZIP 中创建对应的目录项
			if fileRecord.IsFolder == 1 {
				if !strings.HasSuffix(relativePath, "/") {
					relativePath += "/"
				}

				if _, err := zipWriter.Create(relativePath); err != nil {
					pw.CloseWithError(fmt.Errorf("failed to create folder entry %s: %w", relativePath, err))
					return
				}
				continue
			}

			// 如果是文件，从存储中获取内容并写入 ZIP
			if fileRecord.OssKey == nil || *fileRecord.OssKey == "" {
				logger.Warn("DownloadFolder: 文件记录缺少存储键 OssKey,在 ZIP 中跳过",
					zap.Uint64("fileID", fileRecord.ID),
					zap.String("fileName", fileRecord.FileName))
				continue // 跳过没有物理文件的记录
			}

			// 使用一个匿名函数来封装文件读取和写入 ZIP 的逻辑，确保 defer 能够及时执行
			func() {
				// 获取文件内容读取器，并传入 goroutine 的上下文
				fileContentReader, getErr := s.GetFileContentReader(ctx, &fileRecord)
				if getErr != nil {
					logger.Error("DownloadFolder: 获取文件内容读取器失败",
						zap.Uint64("fileID", fileRecord.ID),
						zap.String("ossKey", *fileRecord.OssKey),
						zap.Error(getErr))
					return // 遇到错误立即退出匿名函数
				}
				defer fileContentReader.Close() // 确保每个文件读取器都被关闭

				// 创建 ZIP 文件头
				header := &zip.FileHeader{
					Name:     relativePath,
					Method:   zip.Deflate,          // 默认使用 Deflate 压缩方法
					Modified: fileRecord.UpdatedAt, // 使用文件更新时间
				}
				// 如果你存储了文件的原始大小，可以在这里设置 header.UncompressedSize64
				if fileRecord.Size > 0 {
					header.UncompressedSize64 = uint64(fileRecord.Size) // 确保类型匹配
				}

				writer, err := zipWriter.CreateHeader(header)
				if err != nil {
					pw.CloseWithError(fmt.Errorf("为 %s 创建 ZIP 头失败: %w", relativePath, err))
					return // 遇到错误立即退出匿名函数
				}

				// 将文件内容从读取器复制到 ZIP 写入器
				_, err = io.Copy(writer, fileContentReader)
				if err != nil {
					pw.CloseWithError(fmt.Errorf("复制 %s 内容到 ZIP 失败: %w", relativePath, err))
					return // 遇到错误立即退出匿名函数
				}
			}() // 立即执行匿名函数
		}
		// 所有文件处理完毕后，关闭 zipWriter
		if err := zipWriter.Close(); err != nil {
			pw.CloseWithError(fmt.Errorf("failed to close zip writer: %w", err))
		}
		logger.Info("DownloadFolder: ZIP creation finished for folder", zap.Uint64("folderID", rootFolder.ID))
	}()

	return rootFolder, pr, nil
}

// GetFileContentReader 是一个辅助函数，用于根据存储类型获取文件内容 Reader
// 这个函数与 DownloadFile 逻辑类似，但返回 io.ReadCloser
func (s *fileService) GetFileContentReader(ctx context.Context, file *models.File) (io.ReadCloser, error) {
	storageType := s.cfg.Storage.Type
	if file.OssKey == nil || *file.OssKey == "" {
		logger.Error("GetFileContentReader: File record has no OssKey", zap.Uint64("fileID", file.ID))
		return nil, errors.New("文件记录缺少存储键(OssKey)")
	}

	var bucketName string
	// 根据文件记录中实际存储的 OssBucket 来决定
	if file.OssBucket != nil && *file.OssBucket != "" {
		bucketName = *file.OssBucket
	} else {
		switch storageType {
		case "minio":
			bucketName = s.cfg.MinIO.BucketName
		case "aliyun_oss":
			bucketName = s.cfg.AliyunOSS.BucketName
		// case "qiniu_kodo":
		// 	// 七牛云通常不直接通过桶名访问，而是通过绑定的域名，但为了统一接口，此处仍保留
		// 	bucketName = s.cfg.QiniuKodo.BucketName
		default:
			logger.Error("GetFileContentReader: Unsupported default storage type for getting bucket name",
				zap.String("storageType", storageType))
			return nil, errors.New("不支持的存储类型配置，无法获取桶名")
		}
		logger.Warn("GetFileContentReader: OssBucket is missing in file record, using default bucket name",
			zap.Uint64("fileID", file.ID), zap.String("defaultBucket", bucketName))
	}

	// local存储不处理
	if storageType == "local" {
		return nil, errors.New("本地存储暂不考虑")
	}

	// 将所有云存储类型统一处理
	logger.Info("GetFileContentReader: Attempting to get object from cloud storage",
		zap.String("storageType", storageType),
		zap.String("bucket", bucketName),
		zap.String("ossKey", *file.OssKey))

	// 调用抽象的 StorageService 接口
	objResult, err := s.StorageService.GetObject(ctx, bucketName, *file.OssKey)
	if err != nil {
		logger.Error("GetFileContentReader: Failed to get object from cloud storage",
			zap.String("storageType", storageType),
			zap.String("bucket", bucketName),
			zap.String("ossKey", *file.OssKey),
			zap.Error(err))
		return nil, fmt.Errorf("从云存储获取对象失败 %s/%s: %w", bucketName, *file.OssKey, err)
	}

	return objResult.Reader, nil
}

// 文件操作相关辅助函数
func (s *fileService) moveFile(userID uint64, fileToMove *models.File, targetParentID *uint64, targetParentFolder *models.File) error {
	// 更改文件本身的 ParentFolderID 和 Path
	var newParentPath string
	if targetParentID == nil {
		newParentPath = "/"
	} else {
		newParentPath = targetParentFolder.Path + targetParentFolder.FileName + "/"
	}

	oldFullPathWithSelf := fileToMove.Path + fileToMove.FileName
	newFullPathWithSelf := newParentPath + fileToMove.FileName

	// 更新 fileToMove 对象的字段
	fileToMove.ParentFolderID = targetParentID
	fileToMove.Path = newParentPath

	if err := s.fileRepo.Update(fileToMove); err != nil {
		logger.Error("MoveFile: Failed to update file's parent and path in DB transaction",
			zap.Uint64("fileID", fileToMove.ID),
			zap.String("newName", fileToMove.FileName),
			zap.Error(err))
		return fmt.Errorf("failed to update file name in transaction: %w", err)
	}
	logger.Info("MoveFile: File name updated successfully in DB transaction",
		zap.Uint64("fileID", fileToMove.ID),
		zap.String("newName", fileToMove.FileName))

	// 如果是文件夹,递归更新所有子项的path
	if fileToMove.IsFolder == 1 {
		// `oldChildPathPrefix` 是指被移动文件夹在旧位置的“内部”所有文件的旧父路径前缀
		// 例如，原文件夹是 /root/old_folder/，其内部文件 file.txt 的 Path 是 /root/old_folder/
		// 这里的 `fileToMove.Path` 是 `/root/`，`fileToMove.FileName` 是 `old_folder`
		oldChildPathPrefix := oldFullPathWithSelf + "/" // 完整旧文件夹的逻辑路径，例如 `/user1/documents/MyOldFolder/`
		newChildPathPrefix := newFullPathWithSelf + "/" // 完整新文件夹的逻辑路径，例如 `/user1/archive/MyOldFolder/`

		// 调用 repository 批量更新 Path
		if err := s.fileRepo.UpdateFilesPathInBatch(userID, oldChildPathPrefix, newChildPathPrefix); err != nil {
			logger.Error("MoveFile: Failed to update children paths in DB transaction",
				zap.Uint64("parentFolderID", fileToMove.ID), zap.Error(err))
			return fmt.Errorf("%w: failed to update children paths: %w", xerr.ErrDatabaseTransaction, err)
		}
	}

	return nil
}

func (s *fileService) renameFile(fileToRename *models.File) error {
	err := s.fileRepo.Update(fileToRename)
	if err != nil {
		logger.Error("RenameFile: Failed to update file name in DB transaction",
			zap.Uint64("fileID", fileToRename.ID),
			zap.String("newName", fileToRename.FileName),
			zap.Error(err))
		return fmt.Errorf("failed to update file name in transaction: %w", err)
	}
	return nil
}

func (s *fileService) restoreFile(userID uint64, fileID uint64, finalFileName string) error {
	// 收集所有需要恢复的文件和文件夹 (包括子项)
	filesToRestore, err := s.domainService.CollectAllFiles(userID, fileID)
	if err != nil {
		logger.Error("RestoreFile: Failed to collect files for restoration", zap.Uint64("fileID", fileID), zap.Error(err))
		return fmt.Errorf("failed to collect files for restoration: %w", err)
	}

	//批量恢复数据库记录
	for _, fileToUpdate := range filesToRestore {
		if fileToUpdate.ID == fileID {
			fileToUpdate.FileName = finalFileName
		}

		// 恢复操作：将 status 改为 1，清空 deleted_at
		fileToUpdate.Status = 1
		fileToUpdate.DeletedAt = gorm.DeletedAt{}

		err = s.fileRepo.Update(&fileToUpdate)
		if err != nil {
			logger.Error("RestoreFile: Failed to restore file record in DB transaction",
				zap.Uint64("fileToUpdateID", fileToUpdate.ID),
				zap.Error(err))
			return fmt.Errorf("failed to restore file in transaction: %w", err)
		}
		logger.Info("RestoreFile: File ID restored in DB transaction.", zap.Uint64("fileID", fileToUpdate.ID))
	}
	return nil
}
