package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sort"
	"strconv"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/cache"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/go-redis/redis/v8"
	"github.com/go-viper/mapstructure/v2"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// FileRepository 定义文件数据访问层接口
type FileRepository interface {
	Create(file *models.File) error

	FindByID(id uint64) (*models.File, error)
	//FindByUserID(userID uint64) ([]models.File, error)                                          // 获取用户所有文件
	FindByUserIDAndParentFolderID(userID uint64, parentFolderID *uint64) ([]models.File, error) // 获取指定文件夹下的文件
	FindByPath(path string) (*models.File, error)
	FindByUUID(uuid string) (*models.File, error)                  // 根据 UUID 查找
	FindByOssKey(ossKey string) (*models.File, error)              //根据 OssKey 查找
	FindFileByMD5Hash(md5Hash string) (*models.File, error)        // 根据存储路径查找文件
	FindDeletedFilesByUserID(userID uint64) ([]models.File, error) //查找回收站中的文件
	FindChildrenByPathPrefix(userID uint64, pathPrefix string) ([]models.File, error)
	CountFilesInStorage(ossKey string, md5Hash string, excludeFileID uint64) (int64, error)

	UpdateFilesPathInBatch(tx *gorm.DB, userID uint64, oldPathPrefix, newPathPrefix string) error
	Update(file *models.File) error
	SoftDelete(id uint64) error      // 软删除文件
	PermanentDelete(id uint64) error // 永久删除文件

}

type fileRepository struct {
	db       *gorm.DB
	cache    *cache.RedisCache
	cacheTTL time.Duration // 缓存过期时间默认为5分钟
}

// NewFileRepository 创建一个新的 FileRepository 实例
func NewFileRepository(db *gorm.DB, cache *cache.RedisCache) FileRepository {
	return &fileRepository{
		db:       db,
		cache:    cache,
		cacheTTL: 10 * time.Minute,
	}
}

func (r *fileRepository) Create(file *models.File) error {
	err := r.db.Create(file).Error
	if err != nil {
		logger.Error("Create: Failed to create file in DB", zap.Error(err), zap.Uint64("userID", file.UserID), zap.String("fileName", file.FileName))
		return fmt.Errorf("failed to create file: %w", err)
	}
	ctx := context.Background()
	pipe := r.cache.TxPipeline()
	// 将新文件的元数据存入 file:metadata:<new_file_id>
	fileMetadataKey := generateFileMetadataKey(file.ID)
	fileMap, err := r.fileToMap(file) // 辅助函数将 models.File 映射到 map[string]any
	if err != nil {
		logger.Error("FindByID: Failed to map models.File to hash for caching", zap.Uint64("id", file.ID), zap.Error(err))
	} else {
		pipe.HMSet(ctx, fileMetadataKey, fileMap)
		pipe.Expire(ctx, fileMetadataKey, r.cacheTTL)
	}

	// ZAdd 将新文件的 ID 及其 CreatedAt 的 Unix 时间戳作为 Score，添加到对应的 Sorted Set 中
	listCacheKey := generateFileListKey(file.UserID, file.ParentFolderID)
	pipe.ZAdd(ctx, listCacheKey, &redis.Z{
		Score:  float64(file.CreatedAt.Unix()),
		Member: strconv.FormatUint(file.ID, 10),
	})

	//如果之前存过"__EMPTY_LIST__",需要ZRem掉
	pipe.ZRem(ctx, listCacheKey, "__EMPTY_LIST__")

	//(可选)将MD5Hash查询也缓存
	// if file.MD5Hash != nil && *file.MD5Hash != "" {
	// 	md5CacheKey := generateFileMD5Key(*file.MD5Hash)
	// 	pipe.Set(ctx, md5CacheKey, file.ID, r.cacheTTL)
	// }

	if _, execErr := pipe.Exec(ctx); execErr != nil {
		logger.Error("Create: Failed to execute Redis pipeline for cache update",
			zap.Uint64("fileID", file.ID),
			zap.Uint64("userID", file.UserID),
			zap.Error(execErr),
		)
		// 缓存更新失败通常不返回错误，但需要记录
	}
	logger.Info("Create: File created and cache updated", zap.Uint64("fileID", file.ID), zap.Uint64("userID", file.UserID))
	return nil
}

