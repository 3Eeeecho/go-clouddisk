package setup

import (
	"context"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
)

func InitRedis(ctx context.Context, cfg *config.Config) (*redis.Client, error) {
	redisClient := redis.NewClient(&redis.Options{
		Addr:         cfg.Redis.Addr,
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		PoolSize:     10,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		DialTimeout:  5 * time.Second,
	})

	_, err := redisClient.Ping(context.Background()).Result()
	if err != nil {
		logger.Fatal("Failed to connect to Redis", zap.Error(err))
		return nil, err
	}
	logger.Info("Connected to Redis successfully!")

	//启动消费者
	//TODO 转移
	// logger.Info("Starting cache update consumer...")
	// go consumer.StartCacheUpdateConsumer(ctx, redisClient)
	// logger.Info("Starting cache path Invalidation consumer...")
	// go consumer.StartPathInvalidationConsumer(ctx, DB, redisClient)
	return redisClient, nil
}

func CloseRedis(redisClient *redis.Client) {
	if redisClient != nil {
		err := redisClient.Close()
		if err != nil {
			logger.Error("Error closing Reids connection", zap.Error(err))
		} else {
			logger.Info("Reids connection closed.")
		}
	}
}
