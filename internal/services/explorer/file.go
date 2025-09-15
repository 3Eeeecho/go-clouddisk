package explorer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/mq"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/storage"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type FileService interface {
	// 文件查询
	GetFileByID(userID uint64, fileID uint64) (*models.File, error)
	GetFileByMD5Hash(userID uint64, md5Hash string) (*models.File, error)
	GetFilesByUserID(userID uint64, parentFolderID *uint64) ([]models.File, error)

	//文件上传
	//UploadFile(userID uint64, originalName, mimeType string, filesize uint64, parentFolderID *uint64, fileContent io.Reader) (*models.File, error)

	// 文件下载
	Download(ctx context.Context, userID uint64, fileID uint64) (*models.File, io.ReadCloser, error)
	GetPresignedURLForDownload(ctx context.Context, userID uint64, fileID uint64) (string, error)

	// 文件删除
	SoftDelete(userID uint64, fileID uint64) error
	PermanentDelete(userID uint64, fileID uint64) error
	DeleteFileVersion(userID uint64, fileID uint64, versionID string) error

	// 回收站操作
	ListRecycleBinFiles(userID uint64) ([]models.File, error)
	RestoreFile(userID uint64, fileID uint64) error

	// 文件操作
	CreateFolder(userID uint64, folderName string, parentFolderID *uint64) (*models.File, error)
	RenameFile(userID uint64, fileID uint64, newFileName string) (*models.File, error)
	MoveFile(userID uint64, fileID uint64, parentFolderID *uint64) (*models.File, error)
	ListFileVersions(userID uint64, fileID uint64) ([]models.FileVersion, error)
	RestoreFileVersion(userID uint64, fileID uint64, versionID string) error
}

type fileService struct {
	fileRepo           repositories.FileRepository
	fileVersionRepo    repositories.FileVersionRepository
	domainService      FileDomainService  // 业务逻辑
	transactionManager TransactionManager // 事务管理
	StorageService     storage.StorageService
	mqClient           *mq.RabbitMQClient
	cfg                *config.Config
}

var _ FileService = (*fileService)(nil)

// NewFileService 创建一个新的文件服务实例
func NewFileService(
	fileRepo repositories.FileRepository,
	fileVersionRepo repositories.FileVersionRepository,
	domainService FileDomainService,
	transactionManager TransactionManager,
	storageService storage.StorageService,
	mqClient *mq.RabbitMQClient,
	cfg *config.Config,
) FileService {
	return &fileService{
		fileRepo:           fileRepo,
		fileVersionRepo:    fileVersionRepo,
		domainService:      domainService,
		transactionManager: transactionManager,
		StorageService:     storageService,
		mqClient:           mqClient,
		cfg:                cfg,
	}
}

func (s *fileService) GetFileByID(userID uint64, fileID uint64) (*models.File, error) {
	file, err := s.domainService.CheckFile(userID, fileID)
	if err != nil {
		return nil, err // 错误已在 domainService 中包裹
	}

	logger.Info("GetFileByID success", zap.Uint64("userID", userID), zap.Any("fileID", fileID))
	return file, nil
}

func (s *fileService) GetFileByMD5Hash(userID uint64, md5Hash string) (*models.File, error) {
	file, err := s.fileRepo.FindFileByMD5Hash(md5Hash)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("GetFileByMD5Hash: File not found", zap.String("md5Hash", md5Hash))
			return nil, fmt.Errorf("file service: %w", xerr.ErrFileNotFound)
		}
		logger.Error("GetFileByMD5Hash: Failed to get file by MD5", zap.String("md5Hash", md5Hash), zap.Error(err))
		return nil, fmt.Errorf("file service: failed to get file by md5: %w", xerr.ErrDatabaseError)
	}

	// 检查文件状态
	if err := s.domainService.ValidateFile(userID, file); err != nil {
		return nil, err
	}

	logger.Info("GetFileByMD5Hash success", zap.Uint64("userID", userID), zap.String("md5Hash", md5Hash))
	return file, nil
}