func (r *fileRepository) FindByID(id uint64) (*models.File, error) {
	ctx := context.Background()
	fileMetadataKey := generateFileMetadataKey(id)

	// 尝试从Redis缓存中获取
	// 单文件缓存采用Hash结构
	resultMap, err := r.cache.HGetAll(ctx, fileMetadataKey)
	if err == nil {
		if _, ok := resultMap["__NOT_FOUND__"]; ok {
			return nil, xerr.ErrParentFolderNotFound //  如果从缓存命中不存在标记，直接返回不存在错误
		}
		file, err := r.mapToFile(resultMap) // 辅助函数将 map[string]string 映射到 models.File
		if err == nil {
			return file, nil
		}
		logger.Error("FindByID: Failed to map cached hash to models.File", zap.Uint64("id", id), zap.Error(err))
	} else if !errors.Is(err, cache.ErrCacheMiss) { // 只有不是 ErrCacheMiss 才记录错误
		logger.Error("FindByID: Error getting file hash from cache", zap.Uint64("id", id), zap.Error(err))
	}

	// 缓存未命中或获取失败，从数据库中加载
	var file models.File
	err = r.db.Unscoped().First(&file, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 如果数据库中也不存在，缓存一个空值，防止缓存穿透
			r.cache.HSet(ctx, fileMetadataKey, "__NOT_FOUND__", "1")
			r.cache.Expire(ctx, fileMetadataKey, 1*time.Minute)
			return nil, err // 文件未找到
		}
		return nil, fmt.Errorf("从数据库查询文件失败: %w", err)
	}

	// 将从数据库中获取的数据存入 Redis 缓存
	fileMap, err := r.fileToMap(&file) // 辅助函数将 models.File 映射到 map[string]any
	if err != nil {
		logger.Error("FindByID: Failed to map models.File to hash for caching", zap.Uint64("id", id), zap.Error(err))
	} else {
		r.cache.HMSet(ctx, fileMetadataKey, fileMap)     // 使用封装好的 HMSet
		r.cache.Expire(ctx, fileMetadataKey, r.cacheTTL) // 使用封装好的 Expire
	}

	return &file, nil
}

// 获取指定用户在特定父文件夹下的文件和文件夹
// parentFolderID 可以为 nil，表示根目录
func (r *fileRepository) FindByUserIDAndParentFolderID(userID uint64, parentFolderID *uint64) ([]models.File, error) {
	ctx := context.Background()

	// 尝试从Redis缓存中获取
	var files []models.File
	listCacheKey := generateFileListKey(userID, parentFolderID)

	files, err := r.getFilesFromCacheList(ctx, listCacheKey)
	if err == nil {
		// 缓存命中，并且数据已获取并反序列化
		// 对获取到的文件列表进行 Go 语言内存排序 (模拟数据库的复合排序)
		sort.Slice(files, func(i, j int) bool {
			// 优先显示文件夹 (IsFolder=1在前)
			if files[i].IsFolder != files[j].IsFolder {
				return files[i].IsFolder > files[j].IsFolder // 1 > 0 => folder comes before file
			}
			// 其次按文件名排序
			return files[i].FileName < files[j].FileName // ASC order
		})
		return files, nil
	} else if !errors.Is(err, cache.ErrCacheMiss) { // 只有不是 ErrCacheMiss 才记录错误
		logger.Error("FindByUserIDAndParentFolderID: Error getting file list from cache", zap.String("key", listCacheKey), zap.Error(err))
		// 对于非缓存未命中错误，可以选择继续尝试从数据库获取或直接返回错误
		// 这里选择继续从数据库获取作为兜底
	}

	var dbFiles []models.File
	query := r.db.Where("user_id = ?", userID)

	if parentFolderID == nil {
		query = query.Where("parent_folder_id IS NULL") // 查找根目录
	} else {
		query = query.Where("parent_folder_id = ?", *parentFolderID) // 查找指定文件夹
	}

	// 优先显示文件夹，然后按文件名排序
	err = query.Order("is_folder DESC, file_name ASC").Find(&dbFiles).Error
	if err != nil {
		logger.Error("Error finding files from DB", zap.Uint64("userID", userID), zap.Any("parentFolderID", parentFolderID), zap.Error(err))
		return nil, fmt.Errorf("查询文件列表失败: %w", err)
	}

	saveErr := r.saveFilesToCacheList(ctx, listCacheKey, dbFiles, func(file models.File) float64 {
		return float64(file.CreatedAt.Unix())
	})
	if saveErr != nil {
		logger.Error("FindByUserIDAndParentFolderID: Failed to save files to cache", zap.Error(saveErr))
		// 这里的策略是：即使缓存失败，也返回从数据库查到的数据，不阻塞业务
	}
	return dbFiles, nil
}

