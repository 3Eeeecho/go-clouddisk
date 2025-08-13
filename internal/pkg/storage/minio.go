package storage

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.uber.org/zap"
)

type MinIOStorageService struct {
	client *minio.Client
	core   *minio.Core
	cfg    *config.MinIOConfig // MinIO的配置信息
}

// NewMinIOStorageService 创建并返回一个 MinIOStorageService 实例
func NewMinIOStorageService(cfg *config.MinIOConfig) (*MinIOStorageService, error) {
	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: cfg.UseSSL, // 根据配置决定是否使用 HTTPS
	}

	minioClient, err := minio.New(cfg.Endpoint, opts)
	if err != nil {
		logger.Error("初始化 MinIO 客户端失败", zap.Error(err))
		return nil, fmt.Errorf("无法初始化 MinIO 客户端: %w", err)
	}

	minioCore, err := minio.NewCore(cfg.Endpoint, opts)
	if err != nil {
		logger.Error("初始化 MinIO Core 失败", zap.Error(err))
		return nil, fmt.Errorf("无法初始化 MinIO Core: %w", err)
	}

	logger.Info("MinIO 客户端和 Core 初始化成功", zap.String("endpoint", cfg.Endpoint))

	// 检查并创建存储桶，然后开启版本控制
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exists, err := minioClient.BucketExists(ctx, cfg.BucketName)
	if err != nil {
		return nil, fmt.Errorf("检查 MinIO 存储桶存在性失败: %w", err)
	}
	if !exists {
		if err := minioClient.MakeBucket(ctx, cfg.BucketName, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("创建 MinIO 存储桶失败: %w", err)
		}
		logger.Info("MinIO 存储桶创建成功", zap.String("bucketName", cfg.BucketName))
	}

	// 开启版本控制
	versioningConfig := minio.BucketVersioningConfiguration{Status: "Enabled"}
	if err := minioClient.SetBucketVersioning(ctx, cfg.BucketName, versioningConfig); err != nil {
		return nil, fmt.Errorf("开启 MinIO 存储桶版本控制失败: %w", err)
	}
	logger.Info("MinIO 存储桶版本控制已开启", zap.String("bucketName", cfg.BucketName))

	return &MinIOStorageService{
		client: minioClient,
		core:   minioCore,
		cfg:    cfg,
	}, nil
}

func (s *MinIOStorageService) PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objcetSize int64, contentType string) (PutObjectResult, error) {
	info, err := s.client.PutObject(ctx, bucketName, objectName, reader, objcetSize, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return PutObjectResult{}, fmt.Errorf("MinIO 上传文件失败: %w", err)
	}
	return PutObjectResult{
		Bucket:    info.Bucket,
		Key:       info.Key,
		Size:      info.Size,
		ETag:      info.ETag,
		VersionID: info.VersionID,
	}, nil
}

func (s *MinIOStorageService) GetObject(ctx context.Context, bucketName, objectName, versionID string) (GetObjectResult, error) {
	logger.Info("GetObject", zap.String("versionID", versionID))
	opts := minio.GetObjectOptions{}
	if versionID != "" {
		opts.VersionID = versionID
	}
	logger.Info("GetObject", zap.String("opts.VersionID", opts.VersionID))
	obj, err := s.client.GetObject(ctx, bucketName, objectName, opts)
	if err != nil {
		return GetObjectResult{}, fmt.Errorf("MinIO 获取文件失败: %w", err)
	}
	// 获取对象信息，这里需要读取一部分才能获取到
	objectStat, err := obj.Stat()
	if err != nil {
		// 如果 Stat 失败，尝试返回基本信息，但可能不完整
		logger.Warn("获取 MinIO 对象 stat 失败", zap.String("object", objectName), zap.Error(err))
		return GetObjectResult{
			Reader: obj,
			Size:   -1, // 无法确定大小
		}, nil
	}

	return GetObjectResult{
		Reader:   obj,
		Size:     objectStat.Size,
		MimeType: objectStat.ContentType,
	}, nil
}

