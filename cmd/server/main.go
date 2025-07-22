package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/database"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/router"
	"go.uber.org/zap"
)

func main() {
	// 加载配置
	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Fatal("Failed to load config", zap.Error(err))
	}

	//初始化日志系统
	if err := os.MkdirAll("logs", 0755); err != nil {
		logger.Fatal("Failed to create logs directory", zap.Error(err))
	}
	logger.InitLogger(cfg.Log.OutputPath, cfg.Log.ErrorPath, cfg.Log.Level)
	defer logger.Sync() // 确保在应用退出时刷新所有缓冲的日志条目

	// 初始化数据库连接
	database.InitMySQL(&cfg.MySQL)
	defer database.CloseMySQLDB() // 确保在 main 函数退出时关闭数据库连接

	// 初始化 Redis 连接
	// client := database.InitRedis(&config.AppConfig.Redis)
	// defer database.CloseRedis(client)

	// 初始化 MinIO 客户端
	if cfg.Storage.Type == "minio" {
		err = database.InitMinioClient(&config.AppConfig.MinIO)
		if err != nil {
			logger.Fatal("Failed to initialize MinIO client", zap.Error(err))
		}
	}

	// 初始化Elasticsearch
	database.InitElasticsearchClient(&cfg.Elasticsearch)
	logger.Info("Elasticsearch client initialized.")

	// 初始化 Gin 引擎和注册路由
	// 将所有依赖传入 RouterConfig
	r := router.InitRouter(config.AppConfig)

	// 启动 HTTP 服务器
	addr := ":" + config.AppConfig.Server.Port
	logger.Info(fmt.Sprintf("Server is running on %s", cfg.Server.Port))
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// 优雅关机
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatal("Server failed to start", zap.Error(err))
	}

	// 优雅关机
	// quit := make(chan os.Signal, 1)
	// signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	// <-quit
	// logger.Info("Shutting down server...")

	// // 创建一个带超时的上下文，给服务器一些时间来完成正在处理的请求
	// ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // 5 秒超时
	// defer cancel()                                                          // 确保上下文资源在函数退出时被释放

	// // 尝试优雅地关闭服务器
	// if err := srv.Shutdown(ctx); err != nil {
	// 	// 如果 Shutdown 返回错误（例如超时），则强制关闭
	// 	logger.Error("服务器在优雅关机过程中因错误而被迫停止", zap.Error(err))
	// } else {
	// 	logger.Info("服务器已优雅停止。")
	// }

	// // 最后一条日志，在应用程序真正退出之前
	logger.Info("Go 云盘应用程序已退出。")
}