// FindFileByMD5Hash 根据 MD5Hash 查找文件
func (r *fileRepository) FindFileByMD5Hash(md5Hash string) (*models.File, error) {
	ctx := context.Background()
	fileMetadataKey := generateFileMD5Key(md5Hash)
	// 尝试从Redis缓存中获取
	resultMap, err := r.cache.HGetAll(ctx, fileMetadataKey)
	if err == nil {
		if _, ok := resultMap["__NOT_FOUND__"]; ok {
			return nil, xerr.ErrParentFolderNotFound //  如果从缓存命中不存在标记，直接返回不存在错误
		}
		file, err := r.mapToFile(resultMap) // 辅助函数将 map[string]string 映射到 models.File
		if err == nil {
			return file, nil
		}
		logger.Error("FindByID: Failed to map cached hash to models.File", zap.String("md5Hash", md5Hash), zap.Error(err))
	} else if !errors.Is(err, cache.ErrCacheMiss) { // 只有不是 ErrCacheMiss 才记录错误
		logger.Error("FindByID: Error getting file hash from cache", zap.String("md5Hash", md5Hash), zap.Error(err))
	}

	var file models.File
	// 注意：这里我们可能需要查询的是那些非文件夹且状态正常的文件的 MD5Hash
	err = r.db.Where("md5_hash = ? AND is_folder = 0 AND status = 1", md5Hash).First(&file).Error
	if err != nil {
		log.Printf("Error finding file by MD5 hash %s: %v", md5Hash, err)
		return nil, err
	}

	// 将从数据库中获取的数据存入 Redis 缓存
	fileMap, err := r.fileToMap(&file) // 辅助函数将 models.File 映射到 map[string]any
	if err != nil {
		logger.Error("FindByID: Failed to map models.File to hash for caching", zap.String("md5Hash", md5Hash), zap.Error(err))
	} else {
		r.cache.HMSet(ctx, fileMetadataKey, fileMap)     // 使用封装好的 HMSet
		r.cache.Expire(ctx, fileMetadataKey, r.cacheTTL) // 使用封装好的 Expire
	}

	return &file, nil
}

