package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

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
	database.InitMySQL(&config.AppConfig.MySQL)
	defer database.CloseMySQLDB() // 确保在 main 函数退出时关闭数据库连接

	// 初始化 Redis 连接
	// client := database.InitRedis(&config.AppConfig.Redis)
	// defer database.CloseRedis(client)

	//

	// 初始化 MinIO 客户端
	// minioClient := database.InitMinIO(&config.AppConfig.MinIO)

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
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Server failed to start", zap.Error(err))
		}
	}()

	// 优雅关机
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("Shutting down server...")

	// 这里可以添加更复杂的优雅关机逻辑，例如等待进行中的请求完成
	// context.WithTimeout(context.Background(), 5*time.Second)

	logger.Info("Server exited.")
}
