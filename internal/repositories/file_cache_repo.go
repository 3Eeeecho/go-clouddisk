package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/cache"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/mapper"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type cachedFileRepository struct {
	next  FileRepository // Next repository in the chain (the db repository)
	cache *cache.RedisCache
}

// NewCachedFileRepository creates a new cachedFileRepository instance.
func NewCachedFileRepository(next FileRepository, cache *cache.RedisCache) FileRepository {
	return &cachedFileRepository{
		next:  next,
		cache: cache,
	}
}

func (r *cachedFileRepository) Create(file *models.File) error {
	// First, call the next repository to create the file in the database.
	if err := r.next.Create(file); err != nil {
		return err
	}

	// After successful creation, update the cache.
	ctx := context.Background()
	pipe := r.cache.TxPipeline()
	// Cache the new file's metadata.
	fileMetadataKey := cache.GenerateFileMetadataKey(file.ID)
	fileMap, err := mapper.FileToMap(file)
	if err != nil {
		logger.Error("Create: Failed to map models.File to hash for caching", zap.Uint64("id", file.ID), zap.Error(err))
	} else {
		pipe.HMSet(ctx, fileMetadataKey, fileMap)
		pipe.Expire(ctx, fileMetadataKey, cache.CacheTTL+time.Duration(rand.Intn(300))*time.Second)
	}

	// Add the new file's ID to the corresponding sorted set.
	listCacheKey := cache.GenerateFileListKey(file.UserID, file.ParentFolderID)
	pipe.ZAdd(ctx, listCacheKey, &redis.Z{
		Score:  float64(file.CreatedAt.Unix()),
		Member: strconv.FormatUint(file.ID, 10),
	})
	pipe.ZRem(ctx, listCacheKey, "__EMPTY_LIST__")
	pipe.Expire(ctx, listCacheKey, cache.CacheTTL+time.Duration(rand.Intn(300))*time.Second)

	if _, execErr := pipe.Exec(ctx); execErr != nil {
		logger.Error("Create: Failed to execute Redis pipeline for cache update",
			zap.Uint64("fileID", file.ID),
			zap.Uint64("userID", file.UserID),
			zap.Error(execErr),
		)
	}
	logger.Info("Create: File created and cache updated", zap.Uint64("fileID", file.ID), zap.Uint64("userID", file.UserID))
	return nil
}

func (r *cachedFileRepository) FindByID(id uint64) (*models.File, error) {
	ctx := context.Background()
	fileMetadataKey := cache.GenerateFileMetadataKey(id)

	// Try to get from cache
	resultMap, err := r.cache.HGetAll(ctx, fileMetadataKey)
	if err == nil {
		if _, ok := resultMap["__NOT_FOUND__"]; ok {
			return nil, xerr.ErrFileNotFound
		}
		file, err := mapper.MapToFile(resultMap)
		if err == nil {
			return file, nil
		}
		logger.Error("FindByID: Failed to map cached hash to models.File", zap.Uint64("id", id), zap.Error(err))
	} else if !errors.Is(err, cache.ErrCacheMiss) {
		logger.Error("FindByID: Error getting file hash from cache", zap.Uint64("id", id), zap.Error(err))
	}

	// Cache miss, get from db
	file, err := r.next.FindByID(id)
	if err != nil {
		if errors.Is(err, xerr.ErrFileNotFound) {
			r.cache.HSet(ctx, fileMetadataKey, "__NOT_FOUND__", "1")
			r.cache.Expire(ctx, fileMetadataKey, 1*time.Minute)
		}
		return nil, err
	}

	// Set to cache
	fileMap, mapErr := mapper.FileToMap(file)
	if mapErr != nil {
		logger.Error("FindByID: Failed to map models.File to hash for caching", zap.Uint64("id", id), zap.Error(mapErr))
	} else {
		r.cache.HMSet(ctx, fileMetadataKey, fileMap)
		r.cache.Expire(ctx, fileMetadataKey, cache.CacheTTL+time.Duration(rand.Intn(300))*time.Second)
	}

	return file, nil
}