// GetFilesByUserID 获取用户在指定文件夹下的文件和文件夹列表
func (s *fileService) GetFilesByUserID(userID uint64, parentFolderID *uint64) ([]models.File, error) {
	// 检查父文件夹
	if _, err := s.domainService.CheckDirectory(userID, parentFolderID); err != nil {
		return nil, err
	}

	files, err := s.fileRepo.FindByUserIDAndParentFolderID(userID, parentFolderID)
	if err != nil {
		logger.Error("GetFilesByUserID: Failed to get files", zap.Uint64("userID", userID), zap.Any("parentFolderID", parentFolderID), zap.Error(err))
		return nil, fmt.Errorf("file service: failed to get files: %w", xerr.ErrDatabaseError)
	}
	logger.Info("GetFilesByUserID success", zap.Uint64("userID", userID), zap.Any("parentFolderID", parentFolderID), zap.Int("fileCount", len(files)))
	return files, nil
}

func (s *fileService) CreateFolder(userID uint64, folderName string, parentFolderID *uint64) (*models.File, error) {
	targetParentFolder, err := s.domainService.CheckDirectory(userID, parentFolderID)
	if err != nil {
		return nil, err
	}

	// 用于存储父文件夹的完整路径，默认为根目录的路径 "/"
	var parentPath string

	// 1. 检查父文件夹是否存在和权限
	if parentFolderID != nil {
		parentPath = targetParentFolder.Path + targetParentFolder.FileName + "/"
	} else {
		parentPath = "/"
	}

	// 2. 检查同一父文件夹下是否已存在同名文件夹
	// 这是一个简单的检查，更严谨的实现可能需要查询所有子文件和文件夹的名字
	finalFolderName, err := s.domainService.ResolveFileNameConflict(userID, parentFolderID, folderName, 0, 1) // isFolder = 1
	if err != nil {
		logger.Error("CreateFolder: ResolveFileNameConflict failed", zap.Error(err))
		return nil, err // 错误已在 ResolveFileNameConflict 中记录
	}

	// 3. 创建文件夹记录
	newFolder := &models.File{
		UUID:           uuid.New().String(), // 文件夹也需要一个 UUID
		UserID:         userID,
		ParentFolderID: parentFolderID,
		FileName:       finalFolderName,
		Path:           parentPath,
		IsFolder:       1,   // 1 表示文件夹
		Size:           0,   // 文件夹大小为 0
		MimeType:       nil, // 文件夹没有 MimeType
		OssBucket:      nil, // 文件夹没有 OssBucket
		OssKey:         nil, // 文件夹没有 OssKey
		MD5Hash:        nil, // 文件夹没有 MD5Hash
		Status:         1,   // 正常状态
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := s.fileRepo.Create(newFolder); err != nil {
		logger.Error("CreateFolder: Failed to create folder in DB",
			zap.Uint64("userID", userID),
			zap.Any("parentFolderID", parentFolderID),
			zap.String("folderName", finalFolderName),
			zap.Error(err))
		return nil, fmt.Errorf("file service: failed to create folder: %w", xerr.ErrDatabaseError)
	}

	logger.Info("CreateFolder: Folder created successfully",
		zap.Uint64("folderID", newFolder.ID),
		zap.Uint64("userID", userID),
		zap.String("folderName", finalFolderName))
	return newFolder, nil
}

func (s *fileService) ListRecycleBinFiles(userID uint64) ([]models.File, error) {
	files, err := s.fileRepo.FindDeletedFilesByUserID(userID)
	if err != nil {
		logger.Error("ListRecycleBinFiles: Failed to retrieve deleted files", zap.Uint64("userID", userID), zap.Error(err))
		return nil, fmt.Errorf("file service: failed to retrieve recycle bin files: %w", xerr.ErrDatabaseError)
	}
	logger.Info("ListRecycleBinFiles success", zap.Uint64("userID", userID), zap.Int("fileCount", len(files)))
	return files, nil
}

func (s *fileService) RestoreFile(userID uint64, fileID uint64) error {
	rootFile, err := s.domainService.CheckDeletedFile(userID, fileID)
	if err != nil {
		return err
	}

	// 检查恢复到原始位置是否会引起命名冲突
	// 注意：对于恢复操作，currentFileID 应该传递 0 或一个特殊值，因为恢复的文件在冲突检查时
	// 通常被视为一个“新”文件，不应该排除自身。
	finalFileName, err := s.domainService.ResolveFileNameConflict(userID, rootFile.ParentFolderID, rootFile.FileName, 0, rootFile.IsFolder)
	if err != nil {
		return err
	}
	if finalFileName != rootFile.FileName {
		logger.Info("RestoreFile: Naming conflict resolved for restoration",
			zap.Uint64("fileID", fileID),
			zap.String("originalName", rootFile.FileName),
			zap.String("finalName", finalFileName))
	}
	rootFile.FileName = finalFileName // 更新为最终确定的文件名

	err = s.transactionManager.WithTransaction(context.Background(), func(tx *gorm.DB) error {
		return s.restoreFile(userID, fileID, finalFileName)
	})
	if err != nil {
		return err
	}

	logger.Info("RestoreFile: File/Folder restored successfully",
		zap.Uint64("fileID", fileID),
		zap.String("finalName", finalFileName))
	return nil
}

func (s *fileService) RenameFile(userID uint64, fileID uint64, newFileName string) (*models.File, error) {
	// 获取要改名的文件,检查文件是否处于正常状态
	fileToRename, err := s.domainService.CheckFile(userID, fileID)
	if err != nil {
		return nil, err
	}

	// 如果新旧文件名相同，直接返回，不做任何操作
	if fileToRename.FileName == newFileName {
		logger.Info("RenameFile: New file name is same as old, no operation needed", zap.Uint64("fileID", fileID), zap.String("fileName", newFileName))
		return fileToRename, nil
	}

	// 处理命名冲突,检查当前目录下是否存在同名文件
	finalFileName, err := s.domainService.ResolveFileNameConflict(userID, fileToRename.ParentFolderID, newFileName, fileToRename.ID, fileToRename.IsFolder)
	if err != nil {
		return nil, err // 错误已在 ResolveFileNameConflict 中记录
	}
	fileToRename.FileName = finalFileName

	err = s.transactionManager.WithTransaction(context.Background(), func(tx *gorm.DB) error {
		return s.renameFile(fileToRename)
	})
	if err != nil {
		return nil, err
	}

	logger.Info("RenameFile: File/Folder renamed successfully",
		zap.Uint64("fileID", fileID),
		zap.String("finalName", fileToRename.FileName))

	return fileToRename, nil
}

func (s *fileService) MoveFile(userID uint64, fileID uint64, targetParentID *uint64) (*models.File, error) {
	// 获取要移动的文件并检查文件是否处于正常状态
	fileToMove, err := s.domainService.CheckFile(userID, fileID)
	if err != nil {
		logger.Warn("MoveFile: Cannot rename a deleted or abnormal file", zap.Uint64("fileID", fileID), zap.Uint8("status", fileToMove.Status))
		return nil, err
	}

	// 获取目标父文件夹信息并进行权限和状态检查
	targetParentFolder, err := s.domainService.CheckDirectory(userID, targetParentID)
	if err != nil {
		return nil, err
	}

	// 目标路径
	var targetParentFullPath string
	if targetParentFolder == nil {
		targetParentFullPath = "/"
	} else {
		targetParentFullPath = targetParentFolder.Path + targetParentFolder.FileName + "/"
	}

	// 源路径
	var sourceFullPathWithSelf string
	if fileToMove.IsFolder == 1 {
		sourceFullPathWithSelf = fileToMove.Path + fileToMove.FileName + "/"
	} else {
		sourceFullPathWithSelf = fileToMove.Path + fileToMove.FileName
	}

	// 如果源文件夹的完整路径是目标父文件夹完整路径的前缀，那么就是移动到子目录,返回错误
	if strings.HasPrefix(targetParentFullPath, sourceFullPathWithSelf) {
		logger.Warn("MoveFile: Cannot move folder into its own subdirectory",
			zap.Uint64("fileID", fileID), zap.Uint64("targetParentID", *targetParentID), zap.Uint64("userID", userID))
		return nil, fmt.Errorf("file service: %w", xerr.ErrCannotMoveIntoSubtree)
	}

	// 检查目标文件夹是否是当前文件夹
	isSameDirectory := false
	if targetParentID == nil && fileToMove.ParentFolderID == nil {
		isSameDirectory = true
	} else if targetParentID != nil && fileToMove.ParentFolderID != nil && *targetParentID == *fileToMove.ParentFolderID {
		isSameDirectory = true
	}

	if isSameDirectory {
		logger.Info("MoveFile: No change needed, already in the same directory",
			zap.Uint64("fileID", fileID), zap.Reflect("targetParentID", targetParentID), zap.Uint64("userID", userID))
		return nil, fmt.Errorf("file service: %w", xerr.ErrFileAlreadyExists) // Or a more specific error
	}

	// 解决命名冲突问题
	finalFileName, err := s.domainService.ResolveFileNameConflict(userID, targetParentID, fileToMove.FileName, fileID, fileToMove.IsFolder)
	if err != nil {
		return nil, err
	}
	fileToMove.FileName = finalFileName

	err = s.transactionManager.WithTransaction(context.Background(), func(tx *gorm.DB) error {
		return s.moveFile(userID, fileToMove, targetParentID, targetParentFolder)
	})
	if err != nil {
		return nil, err
	}

	return fileToMove, nil
}

// 文件下载
func (s *fileService) Download(ctx context.Context, userID uint64, fileID uint64) (*models.File, io.ReadCloser, error) {
	file, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("Download: File not found in DB", zap.Uint64("fileID", fileID))
			return nil, nil, fmt.Errorf("file service: %w", xerr.ErrFileNotFound)
		}
		logger.Error("Download: Error retrieving file from DB", zap.Uint64("fileID", fileID), zap.Error(err))
		return nil, nil, fmt.Errorf("file service: failed to retrieve file: %w", xerr.ErrDatabaseError)
	}
	logger.Info("Download", zap.String("versionID", *file.VersionID))
	// 如果file是文件夹,压缩成zip并下载
	if file.IsFolder == 1 {
		err := s.domainService.ValidateFolder(userID, file)
		if err != nil {
			return nil, nil, err
		}
		return s.downloadFolder(ctx, userID, file)
	}

	err = s.domainService.ValidateFile(userID, file)
	if err != nil {
		return nil, nil, err // 错误已在 checkFile 中处理
	}
	return s.downloadFile(ctx, file)
}

