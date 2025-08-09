package explorer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/cache"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/mq"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/storage"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// MergeTask 定义合并任务的消息体
type MergeTask struct {
	FileID         uint64
	FileHash       string  `json:"file_hash"`
	FileName       string  `json:"file_name"`
	TotalChunks    int     `json:"total_chunks"`
	UserID         uint64  `json:"user_id"`
	ParentFolderID *uint64 `json:"parent_folder_id,omitempty"` // 用于记录文件归属
}

type UploadService interface {
	UploadInit(ctx context.Context, userID uint64, req *models.UploadInitRequest) (*models.UploadInitResponse, error)
	UploadChunk(ctx context.Context, userID uint64, req *models.UploadChunkRequest, chunkData io.Reader) error
	UploadComplete(ctx context.Context, userID uint64, req *models.UploadCompleteRequest) (*models.File, error)
}

type UploadServiceDeps struct {
	Cache    *cache.RedisCache
	MQClient *mq.RabbitMQClient
	Config   *config.Config
}

type uploadService struct {
	fileRepo  repositories.FileRepository
	chunkRepo repositories.ChunkRepository
	tm        TransactionManager
	deps      UploadServiceDeps
}

func NewUploadService(
	fileRepo repositories.FileRepository,
	chunkRepo repositories.ChunkRepository,
	tm TransactionManager,
	ss storage.StorageService,
	deps UploadServiceDeps,
) UploadService {
	return &uploadService{
		fileRepo:  fileRepo,
		chunkRepo: chunkRepo,
		tm:        tm,
		deps:      deps,
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
	err = s.tm.WithTransaction(ctx, func(tx *gorm.DB) error {
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
	err = s.tm.WithTransaction(ctx, func(tx *gorm.DB) error {
		// 在事务中创建文件记录
		fileRepo := repositories.NewFileRepository(tx, s.deps.Cache) // 使用事务内的DB
		newFile = &models.File{
			UserID:         userID,
			UUID:           uuid.NewString(),
			FileName:       req.FileName,
			ParentFolderID: req.ParentFolderID,
			Path:           "/", //TODO 根据ParentFolderID获取路径
			IsFolder:       0,
			MD5Hash:        &req.FileHash,
			Status:         1,
		}
		if createErr := fileRepo.Create(newFile); createErr != nil {
			return createErr
		}

		task := &MergeTask{
			FileID:         newFile.ID,
			FileHash:       req.FileHash,
			FileName:       req.FileName,
			TotalChunks:    req.TotalChunks,
			UserID:         userID,
			ParentFolderID: req.ParentFolderID,
		}
		taskBytes, err := json.Marshal(task)
		if err != nil {
			return fmt.Errorf("failed to marshal merge task: %w", err)
		}

		if err := s.deps.MQClient.Publish(taskBytes); err != nil {
			return fmt.Errorf("failed to publish merge task to rabbitmq: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	logger.Info("Merge task published to RabbitMQ", zap.String("fileHash", req.FileHash), zap.Uint64("userID", userID))
	return newFile, nil
}
