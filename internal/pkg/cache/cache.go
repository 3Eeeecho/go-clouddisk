package cache

import (
	"context"
	"time"
)

// 缓存通用接口
type Cache interface {
	// Set在缓存中设置一个值，并指定过期时间。
	// value应该是一个可以被JSON封送的结构体或指向结构体的指针。
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error

	// Get从缓存中检索一个值，并将其解编组到目标接口。
	// target应该是一个指针，指向希望解编组成的类型。
	Get(ctx context.Context, key string, target interface{}) error

	// 删除一个或多个key
	Del(ctx context.Context, keys ...string) error

	// 检查key是否存在
	Exists(ctx context.Context, key string) (bool, error)
}