// 文件删除
func (s *fileService) SoftDelete(userID uint64, fileID uint64) error {
	// 验证文件
	_, err := s.domainService.CheckFile(userID, fileID)
	if err != nil {
		return err
	}

	// 获取所有需要删除的文件或文件夹及其所有子项
	filesToDelete, err := s.domainService.CollectAllFiles(userID, fileID)
	if err != nil {
		logger.Error("SoftDeleteFile: Failed to collect files for soft deletion", zap.Uint64("fileID", fileID), zap.Error(err))
		return fmt.Errorf("file service: %w", err)
	}

	//需要反转文件切片,从尾部开始删除
	slices.Reverse(filesToDelete)
	return s.transactionManager.WithTransaction(context.Background(), func(tx *gorm.DB) error {
		return s.performSoftDelete(userID, filesToDelete)
	})
}

func (s *fileService) PermanentDelete(userID uint64, fileID uint64) error {
	// 验证文件
	file, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("PermanentDeleteFile: File not found in DB", zap.Uint64("fileID", fileID))
			return fmt.Errorf("domain service: %w", xerr.ErrFileNotFound)
		}
		logger.Error("PermanentDeleteFile: Error retrieving file from DB", zap.Uint64("fileID", fileID), zap.Error(err))
		return fmt.Errorf("file service: failed to retrieve file: %w", xerr.ErrDatabaseError)
	}

	if userID != file.UserID {
		logger.Warn("File access denied",
			zap.Uint64("fileID", file.ID),
			zap.Uint64("userID", userID),
			zap.Uint64("ownerID", file.UserID))
		return fmt.Errorf("file service: %w", xerr.ErrPermissionDenied)
	}

	// 开启事务
	return s.transactionManager.WithTransaction(context.Background(), func(tx *gorm.DB) error {
		// 1. 更新文件状态为“待删除”
		if err := s.fileRepo.UpdateFileStatus(fileID, models.StatusDeleting); err != nil {
			logger.Error("PermanentDeleteFile: Failed to update file status to deleting", zap.Uint64("fileID", fileID), zap.Error(err))
			return fmt.Errorf("file service: failed to update file status: %w", xerr.ErrDatabaseError)
		}

		// 2. 发送删除任务到 RabbitMQ
		task := models.DeleteFileTask{
			FileID: file.ID,
			UserID: file.UserID,
			OssKey: *file.OssKey,
		}
		taskBody, _ := json.Marshal(task)

		if err := s.mqClient.Publish("delete_all_versions_queue", taskBody); err != nil {
			logger.Error("PermanentDeleteFile: Failed to publish delete task to RabbitMQ", zap.Uint64("fileID", fileID), zap.Error(err))
			// 注意：这里事务会回滚，文件状态将恢复。
			return fmt.Errorf("file service: failed to publish delete task: %w", xerr.ErrMQError)
		}

		logger.Info("PermanentDeleteFile: Successfully marked file for deletion and published task", zap.Uint64("fileID", fileID))
		return nil
	})
}

