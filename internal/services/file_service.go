package services

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	"github.com/klauspost/compress/zip"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type FileService interface {
	GetFileByID(userID uint64, fileID uint64) (*models.File, error)
	GetFilesByUserID(userID uint64, parentFolderID *uint64) ([]models.File, error)
	UploadFile(userID uint64, originalName, mimeType string, filesize uint64, parentFolderID *uint64, fileContent io.Reader) (*models.File, error)
	CreateFolder(userID uint64, folderName string, parentFolderID *uint64) (*models.File, error)
	DownloadFile(ctx context.Context, userID uint64, fileID uint64) (*models.File, io.ReadCloser, error)     // 下载文件
	DownloadFolder(ctx context.Context, userID uint64, folderID uint64) (*models.File, io.ReadCloser, error) //下载文件夹
	SoftDeleteFile(userID uint64, fileID uint64) error
	PermanentDeleteFile(userID uint64, fileID uint64) error
	ListRecycleBinFiles(userID uint64) ([]models.File, error)
	RestoreFile(userID uint64, fileID uint64) error                                    // 从回收站恢复文件
	RenameFile(userID uint64, fileID uint64, newFileName string) (*models.File, error) //修改文件名
	MoveFile(userID uint64, fileID uint64, parentFolderID *uint64) (*models.File, error)

	//helpers
	GetFileContentReader(ctx context.Context, file models.File) (io.ReadCloser, error)
}

type fileService struct {
	fileRepo           repositories.FileRepository
	userRepo           repositories.UserRepository
	cfg                *config.Config
	db                 *gorm.DB
	fileStorageService storage.StorageService
	cacheService       *cache.RedisCache
}

var _ FileService = (*fileService)(nil)

// NewFileService 创建一个新的文件服务实例
func NewFileService(fileRepo repositories.FileRepository, userRepo repositories.UserRepository, cfg *config.Config, db *gorm.DB, fileStorageService storage.StorageService, cacheService *cache.RedisCache) FileService {
	return &fileService{
		fileRepo:           fileRepo,
		userRepo:           userRepo,
		cfg:                cfg,
		db:                 db,
		fileStorageService: fileStorageService,
		cacheService:       cacheService,
	}
}

func (s *fileService) GetFileByID(userID uint64, fileID uint64) (*models.File, error) {
	logger.Debug("GetFilesByUserID called", zap.Uint64("userID", userID), zap.Any("fileID", fileID))

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
	err = s.checkFile(file, userID)
	if err != nil {
		return nil, err
	}

	logger.Info("GetFilesByUserID success", zap.Uint64("userID", userID), zap.Any("fileID", fileID))
	return file, nil
}

