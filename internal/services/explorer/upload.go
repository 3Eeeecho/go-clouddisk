package explorer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

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

// UploadInit 处理分片上传的初始化。
// 它通过检查 Redis 中是否存在上传会话来支持断点续传。
func (s *uploadService) UploadInit(ctx context.Context, userID uint64, req *models.UploadInitRequest) (*models.UploadInitResponse, error) {
	// 注意：根据之前的要求，检查完整文件是否存在（秒传）的逻辑已被移除。
	// 版本控制完全由 UploadComplete 处理。

	objectName := s.storage.GetUploadObjName(req.FileHash, req.FileName)
	bucketName := s.deps.Config.MinIO.BucketName
	redisKey := fmt.Sprintf("uploadid:%s", req.FileHash)

	// 1. 尝试从 Redis 获取已存在的 uploadID
	var uploadID string
	err := s.deps.Cache.Get(ctx, redisKey, &uploadID)
	if err == nil && uploadID != "" {
		// 2. 如果 uploadID 存在，则检查其在 MinIO 中的状态
		parts, err := s.storage.ListObjectParts(ctx, bucketName, objectName, uploadID)
		if err != nil {
			if s.storage.IsUploadIDNotFound(err) {
				// MinIO 中的会话已过期或被中止。开启一个新的会话。
				logger.Warn("UploadInit: 在 Redis 中找到 UploadID 但在存储中未找到，正在重新初始化。", zap.String("uploadID", uploadID))
				return s.startNewUploadSession(ctx, bucketName, objectName, redisKey)
			}
			logger.Error("UploadInit: 为已存在的 UploadID 列出分片失败", zap.Error(err), zap.String("uploadID", uploadID))
			return nil, fmt.Errorf("upload service: failed to list parts: %w", err)
		}

		// 会话有效，返回现有状态
		logger.Info("UploadInit: 正在恢复已存在的上传会话", zap.String("uploadID", uploadID), zap.Int("partCount", len(parts)))
		return &models.UploadInitResponse{
			FileExists:    false,
			UploadID:      uploadID,
			UploadedParts: convertToModelParts(parts),
		}, nil
	}

	// 3. 如果 Redis 中没有 uploadID 或 Redis 返回错误（例如 redis.Nil），则启动一个新会话。
	if err != cache.ErrCacheMiss && err != nil {
		logger.Error("UploadInit: 从 Redis 获取 uploadID 失败，继续执行新会话", zap.Error(err))
	}

	return s.startNewUploadSession(ctx, bucketName, objectName, redisKey)
}

// startNewUploadSession 在存储中初始化一个新的分片上传并将该会话保存到 Redis。
func (s *uploadService) startNewUploadSession(ctx context.Context, bucketName, objectName, redisKey string) (*models.UploadInitResponse, error) {
	newUploadID, err := s.storage.InitMultiPartUpload(ctx, bucketName, objectName, storage.PutObjectOptions{
		ContentType: "application/octet-stream",
	})
	if err != nil {
		logger.Error("startNewUploadSession: 初始化分片上传失败", zap.Error(err))
		return nil, fmt.Errorf("upload service: failed to init multipart upload: %w", err)
	}

	// 将新的 uploadID 存储在 Redis 中，有效期为 24 小时
	err = s.deps.Cache.Set(ctx, redisKey, newUploadID, 24*time.Hour) // 24 小时
	if err != nil {
		// 如果无法保存到 Redis，我们应该中止 MinIO 上传以避免孤儿上传。
		logger.Error("startNewUploadSession: 无法将新的 uploadID 保存到 Redis", zap.Error(err), zap.String("uploadID", newUploadID))
		_ = s.storage.AbortMultiPartUpload(ctx, bucketName, objectName, newUploadID)
		return nil, fmt.Errorf("upload service: failed to save session: %w", err)
	}

	logger.Info("startNewUploadSession: 已启动新的上传会话", zap.String("uploadID", newUploadID))
	return &models.UploadInitResponse{
		FileExists:    false,
		UploadID:      newUploadID,
		UploadedParts: []models.UploadPartInfo{},
	}, nil
}

// convertToModelParts 将存储分片信息转换为模型分片信息。
func convertToModelParts(storageParts []storage.UploadPartResult) []models.UploadPartInfo {
	modelParts := make([]models.UploadPartInfo, len(storageParts))
	for i, p := range storageParts {
		modelParts[i] = models.UploadPartInfo{
			PartNumber: p.PartNumber,
			ETag:       p.ETag,
		}
	}
	return modelParts
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
		// TODO 注意：这里上传已经成功，但记录失败。需要考虑补偿策略或更强的事务保证。
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
		dbFileRepo := repositories.NewDBFileRepository(tx)
		fileRepo := repositories.NewCachedFileRepository(dbFileRepo, s.deps.Cache)
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

			// 添加新版本记录
			logger.Info("putResult.Size", zap.Uint64("putResult.Size", uint64(putResult.Size)))
			newVersion := &models.FileVersion{
				FileID:    existingFile.ID,
				Version:   uint(newVersionNumber),
				Size:      uint64(putResult.Size),
				OssKey:    putResult.Key,
				VersionID: putResult.VersionID,
				MD5Hash:   req.FileHash,
			}
			if err := fileVersionRepo.Create(newVersion); err != nil {
				return fmt.Errorf("failed to create new file version: %w", err)
			}

			// 更新主文件记录
			existingFile.Size = uint64(putResult.Size)
			existingFile.MD5Hash = &req.FileHash
			existingFile.OssKey = &putResult.Key
			existingFile.MimeType = &req.MimeType
			existingFile.VersionID = &putResult.VersionID
			if err := fileRepo.Update(existingFile); err != nil {
				return fmt.Errorf("failed to update main file record: %w", err)
			}
			finalFile = existingFile
		} else {
			// --- 创建新文件 ---
			var parentPath = "/"
			if req.ParentFolderID != nil {
				parent, err := fileRepo.FindByID(*req.ParentFolderID)
				if err != nil {
					return fmt.Errorf("failed to find parent folder: %w", err)
				}
				parentPath = parent.Path + parent.FileName + "/"
			}

			newFile := &models.File{
				UserID:         userID,
				UUID:           uuid.NewString(),
				FileName:       req.FileName,
				ParentFolderID: req.ParentFolderID,
				Path:           parentPath,
				IsFolder:       0,
				MimeType:       &req.MimeType,
				VersionID:      &putResult.VersionID,
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
				MD5Hash:   req.FileHash,
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

func generatePartKey(uploadID string) string {
	return fmt.Sprintf("upload:%s:parts", uploadID)
}