func (s *fileService) DeleteFileVersion(userID uint64, fileID uint64, versionID string) error {
	// 1. 验证用户是否有权访问该文件
	file, err := s.domainService.CheckFile(userID, fileID)
	if err != nil {
		return err
	}

	// 2. 查找指定的版本
	versionToDelete, err := s.fileVersionRepo.FindByVersionID(versionID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("file service: %w", xerr.ErrFileNotFound)
		}
		return fmt.Errorf("file service: failed to find file version: %w", xerr.ErrDatabaseError)
	}

	// 3. 确保版本属于正确的文件
	if versionToDelete.FileID != file.ID {
		return fmt.Errorf("file service: %w", xerr.ErrPermissionDenied)
	}

	// 4. 发送删除任务到 RabbitMQ
	task := models.DeleteFileTask{
		FileID:    file.ID,
		UserID:    file.UserID,
		OssKey:    versionToDelete.OssKey,
		VersionID: versionToDelete.VersionID,
	}
	taskBody, _ := json.Marshal(task)

	if err := s.mqClient.Publish("delete_specific_version_queue", taskBody); err != nil {
		logger.Error("DeleteFileVersion: Failed to publish delete task to RabbitMQ", zap.Uint64("fileID", fileID), zap.Error(err))
		return fmt.Errorf("file service: failed to publish delete task: %w", xerr.ErrMQError)
	}

	logger.Info("DeleteFileVersion: Successfully deleted file version", zap.Uint64("fileID", fileID), zap.String("versionID", versionID))
	return nil
}

