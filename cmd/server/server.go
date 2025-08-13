package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/handlers"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/cache"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/mq"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/mq/worker"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/storage"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
	"github.com/3Eeeecho/go-clouddisk/internal/router"
	"github.com/3Eeeecho/go-clouddisk/internal/services/admin"
	"github.com/3Eeeecho/go-clouddisk/internal/services/explorer"
	"github.com/3Eeeecho/go-clouddisk/internal/services/share"
	"github.com/3Eeeecho/go-clouddisk/internal/setup"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type Server struct {
	router         *gin.Engine
	httpServer     *http.Server
	db             *gorm.DB
	redisClient    *redis.Client
	rabbitMQClient *mq.RabbitMQClient
}

// NewServer 负责构建所有依赖
func NewServer(cfg *config.Config) (*Server, error) {
	// 初始化数据库连接
	mysqlDB, err := setup.InitMySQL(&cfg.MySQL)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize MySQL: %w", err)
	}

	// 初始化 Redis 连接
	redisClient, err := setup.InitRedis(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Redis: %w", err)
	}

	// 初始化Elasticsearch
	// database.InitElasticsearchClient(&cfg.Elasticsearch)
	// logger.Info("Elasticsearch client initialized.")

	//初始化rabbitmq
	rabbitMQClient, err := mq.NewRabbitMQClient(cfg.RabbitMQ.URL)
	if err != nil {
		logger.Fatal("Failed to connect to RabbitMQ", zap.Any("err", err))
	}

	//  初始化 Repositories
	redisCache := cache.NewRedisCache(redisClient)
	fileRepo := repositories.NewFileRepository(mysqlDB, redisCache)
	userRepo := repositories.NewUserRepository(mysqlDB)
	share_repo := repositories.NewShareRepository(mysqlDB)
	fileVersionRepo := repositories.NewFileVersionRepository(mysqlDB)

	//初始化其他服务
	cacheService := cache.NewRedisCache(redisClient)
	tm := explorer.NewTransactionManager(mysqlDB)
	ss, err := storage.NewStorageService(cfg)
	if err != nil {
		logger.Fatal("failed to initialize storageService", zap.Any("err", err))
	}

	//  初始化 Services
	uploadService := explorer.NewUploadService(fileRepo, fileVersionRepo, tm, ss, explorer.UploadServiceDeps{
		Cache:    cacheService,
		MQClient: rabbitMQClient,
		Config:   cfg,
	})
	domainService := explorer.NewFileDomainService(fileRepo)
	authService := admin.NewAuthService(userRepo, &cfg.JWT)
	fileService := explorer.NewFileService(fileRepo, fileVersionRepo, domainService, tm, ss, rabbitMQClient, cfg)
	shareService := share.NewShareService(share_repo, fileRepo, fileService, domainService, cfg)
	userService := admin.NewUserService(userRepo)

	//  初始化 Handlers
	authHandler := handlers.NewAuthHandler(authService, cfg)
	fileHandler := handlers.NewFileHandler(fileService, cfg)
	shareHandler := handlers.NewShareHandler(shareService, cfg)
	uploadHandler := handlers.NewUploadHandler(uploadService)
	userHandler := handlers.NewUserHandler(userService)

	// 启动所有后台 Worker
	worker.StartAllWorkers(config.AppConfig, rabbitMQClient, fileRepo, fileVersionRepo, tm, ss)

	// 初始化 Gin 引擎和注册路由
	// 将所有依赖传入 RouterConfig
	engine := router.InitRouter(authHandler, fileHandler, shareHandler, uploadHandler, userHandler, cfg)

	// 启动 HTTP 服务器
	addr := ":" + config.AppConfig.Server.Port
	logger.Info(fmt.Sprintf("Server is running on %s", cfg.Server.Port))
	httpServer := &http.Server{
		Addr:    addr,
		Handler: engine,
	}

	return &Server{
		router:         engine,
		httpServer:     httpServer,
		db:             mysqlDB,
		redisClient:    redisClient,
		rabbitMQClient: rabbitMQClient,
	}, nil
}

// Run 启动服务器和 Worker，并处理优雅关机
func (s *Server) Run(ctx context.Context, stopChan chan os.Signal) {
	// 确保在应用关闭时，所有连接都被释放
	// GORM v2 依赖连接池，通常不需要手动关闭。Redis和MQ需要
	defer s.rabbitMQClient.Close()
	defer s.redisClient.Close()

	// 启动 HTTP 服务器
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Server failed to start", zap.Error(err))
		}
	}()

	// 等待停止信号
	<-stopChan
	logger.Info("Shutting down server...")

	// 优雅关机
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Fatal("Server forced to shutdown", zap.Error(err))
	}
	logger.Info("Server exited gracefully")
}