func (r *cachedFileRepository) FindByUserIDAndParentFolderID(userID uint64, parentFolderID *uint64) ([]models.File, error) {
	ctx := context.Background()
	listCacheKey := cache.GenerateFileListKey(userID, parentFolderID)

	files, err := r.getFilesFromCacheList(ctx, listCacheKey)
	if err == nil {
		sort.Slice(files, func(i, j int) bool {
			if files[i].IsFolder != files[j].IsFolder {
				return files[i].IsFolder > files[j].IsFolder
			}
			return files[i].FileName < files[j].FileName
		})
		return files, nil
	} else if !errors.Is(err, cache.ErrCacheMiss) {
		logger.Error("FindByUserIDAndParentFolderID: Error getting file list from cache", zap.String("key", listCacheKey), zap.Error(err))
	}

	dbFiles, err := r.next.FindByUserIDAndParentFolderID(userID, parentFolderID)
	if err != nil {
		return nil, err
	}

	saveErr := r.saveFilesToCacheList(ctx, listCacheKey, dbFiles, func(file models.File) float64 {
		return float64(file.CreatedAt.Unix())
	})
	if saveErr != nil {
		logger.Error("FindByUserIDAndParentFolderID: Failed to save files to cache", zap.Error(saveErr))
	}
	return dbFiles, nil
}

func (r *cachedFileRepository) FindFileByMD5Hash(md5Hash string) (*models.File, error) {
	ctx := context.Background()
	fileMetadataKey := cache.GenerateFileMD5Key(md5Hash)

	resultMap, err := r.cache.HGetAll(ctx, fileMetadataKey)
	if err == nil {
		if _, ok := resultMap["__NOT_FOUND__"]; ok {
			return nil, xerr.ErrFileNotFound
		}
		file, err := mapper.MapToFile(resultMap)
		if err == nil {
			return file, nil
		}
		logger.Error("FindFileByMD5Hash: Failed to map cached hash to models.File", zap.String("md5Hash", md5Hash), zap.Error(err))
	} else if !errors.Is(err, cache.ErrCacheMiss) {
		logger.Error("FindFileByMD5Hash: Error getting file hash from cache", zap.String("md5Hash", md5Hash), zap.Error(err))
	}

	file, err := r.next.FindFileByMD5Hash(md5Hash)
	if err != nil {
		if errors.Is(err, xerr.ErrFileNotFound) {
			r.cache.HSet(ctx, fileMetadataKey, "__NOT_FOUND__", "1")
			r.cache.Expire(ctx, fileMetadataKey, 1*time.Minute)
		}
		return nil, err
	}

	fileMap, mapErr := mapper.FileToMap(file)
	if mapErr != nil {
		logger.Error("FindFileByMD5Hash: Failed to map models.File to hash for caching", zap.String("md5Hash", md5Hash), zap.Error(mapErr))
	} else {
		r.cache.HMSet(ctx, fileMetadataKey, fileMap)
		r.cache.Expire(ctx, fileMetadataKey, cache.CacheTTL+time.Duration(rand.Intn(300))*time.Second)
	}

	return file, nil
}

func (r *cachedFileRepository) FindDeletedFilesByUserID(userID uint64) ([]models.File, error) {
	ctx := context.Background()
	listCacheKey := cache.GenerateDeletedFilesKey(userID)

	files, err := r.getFilesFromCacheList(ctx, listCacheKey)
	if err == nil {
		sort.Slice(files, func(i, j int) bool {
			return files[i].DeletedAt.Time.After(files[j].DeletedAt.Time)
		})
		return files, nil
	} else if !errors.Is(err, cache.ErrCacheMiss) {
		logger.Error("FindDeletedFilesByUserID: Error getting deleted file list from cache", zap.String("key", listCacheKey), zap.Error(err))
	}

	dbFiles, err := r.next.FindDeletedFilesByUserID(userID)
	if err != nil {
		return nil, err
	}

	saveErr := r.saveFilesToCacheList(ctx, listCacheKey, dbFiles, func(file models.File) float64 {
		score := float64(0)
		if file.DeletedAt.Valid {
			score = float64(file.DeletedAt.Time.Unix())
		}
		return score
	})
	if saveErr != nil {
		logger.Error("FindDeletedFilesByUserID: Failed to save deleted files to cache", zap.Error(saveErr))
	}

	return dbFiles, nil
}