func (s *fileService) ListFileVersions(userID uint64, fileID uint64) ([]models.FileVersion, error) {
	// 1. 验证用户是否有权访问该文件
	if _, err := s.domainService.CheckFile(userID, fileID); err != nil {
		return nil, err
	}

	// 2. 查询版本历史
	versions, err := s.fileVersionRepo.FindByFileID(fileID)
	if err != nil {
		logger.Error("ListFileVersions: Failed to get file versions", zap.Uint64("fileID", fileID), zap.Error(err))
		return nil, fmt.Errorf("file service: failed to get file versions: %w", xerr.ErrDatabaseError)
	}

	logger.Info("ListFileVersions: Successfully retrieved file versions", zap.Uint64("fileID", fileID), zap.Int("versionCount", len(versions)))
	return versions, nil
}

// 还原文件版本到指定的版本,需要文件状态正常
func (s *fileService) RestoreFileVersion(userID uint64, fileID uint64, versionID string) error {
	// 1. 验证用户是否有权访问该文件
	file, err := s.domainService.CheckFile(userID, fileID)
	if err != nil {
		return err
	}

	// 2. 查找指定的版本
	versionToRestore, err := s.fileVersionRepo.FindByVersionID(versionID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("file service: %w", xerr.ErrFileNotFound)
		}
		return fmt.Errorf("file service: failed to find file version: %w", xerr.ErrDatabaseError)
	}

	// 3. 确保版本属于正确的文件
	if versionToRestore.FileID != file.ID {
		return fmt.Errorf("file service: %w", xerr.ErrPermissionDenied)
	}

	// 4. 更新主文件记录
	file.Size = versionToRestore.Size
	file.OssKey = &versionToRestore.OssKey
	file.VersionID = &versionToRestore.VersionID
	file.DeletedAt = gorm.DeletedAt{}
	file.MD5Hash = &versionToRestore.MD5Hash

	if err := s.fileRepo.Update(file); err != nil {
		logger.Error("RestoreFileVersion: Failed to update file record", zap.Uint64("fileID", fileID), zap.Error(err))
		return fmt.Errorf("file service: failed to update file record: %w", xerr.ErrDatabaseError)
	}

	logger.Info("RestoreFileVersion: Successfully restored file version", zap.Uint64("fileID", fileID), zap.String("versionID", versionID))
	return nil

}

