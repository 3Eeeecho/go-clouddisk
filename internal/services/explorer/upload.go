package explorer

import (
	"context"
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
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
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
	fileRepo repositories.FileRepository
	tm       TransactionManager
	storage  storage.StorageService
	deps     UploadServiceDeps
}

func NewUploadService(
	fileRepo repositories.FileRepository,
	tm TransactionManager,
	ss storage.StorageService,
	deps UploadServiceDeps,
) UploadService {
	return &uploadService{
		fileRepo: fileRepo,
		tm:       tm,
		storage:  ss,
		deps:     deps,
	}
}

// UploadInit 分片上传初始化,查询是否存在已上传的文件切片或者完整的文件
func (s *uploadService) UploadInit(ctx context.Context, userID uint64, req *models.UploadInitRequest) (*models.UploadInitResponse, error) {
	//查找是否存在相同文件,秒传逻辑
	existingFile, err := s.fileRepo.FindFileByMD5Hash(req.FileHash)
	if err == nil && existingFile != nil {
		logger.Info("UploadInit: File already exists, performing instant upload.", zap.String("md5Hash", req.FileHash))
		return &models.UploadInitResponse{FileExists: true}, nil
	}

	// 文件不存在，初始化分块上传
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
		FileExists: false,
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
	redisKey := generatePartKey(req.UploadID)
	partsMap, err := s.deps.Cache.HGetAll(ctx, redisKey)
	if err != nil {
		logger.Error("UploadComplete: Failed to get parts from redis", zap.Error(err), zap.String("uploadID", req.UploadID))
		return nil, fmt.Errorf("upload service: failed to get parts info: %w", err)
	}

	var parts []storage.UploadPartResult
	for partNumberStr, etag := range partsMap {
		partNumber, _ := strconv.Atoi(partNumberStr)
		parts = append(parts, storage.UploadPartResult{
			PartNumber: partNumber,
			ETag:       etag,
		})
	}

	// 为了保证顺序，最好对 parts 按 PartNumber 排序
	sort.Slice(parts, func(i, j int) bool {
		return parts[i].PartNumber < parts[j].PartNumber
	})

	objectName := s.storage.GetUploadObjName(req.FileHash, req.FileName)
	bucketName := s.deps.Config.MinIO.BucketName

	putResult, err := s.storage.CompleteMultiPartUpload(ctx, bucketName, objectName, req.UploadID, parts)
	if err != nil {
		logger.Error("UploadComplete: Failed to complete multipart upload", zap.Error(err), zap.String("uploadID", req.UploadID))
		// 考虑调用 AbortMultipartUpload 清理
		_ = s.storage.AbortMultiPartUpload(ctx, bucketName, objectName, req.UploadID)
		return nil, fmt.Errorf("upload service: failed to complete multipart upload: %w", err)
	}

	// 清理 Redis 中的缓存
	defer s.deps.Cache.Del(ctx, redisKey)

	var newFile *models.File
	err = s.tm.WithTransaction(ctx, func(tx *gorm.DB) error {
		fileRepo := repositories.NewFileRepository(tx, s.deps.Cache)
		newFile = &models.File{
			UserID:         userID,
			UUID:           uuid.NewString(),
			FileName:       req.FileName,
			ParentFolderID: req.ParentFolderID,
			Path:           "/", // TODO: 确定路径
			IsFolder:       0,
			MD5Hash:        &req.FileHash,
			Status:         1,
			Size:           uint64(putResult.Size), // 使用完成上传后返回的准确大小
			OssKey:         &putResult.Key,
			OssBucket:      &putResult.Bucket,
		}
		if createErr := fileRepo.Create(newFile); createErr != nil {
			logger.Error("UploadComplete: Failed to create file record", zap.Error(createErr))
			return fmt.Errorf("upload service: failed to create file record: %w", xerr.ErrDatabaseError)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	logger.Info("File record created successfully", zap.String("fileHash", req.FileHash), zap.Uint64("fileID", newFile.ID))
	return newFile, nil
}

func (s *uploadService) ListUploadedParts(ctx context.Context, req *models.ListPartsRequest) (*models.ListPartsResponse, error) {
	redisKey := generatePartKey(req.UploadID)
	partsMap, err := s.deps.Cache.HGetAll(ctx, redisKey)
	if err != nil {
		logger.Error("ListUploadedParts: Failed to get parts from redis", zap.Error(err), zap.String("uploadID", req.UploadID))
		return nil, fmt.Errorf("upload service: failed to list parts from redis: %w", err)
	}

	uploadedParts := make([]int, 0, len(partsMap))
	for partNumberStr := range partsMap {
		partNumber, err := strconv.Atoi(partNumberStr)
		if err == nil {
			uploadedParts = append(uploadedParts, partNumber)
		}
	}

	sort.Ints(uploadedParts) // 保证返回的列表是有序的

	return &models.ListPartsResponse{
		UploadedParts: uploadedParts,
	}, nil
}

func generatePartKey(uploadID string) string {
	return fmt.Sprintf("upload:%s:parts", uploadID)
}
