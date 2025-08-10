package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/3Eeeecho/go-clouddisk/cmd/server"
	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"go.uber.org/zap"
)

func main() {
	// 加载配置
	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Fatal("加载配置出错", zap.Error(err))
	}

	//初始化日志系统
	if err = os.MkdirAll("logs", 0755); err != nil {
		logger.Fatal("初始化日志系统失败", zap.Error(err))
	}
	logger.InitLogger(cfg.Log.OutputPath, cfg.Log.ErrorPath, cfg.Log.Level)
	defer logger.Sync() // 确保在应用退出时刷新所有缓冲的日志条目

	// 统一的日志输出
	logger.Info("启动云盘程序...")

	// 创建并构建应用服务器实例
	srv, err := server.NewServer(cfg)
	if err != nil {
		logger.Fatal("无法启动应用程序", zap.Error(err))
	}

	// 创建一个通道用于接收停止信号
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, syscall.SIGINT, syscall.SIGTERM)

	// 启动服务器
	srv.Run(context.Background(), stopChan)

	logger.Info("云盘程序已退出。")
}
