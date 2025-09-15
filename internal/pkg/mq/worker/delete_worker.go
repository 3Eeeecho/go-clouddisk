package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/mq"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/storage"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
	"github.com/3Eeeecho/go-clouddisk/internal/services/explorer"
	"github.com/streadway/amqp"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const DeleteQueueName = "file_delete_queue"

type DeleteWorker struct {
	mqClient        *mq.RabbitMQClient
	fileRepo        repositories.FileRepository
	fileVersionRepo repositories.FileVersionRepository
	tm              explorer.TransactionManager
	storageService  storage.StorageService
	cfg             *config.Config
}

func NewDeleteWorker(
	mqClient *mq.RabbitMQClient,
	fileRepo repositories.FileRepository,
	fileVersionRepo repositories.FileVersionRepository,
	tm explorer.TransactionManager,
	storageService storage.StorageService,
	cfg *config.Config,
) *DeleteWorker {
	return &DeleteWorker{
		mqClient:        mqClient,
		fileRepo:        fileRepo,
		fileVersionRepo: fileVersionRepo,
		tm:              tm,
		storageService:  storageService,
		cfg:             cfg,
	}
}

func (w *DeleteWorker) Start() {
	// 删除指定版本消费者
	_, err := w.mqClient.DeclareQueue("delete_specific_version_queue")
	if err != nil {
		log.Fatalf("Failed to declare queue: %s", err)
	}
	err = w.mqClient.Consume("delete_specific_version_queue", w.DeleteSpecificVersion)
	if err != nil {
		log.Fatalf("Failed to start consuming from queue: %s", err)
	}

	//删除全部版本消费者
	_, err = w.mqClient.DeclareQueue("delete_all_versions_queue")
	if err != nil {
		log.Fatalf("Failed to declare queue: %s", err)
	}
	err = w.mqClient.Consume("delete_all_versions_queue", w.DeleteAllVersions)
	if err != nil {
		log.Fatalf("Failed to start consuming from queue: %s", err)
	}

	log.Println("Delete worker started...")
}

func (w *DeleteWorker) DeleteSpecificVersion(msg amqp.Delivery) {
	var task models.DeleteFileTask
	if err := json.Unmarshal(msg.Body, &task); err != nil {
		logger.Error("Failed to unmarshal delete task", zap.Error(err))
		_ = msg.Nack(false, false) // 解析失败,直接抛弃
		return
	}

	logger.Info("Received file deletion task", zap.Uint64("FileID", task.FileID))

	ctx := context.Background()
	err := w.tm.WithTransaction(ctx, func(tx *gorm.DB) error {
		// 1. 先删除数据库记录（先子后父）
		if err := tx.WithContext(ctx).Unscoped().Where("file_id = ? AND version_id = ?", task.FileID, task.VersionID).
			Delete(&models.FileVersion{}).Error; err != nil {
			return fmt.Errorf("failed to delete version: %w", err)
		}

		// 2. 检查是否是最后一个版本
		var remainingVersions int64
		if err := tx.WithContext(ctx).Unscoped().Model(&models.FileVersion{}).
			Where("file_id = ?", task.FileID).
			Count(&remainingVersions).Error; err != nil {
			return fmt.Errorf("failed to count versions: %w", err)
		}

		// 3. 如果是最后一个版本，删除主记录
		if remainingVersions == 0 {
			if err := w.fileRepo.PermanentDelete(tx, task.FileID); err != nil {
				return fmt.Errorf("failed to delete file: %w", err)
			}
			logger.Info("Last version deleted, removing main file record", zap.Uint64("FileID", task.FileID))
		}
		return nil
	})
	if err != nil {
		logger.Error("Transaction failed", zap.Error(err))
		_ = msg.Nack(false, true)
		return
	}

	// 删除物理文件
	bucketName := w.cfg.MinIO.BucketName
	err = w.storageService.RemoveObject(ctx, bucketName, task.OssKey, task.VersionID)
	if err != nil {
		logger.Error("Failed to delete file from storage", zap.String("OssKey", task.OssKey), zap.Error(err))
		_ = msg.Nack(false, true) // 重新入队
		return
	}

	logger.Info("Successfully processed file deletion task, delete specific version of file", zap.Uint64("FileID", task.FileID), zap.String("VersionID", task.VersionID))
	_ = msg.Ack(false) // 确认消息
}

func (w *DeleteWorker) DeleteAllVersions(msg amqp.Delivery) {
	var task models.DeleteFileTask
	if err := json.Unmarshal(msg.Body, &task); err != nil {
		logger.Error("Failed to unmarshal delete task", zap.Error(err))
		_ = msg.Nack(false, false) // 解析失败,直接抛弃
		return
	}

	logger.Info("Received file deletion task", zap.Uint64("FileID", task.FileID))

	// 在事务中处理数据库删除
	ctx := context.Background()
	err := w.tm.WithTransaction(ctx, func(tx *gorm.DB) error {
		// 1. 先删除所有版本记录（子表）
		if err := tx.WithContext(ctx).Unscoped().Where("file_id = ?", task.FileID).
			Delete(&models.FileVersion{}).Error; err != nil {
			return fmt.Errorf("failed to delete versions: %w", err)
		}

		// 2. 再删除主文件记录（父表）
		if err := w.fileRepo.PermanentDelete(tx, task.FileID); err != nil {
			return fmt.Errorf("failed to delete file: %w", err)
		}

		return nil
	})

	// 处理事务错误
	if err != nil {
		if errors.Is(err, xerr.ErrFileNotFound) {
			logger.Info("File not exist", zap.Uint64("FileID", task.FileID))
			_ = msg.Ack(false) // 文件不存在，确认消息
			return
		}
		logger.Error("Failed to delete records in transaction",
			zap.Uint64("FileID", task.FileID),
			zap.Error(err))
		_ = msg.Nack(false, true) // 数据库错误，重新入队
		return
	}

	// 数据库操作成功后，删除物理文件
	bucketName := w.cfg.MinIO.BucketName
	if err := w.storageService.RemoveObjects(ctx, bucketName, task.OssKey); err != nil {
		// 物理文件删除失败只记录不阻塞流程（因为数据库已更新）
		logger.Error("Failed to delete physical files (need manual cleanup)",
			zap.String("OssKey", task.OssKey),
			zap.Uint64("FileID", task.FileID),
			zap.Error(err))
	}

	logger.Info("Successfully processed file deletion task",
		zap.Uint64("FileID", task.FileID))
	_ = msg.Ack(false) // 确认消息
}