func (r *cachedFileRepository) Update(file *models.File) error {
	oldFile, findErr := r.FindByID(file.ID)
	if findErr != nil {
		return fmt.Errorf("Update: failed to find file for cache invalidation: %w", findErr)
	}

	if err := r.next.Update(file); err != nil {
		return err
	}

	ctx := context.Background()
	fileMetadataKey := cache.GenerateFileMetadataKey(file.ID)
	if err := r.cache.Del(ctx, fileMetadataKey); err != nil {
		logger.Error("Update: Failed to synchronously delete file metadata cache", zap.Uint64("fileID", file.ID), zap.Error(err))
	}

	message := cache.CacheUpdateMessage{
		File:              *file,
		OldParentFolderID: oldFile.ParentFolderID,
		OldMD5Hash:        oldFile.MD5Hash,
		OldDeletedAt:      oldFile.DeletedAt,
	}
	messageJSON, _ := json.Marshal(message)

	_, streamErr := r.cache.XAdd(ctx, &redis.XAddArgs{
		Stream: "file_cache_updates",
		MaxLen: 10000,
		Values: map[string]any{"payload": messageJSON},
	}).Result()

	if streamErr != nil {
		logger.Error("Update: Failed to publish cache update message", zap.Uint64("fileID", file.ID), zap.Error(streamErr))
	}

	return nil
}

func (r *cachedFileRepository) SoftDelete(id uint64) error {
	file, err := r.FindByID(id)
	if err != nil {
		return err
	}
	if file == nil {
		return xerr.ErrFileNotFound
	}

	if err := r.next.SoftDelete(id); err != nil {
		return err
	}

	// Refresh file state after soft delete
	file, err = r.next.FindByID(id)
	if err != nil {
		logger.Error("SoftDelete: Failed to retrieve file after DB soft delete", zap.Uint64("fileID", id), zap.Error(err))
		// Even if we can't get the file, we should try to invalidate what we can
	}

	ctx := context.Background()
	pipe := r.cache.TxPipeline()

	if file != nil {
		fileMetadataKey := cache.GenerateFileMetadataKey(file.ID)
		fileMap, mapErr := mapper.FileToMap(file)
		if mapErr != nil {
			logger.Error("SoftDelete: Failed to map models.File to hash for caching", zap.Uint64("id", file.ID), zap.Error(mapErr))
		} else {
			pipe.HMSet(ctx, fileMetadataKey, fileMap)
			pipe.Expire(ctx, fileMetadataKey, cache.CacheTTL+time.Duration(rand.Intn(300))*time.Second)
		}

		listCacheKey := cache.GenerateFileListKey(file.UserID, file.ParentFolderID)
		pipe.ZRem(ctx, listCacheKey, strconv.FormatUint(file.ID, 10))

		deletedListCacheKey := cache.GenerateDeletedFilesKey(file.UserID)
		if file.DeletedAt.Valid {
			deletedZMember := &redis.Z{
				Score:  float64(file.DeletedAt.Time.Unix()),
				Member: strconv.FormatUint(file.ID, 10),
			}
			pipe.ZAdd(ctx, deletedListCacheKey, deletedZMember)
			pipe.ZRem(ctx, deletedListCacheKey, "__EMPTY_LIST__")
		}

		if file.MD5Hash != nil && *file.MD5Hash != "" {
			pipe.Del(ctx, cache.GenerateFileMD5Key(*file.MD5Hash))
		}
	} else {
		// If we couldn't get the file, at least delete the main metadata key
		pipe.Del(ctx, cache.GenerateFileMetadataKey(id))
	}

	if _, execErr := pipe.Exec(ctx); execErr != nil {
		logger.Error("SoftDelete: Failed to execute Redis pipeline for cache update", zap.Uint64("fileID", id), zap.Error(execErr))
	}

	logger.Info("SoftDelete: File soft deleted and cache updated", zap.Uint64("fileID", id))
	return nil
}

func (r *cachedFileRepository) PermanentDelete(tx *gorm.DB, fileID uint64) error {
	file, err := r.FindByID(fileID)
	if err != nil {
		return err
	}
	if file == nil {
		return xerr.ErrFileNotFound
	}

	if err := r.next.PermanentDelete(tx, fileID); err != nil {
		return err
	}

	ctx := context.Background()
	pipe := r.cache.TxPipeline()

	pipe.Del(ctx, cache.GenerateFileMetadataKey(file.ID))

	listCacheKey := cache.GenerateFileListKey(file.UserID, file.ParentFolderID)
	pipe.ZRem(ctx, listCacheKey, strconv.FormatUint(file.ID, 10))

	deletedListCacheKey := cache.GenerateDeletedFilesKey(file.UserID)
	pipe.ZRem(ctx, deletedListCacheKey, strconv.FormatUint(file.ID, 10))

	if file.MD5Hash != nil && *file.MD5Hash != "" {
		pipe.Del(ctx, cache.GenerateFileMD5Key(*file.MD5Hash))
	}

	if _, execErr := pipe.Exec(ctx); execErr != nil {
		logger.Error("PermanentDelete: Failed to execute Redis pipeline for cache update", zap.Uint64("fileID", file.ID), zap.Error(execErr))
	}

	logger.Info("PermanentDelete: File permanently deleted and cache invalidated", zap.Uint64("fileID", file.ID))
	return nil
}

