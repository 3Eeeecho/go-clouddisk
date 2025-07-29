package router

import (
	"net/http"

	_ "github.com/3Eeeecho/go-clouddisk/docs"
	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/handlers"
	"github.com/3Eeeecho/go-clouddisk/internal/middlewares"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/cache"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/storage"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
	"github.com/3Eeeecho/go-clouddisk/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"gorm.io/gorm"
)

// RouterConfig 包含初始化路由所需的所有依赖
type RouterConfig struct {
	db                 *gorm.DB
	redisClient        *redis.Client
	fileStorageService storage.StorageService
	cfg                *config.Config
}

func NewRouterConfig(db *gorm.DB, redisClient *redis.Client, fileStorageService storage.StorageService, cfg *config.Config) *RouterConfig {
	return &RouterConfig{
		db:                 db,
		redisClient:        redisClient,
		fileStorageService: fileStorageService,
		cfg:                cfg,
	}
}

func InitRouter(routerCfg *RouterConfig) *gin.Engine {
	// 设置 Gin 模式，开发环境为 DebugMode，生产环境为 ReleaseMode
	gin.SetMode(gin.DebugMode) // 或者根据 routerCfg.cfg.AppCfg.Server.Env 来设置

	router := gin.Default() // 使用默认的 Gin 引擎，包含 Logger 和 Recovery 中间件

	// 全局中间件
	// TODO: CORS 跨域处理 (如果需要前端分离)
	// router.Use(middlewares.Cors())

	// Health Check 路由
	router.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	v1 := router.Group("/api/v1")
	{
		// 认证相关路由 (无需认证)
		authGroup := v1.Group("/auth")
		{
			authGroup.POST("/register", handlers.Register(routerCfg.db, routerCfg.cfg))
			authGroup.POST("/login", handlers.Login(routerCfg.db, routerCfg.cfg))
			authGroup.POST("/refresh", handlers.RefreshToken(routerCfg.cfg))
		}

		// 需要认证的路由组
		authenticated := v1.Group("/")
		authenticated.Use(middlewares.AuthMiddleware(routerCfg.cfg))

		// 用户相关路由
		userGroup := authenticated.Group("/users")
		{
			userRepo := repositories.NewUserRepository(routerCfg.db)
			userService := services.NewUserService(userRepo)

			userGroup.GET("/me", handlers.GetUserProfile(userService))
		}

		// 文件相关路由
		fileGroup := authenticated.Group("/files")
		{

			cacheService := cache.NewRedisCache(routerCfg.redisClient)
			fileRepo := repositories.NewFileRepository(routerCfg.db, cacheService)
			userRepo := repositories.NewUserRepository(routerCfg.db)
			fileService := services.NewFileService(fileRepo, userRepo, routerCfg.cfg, routerCfg.db, routerCfg.fileStorageService, cacheService)

			fileGroup.GET("/", handlers.ListUserFiles(fileService, routerCfg.cfg))
			fileGroup.POST("/upload", handlers.UploadFile(fileService, routerCfg.cfg))
			fileGroup.POST("/folder", handlers.CreateFolder(fileService, routerCfg.cfg))
			fileGroup.GET("/download/:file_id", handlers.DownloadFile(fileService, routerCfg.cfg))
			fileGroup.GET("/download/folder/:id", handlers.DownloadFolder(fileService, routerCfg.cfg))
			fileGroup.DELETE("/softdelete/:file_id", handlers.SoftDeleteFile(fileService, routerCfg.cfg))
			fileGroup.DELETE("/permanentdelete/:file_id", handlers.PermanentDeleteFile(fileService, routerCfg.cfg))
			fileGroup.GET("/recyclebin", handlers.ListRecycleBinFiles(fileService))
			fileGroup.PUT("/restore/:file_id", handlers.RestoreFile(fileService))
			fileGroup.PUT("/rename/:id", handlers.RenameFile(fileService))
			fileGroup.PUT("/move", handlers.MoveFile(fileService))
		}

		// 分享相关路由
		shareGroup := authenticated.Group("/shares")
		{
			cacheService := cache.NewRedisCache(routerCfg.redisClient)
			shareRepo := repositories.NewShareRepository(routerCfg.db)
			fileRepo := repositories.NewFileRepository(routerCfg.db, cacheService)
			userRepo := repositories.NewUserRepository(routerCfg.db)
			fileService := services.NewFileService(fileRepo, userRepo, routerCfg.cfg, routerCfg.db, routerCfg.fileStorageService, cacheService)
			shareService := services.NewShareService(shareRepo, fileRepo, fileService, routerCfg.cfg)

			shareGroup.GET("/:share_uuid/details", handlers.GetShareDetails(shareService, routerCfg.cfg))
			shareGroup.POST("/:share_uuid/verify", handlers.VerifySharePassword(shareService, routerCfg.cfg))
			shareGroup.GET("/:share_uuid/download", handlers.DownloadSharedContent(shareService, routerCfg.cfg))

			shareGroup.POST("/", handlers.CreateShare(shareService, routerCfg.cfg))
			shareGroup.GET("/my", handlers.ListUserShares(shareService, routerCfg.cfg))
			shareGroup.DELETE("/:share_id", handlers.RevokeShare(shareService, routerCfg.cfg))
		}
	}

	router.NoRoute(func(c *gin.Context) {
		xerr.Error(c, http.StatusNotFound, http.StatusNotFound, "Route not found")
	})

	return router
}
