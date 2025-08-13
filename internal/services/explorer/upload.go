package explorer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"

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
// type MergeTask struct {
// 	FileID         uint64
// 	FileHash       string  `json:"file_hash"`
// 	FileName       string  `json:"file_name"`
// 	TotalChunks    int     `json:"total_chunks"`
// 	UserID         uint64  `json:"user_id"`
// 	ParentFolderID *uint64 `json:"parent_folder_id,omitempty"` // 用于记录文件归属
// }

type UploadService interface {
	UploadInit(ctx context.Context, userID uint64, req *models.UploadInitRequest) (*models.UploadInitResponse, error)
	UploadChunk(ctx context.Context, userID uint64, req *models.UploadChunkRequest, chunkData io.Reader) error
	UploadComplete(ctx context.Context, userID uint64, req *models.UploadCompleteRequest) (*models.File, error)
	ListUploadedParts(ctx context.Context, req *models.ListPartsRequest) (*models.ListPartsResponse, error)
}

type UploadServiceDeps struct {
	Cache    *cache.RedisCache
	MQClient *mq.RabbitMQClient
	Config   *config.Config
}

type uploadService struct {
	fileRepo        repositories.FileRepository
	fileVersionRepo repositories.FileVersionRepository
	tm              TransactionManager
	storage         storage.StorageService
	deps            UploadServiceDeps
}

func NewUploadService(
	fileRepo repositories.FileRepository,
	fileVersionRepo repositories.FileVersionRepository,
	tm TransactionManager,
	ss storage.StorageService,
	deps UploadServiceDeps,
) UploadService {
	return &uploadService{
		fileRepo:        fileRepo,
		fileVersionRepo: fileVersionRepo,
		tm:              tm,
		storage:         ss,
		deps:            deps,
	}
}

// UploadInit 分片上传初始化,查询是否存在已上传的文件切片或者完整的文件
func (s *uploadService) UploadInit(ctx context.Context, userID uint64, req *models.UploadInitRequest) (*models.UploadInitResponse, error) {
	// 根据用户反馈，移除秒传逻辑，总是初始化一个新的上传会话。
	// 版本控制的逻辑完全由 UploadComplete 处理。
	objectName := s.storage.GetUploadObjName(req.FileHash, req.FileName)
	bucketName := s.deps.Config.MinIO.BucketName

	uploadID, err := s.storage.InitMultiPartUpload(ctx, bucketName, objectName, storage.PutObjectOptions{
		ContentType: "application/octet-stream",
	})
	if err != nil {
		logger.Error("UploadInit: Failed to init multipart upload", zap.Error(err))
		return nil, fmt.Errorf("upload service: failed to init multipart upload: %w", err)
	}

	return &models.UploadInitResponse{
		FileExists: false, // 因为取消了秒传，所以这里总是 false
		UploadID:   uploadID,
	}, nil
}

// UploadChunk 处理分片上传
func (s *uploadService) UploadChunk(ctx context.Context, userID uint64, req *models.UploadChunkRequest, chunkData io.Reader) error {
	objectName := s.storage.GetUploadObjName(req.FileHash, req.FileName)
	bucketName := s.deps.Config.MinIO.BucketName

	partResult, err := s.storage.UploadPart(ctx, bucketName, objectName, req.UploadID, chunkData, req.ChunkNumber, req.ChunkSize)
	if err != nil {
		logger.Error("UploadChunk: Failed to upload part", zap.Error(err), zap.String("uploadID", req.UploadID))
		return fmt.Errorf("upload service: failed to upload part: %w", err)
	}

	// 将上传成功的分块信息存入 Redis
	// 使用 Hash 存储，Key: uploadID, Field: partNumber, Value: ETag
	redisKey := fmt.Sprintf("upload:%s:parts", req.UploadID)
	err = s.deps.Cache.HSet(ctx, redisKey, fmt.Sprintf("%d", partResult.PartNumber), partResult.ETag)
	if err != nil {
		logger.Error("UploadChunk: Failed to save part info to redis", zap.Error(err), zap.String("uploadID", req.UploadID))
		// 注意：这里上传已经成功，但记录失败。需要考虑补偿策略或更强的事务保证。
		// 简单起见，我们先返回错误。
		return fmt.Errorf("upload service: failed to save part info: %w", err)
	}

	logger.Info("UploadChunk: Part uploaded successfully",
		zap.String("uploadID", req.UploadID),
		zap.Int("partNumber", partResult.PartNumber),
		zap.String("etag", partResult.ETag))

	return nil
}