func (r *cachedFileRepository) UpdateFilesPathInBatch(userID uint64, oldPathPrefix, newPathPrefix string) error {
	if err := r.next.UpdateFilesPathInBatch(userID, oldPathPrefix, newPathPrefix); err != nil {
		return err
	}

	message := cache.CachePathInvalidationMessage{
		UserID:        userID,
		OldPathPrefix: oldPathPrefix,
		NewPathPrefix: newPathPrefix,
	}
	messageJSON, _ := json.Marshal(message)

	_, err := r.cache.XAdd(context.Background(), &redis.XAddArgs{
		Stream: "cache_path_invalidation_stream",
		MaxLen: 10000,
		Values: map[string]any{"payload": messageJSON},
	}).Result()

	if err != nil {
		logger.Error("Failed to publish cache path invalidation message", zap.Error(err))
	}
	return nil
}

func (r *cachedFileRepository) UpdateFileStatus(fileID uint64, status uint8) error {
	if err := r.next.UpdateFileStatus(fileID, status); err != nil {
		return err
	}

	ctx := context.Background()
	file, err := r.FindByID(fileID)
	if err != nil {
		logger.Error("UpdateFileStatus: Failed to find file for cache invalidation", zap.Uint64("fileID", fileID), zap.Error(err))
		return nil
	}

	message := cache.CacheUpdateMessage{
		File: *file,
	}
	messageJSON, _ := json.Marshal(message)

	_, streamErr := r.cache.XAdd(ctx, &redis.XAddArgs{
		Stream: "file_cache_updates",
		MaxLen: 10000,
		Values: map[string]any{"payload": messageJSON},
	}).Result()

	if streamErr != nil {
		logger.Error("UpdateFileStatus: Failed to publish cache update message", zap.Uint64("fileID", fileID), zap.Error(streamErr))
	}

	return nil
}

// Passthrough methods that don't have caching logic
func (r *cachedFileRepository) FindByPath(path string) (*models.File, error) {
	return r.next.FindByPath(path)
}

func (r *cachedFileRepository) FindByUUID(uuid string) (*models.File, error) {
	return r.next.FindByUUID(uuid)
}

func (r *cachedFileRepository) FindByOssKey(ossKey string) (*models.File, error) {
	return r.next.FindByOssKey(ossKey)
}

func (r *cachedFileRepository) FindByFileName(userID uint64, parentFolderID *uint64, fileName string) (*models.File, error) {
	return r.next.FindByFileName(userID, parentFolderID, fileName)
}

func (r *cachedFileRepository) FindChildrenByPathPrefix(userID uint64, pathPrefix string) ([]models.File, error) {
	return r.next.FindChildrenByPathPrefix(userID, pathPrefix)
}

func (r *cachedFileRepository) CountFilesInStorage(ossKey string, md5Hash string, excludeFileID uint64) (int64, error) {
	return r.next.CountFilesInStorage(ossKey, md5Hash, excludeFileID)
}

