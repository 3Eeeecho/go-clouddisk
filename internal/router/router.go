package router

import (
	"net/http"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/handlers"
	"github.com/3Eeeecho/go-clouddisk/internal/middlewares"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/minio/minio-go/v7"
	"gorm.io/gorm"
)

// RouterConfig 包含初始化路由所需的所有依赖
type RouterConfig struct {
	DB     *gorm.DB
	Redis  *redis.Client
	Minio  *minio.Client
	AppCfg *config.Config
}

func InitRouter(cfg RouterConfig) *gin.Engine {
	// 设置 Gin 模式，开发环境为 DebugMode，生产环境为 ReleaseMode
	gin.SetMode(gin.DebugMode) // 或者根据 cfg.AppCfg.Server.Env 来设置

	router := gin.Default() // 使用默认的 Gin 引擎，包含 Logger 和 Recovery 中间件

	// 全局中间件
	// TODO: CORS 跨域处理 (如果需要前端分离)
	// router.Use(middlewares.Cors())

	// Health Check 路由
	router.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	// 注册认证相关路由
	authGroup := router.Group("/api/v1/auth")
	{
		// 传递 DB 和 AppCfg 到 handler，或者通过依赖注入框架
		// 这里我们暂时直接传递，后续可以优化为服务层注入
		authGroup.POST("/register", handlers.Register(cfg.DB, cfg.AppCfg))
		authGroup.POST("/login", handlers.Login(cfg.DB, cfg.AppCfg))  // 占位
		authGroup.POST("/refresh", handlers.RefreshToken(cfg.AppCfg)) // 占位
	}

	// 认证后的路由 (需要 JWT 中间件)
	apiV1 := router.Group("/api/v1")
	apiV1.Use(middlewares.AuthMiddleware(config.AppConfig)) // 应用 JWT 认证中间件
	{
		// 	// 用户信息
		// 	apiV1.GET("/user/info", handlers.GetUserInfo()) // 占位

		// 	// 文件管理 (占位)
		// 	fileGroup := apiV1.Group("/files")
		// 	{
		// 		fileGroup.POST("/upload", handlers.UploadFile())
		// 		fileGroup.GET("/list", handlers.ListFiles())
		// 		// ... 其他文件操作
		// 	}
	}

	return router
}