// GetFilesByUserID 获取用户在指定文件夹下的文件和文件夹列表
func (s *fileService) GetFilesByUserID(userID uint64, parentFolderID *uint64) ([]models.File, error) {
	// 检查父文件夹
	_, err := s.ValidateParentFolder(userID, parentFolderID)
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

func (s *fileService) UploadFile(userID uint64, originalName, mimeType string, filesize uint64, parentFolderID *uint64, fileContent io.Reader) (*models.File, error) {
	logger.Debug("UploadFile called", zap.Uint64("userID", userID), zap.String("originalName", originalName), zap.Uint64("filesize", filesize), zap.Any("parentFolderID", parentFolderID))

	targetParentFolder, err := s.ValidateParentFolder(userID, parentFolderID)
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

		info, err := s.fileStorageService.PutObject(context.Background(),
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
			removeErr := s.fileStorageService.RemoveObject(context.Background(), s.cfg.MinIO.BucketName, ossKey)
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

func (s *fileService) CreateFolder(userID uint64, folderName string, parentFolderID *uint64) (*models.File, error) {
	logger.Debug("CreateFolder called", zap.Uint64("userID", userID), zap.String("folderName", folderName), zap.Any("parentFolderID", parentFolderID))

	targetParentFolder, err := s.ValidateParentFolder(userID, parentFolderID)
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
	finalFolderName, err := s.resolveFileNameConflict(userID, parentFolderID, folderName, 0, 1) // isFolder = 1
	if err != nil {
		logger.Error("CreateFolder: resolveFileNameConflict failed", zap.Error(err))
		return nil, err // 错误已在 resolveFileNameConflict 中记录
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

func (s *fileService) DownloadFile(ctx context.Context, userID uint64, fileID uint64) (*models.File, io.ReadCloser, error) {
	file, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("DownloadFile: File not found in DB", zap.Uint64("fileID", fileID))
			return nil, nil, errors.New("文件未找到")
		}
		logger.Error("DownloadFile: Error retrieving file from DB", zap.Uint64("fileID", fileID), zap.Error(err))
		return nil, nil, fmt.Errorf("从数据库获取文件失败: %w", err)
	}

	if file.IsFolder == 1 { // 假设 IsFolder 为 1 表示是文件夹
		logger.Warn("DownloadFile: Attempted to download a folder", zap.Uint64("folderID", fileID))
		// 显式返回一个错误，说明不能下载文件夹
		return nil, nil, errors.New("无法下载文件夹")
	}

	// 检查文件状态是否正常 (例如，不在回收站) 和权限
	err = s.checkFile(file, userID) // 假设 checkFile 包含权限检查
	if err != nil {
		logger.Error("DownloadFile: File check failed", zap.Uint64("fileID", fileID), zap.Error(err))
		return nil, nil, err // 错误已在 checkFile 中处理
	}

	// 检查 OssKey 是否存在
	if file.OssKey == nil || *file.OssKey == "" {
		logger.Error("DownloadFile: File record has no OssKey, cannot retrieve physical file", zap.Uint64("fileID", file.ID))
		return nil, nil, errors.New("文件数据不可用（缺少存储键）")
	}

	var reader io.ReadCloser // 使用 io.ReadCloser
	var bucketName string

	// 根据文件记录中实际存储的 BucketName 来决定
	if file.OssBucket != nil && *file.OssBucket != "" {
		bucketName = *file.OssBucket
	} else {
		// 如果文件记录中没有指定 BucketName，使用默认配置中的 BucketName
		switch s.cfg.Storage.Type {
		case "minio":
			bucketName = s.cfg.MinIO.BucketName
		case "aliyun_oss":
			bucketName = s.cfg.AliyunOSS.BucketName
		}
		logger.Warn("DownloadFile: OssBucket is missing in file record, using default bucket name",
			zap.Uint64("fileID", file.ID), zap.String("defaultBucket", bucketName))
	}

	// 使用抽象的 fileStorageService 进行下载
	// 注意：GetObject 应该返回 GetObjectResult，包含 Reader
	objResult, err := s.fileStorageService.GetObject(ctx, bucketName, *file.OssKey)
	if err != nil {
		// 存储服务特定的“对象不存在”错误判断可能需要调整，
		// 比如 MinIO SDK 返回的错误字符串或错误类型，Aliyun OSS 返回的错误类型。
		// 这里尝试通用判断，但最好在各自的 StorageService 实现中将“对象不存在”映射为一个统一的 error.Is 错误类型。
		// 例如：if errors.Is(err, storage.ErrObjectNotFound) { ... }
		if strings.Contains(err.Error(), "key does not exist") ||
			strings.Contains(err.Error(), "NoSuchKey") ||
			strings.Contains(err.Error(), "not found") { // 通用错误字符串判断
			logger.Warn("DownloadFile: Physical file not found in cloud storage for DB record ID",
				zap.Uint64("fileID", file.ID), zap.String("ossKey", *file.OssKey), zap.Error(err))
			return nil, nil, errors.New("物理文件在云存储中未找到")
		}

		logger.Error("DownloadFile: Failed to get object from cloud storage for download",
			zap.String("ossKey", *file.OssKey), zap.Error(err))
		return nil, nil, fmt.Errorf("从云存储获取文件失败: %w", err)
	}

	reader = objResult.Reader // 从结果中获取 ReadCloser

	logger.Info("DownloadFile: Object retrieved successfully from cloud storage. Returning file and reader.",
		zap.String("bucket", bucketName), zap.String("ossKey", *file.OssKey))

	return file, reader, nil // 返回文件元数据和读取器
}

func (s *fileService) DownloadFolder(ctx context.Context, userID uint64, folderID uint64) (*models.File, io.ReadCloser, error) {
	logger.Debug("DownloadFolder called", zap.Uint64("folderID", folderID), zap.Uint64("userID", userID))
	// 验证根文件夹是否存在且属于用户
	rootFolder, err := s.fileRepo.FindByID(folderID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("DownloadFolder: Folder not found in DB", zap.Uint64("folderID", folderID))
			return nil, nil, errors.New("folder not found")
		}
		logger.Error("DownloadFolder: Error retrieving folder from DB", zap.Uint64("folderID", folderID), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to retrieve folder from DB: %w", err)
	}

	err = s.checkDirectory(rootFolder, userID)
	if err != nil {
		return nil, nil, err
	}

	// 2. 收集所有子文件和子文件夹的记录
	// 假设你有 collectAllChildren 这样的函数，它能递归地获取一个文件夹下的所有文件和子文件夹
	// collectAllChildren 应该返回一个扁平化的列表
	allChildren, err := s.collectAllChildren(folderID, userID)
	if err != nil {
		logger.Error("DownloadFolder: Failed to collect children for folder", zap.Uint64("folderID", folderID), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to collect folder children: %w", err)
	}
	filesToCompress := append([]models.File{*rootFolder}, allChildren...)

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
			// 如果是文件夹，则在 ZIP 中创建对应的目录项
			relativePath := s.getRelativePathInZip(rootFolder, &fileRecord)
			if fileRecord.IsFolder == 1 {
				if !strings.HasSuffix(relativePath, "/") {
					relativePath += "/"
				}
				_, err := zipWriter.Create(relativePath)
				if err != nil {
					logger.Error("DownloadFolder: Failed to create folder entry in zip",
						zap.String("folderPath", relativePath),
						zap.Uint64("folderID", fileRecord.ID),
						zap.Error(err))
					// 通常这里会继续处理下一个文件或回滚，取决于错误性质
					pw.CloseWithError(fmt.Errorf("failed to create folder entry %s: %w", relativePath, err))
					return
				}
				logger.Debug("DownloadFolder: Created folder entry in zip", zap.String("path", relativePath))
				continue
			}

			// 如果是文件，从存储中获取内容并写入 ZIP
			if fileRecord.OssKey == nil || *fileRecord.OssKey == "" {
				logger.Warn("DownloadFolder: 文件记录缺少存储键（OssKey），在 ZIP 中跳过",
					zap.Uint64("fileID", fileRecord.ID),
					zap.String("fileName", fileRecord.FileName))
				continue // 跳过没有物理文件的记录
			}

			// 使用一个匿名函数来封装文件读取和写入 ZIP 的逻辑，确保 defer 能够及时执行
			func() {
				// 获取文件内容读取器，并传入 goroutine 的上下文
				fileContentReader, getErr := s.GetFileContentReader(ctx, fileRecord) // <--- 传入 ctx
				if getErr != nil {
					logger.Error("DownloadFolder: 获取文件内容读取器失败",
						zap.Uint64("fileID", fileRecord.ID),
						zap.String("ossKey", *fileRecord.OssKey),
						zap.Error(getErr))
					pw.CloseWithError(fmt.Errorf("获取文件 %s 内容失败: %w", fileRecord.FileName, getErr))
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
					logger.Error("DownloadFolder: 在 ZIP 中创建文件头失败",
						zap.String("filePath", relativePath),
						zap.Uint64("fileID", fileRecord.ID),
						zap.Error(err))
					pw.CloseWithError(fmt.Errorf("为 %s 创建 ZIP 头失败: %w", relativePath, err))
					return // 遇到错误立即退出匿名函数
				}

				// 将文件内容从读取器复制到 ZIP 写入器
				_, err = io.Copy(writer, fileContentReader)
				if err != nil {
					logger.Error("DownloadFolder: 复制文件内容到 ZIP 写入器失败",
						zap.String("filePath", relativePath),
						zap.Uint64("fileID", fileRecord.ID),
						zap.Error(err))
					pw.CloseWithError(fmt.Errorf("复制 %s 内容到 ZIP 失败: %w", relativePath, err))
					return // 遇到错误立即退出匿名函数
				}
				logger.Debug("DownloadFolder: 文件已添加到 ZIP", zap.String("path", relativePath), zap.Uint64("size", fileRecord.Size))
			}() // 立即执行匿名函数
		}

		logger.Info("DownloadFolder: ZIP creation finished for folder", zap.Uint64("folderID", folderID))
	}()

	return rootFolder, pr, nil
}

// SoftDeleteFile 软删除文件或文件夹
func (s *fileService) SoftDeleteFile(userID uint64, fileID uint64) error {
	file, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("SoftDeleteFile: File ID not found for user", zap.Uint64("fileID", fileID), zap.Uint64("userID", userID))
			return errors.New("file or folder not found")
		}
		logger.Error("SoftDeleteFile: Error retrieving file ID", zap.Uint64("fileID", fileID), zap.Uint64("userID", userID), zap.Error(err))
		return fmt.Errorf("failed to retrieve file: %w", err)
	}

	if err = s.checkFile(file, userID); err != nil {
		return err
	}

	// 获取所有需要删除的文件或文件夹及其所有子项
	filesToDelete, err := s.collectAllChildren(fileID, userID)
	if err != nil {
		logger.Error("SoftDeleteFile: Failed to collect files for soft deletion", zap.Uint64("fileID", fileID), zap.Error(err))
		return fmt.Errorf("failed to collect files for soft deletion: %w", err)
	}

	// 开启事务
	tx := s.db.Begin()
	if tx.Error != nil {
		logger.Error("SoftDeleteFile: Failed to begin transaction", zap.Error(tx.Error))
		return fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}() // 确保 panic 时回滚

	// 在事务中批量更新所有待删除文件的状态和 deleted_at
	for _, fileToUpdate := range filesToDelete {
		// 确保文件属于当前用户 (已在前面检查，这里是双重保险)
		if fileToUpdate.UserID != userID {
			tx.Rollback()
			logger.Warn("SoftDeleteFile: Attempted to delete file ID", zap.Uint64("fileID", fileToUpdate.ID), zap.Uint64("userID", fileToUpdate.UserID), zap.Uint64("currentUserID", userID), zap.Error(errors.New("internal error: file ownership mismatch during batch deletion")))
			return errors.New("internal error: file ownership mismatch during batch deletion")
		}

		// 执行 GORM 的软删除操作，这将同时更新 status 字段为 0 和 deleted_at 字段为当前时间
		// GORM 的 Delete 方法在模型有 DeletedAt 字段时会自动进行软删除。
		// 手动设置 fileToUpdate.Status = 0，并让 GORM 的 Delete 自动处理 DeletedAt。
		err = s.fileRepo.SoftDelete(fileToUpdate.ID)

		if err != nil {
			tx.Rollback()
			logger.Error("SoftDeleteFile: Failed to soft-delete file ID in DB transaction", zap.Uint64("fileID", fileToUpdate.ID), zap.Error(err))
			return fmt.Errorf("failed to soft-delete file in transaction: %w", err)
		}
		logger.Info("SoftDeleteFile: File ID soft-deleted in DB transaction.", zap.Uint64("fileID", fileToUpdate.ID))
	}

	// 提交事务
	err = tx.Commit().Error
	if err != nil {
		logger.Error("SoftDeleteFile: Failed to commit transaction", zap.Error(err))
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	logger.Info("File/Folder ID soft-deleted successfully.", zap.Uint64("fileID", fileID))
	return nil
}

func (s *fileService) PermanentDeleteFile(userID uint64, fileID uint64) error {
	rootFile, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("PermanentDeleteFile: File ID not found for user", zap.Uint64("fileID", fileID), zap.Uint64("userID", userID))
			return errors.New("file or folder not found")
		}
		logger.Error("PermanentDeleteFile: Error retrieving file ID", zap.Uint64("fileID", fileID), zap.Uint64("userID", userID), zap.Error(err))
		return fmt.Errorf("failed to retrieve file: %w", err)
	}

	if rootFile.UserID != userID {
		logger.Warn("PermanentDeleteFile: Access denied for file", zap.Uint64("fileID", rootFile.ID), zap.Uint64("userID", userID), zap.Uint64("ownerID", rootFile.UserID))
		return errors.New("access denied: file or folder does not belong to user")
	}

	// 收集所有需要永久删除的文件和文件夹
	filesToPermanentlyDelete, err := s.collectAllChildren(fileID, userID)
	if err != nil {
		logger.Error("PermanentDeleteFile: Failed to collect files for permanent deletion", zap.Uint64("fileID", fileID), zap.Error(err))
		return fmt.Errorf("failed to collect files for permanent deletion: %w", err)
	}

	// 开启事务，确保数据库删除和物理文件删除的原子性
	// 这是一个简化的事务处理，实际可能需要更复杂的事务管理器
	tx := s.db.Begin()
	if tx.Error != nil {
		logger.Error("PermanentDeleteFile: Failed to begin transaction", zap.Error(tx.Error))
		return fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	// 在事务中批量删除数据库记录和物理文件
	// 为了确保物理文件删除在数据库记录删除之前进行（避免数据库记录没了但物理文件没删成功的情况）
	// 我们先尝试删除所有物理文件，然后删除数据库记录。
	// 注意：这里需要考虑物理文件删除失败的回滚。
	// 为了简化，我们按顺序处理，任何一个失败就回滚。

	// 对要删除的文件进行排序，确保先删除子项的物理文件，再删除父文件夹的物理文件
	// 这样可以避免在删除子项物理文件时，父文件夹的路径已经不存在的情况
	// 但实际上 os.Remove 只需要完整路径，顺序影响不大，更多是逻辑清晰。
	// 关键是所有物理文件删除成功后再删除数据库记录，或者在循环内按序删除。
	for i := len(filesToPermanentlyDelete) - 1; i >= 0; i-- {
		fileToPermanentlyDelete := filesToPermanentlyDelete[i]

		// 确保文件属于当前用户 (双重检查)
		if fileToPermanentlyDelete.UserID != userID {
			tx.Rollback()
			logger.Warn("PermanentDeleteFile: Attempted to delete file ID", zap.Uint64("fileID", fileToPermanentlyDelete.ID), zap.Uint64("userID", fileToPermanentlyDelete.UserID), zap.Uint64("currentUserID", userID), zap.Error(errors.New("internal error: file ownership mismatch during batch permanent deletion")))
			return errors.New("internal error: file ownership mismatch during batch permanent deletion")
		}

		//如果是文件且有 OssKey (即有对应的物理文件)，则删除物理文件
		if fileToPermanentlyDelete.IsFolder == 0 && fileToPermanentlyDelete.OssKey != nil && *fileToPermanentlyDelete.OssKey != "" && fileToPermanentlyDelete.MD5Hash != nil && *fileToPermanentlyDelete.MD5Hash != "" {
			localFilePath := filepath.Join(s.cfg.Storage.LocalBasePath, *fileToPermanentlyDelete.OssKey)
			referencesCount, countErr := s.fileRepo.CountFilesInStorage(*fileToPermanentlyDelete.OssKey, *fileToPermanentlyDelete.MD5Hash, fileToPermanentlyDelete.ID)
			if countErr != nil {
				tx.Rollback()
				return fmt.Errorf("failed to check file references before deleting physical file: %w", countErr)
			}
			if referencesCount == 0 {
				// 没有其他文件记录引用这个物理文件了，可以安全删除物理文件
				logger.Info("PermanentDeleteFile: No other references to physical file, proceeding with physical deletion.",
					zap.String("ossKey", *fileToPermanentlyDelete.OssKey),
					zap.Uint64("fileID", fileToPermanentlyDelete.ID))
				switch s.cfg.Storage.Type {
				case "local":
					err = os.Remove(localFilePath)
					if err != nil {
						tx.Rollback()
						logger.Error("PermanentDeleteFile: Failed to delete physical file", zap.String("path", localFilePath), zap.Uint64("recordID", fileID), zap.Error(err))
						return fmt.Errorf("failed to delete physical file: %w", err)
					}
					logger.Info("PermanentDeleteFile: Physical file deleted.", zap.String("path", localFilePath))
				case "minio":
					bucketName := s.cfg.MinIO.BucketName
					if fileToPermanentlyDelete.OssBucket != nil && *fileToPermanentlyDelete.OssBucket != "" {
						bucketName = *fileToPermanentlyDelete.OssBucket
					}
					logger.Info("PermanentDeleteFile: Attempting to delete object from MinIO",
						zap.String("bucket", bucketName),
						zap.String("ossKey", *fileToPermanentlyDelete.OssKey),
						zap.Uint64("recordID", fileToPermanentlyDelete.ID))
					err = s.fileStorageService.RemoveObject(context.Background(), bucketName, *fileToPermanentlyDelete.OssKey)
					if err != nil {
						tx.Rollback()
						logger.Error("PermanentDeleteFile: Failed to delete object from MinIO",
							zap.String("bucket", bucketName),
							zap.String("ossKey", *fileToPermanentlyDelete.OssKey),
							zap.Uint64("recordID", fileToPermanentlyDelete.ID),
							zap.Error(err))
						return fmt.Errorf("failed to delete object from cloud storage: %w", err)
					}
					logger.Info("PermanentDeleteFile: Object deleted from MinIO.",
						zap.String("bucket", bucketName),
						zap.String("ossKey", *fileToPermanentlyDelete.OssKey),
						zap.Uint64("recordID", fileToPermanentlyDelete.ID))
				default:
					tx.Rollback()
					return errors.New("unsupported storage type configured for permanent deletion")
				}
			} else {
				// 存在其他用户的文件引用,就不删除物理文件
				logger.Info("PermanentDeleteFile: Physical file has other references, skipping physical deletion.",
					zap.String("ossKey", *fileToPermanentlyDelete.OssKey),
					zap.Uint64("fileID", fileToPermanentlyDelete.ID),
					zap.Int64("referencesCount", referencesCount))
			}
		}

		// 无论是否删除物理文件，都删除数据库记录
		// 使用 Unscoped() 绕过 GORM 的软删除，执行真正的 DELETE
		err = s.fileRepo.PermanentDelete(fileToPermanentlyDelete.ID)
		if err != nil {
			tx.Rollback()
			logger.Error("PermanentDeleteFile: Failed to delete file record ID from DB via transaction", zap.Uint64("fileID", fileToPermanentlyDelete.ID), zap.Error(err))
			return fmt.Errorf("failed to delete file record: %w", err)
		}
		logger.Info("PermanentDeleteFile: File record ID deleted from DB via transaction.", zap.Uint64("fileID", fileToPermanentlyDelete.ID))
	}

	tx.Commit()
	if tx.Error != nil {
		logger.Error("PermanentDeleteFile: Failed to commit transaction", zap.Error(tx.Error))
		return fmt.Errorf("failed to commit transaction: %w", tx.Error)
	}

	logger.Info("File ID permanently deleted successfully.", zap.Uint64("fileID", fileID))
	return nil
}

