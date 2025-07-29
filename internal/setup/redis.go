package setup

import (
	"context"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
)

var RedisClientGlobal *redis.Client

func InitRedis(cfg *config.Config) {
	RedisClientGlobal = redis.NewClient(&redis.Options{
		Addr:         cfg.Redis.Addr,
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		PoolSize:     10,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		DialTimeout:  5 * time.Second,
	})

	_, err := RedisClientGlobal.Ping(context.Background()).Result()
	if err != nil {
		logger.Fatal("Failed to connect to Redis", zap.Error(err))
	}
	logger.Info("Connected to Redis successfully!")
}

func CloseRedis() {
	if RedisClientGlobal != nil {
		err := RedisClientGlobal.Close()
		if err != nil {
			logger.Error("Error closing Reids connection", zap.Error(err))
		} else {
			logger.Info("Reids connection closed.")
		}
	}
}
