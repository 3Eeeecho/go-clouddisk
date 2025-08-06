package explorer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/cache"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/storage"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type UploadService interface {
	UploadInit(ctx context.Context, userID uint64, req *models.UploadInitRequest) (*models.UploadInitResponse, error)
	UploadChunk(ctx context.Context, userID uint64, req *models.UploadChunkRequest, chunkData io.Reader) error
	UploadComplete(ctx context.Context, userID uint64, req *models.UploadCompleteRequest) (*models.File, error)
}

type uploadService struct {
	fileRepo           repositories.FileRepository
	chunkRepo          repositories.ChunkRepository // 假设你有一个ChunkRepository
	transactionManager TransactionManager
	storageService     storage.StorageService
	cache              *cache.RedisCache
	cfg                *config.Config
}

func NewUploadService(
	fileRepo repositories.FileRepository,
	chunkRepo repositories.ChunkRepository,
	tm TransactionManager,
	ss storage.StorageService,
	cache *cache.RedisCache,
	cfg *config.Config,
) UploadService {
	return &uploadService{
		fileRepo:           fileRepo,
		chunkRepo:          chunkRepo,
		transactionManager: tm,
		storageService:     ss,
		cache:              cache,
		cfg:                cfg,
	}
}

// UploadInit 检查文件是否已存在（秒传），或返回已上传的分片列表
func (s *uploadService) UploadInit(ctx context.Context, userID uint64, req *models.UploadInitRequest) (*models.UploadInitResponse, error) {
	// 1. 检查文件是否已存在 (秒传)
	// 这里 FindFileByMD5Hash 的逻辑需要修改，以区分是不同用户但文件已存在的情况
	existingFile, err := s.fileRepo.FindFileByMD5Hash(req.FileHash)
	if err == nil && existingFile != nil {
		logger.Info("UploadInit: File already exists for MD5 hash, performing instant upload",
			zap.Uint64("userID", userID),
			zap.String("md5Hash", req.FileHash))
		return &models.UploadInitResponse{FileExists: true}, nil
	}

	// 2. 查找已上传的分片
	chunks, err := s.chunkRepo.GetUploadedChunks(req.FileHash)
	if err != nil {
		return nil, fmt.Errorf("failed to get uploaded chunks: %w", err)
	}

	uploadedIndexes := make([]int, 0, len(chunks))
	for _, chunk := range chunks {
		uploadedIndexes = append(uploadedIndexes, chunk.ChunkIndex)
	}

	return &models.UploadInitResponse{
		FileExists:     false,
		UploadedChunks: uploadedIndexes,
	}, nil
}

