package router

import (
	"net/http"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/database"
	"github.com/3Eeeecho/go-clouddisk/internal/handlers"
	"github.com/3Eeeecho/go-clouddisk/internal/middlewares"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
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

func InitRouter(cfg *config.Config) *gin.Engine {
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

	v1 := router.Group("/api/v1")
	{
		// 认证相关路由 (无需认证)
		authGroup := v1.Group("/auth")
		{
			// 传递 database.DB 和 cfg
			authGroup.POST("/register", handlers.Register(database.DB, cfg)) // <-- 使用 database.DB
			authGroup.POST("/login", handlers.Login(database.DB, cfg))       // <-- 使用 database.DB
			authGroup.POST("/refresh", handlers.RefreshToken(cfg))
		}

		// 需要认证的路由组
		authenticated := v1.Group("/")
		authenticated.Use(middlewares.AuthMiddleware(cfg))

		// 用户相关路由
		userGroup := authenticated.Group("/users")
		{
			userGroup.GET("/info", handlers.GetUserInfo()) // GetUserInfo 不直接依赖 DB，但可以获取用户ID
		}

		// 文件相关路由
		fileGroup := authenticated.Group("/files")
		{
			// 传递 database.DB 和 cfg
			fileGroup.GET("/", handlers.ListUserFiles(database.DB, cfg))
			fileGroup.POST("/upload", handlers.UploadFile(database.DB, cfg))
			fileGroup.POST("/folder", handlers.CreateFolder(database.DB, cfg))
			fileGroup.GET("/download/:file_id", handlers.DownloadFile(database.DB, cfg))
			fileGroup.DELETE("/:file_id", handlers.DeleteFile(database.DB, cfg))
		}
	}

	router.NoRoute(func(c *gin.Context) {
		xerr.Error(c, http.StatusNotFound, http.StatusNotFound, "Route not found")
	})

	return router
}
