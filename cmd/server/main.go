package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/storage"
	"github.com/3Eeeecho/go-clouddisk/internal/router"
	"github.com/3Eeeecho/go-clouddisk/internal/setup"
	"go.uber.org/zap"
)

func main() {
	// 加载配置
	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Fatal("Failed to load config", zap.Error(err))
	}

	//初始化日志系统
	if err = os.MkdirAll("logs", 0755); err != nil {
		logger.Fatal("Failed to create logs directory", zap.Error(err))
	}
	logger.InitLogger(cfg.Log.OutputPath, cfg.Log.ErrorPath, cfg.Log.Level)
	defer logger.Sync() // 确保在应用退出时刷新所有缓冲的日志条目

	// 初始化数据库连接
	setup.InitMySQL(&cfg.MySQL)
	defer setup.CloseMySQLDB() // 确保在 main 函数退出时关闭数据库连接

	// 初始化 Redis 连接
	// client := database.InitRedis(&config.AppConfig.Redis)
	// defer database.CloseRedis(client)

	// 初始化 MinIO 客户端
	var fileStorageService storage.StorageService
	switch cfg.Storage.Type {
	case "minio":
		minioSvc, err := setup.InitMinIOStorage(cfg) // 调用新的 MinIO 初始化函数
		if err != nil {
			logger.Fatal("初始化 MinIO 存储服务失败", zap.Error(err))
		}
		fileStorageService = minioSvc
	case "aliyun_oss":
		aliyunSvc, err := setup.InitAliyunOSSStorage(cfg) // 调用新的阿里云 OSS 初始化函数
		if err != nil {
			logger.Fatal("初始化阿里云 OSS 存储服务失败", zap.Error(err))
		}
		fileStorageService = aliyunSvc
	default:
		logger.Fatal("未知的存储服务类型，请检查配置: " + cfg.Storage.Type)

	}

	// 初始化Elasticsearch
	// database.InitElasticsearchClient(&cfg.Elasticsearch)
	// logger.Info("Elasticsearch client initialized.")

	// 初始化 Gin 引擎和注册路由
	// 将所有依赖传入 RouterConfig
	routerCfg := router.NewRouterConfig(setup.DB, nil, fileStorageService, cfg)
	r := router.InitRouter(routerCfg)

	// 启动 HTTP 服务器
	addr := ":" + config.AppConfig.Server.Port
	logger.Info(fmt.Sprintf("Server is running on %s", cfg.Server.Port))
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// 优雅关机
	if err = srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatal("Server failed to start", zap.Error(err))
	}

	logger.Info("Go 云盘应用程序已退出。")
}
