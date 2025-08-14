package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/cache"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/mapper"
	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func StartCacheUpdateConsumer(ctx context.Context, redisClient *redis.Client) {
	// 创建消费者组
	// "0" 表示从 Stream 的开头读取所有消息。
	streamName := "file_cache_updates"
	groupName := "file_cache_group"
	redisClient.XGroupCreateMkStream(ctx, streamName, groupName, "0").Result()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			streams, err := redisClient.XReadGroup(ctx, &redis.XReadGroupArgs{
				Group:    groupName,
				Consumer: "file_cache_consumer_1",
				Streams:  []string{streamName, ">"}, // 从未消费的消息开始读
				Count:    10,                        // 每次批量读取10条
				Block:    0,                         // 不阻塞
			}).Result()
			if err != nil {
				logger.Error("Consumer: Failed to read from Redis Streams", zap.Error(err))
				time.Sleep(5 * time.Second)
				continue
			}

			if len(streams) > 0 {
				for _, stream := range streams {
					for _, message := range stream.Messages {
						//处理每条消息
						if err := processCacheMessage(ctx, redisClient, message); err != nil {
							logger.Error("Consumer: Failed to process message", zap.Error(err))
							// 消息处理失败，不发送 XACK，让消息保留在 pending list，等待重试
							continue
						}
						// 成功处理后发送确认，告知 Redis 可以删除这条消息
						redisClient.XAck(ctx, "file_cache_updates", "file_cache_group", message.ID).Result()
					}
				}
			}
		}
	}
}

// 负责实际的缓存更新逻辑
func processCacheMessage(ctx context.Context, redisClient *redis.Client, message redis.XMessage) error {
	// 从 message 中解析出 CacheUpdateMessage 结构体
	var updateMsg cache.CacheUpdateMessage
	jsonBytes, ok := message.Values["payload"].(string)
	if !ok {
		return fmt.Errorf("invalid message payload format")
	}
	if err := json.Unmarshal([]byte(jsonBytes), &updateMsg); err != nil {
		return fmt.Errorf("failed to unmarshal message: %w", err)
	}

	pipe := redisClient.TxPipeline()
	fileMetadataKey := cache.GenerateFileMetadataKey(updateMsg.File.ID)

	fileMap, err := mapper.FileToMap(&updateMsg.File)
	if err != nil {
		logger.Error("FindByID: Failed to map models.File to hash for caching", zap.Uint64("id", updateMsg.File.ID), zap.Error(err))
	} else {
		pipe.HMSet(ctx, fileMetadataKey, fileMap)
		pipe.Expire(ctx, fileMetadataKey, cache.CacheTTL+time.Duration(rand.Intn(300))*time.Second)
	}

	// 获取旧的父文件夹键和新的父文件夹键
	oldListCacheKey := cache.GenerateFileListKey(updateMsg.File.UserID, updateMsg.OldParentFolderID)
	newListCacheKey := cache.GenerateFileListKey(updateMsg.File.UserID, updateMsg.File.ParentFolderID)
	logger.Info("processCacheMessage", zap.String("oldListCacheKey", oldListCacheKey), zap.String("newListCacheKey", newListCacheKey))

	// 文件ID的字符串形式
	fileIDStr := strconv.FormatUint(updateMsg.File.ID, 10)
	newZMember := &redis.Z{
		Score:  float64(updateMsg.File.CreatedAt.Unix()), // 假设 Score 仍然基于 CreatedAt
		Member: fileIDStr,
	}

	// 判断 ParentFolderID 是否变化
	if oldListCacheKey != newListCacheKey {
		// 从旧父目录的 Sorted Set 中 ZRem 掉该文件 ID
		pipe.ZRem(ctx, oldListCacheKey, fileIDStr)

		// ZAdd 到新父目录的 Sorted Set 中
		pipe.ZAdd(ctx, newListCacheKey, newZMember)
		pipe.ZRem(ctx, newListCacheKey, "__EMPTY_LIST__") // 如果新列表之前有空标记，删除
	} else {
		// ParentFolderID 没有变化，但可能需要更新文件在当前列表中的排序分数
		// 稳妥的做法是先移除旧的，再添加新的，以确保分数更新
		pipe.ZRem(ctx, newListCacheKey, fileIDStr)
		pipe.ZAdd(ctx, newListCacheKey, newZMember)
		pipe.ZRem(ctx, newListCacheKey, "__EMPTY_LIST__") // 确保移除空标记
	}

	// --- 精确更新回收站缓存 ---
	deletedListCacheKey := cache.GenerateDeletedFilesKey(updateMsg.File.UserID)
	wasDeleted := updateMsg.OldDeletedAt.Valid
	isNowDeleted := updateMsg.File.DeletedAt.Valid

	if !wasDeleted && isNowDeleted {
		// 文件被软删除：添加到回收站列表
		deletedZMember := &redis.Z{
			Score:  float64(updateMsg.File.DeletedAt.Time.Unix()),
			Member: fileIDStr,
		}
		pipe.ZAdd(ctx, deletedListCacheKey, deletedZMember)
		pipe.ZRem(ctx, deletedListCacheKey, "__EMPTY_LIST__") // 确保移除空标记
	} else if wasDeleted && !isNowDeleted {
		// 文件被恢复：从回收站列表移除
		pipe.ZRem(ctx, deletedListCacheKey, fileIDStr)
	}
	// 如果删除状态没有改变，则不执行任何操作

	// TODO: 如果业务允许MD5更新（例如文件内容更新），则需要删除旧缓存,并设置新缓存

	// 执行管道命令
	if _, execErr := pipe.Exec(ctx); execErr != nil {
		// 修复错误包装：应该包装 execErr 而不是 err
		return fmt.Errorf("failed to execute Redis pipeline: %w", execErr)
	}
	logger.Info("successfully process message", zap.Uint64("file_id", updateMsg.File.ID))
	return nil
}

