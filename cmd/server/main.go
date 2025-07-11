package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/database"
	"github.com/gin-gonic/gin"
)

func main() {
	// 1. 加载配置
	config.LoadConfig()
	log.Printf("Loaded config: %+v\n", config.AppConfig) // 打印部分配置验证

	// 2. 初始化数据库连接
	database.InitMySQL(&config.AppConfig.MySQL)
	defer database.CloseMySQLDB() // 确保在 main 函数退出时关闭数据库连接

	// 3. 初始化 Gin 引擎
	// 生产环境建议设置为 gin.ReleaseMode
	gin.SetMode(gin.DebugMode)
	router := gin.Default()

	// TODO: 注册路由和中间件 (后续步骤会添加)
	// router.Use(middlewares.AuthMiddleware())
	// router.GET("/ping", func(c *gin.Context) {
	// 	c.JSON(http.StatusOK, gin.H{"message": "pong"})
	// })

	// 启动 HTTP 服务器
	addr := ":" + config.AppConfig.Server.Port
	log.Printf("Server starting on %s\n", addr)
	go func() {
		if err := http.ListenAndServe(addr, router); err != nil && err != http.ErrServerClosed {
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