// 从指定存储桶删除指定版本文件
func (s *MinIOStorageService) RemoveObject(ctx context.Context, bucketName, objectName, VersionID string) error {
	//TODO 处理空版本号问题
	opts := &minio.RemoveObjectOptions{
		GovernanceBypass: true,
		VersionID:        VersionID,
	}
	err := s.client.RemoveObject(ctx, bucketName, objectName, *opts)
	if err != nil {
		return fmt.Errorf("failed to remove object version: %w", err)
	}
	return nil
}

func (s *MinIOStorageService) RemoveObjects(ctx context.Context, bucketName, objectName string) error {
	//TODO 对重要版本添加标记防止被自动删除
	objectsCh := make(chan minio.ObjectInfo)

	go func() {
		defer close(objectsCh)
		// 列出桶内Object所有版本号,并发送到channel
		for object := range s.client.ListObjects(ctx, bucketName, minio.ListObjectsOptions{
			Prefix:       objectName,
			Recursive:    true,
			WithVersions: true,
		}) {
			if object.Err != nil {
				logger.Error("Error listing objects", zap.Error(object.Err))
				return
			}
			objectsCh <- object
		}
	}()

	opts := minio.RemoveObjectsOptions{
		GovernanceBypass: true,
	}

	//删除所有版本的物理文件
	errorCh := s.client.RemoveObjects(ctx, bucketName, objectsCh, opts)

	// 从errChannel检查错误
	for e := range errorCh {
		logger.Error("Failed to remove object", zap.String("object", e.ObjectName), zap.String("version", e.VersionID), zap.Error(e.Err))
	}

	return nil
}

func (s *MinIOStorageService) IsBucketExist(ctx context.Context, bucketName string) (bool, error) {
	found, err := s.client.BucketExists(ctx, bucketName)
	if err != nil {
		return false, fmt.Errorf("检查 MinIO 存储桶存在性失败: %w", err)
	}
	return found, nil
}

func (s *MinIOStorageService) MakeBucket(ctx context.Context, bucketName string) error {
	err := s.client.MakeBucket(ctx, bucketName, minio.MakeBucketOptions{})
	if err != nil {
		// 如果桶已存在，通常不是错误
		exists, errBucketExists := s.client.BucketExists(ctx, bucketName)
		if errBucketExists == nil && exists {
			logger.Info("MinIO 存储桶已存在，无需创建", zap.String("bucket", bucketName))
			return nil
		}
		return fmt.Errorf("创建 MinIO 存储桶失败: %w", err)
	}
	logger.Info("MinIO 存储桶创建成功", zap.String("bucket", bucketName))
	return nil
}

// GetObjectURL 实现 StorageService 接口的 GetObjectURL 方法
func (s *MinIOStorageService) GetObjectURL(bucketName, objectName string) string {
	// MinIO 的 URL 格式通常是：Endpoint/bucketName/objectName
	// 确保 Endpoint 有前缀，如 http:// 或 https://
	endpoint := s.cfg.Endpoint
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "http://" + endpoint // 默认为 HTTP，取决于你的 MinIO 配置
	}
	return fmt.Sprintf("%s/%s/%s", endpoint, bucketName, objectName)
}

// PreSignGetObjectURL 为下载生成预签名URL (如果需要，MinIO支持)
func (s *MinIOStorageService) PreSignGetObjectURL(ctx context.Context, bucketName, objectName string, expiry time.Duration) (string, error) {
	presignedURL, err := s.client.Presign(ctx, "GET", bucketName, objectName, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("生成 MinIO 预签名URL失败: %w", err)
	}
	return presignedURL.String(), nil
}

// --- 分块上传实现 ---

func (s *MinIOStorageService) InitMultiPartUpload(ctx context.Context, bucketName, objectName string, opts PutObjectOptions) (string, error) {
	uploadID, err := s.core.NewMultipartUpload(ctx, bucketName, objectName, minio.PutObjectOptions{
		ContentType: opts.ContentType,
	})
	if err != nil {
		return "", fmt.Errorf("MinIO 初始化分块上传失败: %w", err)
	}
	return uploadID, nil
}