// UploadComplete now only creates the final file metadata record in the database.
func (s *uploadService) UploadComplete(ctx context.Context, userID uint64, req *models.UploadCompleteRequest) (*models.File, error) {
	// 1. 合并分块
	redisKey := generatePartKey(req.UploadID)
	partsMap, err := s.deps.Cache.HGetAll(ctx, redisKey)
	if err != nil {
		logger.Error("UploadComplete: Failed to get parts from redis", zap.Error(err), zap.String("uploadID", req.UploadID))
		return nil, fmt.Errorf("upload service: failed to get parts info: %w", err)
	}

	var parts []storage.UploadPartResult
	for partNumberStr, etag := range partsMap {
		partNumber, _ := strconv.Atoi(partNumberStr)
		parts = append(parts, storage.UploadPartResult{PartNumber: partNumber, ETag: etag})
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })

	objectName := s.storage.GetUploadObjName(req.FileHash, req.FileName)
	bucketName := s.deps.Config.MinIO.BucketName

	putResult, err := s.storage.CompleteMultiPartUpload(ctx, bucketName, objectName, req.UploadID, parts)
	if err != nil {
		logger.Error("UploadComplete: Failed to complete multipart upload", zap.Error(err), zap.String("uploadID", req.UploadID))
		_ = s.storage.AbortMultiPartUpload(ctx, bucketName, objectName, req.UploadID)
		return nil, fmt.Errorf("upload service: failed to complete multipart upload: %w", err)
	}
	// 清理 Redis 中的缓存
	logger.Info("UploadComplete: Clearing redis cache for completed upload", zap.String("uploadID", req.UploadID))
	defer s.deps.Cache.Del(ctx, redisKey)

	// 2. 数据库操作
	var finalFile *models.File
	err = s.tm.WithTransaction(ctx, func(tx *gorm.DB) error {
		fileRepo := repositories.NewFileRepository(tx, s.deps.Cache)
		fileVersionRepo := repositories.NewFileVersionRepository(tx)

		// 检查是否存在同名文件的旧版本
		existingFile, err := fileRepo.FindByFileName(userID, req.ParentFolderID, req.FileName)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("failed to check for existing file: %w", err)
		}

		if existingFile != nil && err == nil {
			// --- 创建新版本 ---
			latestVersion, err := fileVersionRepo.FindLatestVersion(existingFile.ID)
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("failed to find latest version: %w", err)
			}

			newVersionNumber := 1
			if latestVersion != nil {
				newVersionNumber = int(latestVersion.Version) + 1
			}

			logger.Info("putResult.Size", zap.Uint64("putResult.Size", uint64(putResult.Size)))
			newVersion := &models.FileVersion{
				FileID:    existingFile.ID,
				Version:   uint(newVersionNumber),
				Size:      uint64(putResult.Size),
				OssKey:    putResult.Key,
				VersionID: putResult.VersionID,
			}
			if err := fileVersionRepo.Create(newVersion); err != nil {
				return fmt.Errorf("failed to create new file version: %w", err)
			}

			// 更新主文件记录
			existingFile.Size = uint64(putResult.Size)
			existingFile.MD5Hash = &req.FileHash
			existingFile.OssKey = &putResult.Key
			if err := fileRepo.Update(existingFile); err != nil {
				return fmt.Errorf("failed to update main file record: %w", err)
			}
			finalFile = existingFile
		} else {
			// --- 创建新文件 ---
			newFile := &models.File{
				UserID:         userID,
				UUID:           uuid.NewString(),
				FileName:       req.FileName,
				ParentFolderID: req.ParentFolderID,
				Path:           "/", // TODO: 确定路径
				IsFolder:       0,
				MD5Hash:        &req.FileHash,
				Status:         models.StatusNormal,
				Size:           uint64(putResult.Size),
				OssKey:         &putResult.Key,
				OssBucket:      &bucketName,
			}
			// 1. 先创建主文件记录
			if err := fileRepo.Create(newFile); err != nil {
				return fmt.Errorf("failed to create new file: %w", err)
			}

			fmt.Println("fileID", newFile.ID)

			// 2. 为新文件创建第一个版本记录
			firstVersion := &models.FileVersion{
				FileID:    newFile.ID,
				Version:   1,
				Size:      uint64(putResult.Size),
				OssKey:    putResult.Key,
				VersionID: putResult.VersionID,
			}
			if err := fileVersionRepo.Create(firstVersion); err != nil {
				return fmt.Errorf("failed to create first file version: %w", err)
			}
			finalFile = newFile
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	logger.Info("Upload complete and versioning handled", zap.Uint64("fileID", finalFile.ID))
	return finalFile, nil
}

