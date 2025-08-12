package worker

import (
	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/mq"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/storage"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
)

// StartAllWorkers 启动应用中所有定义的后台 Worker
func StartAllWorkers(
	cfg *config.Config,
	mqClient *mq.RabbitMQClient,
	fileRepo repositories.FileRepository,
	storageService storage.StorageService,
) {
	// --- 启动文件删除 Worker ---
	deleteWorker := NewDeleteWorker(mqClient, fileRepo, storageService, cfg)
	go deleteWorker.Start()

	// --- 在这里启动其他 Worker ---

	logger.Info("所有后台工作进程已启动。")
}
