package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
)

var ErrCacheMiss error = errors.New("缓存未命中,key不存在")

type RedisCache struct {
	client *redis.Client
}

func NewRedisCache(client *redis.Client) *RedisCache {
	return &RedisCache{client: client}
}

func (r *RedisCache) Set(ctx context.Context, key string, value any, expiration time.Duration) error {
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

func (r *RedisCache) Get(ctx context.Context, key string, target any) error {
	data, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return ErrCacheMiss
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

func (r *RedisCache) HSet(ctx context.Context, key string, field string, value any) error {
	// Redis HSet 接受 interface{} 作为值，它会内部转换为 string 或二进制
	// 如果你希望更严格地控制，可以在这里先进行 marshal
	err := r.client.HSet(ctx, key, field, value).Err()
	if err != nil {
		logger.Error("Failed to HSet field in Redis", zap.String("key", key), zap.String("field", field), zap.Any("value", value), zap.Error(err))
		return fmt.Errorf("HSet 操作失败: %w", err)
	}
	return nil
}

// HMSet 设置key的多个field
// go-redis/v8中HMSet已经被弃用,选择HSet配合map实现
func (r *RedisCache) HMSet(ctx context.Context, key string, fields map[string]any) error {
	// Using HSet with map is the recommended way for HMSet in go-redis/v8
	err := r.client.HSet(ctx, key, fields).Err()
	if err != nil {
		logger.Error("Failed to HMSet fields in Redis", zap.String("key", key), zap.Any("fields", fields), zap.Error(err))
		return fmt.Errorf("HMSet 操作失败: %w", err)
	}
	return nil
}

func (r *RedisCache) HGet(ctx context.Context, key string, field string) (string, error) {
	val, err := r.client.HGet(ctx, key, field).Result()
	if err != nil {
		if err == redis.Nil {
			return "", ErrCacheMiss // HGet 针对不存在的 field 也会返回 redis.Nil
		}
		logger.Error("Failed to HGet field from Redis", zap.String("key", key), zap.String("field", field), zap.Error(err))
		return "", fmt.Errorf("HGet 操作失败: %w", err)
	}
	return val, nil
}

func (r *RedisCache) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	resultMap, err := r.client.HGetAll(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, ErrCacheMiss // 如果整个 Hash Key 不存在，HGetAll 也会返回 redis.Nil
		}
		logger.Error("Failed to HGetAll from Redis", zap.String("key", key), zap.Error(err))
		return nil, fmt.Errorf("HGetAll 操作失败: %w", err)
	}
	// 如果 key 存在但 Hash 为空，resultMap 会是空 map，而不是 nil。这很好。
	if len(resultMap) == 0 {
		return nil, ErrCacheMiss // 明确表示没有找到数据，可以根据业务决定是否返回 ErrCacheMiss
		// 或者返回一个空 map 代表找到了一个空Hash
	}
	return resultMap, nil
}

func (r *RedisCache) HDel(ctx context.Context, key string, fields ...string) error {
	if len(fields) == 0 {
		return nil
	}
	err := r.client.HDel(ctx, key, fields...).Err()
	if err != nil {
		logger.Error("Failed to HDel fields from Redis", zap.String("key", key), zap.Strings("fields", fields), zap.Error(err))
		return fmt.Errorf("HDel 操作失败: %w", err)
	}
	return nil
}

func (r *RedisCache) Expire(ctx context.Context, key string, expiration time.Duration) error {
	err := r.client.Expire(ctx, key, expiration).Err()
	if err != nil {
		logger.Error("Failed to set expiration for key in Redis", zap.String("key", key), zap.Duration("expiration", expiration), zap.Error(err))
		return fmt.Errorf("设置键过期时间失败: %w", err)
	}
	return nil
}

func (r *RedisCache) TTL(ctx context.Context, key string) (time.Duration, error) {
	ttl, err := r.client.TTL(ctx, key).Result()
	if err != nil {
		logger.Error("Failed to get TTL for key in Redis", zap.String("key", key), zap.Error(err))
		return 0, fmt.Errorf("获取键 TTL 失败: %w", err)
	}
	return ttl, nil
}

func (r *RedisCache) TxPipeline() redis.Pipeliner {
	return r.client.TxPipeline()
}