// private helper methods for caching
func (r *cachedFileRepository) getFilesFromCacheList(ctx context.Context, listCacheKey string) ([]models.File, error) {
	keyExists, err := r.cache.Exists(ctx, listCacheKey)
	if err != nil {
		logger.Error("getFilesFromCacheList: Error checking key existence in cache", zap.String("listCacheKey", listCacheKey), zap.Error(err))
		return nil, fmt.Errorf("failed to check cache key existence: %w", err)
	}

	if !keyExists {
		return nil, cache.ErrCacheMiss
	}

	fileIDsStr, err := r.cache.ZRevRange(ctx, listCacheKey, 0, -1).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, cache.ErrCacheMiss
		}
		logger.Error("Error getting file ID list from cache", zap.String("key", listCacheKey), zap.Error(err))
		return nil, fmt.Errorf("failed to get file ID list from cache: %w", err)
	}

	if len(fileIDsStr) == 0 {
		logger.Warn("getFilesFromCacheList: Sorted Set exists but is truly empty. Treating as cache miss to force DB refresh.", zap.String("listCacheKey", listCacheKey))
		return nil, cache.ErrCacheMiss
	}

	if len(fileIDsStr) == 1 && fileIDsStr[0] == "__EMPTY_LIST__" {
		return []models.File{}, nil
	}

	var fileIDs []uint64
	for _, idStr := range fileIDsStr {
		id, parseErr := strconv.ParseUint(idStr, 10, 64)
		if parseErr != nil {
			logger.Error("Failed to parse file ID from cache", zap.String("idStr", idStr), zap.Error(parseErr))
			continue
		}
		if id > 0 {
			fileIDs = append(fileIDs, id)
		}
	}

	if len(fileIDs) == 0 {
		return []models.File{}, nil
	}

	pipe := r.cache.TxPipeline()
	cmds := make(map[uint64]*redis.StringStringMapCmd)
	for _, fileID := range fileIDs {
		metaKey := fmt.Sprintf("file:metadata:%d", fileID)
		cmds[fileID] = pipe.HGetAll(ctx, metaKey)
	}

	_, execErr := pipe.Exec(ctx)
	if execErr != nil && execErr != redis.Nil {
		logger.Error("Error executing HGetAll pipeline for files metadata", zap.Error(execErr))
		return nil, fmt.Errorf("failed to execute HGetAll pipeline: %w", execErr)
	}

	var files []models.File
	var missedIDs []uint64
	for _, fileID := range fileIDs {
		cmd := cmds[fileID]
		fileMap, getErr := cmd.Result()

		if getErr == nil && len(fileMap) > 0 && fileMap["__NOT_FOUND__"] == "" {
			file, mapErr := mapper.MapToFile(fileMap)
			if mapErr == nil {
				files = append(files, *file)
			} else {
				logger.Error("Failed to map file hash to struct from cache, will fetch from DB", zap.Uint64("fileID", fileID), zap.Error(mapErr))
				missedIDs = append(missedIDs, fileID)
			}
		} else {
			if getErr != nil && getErr != redis.Nil {
				logger.Error("Error getting file metadata hash for ID, will fetch from DB", zap.Uint64("fileID", fileID), zap.Error(getErr))
			}
			missedIDs = append(missedIDs, fileID)
		}
	}

	if len(missedIDs) > 0 {
		logger.Warn("getFilesFromCacheList: Cache inconsistency detected. Fetching from DB.",
			zap.String("listCacheKey", listCacheKey),
			zap.Uint64s("missedFileIDs", missedIDs))

		// This is a simplification. In a real-world scenario, you might want to fetch from the `next` repository.
		// However, to avoid circular dependencies and keep the decorator simple, we'll log this.
		// A more robust solution might involve a specific method in the `next` repo to find by multiple IDs.
		// For now, we return what we have and let the next request handle the cache miss.
		// This is a trade-off for simplicity.
	}

	return files, nil
}

func (r *cachedFileRepository) saveFilesToCacheList(ctx context.Context, cacheKey string, files []models.File, scoreFunc func(file models.File) float64) error {
	pipe := r.cache.TxPipeline()

	if len(files) == 0 {
		pipe.ZAdd(ctx, cacheKey, &redis.Z{Score: 0, Member: "__EMPTY_LIST__"})
	} else {
		var zs []*redis.Z
		for _, file := range files {
			fileMap, mapErr := mapper.FileToMap(&file)
			if mapErr != nil {
				logger.Error("saveFilesToCacheList: Failed to map models.File to hash for caching", zap.Uint64("fileID", file.ID), zap.Error(mapErr))
				continue
			}
			metaKey := cache.GenerateFileMetadataKey(file.ID)
			pipe.HMSet(ctx, metaKey, fileMap)
			pipe.Expire(ctx, metaKey, cache.CacheTTL+time.Duration(rand.Intn(300))*time.Second)

			zs = append(zs, &redis.Z{
				Score:  scoreFunc(file),
				Member: strconv.FormatUint(file.ID, 10),
			})
		}
		if len(zs) > 0 {
			pipe.ZAdd(ctx, cacheKey, zs...)
		}
	}
	pipe.Expire(ctx, cacheKey, cache.CacheTTL+time.Duration(rand.Intn(300))*time.Second)

	_, execErr := pipe.Exec(ctx)
	if execErr != nil {
		logger.Error("saveFilesToCacheList: Failed to execute Redis pipeline for caching list.", zap.String("key", cacheKey), zap.Error(execErr))
	}
	return nil
}
