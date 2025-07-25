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
	cfg    *config.MinIOConfig // MinIO的配置信息
}

// NewMinIOStorageService 创建并返回一个 MinIOStorageService 实例
func NewMinIOStorageService(cfg *config.MinIOConfig) (*MinIOStorageService, error) {
	minioClient, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: cfg.UseSSL, // 根据配置决定是否使用 HTTPS
	})
	if err != nil {
		logger.Error("初始化 MinIO 客户端失败", zap.Error(err))
		return nil, fmt.Errorf("无法初始化 MinIO 客户端: %w", err)
	}

	logger.Info("MinIO 客户端初始化成功", zap.String("endpoint", cfg.Endpoint))
	return &MinIOStorageService{
		client: minioClient,
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
		Bucket: info.Bucket,
		Key:    info.Key,
		Size:   info.Size,
		ETag:   info.ETag,
	}, nil
}

func (s *MinIOStorageService) GetObject(ctx context.Context, bucketName, objectName string) (GetObjectResult, error) {
	obj, err := s.client.GetObject(ctx, bucketName, objectName, minio.GetObjectOptions{})
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

func (s *MinIOStorageService) RemoveObject(ctx context.Context, bucketName, objectName string) error {
	opts := minio.RemoveObjectOptions{
		GovernanceBypass: true, // 如果需要，可以绕过保留策略
	}
	err := s.client.RemoveObject(ctx, bucketName, objectName, opts)
	if err != nil {
		return fmt.Errorf("MinIO 删除文件失败: %w", err)
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