func (s *fileService) GetPresignedURLForDownload(ctx context.Context, userID uint64, fileID uint64) (string, error) {
	// 1. 验证文件是否存在且用户有权访问
	file, err := s.domainService.CheckFile(userID, fileID)
	if err != nil {
		return "", err // 错误已在 domainService 中包裹
	}

	// 2. 检查文件是否为文件夹，文件夹不支持生成预签名URL
	if file.IsFolder == 1 {
		return "", fmt.Errorf("file service: %w", xerr.ErrTargetNotFolder)
	}

	// 3. 检查 OssKey 是否存在
	if file.OssKey == nil || *file.OssKey == "" {
		logger.Error("GetPresignedURLForDownload: File record has no OssKey", zap.Uint64("fileID", file.ID))
		return "", fmt.Errorf("file service: %w", xerr.ErrStorageError)
	}

	// 4. 从配置中获取预签名URL的有效期
	expiry := time.Duration(s.cfg.Storage.PresignedURLExpiry) * time.Minute

	// 5. 调用存储服务生成预签名URL
	presignedURL, err := s.StorageService.GeneratePresignedURL(ctx, *file.OssBucket, *file.OssKey, *file.VersionID, expiry)
	if err != nil {
		logger.Error("GetPresignedURLForDownload: Failed to generate presigned URL",
			zap.Uint64("fileID", file.ID),
			zap.Error(err))
		return "", fmt.Errorf("file service: failed to generate presigned URL: %w", xerr.ErrStorageError)
	}

	logger.Info("GetPresignedURLForDownload: Successfully generated presigned URL",
		zap.Uint64("fileID", fileID),
		zap.Uint64("userID", userID))

	return presignedURL, nil
}
