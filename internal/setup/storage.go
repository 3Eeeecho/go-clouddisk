// internal/setup/storage.go

package setup

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/storage" // 引入你的抽象存储包
)

// InitMinIOStorage 初始化 MinIO 存储服务并确保存储桶存在。
func InitMinIOStorage(cfg *config.Config) (storage.StorageService, error) {
	minioCfg := &cfg.MinIO // 获取 MinIO 配置

	// 初始化 MinIO 存储服务
	minioSvc, err := storage.NewMinIOStorageService(minioCfg)
	if err != nil {
		return nil, fmt.Errorf("初始化 MinIO 存储服务失败: %w", err)
	}
	logger.Info("MinIO 存储服务已选择并初始化")

	// 检查并创建 MinIO 存储桶
	// 为外部调用使用带超时的上下文
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second) // 增加超时时间以应对网络延迟
	defer cancel()

	exists, err := minioSvc.IsBucketExist(ctx, minioCfg.BucketName)
	if err != nil {
		return nil, fmt.Errorf("检查 MinIO 存储桶存在性失败: %w", err)
	}

	if !exists {
		logger.Info("MinIO 存储桶不存在，尝试创建...", zap.String("bucketName", minioCfg.BucketName))
		if err := minioSvc.MakeBucket(ctx, minioCfg.BucketName); err != nil {
			return nil, fmt.Errorf("创建 MinIO 存储桶失败: %w", err)
		}
		logger.Info("MinIO 存储桶创建成功", zap.String("bucketName", minioCfg.BucketName))
	} else {
		logger.Info("MinIO 存储桶已存在", zap.String("bucketName", minioCfg.BucketName))
	}

	return minioSvc, nil
}

// InitAliyunOSSStorage 初始化阿里云 OSS 存储服务并确保存储桶存在。
func InitAliyunOSSStorage(cfg *config.Config) (storage.StorageService, error) {
	aliyunCfg := &cfg.AliyunOSS // 获取阿里云 OSS 配置

	// 初始化阿里云 OSS 存储服务
	aliyunSvc, err := storage.NewAliyunOSSStorageService(aliyunCfg)
	if err != nil {
		return nil, fmt.Errorf("初始化阿里云 OSS 存储服务失败: %w", err)
	}
	logger.Info("阿里云 OSS 存储服务已选择并初始化")

	// 检查并创建 OSS 存储桶
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exists, err := aliyunSvc.IsBucketExist(ctx, aliyunCfg.BucketName)
	if err != nil {
		return nil, fmt.Errorf("检查阿里云 OSS 存储桶存在性失败: %w", err)
	}

	if !exists {
		logger.Info("阿里云 OSS 存储桶不存在，尝试创建...", zap.String("bucketName", aliyunCfg.BucketName))
		if err := aliyunSvc.MakeBucket(ctx, aliyunCfg.BucketName); err != nil {
			return nil, fmt.Errorf("创建阿里云 OSS 存储桶失败: %w", err)
		}
		logger.Info("阿里云 OSS 存储桶创建成功", zap.String("bucketName", aliyunCfg.BucketName))
	} else {
		logger.Info("阿里云 OSS 存储桶已存在", zap.String("bucketName", aliyunCfg.BucketName))
	}

	return aliyunSvc, nil
}

func InitStorage(cfg *config.Config) storage.StorageService {
	var fileStorageService storage.StorageService
	switch cfg.Storage.Type {
	case "minio":
		minioSvc, err := InitMinIOStorage(cfg) // 调用新的 MinIO 初始化函数
		if err != nil {
			logger.Fatal("初始化 MinIO 存储服务失败", zap.Error(err))
		}
		fileStorageService = minioSvc
	case "aliyun_oss":
		aliyunSvc, err := InitAliyunOSSStorage(cfg) // 调用新的阿里云 OSS 初始化函数
		if err != nil {
			logger.Fatal("初始化阿里云 OSS 存储服务失败", zap.Error(err))
		}
		fileStorageService = aliyunSvc
	default:
		logger.Fatal("未知的存储服务类型，请检查配置: " + cfg.Storage.Type)
	}
	return fileStorageService
}