func StartPathInvalidationConsumer(ctx context.Context, db *gorm.DB, redisClient *redis.Client) {
	streamName := "cache_path_invalidation_stream"
	groupName := "path_invalidation_group"
	consumerName := "path_invalidation_consumer_1"

	redisClient.XGroupCreateMkStream(ctx, streamName, groupName, "0")
	for {
		select {
		case <-ctx.Done():
			return
		default:
			streams, err := redisClient.XReadGroup(ctx, &redis.XReadGroupArgs{
				Group:    groupName,
				Consumer: consumerName,
				Streams:  []string{streamName, ">"},
				Block:    0,
				Count:    10,
			}).Result()

			if err != nil {
				logger.Error("BatchInvalidationConsumer: Failed to read from stream", zap.Error(err))
				time.Sleep(time.Second * 5)
				continue
			}

			if len(streams) > 0 {
				for _, message := range streams[0].Messages {
					if err := processInvalidationMessage(ctx, db, redisClient, message); err != nil {
						logger.Error("Failed to process invalidation message", zap.Error(err))
					} else {
						redisClient.XAck(ctx, streamName, groupName, message.ID).Result()
					}
				}
			}
		}
	}
}

// 处理具体的缓存失效逻辑
func processInvalidationMessage(ctx context.Context, db *gorm.DB, redisClient *redis.Client, message redis.XMessage) error {
	var pathInvalidationMsg cache.CachePathInvalidationMessage
	jsonBytes, ok := message.Values["payload"].(string)
	if !ok {
		return fmt.Errorf("invalid message payload format")
	}
	if err := json.Unmarshal([]byte(jsonBytes), &pathInvalidationMsg); err != nil {
		return fmt.Errorf("failed to unmarshal message :%w", err)
	}

	//从数据库中找出所有受影响的文件ID
	// ⚠️ 注意：这个查询可能需要一段时间，因为它需要回源数据库
	var affectedFiles []models.File
	if err := db.WithContext(ctx).
		Where("user_id = ? AND path LIKE ?", pathInvalidationMsg.UserID, pathInvalidationMsg.OldPathPrefix+"%").
		Find(&affectedFiles).Error; err != nil {
		return fmt.Errorf("failed to find affected files for invalidation: %w", err)
	}

	if len(affectedFiles) == 0 {
		return nil //没有受影响文件,直接返回
	}

	//批量删除Redis缓存
	//首先删除元数据缓存
	var metaKeys []string
	for _, file := range affectedFiles {
		metaKeys = append(metaKeys, cache.GenerateFileMetadataKey(file.ID))
	}
	if len(metaKeys) > 0 {
		redisClient.Del(ctx, metaKeys...)
	}

	// 删除文件列表缓存的逻辑被移除。
	// 移动文件夹时，文件夹本身的 ParentFolderID 会变化，这会触发 processCacheMessage，
	// 正确地更新源目录和目标目录的列表缓存。
	// 子文件的 Path 字段虽然变了，但它们的 ParentFolderID 没有变，所以它们所属的列表缓存 (files:user:id:folder:id) 是不变的。
	// 因此，我们只需要让子文件的元数据缓存失效即可，这在前面已经通过 Del(metaKeys) 完成了。
	// 删除所有文件夹列表缓存是错误且没有必要的。

	logger.Info("Successfully invalidated metadata caches for path update",
		zap.Int("affected_files_count", len(affectedFiles)),
		zap.Uint64("user_id", pathInvalidationMsg.UserID),
		zap.String("old_path_prefix", pathInvalidationMsg.OldPathPrefix),
	)

	return nil
}