func (r *fileRepository) FindDeletedFilesByUserID(userID uint64) ([]models.File, error) {
	ctx := context.Background()
	// 尝试从Redis缓存中获取
	listCacheKey := generateDeletedFilesKey(userID)

	files, err := r.getFilesFromCacheList(ctx, listCacheKey)
	if err == nil {
		// 这里的排序通常是 DeletedAt DESC，ZRevRange 已经能保证这个顺序
		sort.Slice(files, func(i, j int) bool {
			return files[i].DeletedAt.Time.After(files[j].DeletedAt.Time)
		})
		logger.Info("successfully hit cache")
		return files, nil
	} else if !errors.Is(err, cache.ErrCacheMiss) {
		logger.Error("FindDeletedFilesByUserID: Error getting deleted file list from cache", zap.String("key", listCacheKey), zap.Error(err))
	}

	var dbFiles []models.File
	err = r.db.Unscoped().Where("user_id = ?", userID).Where("deleted_at IS NOT NULL").Order("deleted_at DESC").Find(&dbFiles).Error
	if err != nil {
		logger.Error("Error finding deleted files from DB", zap.Uint64("userID", userID), zap.Error(err))
		return nil, fmt.Errorf("查询已删除文件列表失败: %w", err)
	}
	logger.Info("DB Query Result (after Find)", zap.Int("count", len(dbFiles)), zap.Uint64("userID", userID))
	for i, file := range dbFiles {
		logger.Info("DB File", zap.Int("index", i), zap.Uint64("id", file.ID), zap.Timep("deleted_at", &file.DeletedAt.Time), zap.String("filename", file.FileName))
	}

	// 将从数据库中获取的数据存入 Redis 缓存
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

// 未使用函数均没设置redis相关的逻辑,也没有再update函数中添加删除缓存逻辑
// 获取用户所有文件 (不包括文件夹，或者可以根据IsFolder过滤) (未使用)
func (r *fileRepository) FindByUserID(userID uint64) ([]models.File, error) {
	ctx := context.Background()

	// 尝试从Redis缓存中获取
	var files []models.File
	cacheKey := fmt.Sprintf("file:user_id:%d", userID)
	err := r.cache.Get(ctx, cacheKey, &files)
	if err == nil {
		return files, nil
	} else {
		logger.Error("获取缓存数据发生错误", zap.Uint64("id", userID), zap.Error(err))
	}

	// 查询用户所有文件和文件夹，按创建时间排序，不包括已删除的
	err = r.db.Where("user_id = ?", userID).Order("created_at desc").Find(&files).Error
	if err != nil {
		log.Printf("Error finding files for user %d: %v", userID, err)
		return nil, err
	}

	// 将从数据库中获取的数据存入 Redis 缓存
	r.cache.Set(ctx, cacheKey, files, r.cacheTTL)

	return files, nil
}

// FindByUUID 根据 UUID 查找文件 (未使用)
func (r *fileRepository) FindByUUID(uuid string) (*models.File, error) {
	ctx := context.Background()

	// 尝试从Redis缓存中获取
	var file models.File
	cacheKey := fmt.Sprintf("file:uuid:%s", uuid)
	err := r.cache.Get(ctx, cacheKey, &file)
	if err == nil {
		return &file, nil
	} else {
		logger.Error("获取文件缓存数据发生错误", zap.String("uuid", uuid), zap.Error(err))
	}

	// 从数据库中获取数据
	err = r.db.Where("uuid = ?", uuid).First(&file).Error
	if err != nil {
		log.Printf("Error finding file by UUID %s: %v", uuid, err)
		return nil, err
	}

	// 将从数据库中获取的数据存入 Redis 缓存
	r.cache.Set(ctx, cacheKey, file, r.cacheTTL)

	return &file, nil
}

// FindByOssKey 根据 OssKey 查找文件 (未使用)
func (r *fileRepository) FindByOssKey(ossKey string) (*models.File, error) {
	ctx := context.Background()

	// 尝试从Redis缓存中获取
	var file models.File
	cacheKey := fmt.Sprintf("file:ossKey:%s", ossKey)
	err := r.cache.Get(ctx, cacheKey, &file)
	if err == nil {
		return &file, nil
	} else {
		logger.Error("获取文件缓存数据发生错误", zap.String("ossKey", ossKey), zap.Error(err))
	}

	err = r.db.Where("oss_key = ?", ossKey).First(&file).Error
	if err != nil {
		log.Printf("Error finding file by OssKey %s: %v", ossKey, err)
		return nil, err
	}

	// 将从数据库中获取的数据存入 Redis 缓存
	r.cache.Set(ctx, cacheKey, file, r.cacheTTL)

	return &file, nil
}

// 根据存储路径查找文件
// 缓存失效逻辑非常复杂，维护成本高,暂时不考虑添加缓存逻辑
func (r *fileRepository) FindByPath(path string) (*models.File, error) {
	var file models.File
	err := r.db.Where("storage_path = ?", path).First(&file).Error
	if err != nil {
		log.Printf("Error finding file by path %s: %v", path, err)
		return nil, err
	}
	return &file, nil
}

func (r *fileRepository) Update(file *models.File) error {
	// 获取文件旧状态，用于判断 ParentFolderID 是否变化
	oldFile, findErr := r.FindByID(file.ID)
	if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) { // 如果是其他查找错误
		logger.Error("Update: Failed to retrieve old file state for cache invalidation", zap.Uint64("fileID", file.ID), zap.Error(findErr))
		// 这里的错误应该向上返回，因为它可能影响更新逻辑
		return fmt.Errorf("failed to get old file state for update: %w", findErr)
	}
	// 如果 oldFile 为 nil (gorm.ErrRecordNotFound) 表示文件不存在，直接返回错误
	// 这里假设 Save 会根据 ID 更新，如果 ID 不存在，Save 也会报错。
	// 所以如果 FindByID 返回 NotFound，通常 Update 也不应该进行。
	if errors.Is(findErr, gorm.ErrRecordNotFound) || oldFile == nil {
		return fmt.Errorf("Update: file with ID %d not found in DB", file.ID)
	}

	err := r.db.Save(file).Error
	if err != nil {
		logger.Error("Update: Failed to update file in DB", zap.Error(err), zap.Uint64("fileID", file.ID), zap.Uint64("userID", file.UserID))
		return fmt.Errorf("failed to update file: %w", err)
	}

	ctx := context.Background()
	pipe := r.cache.TxPipeline()
	fileMetadataKey := generateFileMetadataKey(file.ID)

	fileMap, err := r.fileToMap(file)
	if err != nil {
		logger.Error("FindByID: Failed to map models.File to hash for caching", zap.Uint64("id", file.ID), zap.Error(err))
	} else {
		pipe.HMSet(ctx, fileMetadataKey, fileMap)
		pipe.Expire(ctx, fileMetadataKey, r.cacheTTL)
	}

	// 获取旧的父文件夹键和新的父文件夹键
	oldListCacheKey := generateFileListKey(oldFile.UserID, oldFile.ParentFolderID)
	newListCacheKey := generateFileListKey(file.UserID, file.ParentFolderID)

	// 文件ID的字符串形式
	fileIDStr := strconv.FormatUint(file.ID, 10)
	newZMember := &redis.Z{
		Score:  float64(file.CreatedAt.Unix()), // 假设 Score 仍然基于 CreatedAt
		Member: fileIDStr,
	}

	// 判断 ParentFolderID 是否变化
	parentFolderIDChanged := false
	if oldListCacheKey != newListCacheKey { // 更简洁的判断方式
		parentFolderIDChanged = true
	}

	if parentFolderIDChanged {
		// 从旧父目录的 Sorted Set 中 ZRem 掉该文件 ID
		pipe.ZRem(ctx, oldListCacheKey, fileIDStr)
		// 考虑旧列表变空时是否写入 __EMPTY_LIST__
		// 这是一个复杂的逻辑点，通常在GetFilesFromCacheList的回源逻辑中处理更简单
		// pipe.ZAdd(ctx, oldListCacheKey, &redis.Z{Score: 0, Member: "__EMPTY_LIST__"}) // 谨慎添加

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

	// TODO 如果业务允许MD5更新（例如文件内容更新），则需要删除旧缓存,并设置新缓存

	// 	如果文件状态从“已删除”恢复，或从“正常”变为“已删除”，需要更新已删除列表缓存
	// 这里先简单地删除整个 deleted 列表缓存，强制下次查询时重建
	// 更精确的做法是根据 oldFile.DeletedAt 和 file.DeletedAt 的状态来 ZRem/ZAdd
	pipe.Del(ctx, fmt.Sprintf("files:deleted:user:%d", file.UserID))

	// 执行管道命令
	if _, execErr := pipe.Exec(ctx); execErr != nil {
		logger.Error("Update: Failed to execute Redis pipeline for cache update",
			zap.Uint64("fileID", file.ID),
			zap.Uint64("userID", file.UserID),
			zap.Error(execErr),
		)
	}

	return err
}

// 软删除文件,设置DeletedAt字段
func (r *fileRepository) SoftDelete(fileID uint64) error {
	file, err := r.FindByID(fileID) // 这里 FindByID 会从缓存或DB获取，以获取文件详细信息
	if err != nil {
		return err
	}
	if file == nil {
		return xerr.ErrFileNotFound
	}

	err = r.db.Model(file).
		Where("id = ?", fileID).
		Updates(map[string]any{
			"deleted_at": time.Now(), // 显式设置 deleted_at，以防 GORM 版本行为不一致
			"status":     0,          // 设置 status 字段为 0
		}).Error
	if err != nil {
		logger.Error("SoftDelete: Failed to soft delete file in DB", zap.Error(err), zap.Uint64("fileID", fileID))
		return fmt.Errorf("failed to soft delete file: %w", err)
	}

	ctx := context.Background()
	pipe := r.cache.TxPipeline()
	fileMetadataKey := generateFileMetadataKey(file.ID)

	fileMap, err := r.fileToMap(file)
	if err != nil {
		logger.Error("FindByID: Failed to map models.File to hash for caching", zap.Uint64("id", file.ID), zap.Error(err))
	} else {
		// HMSet 更新 file:metadata:<file_id> 中的 status 和 deleted_at 字段
		// 因为软删除会更新 DeletedAt 字段，所以重新存储整个 map
		pipe.HMSet(ctx, fileMetadataKey, fileMap)
		pipe.Expire(ctx, fileMetadataKey, r.cacheTTL)
	}

	//从原本列表中移除该文件 ID
	listCacheKey := generateFileListKey(file.UserID, file.ParentFolderID)
	pipe.ZRem(ctx, listCacheKey, strconv.FormatUint(file.ID, 10))

	// 使用 deleted_at 的 Unix 时间戳作为 Score
	deletedListCacheKey := generateDeletedFilesKey(file.UserID)
	if file.DeletedAt.Valid { // 确保 DeletedAt 是有效的
		deletedZMember := &redis.Z{
			Score:  float64(file.DeletedAt.Time.Unix()),
			Member: strconv.FormatUint(file.ID, 10),
		}
		pipe.ZAdd(ctx, deletedListCacheKey, deletedZMember)
		pipe.ZRem(ctx, deletedListCacheKey, "__EMPTY_LIST__") // 如果之前有空标记，删除
	} else {
		logger.Warn("SoftDelete: file.DeletedAt is not valid after GORM Delete. Check model hooks.", zap.Uint64("fileID", file.ID))
	}

	// 删除单文件 MD5 缓存，因为文件状态变化可能影响其查找
	if file.MD5Hash != nil && *file.MD5Hash != "" {
		pipe.Del(ctx, generateFileMD5Key(*file.MD5Hash))
	}

	// 执行管道命令
	if _, execErr := pipe.Exec(ctx); execErr != nil {
		logger.Error("SoftDelete: Failed to execute Redis pipeline for cache update",
			zap.Uint64("fileID", file.ID),
			zap.Uint64("userID", file.UserID),
			zap.Error(execErr),
		)
	}

	logger.Info("SoftDelete: File soft deleted and cache updated", zap.Uint64("fileID", file.ID), zap.Uint64("userID", file.UserID))
	return nil
}

// 永久删除文件
func (r *fileRepository) PermanentDelete(id uint64) error {
	file, err := r.FindByID(id) // 获取文件信息以便删除相关缓存
	if err != nil {
		return err
	}
	if file == nil {
		return xerr.ErrFileNotFound
	}

	// 永久删除数据库记录
	err = r.db.Unscoped().Delete(&models.File{}, id).Error // Unscoped() 绕过软删除
	if err != nil {
		return fmt.Errorf("永久删除文件失败: %w", err)
	}

	ctx := context.Background()
	pipe := r.cache.TxPipeline()
	fileMetadataKey := generateFileMetadataKey(file.ID)

	fileMap, err := r.fileToMap(file)
	if err != nil {
		logger.Error("FindByID: Failed to map models.File to hash for caching", zap.Uint64("id", file.ID), zap.Error(err))
	} else {
		// HMSet 更新 file:metadata:<file_id> 中的 status 和 deleted_at 字段
		// 因为软删除会更新 DeletedAt 字段，所以重新存储整个 map
		pipe.HMSet(ctx, fileMetadataKey, fileMap)
		pipe.Expire(ctx, fileMetadataKey, r.cacheTTL)
	}

	// Del 删除 file:metadata:<file_id> 哈希键
	pipe.Del(ctx, generateFileMetadataKey(file.ID))

	// ZRem 从所有可能存在的相关 Sorted Set 中移除该文件 ID
	// 从原父目录的 Sorted Set 中移除 (无论它是否在回收站，原父目录列表都不应再包含它)
	listCacheKey := generateFileListKey(file.UserID, file.ParentFolderID)
	pipe.ZRem(ctx, listCacheKey, strconv.FormatUint(file.ID, 10))

	// 从回收站列表 Sorted Set 中移除 (如果它在回收站的话)
	deletedListCacheKey := fmt.Sprintf("files:deleted:user:%d", file.UserID)
	pipe.ZRem(ctx, deletedListCacheKey, strconv.FormatUint(file.ID, 10))

	if file.MD5Hash != nil && *file.MD5Hash != "" {
		pipe.Del(ctx, generateFileMD5Key(*file.MD5Hash))
	}

	// 执行管道命令
	if _, execErr := pipe.Exec(ctx); execErr != nil {
		logger.Error("PermanentDelete: Failed to execute Redis pipeline for cache update",
			zap.Uint64("fileID", file.ID),
			zap.Uint64("userID", file.UserID),
			zap.Error(execErr),
		)
	}

	logger.Info("PermanentDelete: File permanently deleted and cache invalidated", zap.Uint64("fileID", file.ID), zap.Uint64("userID", file.UserID))
	return nil
}

// GetChildrenByPathPrefix 获取所有以给定路径前缀开头的子项 (用于更新 Path 字段) (未使用)
func (r *fileRepository) FindChildrenByPathPrefix(userID uint64, pathPrefix string) ([]models.File, error) {
	var files []models.File
	err := r.db.Where("user_id = ? AND path LIKE ?", userID, pathPrefix+"%").Find(&files).Error
	if err != nil {
		return nil, err
	}
	return files, nil
}

// UpdateFilesPathInBatch 批量更新文件的 Path 字段
// TODO 异步缓存失效
// 思路： 在批量更新数据库后，不立即同步删除 Redis 缓存，
// 而是发送一个消息到消息队列（如 RabbitMQ），由一个独立的消费者进程异步地去处理缓存失效逻辑。
func (r *fileRepository) UpdateFilesPathInBatch(tx *gorm.DB, userID uint64, oldPathPrefix, newPathPrefix string) error {
	// 使用 REPLACE SQL 函数进行字符串替换
	return tx.Model(&models.File{}).
		Where("user_id = ? AND path LIKE ?", userID, oldPathPrefix+"%").
		Update("path", gorm.Expr("REPLACE(path, ?, ?)", oldPathPrefix, newPathPrefix)).Error
}

// CountFilesInStorage 根据 OssKey 和 MD5Hash 检查数据库中是否存在除给定 fileID 之外的其他文件记录
// 返回引用该 OssKey 的文件数量 (包括自身，但不包括已逻辑删除的或正在被删除的)
// 传入 currentDeletingFileID 是为了在计算引用数时排除当前正在永久删除的文件，避免计算错误。
// 在本函数中，我们应该计算所有"正常"状态的引用。
func (r *fileRepository) CountFilesInStorage(ossKey string, md5Hash string, excludeFileID uint64) (int64, error) {
	var count int64
	// 查找所有 status = 1 (正常) 的文件记录，且 OssKey 和 MD5Hash 匹配
	// 同时排除当前正在被永久删除的文件记录本身
	err := r.db.Model(&models.File{}).
		Where("oss_key = ? AND md5_hash = ? AND status = 1 AND id != ?", ossKey, md5Hash, excludeFileID).
		Count(&count).Error
	if err != nil {
		logger.Error("Failed to count files in storage for ossKey",
			zap.String("ossKey", ossKey),
			zap.String("md5Hash", md5Hash),
			zap.Uint64("excludeFileID", excludeFileID),
			zap.Error(err))
		return 0, fmt.Errorf("failed to count files in storage: %w", err)
	}
	return count, nil
}

// 辅助函数
func (r *fileRepository) fileToMap(file *models.File) (map[string]any, error) {
	// 使用 json.Marshal 和 json.Unmarshal 是一个将 struct 转换为 map 的高效技巧
	data, err := json.Marshal(file)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	// 为确保 Redis 中存储的是可预测的格式，我们手动处理特殊类型。
	// 虽然很多客户端会自动转换，但显式处理更安全。
	if file.CreatedAt.IsZero() {
		result["created_at"] = ""
	} else {
		result["created_at"] = file.CreatedAt.Format(time.RFC3339Nano)
	}

	if file.UpdatedAt.IsZero() {
		result["updated_at"] = ""
	} else {
		result["updated_at"] = file.UpdatedAt.Format(time.RFC3339Nano)
	}

	if file.DeletedAt.Valid {
		result["deleted_at"] = file.DeletedAt.Time.Format(time.RFC3339Nano)
	} else {
		// 如果 DeletedAt 无效，json omitempty 可能会直接移除该字段
		// 确保它存在且为空字符串，以保持字段统一
		result["deleted_at"] = ""
	}

	// 对于指针类型，如果为 nil，json.Marshal 会将其变为 null。
	// 需要确保它们在 map 中，以便后续转换，或者直接在这里处理成空字符串。
	// json marshal 的默认行为通常是可接受的。

	return result, nil
}

// 将 map[string]string 映射回 models.File
// 需要处理字符串到正确类型的转换，尤其是时间类型和指针
// 采用手动转换，确保类型安全，彻底解决 unmarshal 错误。
func (r *fileRepository) mapToFile(dataMap map[string]string) (*models.File, error) {
	var file models.File

	// 定义一个解码钩子，用于将字符串转换为各种目标类型
	hook := func(f reflect.Type, t reflect.Type, data any) (any, error) {
		// 只处理从 string 到其他类型的转换
		if f.Kind() != reflect.String {
			return data, nil
		}

		// 获取源字符串
		sourceString := data.(string)

		// 如果源字符串为空，对于指针类型应为 nil，对于值类型应为其零值
		if sourceString == "" {
			if t.Kind() == reflect.Ptr {
				return nil, nil // 返回 nil 指针
			}
			// 对于非指针类型，返回其零值
			return reflect.Zero(t).Interface(), nil
		}

		// 根据目标类型进行转换
		switch t {
		case reflect.TypeOf(time.Time{}):
			return time.Parse(time.RFC3339Nano, sourceString)

		case reflect.TypeOf(gorm.DeletedAt{}):
			parsedTime, err := time.Parse(time.RFC3339Nano, sourceString)
			if err != nil {
				return nil, err
			}
			return gorm.DeletedAt{Time: parsedTime, Valid: true}, nil
		}

		// 处理所有数值类型和指针数值类型
		switch t.Kind() {
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return strconv.ParseUint(sourceString, 10, 64)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return strconv.ParseInt(sourceString, 10, 64)
		case reflect.Ptr:
			// 处理指针类型的数值，例如 *uint64
			switch t.Elem().Kind() {
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				val, err := strconv.ParseUint(sourceString, 10, 64)
				if err != nil {
					return nil, err
				}
				// 需要返回一个指向该值的指针，但类型要匹配
				// 例如，如果目标是 *uint64，我们需要返回一个 *uint64
				// 使用反射来创建正确类型的指针
				ptr := reflect.New(t.Elem())
				ptr.Elem().SetUint(val)
				return ptr.Interface(), nil
			}
		}

		// 其他类型保持默认转换行为
		return data, nil
	}

	// 配置解码器
	config := &mapstructure.DecoderConfig{
		Result:  &file,
		TagName: "json", // 使用 'json' 标签来匹配 map 的键和结构体字段
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			// 可以组合多个钩子，这里用一个就够了
			hook,
		),
		// 当 map 中的 key 在 struct 中找不到时，返回错误
		// 这有助于发现字段名不匹配的问题
		ErrorUnused: false,
	}

	decoder, err := mapstructure.NewDecoder(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create map decoder: %w", err)
	}

	// 执行解码
	if err := decoder.Decode(dataMap); err != nil {
		return nil, fmt.Errorf("failed to decode map to File struct: %w", err)
	}

	return &file, nil
}

