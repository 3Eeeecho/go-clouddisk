package explorer

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/cache"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
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

	// 文件夹操作
	CreateFolder(userID uint64, folderName string, parentFolderID *uint64) (*models.File, error)

	//文件上传
	UploadFile(userID uint64, originalName, mimeType string, filesize uint64, parentFolderID *uint64, fileContent io.Reader) (*models.File, error)

	// 文件下载
	Download(ctx context.Context, userID uint64, fileID uint64) (*models.File, io.ReadCloser, error)

	// 文件删除
	SoftDeleteFile(userID uint64, fileID uint64) error
	PermanentDeleteFile(userID uint64, fileID uint64) error

	// 回收站操作
	ListRecycleBinFiles(userID uint64) ([]models.File, error)
	RestoreFile(userID uint64, fileID uint64) error

	// 文件操作
	RenameFile(userID uint64, fileID uint64, newFileName string) (*models.File, error)
	MoveFile(userID uint64, fileID uint64, parentFolderID *uint64) (*models.File, error)
}

type fileService struct {
	fileRepo           repositories.FileRepository
	userRepo           repositories.UserRepository
	domainService      FileDomainService  // 业务逻辑
	transactionManager TransactionManager // 事务管理
	StorageService     storage.StorageService
	cacheService       *cache.RedisCache
	cfg                *config.Config
}

var _ FileService = (*fileService)(nil)

// NewFileService 创建一个新的文件服务实例
func NewFileService(
	fileRepo repositories.FileRepository,
	userRepo repositories.UserRepository,
	domainService FileDomainService,
	transactionManager TransactionManager,
	storageService storage.StorageService,
	cacheService *cache.RedisCache,
	cfg *config.Config,

) FileService {
	return &fileService{
		fileRepo:           fileRepo,
		userRepo:           userRepo,
		domainService:      domainService,
		transactionManager: transactionManager,
		StorageService:     storageService,
		cacheService:       cacheService,
		cfg:                cfg,
	}
}

func (s *fileService) GetFileByID(userID uint64, fileID uint64) (*models.File, error) {
	file, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Error("Not found file", zap.Uint64("userID", userID), zap.Any("fileID", fileID), zap.Error(err))
			return nil, fmt.Errorf("not found file")
		}
		logger.Error("Failed to get file for user", zap.Uint64("userID", userID), zap.Any("fileID", fileID), zap.Error(err))
		return nil, fmt.Errorf("failed to get file for user %d , fileID:%d, err: %w", userID, fileID, err)
	}

	// 检查文件状态
	err = s.domainService.ValidateFile(userID, file)
	if err != nil {
		return nil, err
	}

	logger.Info("GetFilesByUserID success", zap.Uint64("userID", userID), zap.Any("fileID", fileID))
	return file, nil
}

func (s *fileService) GetFileByMD5Hash(userID uint64, md5Hash string) (*models.File, error) {
	file, err := s.fileRepo.FindFileByMD5Hash(md5Hash)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Error("Not found file", zap.Uint64("userID", userID), zap.Any("md5Hash", md5Hash), zap.Error(err))
			return nil, fmt.Errorf("not found file")
		}
		logger.Error("Failed to get file for user", zap.Uint64("userID", userID), zap.Any("md5Hash", md5Hash), zap.Error(err))
		return nil, fmt.Errorf("failed to get file for user %d , md5Hash:%s, err: %w", userID, md5Hash, err)
	}

	// 检查文件状态
	err = s.domainService.ValidateFile(userID, file)
	if err != nil {
		return nil, err
	}

	logger.Info("GetFilesByMD5Hash success", zap.Uint64("userID", userID), zap.Any("md5Hash", md5Hash))
	return file, nil
}