// UploadChunk 保存分片到本地临时目录并更新数据库
func (s *uploadService) UploadChunk(ctx context.Context, userID uint64, req *models.UploadChunkRequest, chunkData io.Reader) error {
	// 1. 将分片保存到临时目录
	tempDir := filepath.Join(os.TempDir(), "clouddisk_chunks", req.FileHash)
	if err := os.MkdirAll(tempDir, os.ModePerm); err != nil {
		logger.Error("UploadChunk: Failed to create temp directory", zap.Error(err), zap.String("dir", tempDir))
		return fmt.Errorf("failed to create temp directory: %w", err)
	}

	tempFilePath := filepath.Join(tempDir, fmt.Sprintf("%d.chunk", req.ChunkIndex))
	logger.Info("tempFilePath", zap.String("path:", tempFilePath))

	outFile, err := os.Create(tempFilePath)
	if err != nil {
		logger.Error("UploadChunk: Failed to create chunk file", zap.Error(err), zap.String("path", tempFilePath))
		return fmt.Errorf("failed to create chunk file: %w", err)
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, chunkData); err != nil {
		logger.Error("UploadChunk: Failed to write chunk data", zap.Error(err), zap.String("path", tempFilePath))
		return fmt.Errorf("failed to write chunk data: %w", err)
	}

	// 2. 更新数据库分片状态
	// 这里使用事务来保证原子性
	err = s.transactionManager.WithTransaction(ctx, func(tx *gorm.DB) error {
		chunkRepo := repositories.NewChunkRepository(tx, nil) // 使用事务内的DB
		chunk := models.Chunk{
			FileHash:   req.FileHash,
			ChunkIndex: req.ChunkIndex,
		}
		if err := chunkRepo.SaveChunk(&chunk); err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		// 如果数据库操作失败，可以考虑删除已写入的临时文件
		os.Remove(tempFilePath)
		return fmt.Errorf("failed to save chunk record: %w", err)
	}

	logger.Info("UploadChunk: Chunk uploaded and record saved",
		zap.String("fileHash", req.FileHash),
		zap.Int("chunkIndex", req.ChunkIndex))
	return nil
}

// UploadComplete 合并所有分片，上传到OSS，并创建文件记录
func (s *uploadService) UploadComplete(ctx context.Context, userID uint64, req *models.UploadCompleteRequest) (*models.File, error) {
	// 1. 验证所有分片是否都已上传
	uploadedCount, err := s.chunkRepo.CountUploadedChunks(req.FileHash)
	if err != nil || uploadedCount != int64(req.TotalChunks) {
		logger.Warn("UploadComplete: Not all chunks uploaded",
			zap.String("fileHash", req.FileHash),
			zap.Int64("uploaded", uploadedCount),
			zap.Int("total", req.TotalChunks))
		return nil, errors.New("not all chunks have been uploaded")
	}

	var newFile *models.File
	err = s.transactionManager.WithTransaction(ctx, func(tx *gorm.DB) error {
		// 2. 合并分片并上传到 OSS
		ossKey := fmt.Sprintf("%s/%s", req.FileHash, uuid.NewString())
		fileSize, bucketName, contentType, uploadErr := s.mergeAndUpload(ctx, ossKey, req.FileHash, req.TotalChunks, req.FileName)
		if uploadErr != nil {
			return uploadErr
		}

		// 3. 在事务中创建文件记录
		fileRepo := repositories.NewFileRepository(tx, s.cache) // 使用事务内的DB
		newFile = &models.File{
			UserID:    userID,
			UUID:      uuid.NewString(),
			FileName:  req.FileName,
			Path:      "/", //默认为根目录
			IsFolder:  0,
			Size:      uint64(fileSize),
			OssKey:    &ossKey,
			OssBucket: &bucketName,
			MD5Hash:   &req.FileHash,
			MimeType:  &contentType,
			Status:    1,
		}
		if createErr := fileRepo.Create(newFile); createErr != nil {
			return createErr
		}

		// 4. 清理临时分片和数据库记录 (如果事务成功)
		// 这里只在事务中删除 Chunk 记录，临时文件在事务外清理
		if cleanupErr := s.chunkRepo.DeleteChunksByFileHash(req.FileHash); cleanupErr != nil {
			// 记录警告，不阻塞事务提交
			logger.Warn("UploadComplete: Failed to delete chunk records in transaction", zap.Error(cleanupErr))
		}
		return nil
	})

	// 事务结束后，清理临时文件s
	if err == nil {
		s.cleanupChunks(req.FileHash)
	}

	if err != nil {
		logger.Error("UploadComplete: Transaction failed", zap.Error(err))
		return nil, fmt.Errorf("failed to complete upload transaction: %w", err)
	}

	logger.Info("UploadComplete: File uploaded and merged successfully",
		zap.Uint64("fileID", newFile.ID),
		zap.Uint64("userID", userID))

	return newFile, nil
}

// mergeAndUpload 合并分片文件并上传到 OSS (私有辅助方法)
func (s *uploadService) mergeAndUpload(ctx context.Context, ossKey, fileHash string, totalChunks int, fileName string) (int64, string, string, error) {
	tempDir := filepath.Join(os.TempDir(), "clouddisk_chunks", fileHash)

	var readers []io.Reader
	var totalSize int64
	for i := range totalChunks {
		chunkPath := filepath.Join(tempDir, fmt.Sprintf("%d.chunk", i))
		chunkFile, err := os.Open(chunkPath)
		if err != nil {
			return 0, "", "", fmt.Errorf("failed to open chunk %d at %s: %w", i, chunkPath, err)
		}
		defer chunkFile.Close()

		info, _ := chunkFile.Stat()
		totalSize += info.Size()
		readers = append(readers, chunkFile)
	}

	mergedReader := io.MultiReader(readers...)

	// 根据存储类型获取 bucketName
	var bucketName string
	switch s.cfg.Storage.Type {
	case "minio":
		bucketName = s.cfg.MinIO.BucketName
	case "aliyun_oss":
		bucketName = s.cfg.AliyunOSS.BucketName
	default:
		return 0, "", "", errors.New("unsupported storage type")
	}

	// 推断文件类型(Content-Type/MimeType)
	ext := filepath.Ext(fileName)
	contentType := "application/octet-stream" // 默认值
	if mimeType := mime.TypeByExtension(ext); mimeType != "" {
		contentType = mimeType
	}

	// 调用 PutObject 方法
	// PutObject 还需要 contentType 参数，这里可以根据文件名推断，为了简化，先给空值
	_, err := s.storageService.PutObject(ctx, bucketName, ossKey, mergedReader, totalSize, contentType)
	if err != nil {
		return 0, "", "", fmt.Errorf("failed to upload merged file to storage: %w", err)
	}

	return totalSize, bucketName, contentType, nil
}

// cleanupChunks 清理临时分片文件 (私有辅助方法)
func (s *uploadService) cleanupChunks(fileHash string) error {
	tempDir := filepath.Join(os.TempDir(), "clouddisk_chunks", fileHash)
	if err := os.RemoveAll(tempDir); err != nil {
		logger.Error("cleanupChunks: Failed to remove temporary chunk directory", zap.Error(err), zap.String("dir", tempDir))
		return fmt.Errorf("failed to remove temporary chunk directory: %w", err)
	}
	return nil
}
