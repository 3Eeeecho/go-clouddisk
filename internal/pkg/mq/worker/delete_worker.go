package worker

import (
	"encoding/json"
	"log"

	"context"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/mq"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/storage"
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

	err = w.mqClient.Consume(DeleteQueueName, w.handleMessage)
	if err != nil {
		log.Fatalf("Failed to start consuming from queue: %s", err)
	}

	log.Println("Delete worker started...")
}

func (w *DeleteWorker) handleMessage(msg amqp.Delivery) {
	var task models.DeleteFileTask
	if err := json.Unmarshal(msg.Body, &task); err != nil {
		logger.Error("Failed to unmarshal delete task", zap.Error(err))
		_ = msg.Nack(false, false) // Discard message
		return
	}

	logger.Info("Received file deletion task", zap.Uint64("FileID", task.FileID))

	// 1. Delete file from object storage
	ctx := context.Background()
	bucketName := w.cfg.MinIO.BucketName // Assuming MinIO is the storage
	err := w.storageService.RemoveObject(ctx, bucketName, task.OssKey)
	if err != nil {
		logger.Error("Failed to delete file from storage", zap.String("OssKey", task.OssKey), zap.Error(err))
		// We can choose to requeue the message for another attempt
		_ = msg.Nack(false, true) // Requeue
		return
	}

	// 2. Delete file record from database
	if err := w.fileRepo.PermanentDelete(task.FileID); err != nil {
		logger.Error("Failed to permanently delete file record from DB", zap.Uint64("FileID", task.FileID), zap.Error(err))
		_ = msg.Nack(false, true) // Requeue
		return
	}

	logger.Info("Successfully processed file deletion task", zap.Uint64("FileID", task.FileID))
	_ = msg.Ack(false) // Acknowledge the message
}
