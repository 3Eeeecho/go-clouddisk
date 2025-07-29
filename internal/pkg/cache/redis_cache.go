package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
)

type RedisCache struct {
	client *redis.Client
}

func NewRedisCache(client *redis.Client) *RedisCache {
	return &RedisCache{client: client}
}

func (r *RedisCache) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		logger.Error("Failed to marshal cache value", zap.String("key", key), zap.Error(err))
		return fmt.Errorf("序列化缓存值失败: %w", err)
	}

	err = r.client.Set(ctx, key, data, expiration).Err()
	if err != nil {
		logger.Error("Failed to set value in Redis", zap.String("key", key), zap.Error(err))
		return fmt.Errorf("写入 Redis 失败: %w", err)
	}
	return nil
}

func (r *RedisCache) Get(ctx context.Context, key string, target interface{}) error {
	data, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return xerr.ErrCacheMiss
		}
		logger.Error("Failed to get value from Redis", zap.String("key", key), zap.Error(err))
		return fmt.Errorf("从 Redis 读取失败: %w", err)
	}

	err = json.Unmarshal(data, target)
	if err != nil {
		logger.Error("Failed to unmarshal cached value", zap.String("key", key), zap.Error(err))
		return fmt.Errorf("反序列化缓存值失败: %w", err)
	}
	return nil
}

func (r *RedisCache) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	err := r.client.Del(ctx, keys...).Err()
	if err != nil {
		logger.Error("Failed to delete keys from Redis", zap.Strings("keys", keys), zap.Error(err))
		return fmt.Errorf("从 Redis 删除键失败: %w", err)
	}
	return nil
}

func (r *RedisCache) Exists(ctx context.Context, key string) (bool, error) {
	count, err := r.client.Exists(ctx, key).Result()
	if err != nil {
		logger.Error("Failed to check key existence in Redis", zap.String("key", key), zap.Error(err))
		return false, fmt.Errorf("检查 Redis 键存在性失败: %w", err)
	}
	return count > 0, nil
}
