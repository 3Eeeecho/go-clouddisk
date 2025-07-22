package database

import (
	"context"
	"fmt"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.uber.org/zap"
)

var MinioClientGlobal *minio.Client

func InitMinioClient(cfg *config.MinIOConfig) error {
	// 创建 MinIO 客户端
	var err error
	MinioClientGlobal, err = minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: cfg.UseSSL,
		// Transport: customHTTPClient.Transport, // 如果需要自定义 HTTP 传输层
	})
	if err != nil {
		logger.Error("Failed to initialize MinIO client", zap.Error(err))
		return fmt.Errorf("failed to initialize MinIO client: %w", err)
	}

	// 检查连接是否成功 (Ping MinIO 服务)
	// 虽然 MinIO SDK 没有直接的 Ping 方法，但执行一个 BucketExists 操作可以验证连接
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // 设置一个超时上下文
	defer cancel()

	// 检查桶是否存在，如果不存在则创建
	exists, err := MinioClientGlobal.BucketExists(ctx, cfg.BucketName)
	if err != nil {
		logger.Error("Failed to check MinIO bucket existence",
			zap.String("bucketName", cfg.BucketName),
			zap.Error(err))
		return fmt.Errorf("failed to check MinIO bucket existence: %w", err)
	}

	if !exists {
		logger.Info("MinIO bucket does not exist, creating...", zap.String("bucketName", cfg.BucketName))
		// MakeBucketOptions 可以添加区域信息，如果 MinIO 是分布式且有区域概念
		err = MinioClientGlobal.MakeBucket(ctx, cfg.BucketName, minio.MakeBucketOptions{}) // 可以添加 Region: cfg.Location
		if err != nil {
			logger.Error("Failed to create MinIO bucket",
				zap.String("bucketName", cfg.BucketName),
				zap.Error(err))
			return fmt.Errorf("failed to create MinIO bucket: %w", err)
		}
		logger.Info("MinIO bucket created successfully", zap.String("bucketName", cfg.BucketName))
	} else {
		logger.Info("MinIO bucket already exists", zap.String("bucketName", cfg.BucketName))
	}

	logger.Info("Minio client initialized successfully", zap.String("endpoint", cfg.Endpoint))
	return nil
}