func (s *uploadService) ListUploadedParts(ctx context.Context, req *models.ListPartsRequest) (*models.ListPartsResponse, error) {
	redisKey := generatePartKey(req.UploadID)
	partsMap, err := s.deps.Cache.HGetAll(ctx, redisKey)
	if err == nil && len(partsMap) > 0 {
		// 缓存命中，直接返回
		var uploadedParts []int
		for partNumberStr := range partsMap {
			partNumber, _ := strconv.Atoi(partNumberStr)
			uploadedParts = append(uploadedParts, partNumber)
		}
		sort.Ints(uploadedParts)
		return &models.ListPartsResponse{UploadedParts: uploadedParts}, nil
	}

	// 缓存未命中或为空，回源到 MinIO
	logger.Info("ListUploadedParts: Cache miss, fetching from storage", zap.String("uploadID", req.UploadID))
	objectName := s.storage.GetUploadObjName(req.FileHash, req.FileName)
	bucketName := s.deps.Config.MinIO.BucketName
	storageParts, err := s.storage.ListObjectParts(ctx, bucketName, objectName, req.UploadID)
	if err != nil {
		// 检查是否是 "uploadID not found" 类型的错误
		if s.storage.IsUploadIDNotFound(err) {
			logger.Warn("ListUploadedParts: Upload session not found in storage, re-initializing.", zap.String("uploadID", req.UploadID))

			// 重新初始化一个新的分片上传
			newUploadID, initErr := s.storage.InitMultiPartUpload(ctx, bucketName, objectName, storage.PutObjectOptions{
				ContentType: "application/octet-stream",
			})
			if initErr != nil {
				logger.Error("ListUploadedParts: Failed to re-initialize multipart upload", zap.Error(initErr))
				return nil, fmt.Errorf("upload service: failed to re-initialize multipart upload: %w", initErr)
			}

			// 返回新的 UploadID 和空的分片列表，并携带一个特殊标志
			return &models.ListPartsResponse{
				UploadedParts:  []int{},
				NewUploadID:    newUploadID, // 客户端需要使用这个新的ID
				SessionExpired: true,
			}, nil
		}

		logger.Error("ListUploadedParts: Failed to list parts from storage", zap.Error(err), zap.String("uploadID", req.UploadID))
		return nil, fmt.Errorf("upload service: failed to list parts from storage: %w", err)
	}

	// 回填缓存并返回结果
	var uploadedParts []int
	pipe := s.deps.Cache.TxPipeline()
	for _, part := range storageParts {
		uploadedParts = append(uploadedParts, part.PartNumber)
		pipe.HSet(ctx, redisKey, fmt.Sprintf("%d", part.PartNumber), part.ETag)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		logger.Error("ListUploadedParts: Failed to backfill cache", zap.Error(err), zap.String("uploadID", req.UploadID))
		// 即使缓存回填失败，也应该返回从存储中获取的正确结果
	}

	sort.Ints(uploadedParts)
	return &models.ListPartsResponse{UploadedParts: uploadedParts}, nil
}

func generatePartKey(uploadID string) string {
	return fmt.Sprintf("upload:%s:parts", uploadID)
}
