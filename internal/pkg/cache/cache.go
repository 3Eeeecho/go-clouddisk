package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
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
	ZRem(ctx context.Context, key string, members ...any) *redis.IntCmd

	XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd

	Expire(ctx context.Context, key string, expiration time.Duration) error
	TTL(ctx context.Context, key string) (time.Duration, error)
	TxPipeline() redis.Pipeliner
}

type CacheUpdateMessage struct {
	File              models.File
	OldParentFolderID *uint64        `json:"old_parent_folder_id"` // 指针类型，用于区分 nil (根目录) 和 0 (父目录ID)
	OldMD5Hash        *string        `json:"old_md5_hash"`
	OldDeletedAt      gorm.DeletedAt `json:"old_deleted_at"`
}

type CachePathInvalidationMessage struct {
	UserID        uint64 `json:"user_id"`
	OldPathPrefix string `json:"old_path_prefix"`
	NewPathPrefix string `json:"new_path_prefix"`
}

func GenerateFileListKey(userID uint64, parentFolderID *uint64) string {
	if parentFolderID == nil {
		return fmt.Sprintf("files:user:%d:folder:root", userID)
	}
	return fmt.Sprintf("files:user:%d:folder:%d", userID, *parentFolderID)
}

func GenerateDeletedFilesKey(userID uint64) string {
	return fmt.Sprintf("files:deleted:user:%d", userID)
}

func GenerateFileMetadataKey(fileID uint64) string {
	return fmt.Sprintf("file:metadata:%d", fileID)
}

func GenerateFileMD5Key(md5Hash string) string {
	return fmt.Sprintf("file:md5:%s", md5Hash)
}