func (s *fileService) ListRecycleBinFiles(userID uint64) ([]models.File, error) {
	logger.Debug("ListRecycleBinFiles called", zap.Uint64("userID", userID))
	files, err := s.fileRepo.FindDeletedFilesByUserID(userID)
	if err != nil {
		logger.Error("ListRecycleBinFiles: Failed to retrieve deleted files for user", zap.Uint64("userID", userID), zap.Error(err))
		return nil, fmt.Errorf("failed to retrieve recycle bin files: %w", err)
	}
	logger.Info("ListRecycleBinFiles success", zap.Uint64("userID", userID), zap.Int("fileCount", len(files)))
	return files, nil
}

func (s *fileService) RestoreFile(userID uint64, fileID uint64) error {
	// 1. 获取要恢复的文件/文件夹记录
	rootFile, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("RestoreFile: File ID not found for user", zap.Uint64("fileID", fileID), zap.Uint64("userID", userID))
			return errors.New("file or folder not found")
		}
		logger.Error("RestoreFile: Error retrieving file ID", zap.Uint64("fileID", fileID), zap.Uint64("userID", userID), zap.Error(err))
		return fmt.Errorf("failed to retrieve file: %w", err)
	}

	// 2. 权限检查
	if rootFile.UserID != userID {
		logger.Warn("RestoreFile: Access denied for file", zap.Uint64("fileID", rootFile.ID), zap.Uint64("userID", userID), zap.Uint64("ownerID", rootFile.UserID))
		return errors.New("access denied: file or folder does not belong to user")
	}

	// 3. 检查文件是否在回收站中
	// 只有 deleted_at 不为空，且 status 为 0，才认为是回收站中的文件
	logger.Info("rootFile Debug", zap.Uint64("id", rootFile.ID), zap.Any("deleted_at", rootFile.DeletedAt), zap.Uint8("status", rootFile.Status))
	if !rootFile.DeletedAt.Valid || rootFile.Status == 1 {
		return errors.New("file or folder is not in the recycle bin")
	}

	// 4. 检查恢复到原始位置是否会引起命名冲突
	// 注意：对于恢复操作，currentFileID 应该传递 0 或一个特殊值，因为恢复的文件在冲突检查时
	// 通常被视为一个“新”文件，不应该排除自身。
	// 或者，更直接地，理解为恢复的文件本身就是软删除状态，FindByUserIDAndParentFolderID 不会返回它，
	// 所以这里的 currentFileID 传递 0 是合适的。
	finalFileName, err := s.resolveFileNameConflict(userID, rootFile.ParentFolderID, rootFile.FileName, 0, rootFile.IsFolder)
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

	// 5.开启事务
	tx := s.db.Begin()
	if tx.Error != nil {
		logger.Error("RestoreFile: Failed to begin transaction", zap.Error(tx.Error))
		return fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	// 6. 收集所有需要恢复的文件和文件夹 (包括子项)
	// collectAllChildren 能够获取根文件及其所有（包括软删除的）子项
	// 注意：这里收集到的 rootFile 还是原始文件名，需要在循环里更新
	filesToRestore, err := s.collectAllChildren(fileID, userID)
	if err != nil {
		tx.Rollback()
		logger.Error("RestoreFile: Failed to collect files for restoration", zap.Uint64("fileID", fileID), zap.Error(err))
		return fmt.Errorf("failed to collect files for restoration: %w", err)
	}

	//批量恢复数据库记录
	for _, fileToUpdate := range filesToRestore {
		// 权限再次检查 (尽管 collectAllChildren 已经过滤了)
		if fileToUpdate.UserID != userID {
			tx.Rollback()
			logger.Error("RestoreFile: File ownership mismatch during batch restoration",
				zap.Uint64("fileToUpdateID", fileToUpdate.ID),
				zap.Uint64("fileToUpdateOwner", fileToUpdate.UserID),
				zap.Uint64("currentUserID", userID))
			return errors.New("internal error: file ownership mismatch during batch restoration")
		}

		if fileToUpdate.ID == fileID {
			fileToUpdate.FileName = finalFileName
		}

		// 恢复操作：将 status 改为 1，清空 deleted_at
		fileToUpdate.Status = 1
		fileToUpdate.DeletedAt = gorm.DeletedAt{}

		err = s.fileRepo.Update(&fileToUpdate)
		if err != nil {
			tx.Rollback()
			logger.Error("RestoreFile: Failed to restore file record in DB transaction",
				zap.Uint64("fileToUpdateID", fileToUpdate.ID),
				zap.Error(err))
			return fmt.Errorf("failed to restore file in transaction: %w", err)
		}
		logger.Info("RestoreFile: File ID restored in DB transaction.", zap.Uint64("fileID", fileToUpdate.ID))
	}

	// 8. 提交事务
	err = tx.Commit().Error
	if err != nil {
		logger.Error("RestoreFile: Failed to commit transaction", zap.Error(err))
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	logger.Info("RestoreFile: File/Folder restored successfully",
		zap.Uint64("fileID", fileID),
		zap.String("finalName", finalFileName))
	return nil
}

func (s *fileService) RenameFile(userID uint64, fileID uint64, newFileName string) (*models.File, error) {
	// 获取要改名的文件
	fileToRename, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Info("RenameFile: File not found", zap.Uint64("fileID", fileID), zap.Uint64("userID", userID))
			return nil, errors.New("file or folder not found")
		}
		logger.Error("RenameFile: Error retrieving file", zap.Uint64("fileID", fileID), zap.Uint64("userID", userID), zap.Error(err))
		return nil, fmt.Errorf("failed to retrieve file: %w", err)
	}

	// 检查文件是否处于正常状态
	if err = s.checkFile(fileToRename, userID); err != nil {
		return nil, err
	}

	// 如果新旧文件名相同，直接返回，不做任何操作
	if fileToRename.FileName == newFileName {
		logger.Info("RenameFile: New file name is same as old, no operation needed", zap.Uint64("fileID", fileID), zap.String("fileName", newFileName))
		return fileToRename, nil
	}

	// 处理命名冲突,检查当前目录下是否存在同名文件
	finalFileName, err := s.resolveFileNameConflict(userID, fileToRename.ParentFolderID, newFileName, fileToRename.ID, fileToRename.IsFolder)
	if err != nil {
		return nil, err // 错误已在 resolveFileNameConflict 中记录
	}
	fileToRename.FileName = finalFileName

	tx := s.db.Begin()
	if tx.Error != nil {
		tx.Rollback()
		logger.Error("RenameFile: Failed to update file name in DB transaction",
			zap.Uint64("fileID", fileToRename.ID),
			zap.String("newName", fileToRename.FileName),
			zap.Error(err))
		return nil, fmt.Errorf("failed to update file name in transaction: %w", err)
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	err = tx.Save(fileToRename).Error
	if err != nil {
		tx.Rollback()
		logger.Error("RenameFile: Failed to update file name in DB transaction",
			zap.Uint64("fileID", fileToRename.ID),
			zap.String("newName", fileToRename.FileName),
			zap.Error(err))
		return nil, fmt.Errorf("failed to update file name in transaction: %w", err)
	}

	logger.Info("RenameFile: File name updated successfully in DB transaction",
		zap.Uint64("fileID", fileToRename.ID),
		zap.String("newName", fileToRename.FileName))

	err = tx.Commit().Error
	if err != nil {
		logger.Error("RenameFile: Failed to commit transaction", zap.Error(err))
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	logger.Info("RenameFile: File/Folder renamed successfully",
		zap.Uint64("fileID", fileID),
		zap.String("finalName", fileToRename.FileName))

	return fileToRename, nil
}

func (s *fileService) MoveFile(userID uint64, fileID uint64, targetParentID *uint64) (*models.File, error) {
	// 获取要改名的文件并检查文件是否处于正常状态
	fileToMove, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Info("MoveFile: File not found", zap.Uint64("fileID", fileID), zap.Uint64("userID", userID))
			return nil, errors.New("file or folder not found")
		}
		logger.Error("MoveFile: Error retrieving file", zap.Uint64("fileID", fileID), zap.Uint64("userID", userID), zap.Error(err))
		return nil, fmt.Errorf("failed to retrieve file: %w", err)
	}

	// 检查文件自身是否处于正常状态 (例如未被软删除或状态异常)
	if err = s.checkFile(fileToMove, userID); err != nil {
		logger.Warn("MoveFile: Cannot rename a deleted or abnormal file", zap.Uint64("fileID", fileID), zap.Uint8("status", fileToMove.Status))
		return nil, err
	}

	// 获取目标父文件夹信息并进行权限和状态检查
	targetParentFolder, err := s.ValidateParentFolder(userID, targetParentID)
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

	// 如果源文件夹的完整路径是目标父文件夹完整路径的前缀，那么就是移动到子目录
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
	finalFileName, err := s.resolveFileNameConflict(userID, targetParentID, fileToMove.FileName, fileID, fileToMove.IsFolder)
	if err != nil {
		return nil, err
	}
	fileToMove.FileName = finalFileName

	// 开启事务
	tx := s.db.Begin()
	if tx.Error != nil {
		tx.Rollback()
		logger.Error("MoveFile: Failed to update file name in DB transaction",
			zap.Uint64("fileID", fileToMove.ID),
			zap.String("newName", fileToMove.FileName),
			zap.Error(err))
		return nil, fmt.Errorf("failed to update file name in transaction: %w", err)
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

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

	if err = tx.Save(fileToMove).Error; err != nil {
		tx.Rollback()
		logger.Error("MoveFile: Failed to update file's parent and path in DB transaction",
			zap.Uint64("fileID", fileToMove.ID),
			zap.String("newName", fileToMove.FileName),
			zap.Error(err))
		return nil, fmt.Errorf("failed to update file name in transaction: %w", err)
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
		if err = s.fileRepo.UpdateFilesPathInBatch(
			tx, userID, oldChildPathPrefix, newChildPathPrefix); err != nil {
			tx.Rollback()
			logger.Error("MoveFile: Failed to update children paths in DB transaction",
				zap.Uint64("parentFolderID", fileToMove.ID), zap.Error(err))
			return nil, fmt.Errorf("%w: failed to update children paths: %w", xerr.ErrDatabaseTransaction, err)
		}
	}

	err = tx.Commit().Error
	if err != nil {
		logger.Error("MoveFile: Failed to commit transaction", zap.Error(err))
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	logger.Info("MoveFile: File/Folder moved successfully",
		zap.Uint64("fileID", fileID),
		zap.Reflect("targetParentID", targetParentID),
		zap.String("fileName", fileToMove.FileName),
		zap.String("oldPath", oldFullPathWithSelf), // 记录旧的完整路径
		zap.String("newPath", newFullPathWithSelf), // 记录新的完整路径
		zap.Uint64("userID", userID))

	return fileToMove, nil
}
