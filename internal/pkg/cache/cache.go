package cache

import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"
)

// 缓存通用接口
type Cache interface {
	// Set在缓存中设置一个值，并指定过期时间。
	// value应该是一个可以被JSON封送的结构体或指向结构体的指针。
	Set(ctx context.Context, key string, value any, expiration time.Duration) error

	// Get从缓存中检索一个值，并将其解编组到目标接口。
	// target应该是一个指针，指向希望解编组成的类型。
	Get(ctx context.Context, key string, target any) error

	// 删除一个或多个key
	Del(ctx context.Context, keys ...string) error

	// 检查key是否存在
	Exists(ctx context.Context, key string) (bool, error)

	// 哈希操作函数
	HSet(ctx context.Context, key string, field string, value any) error
	HMSet(ctx context.Context, key string, fields map[string]any) error
	HGet(ctx context.Context, key string, field string) (string, error)
	HGetAll(ctx context.Context, key string) (map[string]string, error)
	HDel(ctx context.Context, key string, fields ...string) error

	//有序集合操作函数
	ZAdd(ctx context.Context, key string, members ...*redis.Z) *redis.IntCmd
	ZRevRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd
	ZRem(ctx context.Context, key string, members ...interface{}) *redis.IntCmd

	Expire(ctx context.Context, key string, expiration time.Duration) error
	TTL(ctx context.Context, key string) (time.Duration, error)
	TxPipeline() redis.Pipeliner
}