// getFilesFromCacheList 是一个私有的辅助函数，用于从 Redis Sorted Set 缓存中获取文件 ID 列表，
// 并批量从 Hash 缓存中获取对应的文件元数据，最后反序列化为 []models.File。
// 它处理了空列表标记和缓存读取错误。
func (r *fileRepository) getFilesFromCacheList(ctx context.Context, listCacheKey string) ([]models.File, error) {
	keyExists, err := r.cache.Exists(ctx, listCacheKey)
	if err != nil {
		logger.Error("getFilesFromCacheList: Error checking key existence in cache", zap.String("listCacheKey", listCacheKey), zap.Error(err))
		return nil, fmt.Errorf("failed to check cache key existence: %w", err)
	}

	if !keyExists { // 键不存在
		logger.Debug("getFilesFromCacheList: Cache miss - list key not found (Exists returned 0)", zap.String("listCacheKey", listCacheKey))
		return nil, cache.ErrCacheMiss // 明确返回缓存未命中
	}

	// 1. 从 Redis Sorted Set 获取文件 ID 列表
	fileIDsStr, err := r.cache.ZRevRange(ctx, listCacheKey, 0, -1).Result()
	if err != nil {
		if err == redis.Nil { // 列表 Key 不存在，视为缓存未命中
			logger.Info("getFilesFromCacheList: Cache miss - list key not found", zap.String("listCacheKey", listCacheKey))
			return nil, cache.ErrCacheMiss // 返回自定义的缓存未命中错误
		}
		logger.Error("Error getting file ID list from cache", zap.String("key", listCacheKey), zap.Error(err))
		return nil, fmt.Errorf("failed to get file ID list from cache: %w", err)
	}

	if len(fileIDsStr) == 0 {
		// 这是一个存在的空 Sorted Set，检查是否有 __EMPTY_LIST__ 标记
		// 因为 ZRevRange 已经返回空，所以这里不需要再 ZRange 确认了
		// 如果 saveFilesToCacheList 写入了 __EMPTY_LIST__，ZRevRange 不会返回空切片
		// 所以走到这里，说明是存在一个空的 Sorted Set，且没有 __EMPTY_LIST__ 标记
		logger.Warn("getFilesFromCacheList: Sorted Set exists but is truly empty (no members, no __EMPTY_LIST__ marker). Treating as cache miss to force DB refresh.", zap.String("listCacheKey", listCacheKey))
		return nil, cache.ErrCacheMiss // 强制视为缓存未命中，回源
	}

	// 检查是否是空列表标记 (防止缓存穿透的特殊标记)
	if len(fileIDsStr) == 1 {
		if fileIDsStr[0] == "__EMPTY_LIST__" {
			return []models.File{}, nil // 返回空切片
		}
	}

	// 转换文件 ID 字符串为 uint64
	var fileIDs []uint64
	for _, idStr := range fileIDsStr {
		id, parseErr := strconv.ParseUint(idStr, 10, 64)
		if parseErr != nil {
			logger.Error("Failed to parse file ID from cache", zap.String("idStr", idStr), zap.Error(parseErr))
			continue // 跳过无效 ID
		}
		if id > 0 { // 确保 ID 有效
			fileIDs = append(fileIDs, id)
		}
	}

	// 如果没有有效文件ID，直接返回空列表
	if len(fileIDs) == 0 {
		return []models.File{}, nil
	}

	// 2. 批量从 Hash (file:metadata:<id>) 中获取每个文件的元数据
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

	// 处理管道结果并反序列化
	var files []models.File
	for _, fileID := range fileIDs {
		cmd := cmds[fileID]
		fileMap, getErr := cmd.Result()
		if getErr == nil && len(fileMap) > 0 {
			// 忽略空值标记的哈希
			if _, ok := fileMap["__NOT_FOUND__"]; !ok {
				file, mapErr := r.mapToFile(fileMap)
				if mapErr == nil {
					files = append(files, *file)
				} else {
					logger.Error("Failed to map file hash to struct from cache", zap.Uint64("fileID", fileID), zap.Error(mapErr))
					// 记录错误但不阻止其他文件被处理
				}
			}
		} else if getErr != nil && getErr != redis.Nil {
			logger.Error("Error getting file metadata hash for ID", zap.Uint64("fileID", fileID), zap.Error(getErr))
			// 记录错误但不阻止其他文件被处理
		}
	}

	return files, nil
}

