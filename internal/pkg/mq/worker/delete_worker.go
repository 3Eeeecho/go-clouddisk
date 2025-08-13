package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/mq"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/storage"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
	"github.com/streadway/amqp"
	"go.uber.org/zap"
)

const DeleteQueueName = "file_delete_queue"

type DeleteWorker struct {
	mqClient       *mq.RabbitMQClient
	fileRepo       repositories.FileRepository
	storageService storage.StorageService
	cfg            *config.Config
}

func NewDeleteWorker(
	mqClient *mq.RabbitMQClient,
	fileRepo repositories.FileRepository,
	storageService storage.StorageService,
	cfg *config.Config,
) *DeleteWorker {
	return &DeleteWorker{
		mqClient:       mqClient,
		fileRepo:       fileRepo,
		storageService: storageService,
		cfg:            cfg,
	}
}

func (w *DeleteWorker) Start() {
	_, err := w.mqClient.DeclareQueue(DeleteQueueName)
	if err != nil {
		log.Fatalf("Failed to declare queue: %s", err)
	}

	err = w.mqClient.Consume(DeleteQueueName, w.DeleteSpecificVersion)
	if err != nil {
		log.Fatalf("Failed to start consuming from queue: %s", err)
	}
	err = w.mqClient.Consume(DeleteQueueName, w.DeleteAllVersions)
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

	// 1. 先删除物理文件
	ctx := context.Background()
	bucketName := w.cfg.MinIO.BucketName
	err := w.storageService.RemoveObject(ctx, bucketName, task.OssKey, task.VersionID)
	if err != nil {
		logger.Error("Failed to delete file from storage", zap.String("OssKey", task.OssKey), zap.Error(err))
		_ = msg.Nack(false, true) // 重新入队
		return
	}

	// 2. 从数据库中删除记录
	if err := w.fileRepo.PermanentDelete(task.FileID); err != nil {
		if errors.Is(err, xerr.ErrFileNotFound) {
			logger.Info("file not exist", zap.Uint64("FileID", task.FileID))
			_ = msg.Ack(false) // 确认消息
		}
		logger.Error("Failed to permanently delete file record from DB", zap.Uint64("FileID", task.FileID), zap.Error(err))
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

	// 1. 先删除物理文件
	ctx := context.Background()
	bucketName := w.cfg.MinIO.BucketName
	err := w.storageService.RemoveObjects(ctx, bucketName, task.OssKey)
	if err != nil {
		logger.Error("Failed to delete file from storage", zap.String("OssKey", task.OssKey), zap.Error(err))
		_ = msg.Nack(false, true) // 重新入队
		return
	}

	// 2. 从数据库中删除记录
	if err := w.fileRepo.PermanentDelete(task.FileID); err != nil {
		if errors.Is(err, xerr.ErrFileNotFound) {
			logger.Info("file not exist", zap.Uint64("FileID", task.FileID))
			_ = msg.Ack(false) // 确认消息
		}
		logger.Error("Failed to permanently delete file record from DB", zap.Uint64("FileID", task.FileID), zap.Error(err))
		_ = msg.Nack(false, true) // 重新入队
		return
	}

	logger.Info("Successfully processed file deletion task, delete all versions of file", zap.Uint64("FileID", task.FileID))
	_ = msg.Ack(false) // 确认消息
}
