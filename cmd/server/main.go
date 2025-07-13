package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/database"
	"github.com/3Eeeecho/go-clouddisk/internal/router"
)

func main() {
	// 1. 加载配置
	config.LoadConfig()
	log.Printf("Loaded config: %+v\n", config.AppConfig) // 打印部分配置验证

	// 2. 初始化数据库连接
	database.InitMySQL(&config.AppConfig.MySQL)
	defer database.CloseMySQLDB() // 确保在 main 函数退出时关闭数据库连接

	// 3. TODO: 初始化 Redis 连接
	// client := database.InitRedis(&config.AppConfig.Redis)
	// defer database.CloseRedis(client)

	// 4. TODO: 初始化 MinIO 客户端
	// minioClient := database.InitMinIO(&config.AppConfig.MinIO)

	// 5. 初始化 Gin 引擎和注册路由
	// 将所有依赖传入 RouterConfig
	routerCfg := router.RouterConfig{
		DB: database.DB,
		// Redis:  client, // 待实现
		// Minio:  minioClient, // 待实现
		AppCfg: config.AppConfig,
	}
	r := router.InitRouter(routerCfg)

	// 启动 HTTP 服务器
	addr := ":" + config.AppConfig.Server.Port
	log.Printf("Server starting on %s\n", addr)
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// 优雅关机
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// 优雅关机
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	// 这里可以添加更复杂的优雅关机逻辑，例如等待进行中的请求完成
	// context.WithTimeout(context.Background(), 5*time.Second)

	log.Println("Server exited.")
}
