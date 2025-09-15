package router

import (
	"net/http"

	_ "github.com/3Eeeecho/go-clouddisk/docs"
	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/handlers"
	"github.com/3Eeeecho/go-clouddisk/internal/handlers/response"
	"github.com/3Eeeecho/go-clouddisk/internal/middlewares"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

func InitRouter(authHandler *handlers.AuthHandler,
	fileHandler *handlers.FileHandler,
	shareHandler *handlers.ShareHandler,
	uploadHandler *handlers.UploadHandler,
	userHandler *handlers.UserHandler,
	cfg *config.Config,
) *gin.Engine {
	// 设置 Gin 模式，开发环境为 DebugMode，生产环境为 ReleaseMode
	gin.SetMode(gin.DebugMode) // 或者根据 routerCfg.cfg.AppCfg.Server.Env 来设置

	router := gin.Default() // 使用默认的 Gin 引擎，包含 Logger 和 Recovery 中间件

	// 全局中间件 CORS 跨域处理 (前端分离)
	router.Use(middlewares.Cors())

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
			authGroup.POST("/register", authHandler.Register)
			authGroup.POST("/login", authHandler.Login)
			authGroup.POST("/refresh", authHandler.RefreshToken)
		}

		// 需要认证的路由组
		authenticated := v1.Group("/")
		authenticated.Use(middlewares.AuthMiddleware(cfg))

		// 用户相关路由
		userGroup := authenticated.Group("/users")
		{
			userGroup.GET("/me", userHandler.GetUserProfile)
		}

		// 文件相关路由
		fileGroup := authenticated.Group("/files")
		{

			fileGroup.GET("", fileHandler.ListUserFiles)
			fileGroup.GET("/:file_id", fileHandler.GetSpecificFile)
			fileGroup.POST("/folder", fileHandler.CreateFolder)
			fileGroup.GET("/download/:file_id", fileHandler.DownloadFile)
			fileGroup.GET("/download/folder/:id", fileHandler.DownloadFolder)
			fileGroup.DELETE("/softdelete/:file_id", fileHandler.SoftDeleteFile)
			fileGroup.DELETE("/permanentdelete/:file_id", fileHandler.PermanentDeleteFile)
			fileGroup.GET("/recyclebin", fileHandler.ListRecycleBinFiles)
			fileGroup.PUT("/restore/:file_id", fileHandler.RestoreFile)
			fileGroup.PUT("/rename/:id", fileHandler.RenameFile)
			fileGroup.PUT("/move", fileHandler.MoveFile)

			//fileVersion
			fileGroup.DELETE("/:file_id/versions/:version_id", fileHandler.DeleteFileVersion)
			fileGroup.GET("/versions/:file_id", fileHandler.ListFileVersions)
			fileGroup.POST("/:file_id/versions/:version_id/restore", fileHandler.RestoreFileVersion)
		}

		// 分享相关路由 (需要认证)
		shareAuthGroup := authenticated.Group("/shares")
		{
			shareAuthGroup.POST("/", shareHandler.CreateShare)
			shareAuthGroup.GET("/my", shareHandler.ListUserShares)
			shareAuthGroup.DELETE("/:share_id", shareHandler.RevokeShare)
		}

		// 注册断点续传路由
		uploadRoutes := authenticated.Group("/uploads")
		{
			uploadRoutes.POST("/init", uploadHandler.InitUploadHandler)
			uploadRoutes.POST("/chunk", uploadHandler.UploadChunkHandler)
			uploadRoutes.POST("/complete", uploadHandler.CompleteUploadHandler)
		}
	}

	// 公开的分享链接路由 (无需认证)
	sharePublicGroup := router.Group("/share")
	{
		sharePublicGroup.GET("/:share_uuid/details", shareHandler.GetShareDetails)
		sharePublicGroup.POST("/:share_uuid/verify", shareHandler.VerifySharePassword)
		sharePublicGroup.GET("/:share_uuid/download", shareHandler.DownloadSharedContent)
	}

	router.NoRoute(func(c *gin.Context) {
		response.Error(c, http.StatusNotFound, http.StatusNotFound, "Route not found")
	})

	return router
}
