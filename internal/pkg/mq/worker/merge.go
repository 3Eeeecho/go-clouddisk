package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/mq"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/storage"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
	"github.com/3Eeeecho/go-clouddisk/internal/services/explorer"
	"github.com/google/uuid"
	"github.com/streadway/amqp"
	"go.uber.org/zap"
)

type FileMergerWorker struct {
	mqClient       *mq.RabbitMQClient
	uploadService  *explorer.UploadService
	fileRepo       repositories.FileRepository
	storageService storage.StorageService
	cfg            *config.Config
}

func NewFileMergerWorker(mqClient *mq.RabbitMQClient,
	uploadService *explorer.UploadService,
	fileRepo repositories.FileRepository,
	ss storage.StorageService,
	cfg *config.Config,
) *FileMergerWorker {
	return &FileMergerWorker{
		mqClient:       mqClient,
		uploadService:  uploadService,
		fileRepo:       fileRepo,
		storageService: ss,
		cfg:            cfg,
	}
}

func (w *FileMergerWorker) Start() {
	logger.Info("Starting file merger worker...")
	err := w.mqClient.Consume(func(msg amqp.Delivery) {
		var task explorer.MergeTask
		if err := json.Unmarshal(msg.Body, &task); err != nil {
			logger.Error("Failed to unmarshal merge task", zap.Any("err", err))
			msg.Nack(false, false) // 拒绝消息，不重新入队
			return
		}

		logger.Info("Received merge task for file", zap.String("fileHash", task.FileHash))
		file, err := w.fileRepo.FindFileByMD5Hash(task.FileHash)
		if err != nil {
			logger.Error("Failed to find file", zap.Any("md5Hash", task.FileHash))
			msg.Nack(false, false) // 拒绝消息，不重新入队
			return
		}

		// 更新文件状态为“合并中”
		//TODO Update函数过于昂贵
		file.MergeStatus = models.MergeStatusMerging
		if err := w.fileRepo.Update(file); err != nil {
			logger.Info("Failed to update file status to merging", zap.String("fileHash", task.FileHash), zap.Any("err", err))
			msg.Nack(false, true) // 拒绝消息，重新入队
			return
		}

		//执行实际的合并操作
		if mergeErr := w.doMergeTask(file, task); mergeErr != nil {
			logger.Error("Failed to merge file", zap.String("fileHash", task.FileHash), zap.Any("err", mergeErr))
			file.MergeStatus = models.MergeStatusFailed
			w.fileRepo.Update(file)
			msg.Nack(false, true) // 拒绝消息，重新入队
			return
		}

		// 合并成功，更新状态
		file.MergeStatus = models.MergeStatusSuccess
		if err := w.fileRepo.Update(file); err != nil {
			logger.Error("Failed to update file status to success", zap.String("fileHash", task.FileHash), zap.Any("err", err))
			// TODO 此时文件已合并，数据库更新失败，这里需要更复杂的错误处理
		}

		msg.Ack(false) // 确认消息，表示处理成功
		logger.Info("Successfully merged file with hash", zap.String("fileHash", task.FileHash))
	})

	if err != nil {
		logger.Error("Failed to start worker", zap.Any("err", err))
	}

}

// 合并分片文件并上传到 OSS
func (w *FileMergerWorker) doMergeTask(file *models.File, task explorer.MergeTask) error {
	tempDir := filepath.Join(os.TempDir(), "clouddisk_chunks", task.FileHash)

	var readers []io.Reader
	var totalSize int64
	for i := range task.TotalChunks {
		chunkPath := filepath.Join(tempDir, fmt.Sprintf("%d.chunk", i))
		// 使用匿名函数来确保文件及时关闭
		func() {
			chunkFile, err := os.Open(chunkPath)
			if err != nil {
				return
			}
			defer chunkFile.Close()

			info, _ := chunkFile.Stat()
			totalSize += info.Size()
			readers = append(readers, chunkFile)
		}()
	}

	mergedReader := io.MultiReader(readers...)

	// 根据存储类型获取 bucketName
	var bucketName string
	switch w.cfg.Storage.Type {
	case "minio":
		bucketName = w.cfg.MinIO.BucketName
	case "aliyun_oss":
		bucketName = w.cfg.AliyunOSS.BucketName
	default:
		return errors.New("unsupported storage type")
	}

	// 推断文件类型(Content-Type/MimeType)
	ext := filepath.Ext(task.FileName)
	contentType := "application/octet-stream" // 默认值
	if mimeType := mime.TypeByExtension(ext); mimeType != "" {
		contentType = mimeType
	}

	// 调用 PutObject 方法
	ossKey := fmt.Sprintf("%s/%s", task.FileHash, uuid.NewString())
	_, err := w.storageService.PutObject(context.Background(), bucketName, ossKey, mergedReader, totalSize, contentType)
	if err != nil {
		return fmt.Errorf("failed to upload merged file to storage: %w", err)
	}

	//更新数据库记录
	file.OssBucket = &bucketName
	file.OssKey = &ossKey
	file.Size = uint64(totalSize)
	file.MimeType = &contentType
	if err := w.fileRepo.Update(file); err != nil {
		logger.Error("Failed to update file status", zap.String("fileHash", task.FileHash), zap.Any("err", err))
		// TODO 此时文件已上传，数据库更新失败，这里需要更复杂的错误处理
	}

	//上传成功后,清理临时文件
	if err := os.RemoveAll(tempDir); err != nil {
		logger.Error("cleanupChunks: Failed to remove temporary chunk directory", zap.Error(err), zap.String("dir", tempDir))
		return fmt.Errorf("failed to remove temporary chunk directory: %w", err)
	}

	return nil
}