// 用于将 []models.File 列表存储到 Redis 缓存中。
// 它将文件元数据存储为 Hash，并将文件 ID 存储到 Sorted Set。
func (r *fileRepository) saveFilesToCacheList(ctx context.Context, cacheKey string, files []models.File, scoreFunc func(file models.File) float64) error {
	pipe := r.cache.TxPipeline()

	if len(files) == 0 {
		// 如果列表为空，存一个空列表标记，防止缓存穿透
		pipe.ZAdd(ctx, cacheKey, &redis.Z{Score: 0, Member: "__EMPTY_LIST__"})
		logger.Info("successfully zadd cacheKey member:__EMPTY_LIST__")
	} else {
		var zs []*redis.Z
		for _, file := range files {
			// 存储文件元数据到 Hash
			fileMap, mapErr := r.fileToMap(&file)
			if mapErr != nil {
				logger.Error("saveFilesToCacheList: Failed to map models.File to hash for caching", zap.Uint64("fileID", file.ID), zap.Error(mapErr))
				continue // 记录错误但不阻止其他文件被缓存
			}
			metaKey := fmt.Sprintf("file:metadata:%d", file.ID)
			pipe.HMSet(ctx, metaKey, fileMap)
			pipe.Expire(ctx, metaKey, r.cacheTTL) // Hash 也要设置 TTL

			// 准备 Sorted Set 成员：使用传入的 scoreFunc 计算 Score
			zs = append(zs, &redis.Z{
				Score:  scoreFunc(file),
				Member: strconv.FormatUint(file.ID, 10),
			})
		}
		if len(zs) > 0 {
			pipe.ZAdd(ctx, cacheKey, zs...) // 添加所有文件 ID 到 Sorted Set
		}
	}
	pipe.Expire(ctx, cacheKey, r.cacheTTL) // 设置列表的 TTL

	// 执行所有管道命令
	_, execErr := pipe.Exec(ctx)
	if execErr != nil {
		logger.Error("saveFilesToCacheList: Failed to execute Redis pipeline for caching list", zap.String("key", cacheKey), zap.Error(execErr))
		return fmt.Errorf("failed to save files to cache: %w", execErr)
	}
	return nil
}

func generateFileListKey(userID uint64, parentFolderID *uint64) string {
	if parentFolderID == nil {
		return fmt.Sprintf("files:user:%d:folder:root", userID)
	}
	return fmt.Sprintf("files:user:%d:folder:%d", userID, *parentFolderID)
}

func generateDeletedFilesKey(userID uint64) string {
	return fmt.Sprintf("files:deleted:user:%d", userID)
}

func generateFileMetadataKey(fileID uint64) string {
	return fmt.Sprintf("file:metadata:%d", fileID)
}

func generateFileMD5Key(md5Hash string) string {
	return fmt.Sprintf("file:md5:%s", md5Hash)
}