// GetFilesByUserID 获取用户在指定文件夹下的文件和文件夹列表
func (s *fileService) GetFilesByUserID(userID uint64, parentFolderID *uint64) ([]models.File, error) {
	// 检查父文件夹
	_, err := s.domainService.CheckDirectory(userID, parentFolderID)
	if err != nil {
		return nil, err
	}

	files, err := s.fileRepo.FindByUserIDAndParentFolderID(userID, parentFolderID)
	if err != nil {
		logger.Error("Failed to get files for user in folder", zap.Uint64("userID", userID), zap.Any("parentFolderID", parentFolderID), zap.Error(err))
		return nil, fmt.Errorf("failed to get files for user %d in folder %d: %w", userID, parentFolderID, err)
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
		return nil, fmt.Errorf("failed to create folder record: %w", err)
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
		logger.Error("ListRecycleBinFiles: Failed to retrieve deleted files for user", zap.Uint64("userID", userID), zap.Error(err))
		return nil, fmt.Errorf("failed to retrieve recycle bin files: %w", err)
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
		return nil, xerr.ErrCannotMoveToSelfOrSub
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
		return nil, errors.New("no change needed: already in the same directory")
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

func (s *fileService) UploadFile(userID uint64, originalName, mimeType string, filesize uint64, parentFolderID *uint64, fileContent io.Reader) (*models.File, error) {
	logger.Debug("UploadFile called", zap.Uint64("userID", userID), zap.String("originalName", originalName), zap.Uint64("filesize", filesize), zap.Any("parentFolderID", parentFolderID))

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

	// 2. 计算文件内容的哈希值，用于去重
	md5Hasher := md5.New()
	md5CopyBytes, err := io.Copy(md5Hasher, fileContent)
	if err != nil {
		logger.Error("Failed to compute file MD5 hash", zap.String("file", originalName), zap.Error(err))
		return nil, fmt.Errorf("failed to compute file MD5 hash: %w", err)
	}
	fileMD5Hash := hex.EncodeToString(md5Hasher.Sum(nil))
	logger.Info("File MD5 calculated", zap.String("file", originalName), zap.Uint64("size", filesize), zap.Int64("md5CopyBytes", md5CopyBytes))

	// 重要：将 fileContent 的读取位置重置到文件开头，以便再次读取用于写入物理存储
	// 确保 fileContent 实现了 io.Seeker 接口（在 Handler 中我们使用了临时文件，它实现了）
	if seeker, ok := fileContent.(io.Seeker); ok {
		_, err := seeker.Seek(0, 0)
		if err != nil {
			logger.Error("Failed to reset file reader position", zap.String("file", originalName), zap.Error(err))
			return nil, fmt.Errorf("failed to reset file reader position: %w", err)
		}
	} else {
		logger.Error("fileContent reader is not seekable, cannot re-read for storage", zap.String("file", originalName))
		return nil, errors.New("fileContent reader is not seekable, cannot re-read for storage")
	}

	// 3. 检查文件是否已存在（秒传逻辑）
	existingFileByMD5, err := s.fileRepo.FindFileByMD5Hash(fileMD5Hash)
	if err == nil && existingFileByMD5 != nil {
		logger.Info("Fast upload: file already exists, creating new record", zap.String("file", originalName), zap.String("md5", fileMD5Hash))
		// 文件内容已存在，执行秒传：直接创建新的文件记录指向旧的物理文件
		// 注意：这里的 UUID 和 OssKey/OssBucket 应该引用现有的物理文件信息
		// 我们假设 existingFileByMD5 包含了正确的 OSS 相关信息
		newFileRecord := &models.File{
			UUID:           uuid.New().String(), // 每个文件记录有自己的 UUID
			UserID:         userID,
			ParentFolderID: parentFolderID,
			FileName:       originalName, // 用户原始文件名'
			Path:           parentPath,
			IsFolder:       0, // 0表示文件
			Size:           existingFileByMD5.Size,
			MimeType:       &mimeType,
			OssBucket:      existingFileByMD5.OssBucket, // 引用现有物理文件的 bucket
			OssKey:         existingFileByMD5.OssKey,    // 引用现有物理文件的 key
			MD5Hash:        &fileMD5Hash,
			Status:         1,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		if err := s.fileRepo.Create(newFileRecord); err != nil {
			logger.Error("Error creating new file record for existing file", zap.String("file", originalName), zap.Error(err))
			return nil, fmt.Errorf("failed to create new file record for existing file: %w", err)
		}
		logger.Info("Fast upload successful for file", zap.String("file", originalName), zap.Uint64("newRecordID", newFileRecord.ID))
		return newFileRecord, nil //秒传成功
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		// 查找文件 MD5 时发生其他数据库错误
		logger.Error("Error checking existing file by MD5 hash", zap.String("md5Hash", fileMD5Hash), zap.Error(err))
		return nil, fmt.Errorf("failed to check existing file by MD5 hash: %w", err)
	}

	// 4. 文件内容不存在，需要上传到物理存储 (MinIO/S3 或本地)
	logger.Info("Uploading new file to storage", zap.String("file", originalName), zap.String("md5", fileMD5Hash))
	// 生成新的 UUID 作为文件在OSS中的唯一标识
	// newUUID := uuid.New().String()

	//提取原始拓展名
	extension := filepath.Ext(originalName)

	// OssKey：文件MD5分层
	ossKey := fmt.Sprintf("%d:%s%s", userID, fileMD5Hash, extension)

	var uploadedSize int64
	switch s.cfg.Storage.Type {
	case "minio":
		// 上传到 MinIO
		logger.Info("Attempting to save physical file to MinIO",
			zap.String("bucket", s.cfg.MinIO.BucketName),
			zap.String("ossKey", ossKey))

		info, err := s.StorageService.PutObject(context.Background(),
			s.cfg.MinIO.BucketName,
			ossKey,
			fileContent,
			int64(filesize),
			mimeType,
		)

		if err != nil {
			logger.Error("Failed to upload file to MinIO",
				zap.String("ossKey", ossKey), zap.Error(err))
			return nil, fmt.Errorf("failed to upload file to cloud storage: %w", err)
		}
		uploadedSize = info.Size
		logger.Info("File uploaded to MinIO successfully",
			zap.String("ossKey", ossKey), zap.Int64("size", uploadedSize))
	case "local":
		// 构造本地存储路径,使用 OssKey 作为本地文件名
		logger.Info("show localbasepath to debug", zap.String("localBasePath", s.cfg.Storage.LocalBasePath))
		localPath := filepath.Join(s.cfg.Storage.LocalBasePath, ossKey)
		logger.Info("Attempting to save physical file to local disk", zap.String("path", localPath))

		// 确保存储目录存在
		err = os.MkdirAll(filepath.Dir(localPath), 0755)
		if err != nil {
			logger.Error("Failed to create storage directory", zap.String("path", localPath), zap.Error(err))
			return nil, fmt.Errorf("failed to create storage directory: %w", err)
		}

		out, err := os.Create(localPath)
		if err != nil {
			logger.Error("Failed to create file on disk", zap.String("path", localPath), zap.Error(err))
			return nil, fmt.Errorf("failed to create file on disk at %s: %w", localPath, err)
		}
		defer out.Close()

		// 从 fileContent 读取并写入到物理文件
		writtenBytes, err := io.Copy(out, fileContent)
		logger.Info("Physical file written successfully", zap.String("path", localPath), zap.Int64("writtenBytes", writtenBytes))
		if err != nil {
			logger.Error("Failed to write file to disk", zap.String("path", localPath), zap.Error(err))
			os.Remove(localPath) // 写入失败，删除不完整文件
			return nil, fmt.Errorf("failed to write file to disk: %w", err)
		}
		uploadedSize = writtenBytes
		logger.Info("Physical file written successfully to local disk", zap.String("path", localPath), zap.Int64("writtenBytes", uploadedSize))
	default:
		return nil, errors.New("unsupported storage type configured for upload")
	}

	// 5. 将文件元数据记录到数据库
	logger.Debug("Saving file record to database", zap.String("file", originalName), zap.String("md5", fileMD5Hash))
	fileRecord := &models.File{
		UUID:           uuid.New().String(),
		UserID:         userID,
		ParentFolderID: parentFolderID,
		FileName:       originalName,
		Path:           parentPath,
		IsFolder:       0, // 0表示文件
		Size:           uint64(uploadedSize),
		MimeType:       &mimeType,               // 传入指针
		OssBucket:      &s.cfg.MinIO.BucketName, // 假设使用 MinIO 的 bucket name，即使是本地存储
		OssKey:         &ossKey,                 // 传入指针
		MD5Hash:        &fileMD5Hash,            // 传入指针
		Status:         1,                       // 正常状态
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := s.fileRepo.Create(fileRecord); err != nil {
		logger.Error("Failed to save file record to database", zap.String("file", originalName), zap.Error(err))
		switch s.cfg.Storage.Type {
		case "minio":
			removeErr := s.StorageService.RemoveObject(context.Background(), s.cfg.MinIO.BucketName, ossKey)
			if removeErr != nil {
				logger.Error("Failed to remove MinIO object after DB save failure",
					zap.String("ossKey", ossKey), zap.Error(removeErr))
			} else {
				logger.Info("Successfully removed MinIO object after DB save failure", zap.String("ossKey", ossKey))
			}
		case "local":
			// 如果数据库记录失败，删除已经写入的物理文件
			os.Remove(filepath.Join(s.cfg.Storage.LocalBasePath, ossKey))
		}
		return nil, fmt.Errorf("failed to save file record to database: %w", err)
	}

	logger.Info("UploadFile success",
		zap.String("file", originalName),
		zap.Uint64("userID", userID),
		zap.String("md5", fileMD5Hash),
		zap.String("storageType", s.cfg.Storage.Type),
		zap.Stringp("ossKey", fileRecord.OssKey))
	return fileRecord, nil
}

// 文件下载
func (s *fileService) Download(ctx context.Context, userID uint64, fileID uint64) (*models.File, io.ReadCloser, error) {
	file, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("DownloadFile: File not found in DB", zap.Uint64("fileID", fileID))
			return nil, nil, errors.New("文件未找到")
		}
		logger.Error("DownloadFile: Error retrieving file from DB", zap.Uint64("fileID", fileID), zap.Error(err))
		return nil, nil, fmt.Errorf("从数据库获取文件失败: %w", err)
	}

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
func (s *fileService) SoftDeleteFile(userID uint64, fileID uint64) error {
	// 验证文件
	_, err := s.domainService.CheckFile(userID, fileID)
	if err != nil {
		return err
	}

	// 获取所有需要删除的文件或文件夹及其所有子项
	filesToDelete, err := s.domainService.CollectAllFiles(userID, fileID)
	if err != nil {
		logger.Error("SoftDeleteFile: Failed to collect files for soft deletion", zap.Uint64("fileID", fileID), zap.Error(err))
		return fmt.Errorf("failed to collect files for soft deletion: %w", err)
	}

	//需要反转文件切片,从尾部开始删除
	slices.Reverse(filesToDelete)
	return s.transactionManager.WithTransaction(context.Background(), func(tx *gorm.DB) error {
		return s.performSoftDelete(userID, filesToDelete)
	})
}

func (s *fileService) PermanentDeleteFile(userID uint64, fileID uint64) error {
	// 验证文件
	_, err := s.domainService.CheckFile(userID, fileID)
	if err != nil {
		return err
	}

	// 获取所有需要删除的文件或文件夹及其所有子项
	filesToDelete, err := s.domainService.CollectAllFiles(fileID, userID)
	if err != nil {
		logger.Error("PermanentDeleteFile: Failed to collect files for permanent deletion", zap.Uint64("fileID", fileID), zap.Error(err))
		return fmt.Errorf("failed to collect files for permanent deletion: %w", err)
	}

	//需要反转文件切片,从尾部开始删除
	slices.Reverse(filesToDelete)
	return s.transactionManager.WithTransaction(context.Background(), func(tx *gorm.DB) error {
		return s.performPermanentDelete(filesToDelete, userID)
	})
}