func (s *MinIOStorageService) UploadPart(ctx context.Context, bucketName, objectName, uploadID string, reader io.Reader, partNumber int, partSize int64) (UploadPartResult, error) {
	uploadInfo, err := s.core.PutObjectPart(ctx, bucketName, objectName, uploadID, partNumber, reader, partSize, minio.PutObjectPartOptions{})
	if err != nil {
		return UploadPartResult{}, fmt.Errorf("MinIO 上传分块失败: %w", err)
	}
	return UploadPartResult{
		PartNumber: uploadInfo.PartNumber,
		ETag:       uploadInfo.ETag,
	}, nil
}

func (s *MinIOStorageService) CompleteMultiPartUpload(ctx context.Context, bucketName, objectName, uploadID string, parts []UploadPartResult) (PutObjectResult, error) {
	var completeParts []minio.CompletePart
	for _, part := range parts {
		completeParts = append(completeParts, minio.CompletePart{
			PartNumber: part.PartNumber,
			ETag:       part.ETag,
		})
	}

	uploadInfo, err := s.core.CompleteMultipartUpload(ctx, bucketName, objectName, uploadID, completeParts, minio.PutObjectOptions{})
	if err != nil {
		return PutObjectResult{}, fmt.Errorf("MinIO 完成分块上传失败: %w", err)
	}

	// 后备方案：在合并后立即获取对象信息，以确保获取到正确的文件大小
	objInfo, err := s.client.StatObject(ctx, bucketName, objectName, minio.StatObjectOptions{
		VersionID: uploadInfo.VersionID, // 确保获取的是刚刚创建的版本的 stat
	})
	if err != nil {
		logger.Error("MinIO StatObject after complete failed", zap.Error(err), zap.String("objectName", objectName))
		// 即使 stat 失败，也返回从 CompleteMultipartUpload 获得的信息，避免整个操作失败
		return PutObjectResult{
			Bucket:    uploadInfo.Bucket,
			Key:       uploadInfo.Key,
			Size:      uploadInfo.Size, // 可能是 0
			ETag:      uploadInfo.ETag,
			VersionID: uploadInfo.VersionID,
		}, nil
	}

	return PutObjectResult{
		Bucket:    bucketName, // 直接使用传入的 bucketName
		Key:       objInfo.Key,
		Size:      objInfo.Size, // 使用 StatObject 返回的权威大小
		ETag:      objInfo.ETag,
		VersionID: objInfo.VersionID,
	}, nil
}

func (s *MinIOStorageService) AbortMultiPartUpload(ctx context.Context, bucketName, objectName, uploadID string) error {
	return s.core.AbortMultipartUpload(ctx, bucketName, objectName, uploadID)
}

func (s *MinIOStorageService) GetUploadObjName(fileHash, fileName string) string {
	// 结论：`fileHash` 必须从 `objectName` 的生成中移除。
	// 我将使用 `fileName`，并接受在多用户环境下可能存在的冲突，作为一个临时修复。
	// TODO 长期来看，必须重构。
	return fmt.Sprintf("uploads/%s", fileName)
}

func (s *MinIOStorageService) ListObjectParts(ctx context.Context, bucketName, objectName, uploadID string) ([]UploadPartResult, error) {
	partsInfo, err := s.core.ListObjectParts(ctx, bucketName, objectName, uploadID, 0, 10000)
	if err != nil {
		return nil, fmt.Errorf("MinIO 列出已上传分块失败: %w", err)
	}

	var parts []UploadPartResult
	for _, part := range partsInfo.ObjectParts {
		parts = append(parts, UploadPartResult{
			PartNumber: part.PartNumber,
			ETag:       part.ETag,
		})
	}
	return parts, nil
}

func (s *MinIOStorageService) IsUploadIDNotFound(err error) bool {
	if err == nil {
		return false
	}
	// MinIO a "NoSuchUpload" error code when the upload ID does not exist.
	return strings.Contains(err.Error(), "The specified multipart upload does not exist")
}
