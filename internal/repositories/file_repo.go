package repositories

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/cache"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
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
	cache    cache.Cache
	cacheTTL time.Duration // 缓存过期时间默认为5分钟
}

// NewFileRepository 创建一个新的 FileRepository 实例
func NewFileRepository(db *gorm.DB, cache cache.Cache) FileRepository {
	return &fileRepository{
		db:       db,
		cache:    cache,
		cacheTTL: 10 * time.Minute,
	}
}

func (r *fileRepository) Create(file *models.File) error {
	err := r.db.Create(file).Error
	if err == nil {
		ctx := context.Background()
		//删除单文件缓存
		r.cache.Del(ctx,
			fmt.Sprintf("file:id:%d", file.ID),
			fmt.Sprintf("file:md5:%v", file.MD5Hash),
		)
		//删除列表缓存
		if file.ParentFolderID == nil {
			r.cache.Del(ctx, fmt.Sprintf("files:user:%d:folder:root", file.UserID)) // 父目录列表
		} else {
			r.cache.Del(ctx, fmt.Sprintf("files:user:%d:folder:%d", file.UserID, *file.ParentFolderID)) // 父目录列表
		}
	}
	return err
}

func (r *fileRepository) FindByID(id uint64) (*models.File, error) {
	ctx := context.Background()

	// 尝试从Redis缓存中获取
	cacheKey := fmt.Sprintf("file:id:%d", id)
	var file models.File
	err := r.cache.Get(ctx, cacheKey, &file)
	if err == nil {
		return &file, nil
	} else {
		logger.Error("获取文件缓存数据发生错误", zap.Uint64("id", id), zap.Error(err))
	}

	// 缓存未命中或获取失败，从数据库中加载
	err = r.db.First(&file, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 如果数据库中也不存在，缓存一个空值，防止缓存穿透
			r.cache.Set(ctx, cacheKey, nil, 1*time.Minute)
			return nil, nil // 文件未找到
		}
		return nil, fmt.Errorf("从数据库查询文件失败: %w", err)
	}

	// 将从数据库中获取的数据存入 Redis 缓存
	r.cache.Set(ctx, cacheKey, file, r.cacheTTL)

	return &file, nil
}

// 获取指定用户在特定父文件夹下的文件和文件夹
// parentFolderID 可以为 nil，表示根目录
func (r *fileRepository) FindByUserIDAndParentFolderID(userID uint64, parentFolderID *uint64) ([]models.File, error) {
	ctx := context.Background()
	// 尝试从Redis缓存中获取
	var cacheKey string
	var files []models.File
	if parentFolderID == nil {
		cacheKey = fmt.Sprintf("file:user:%d:folder:root", userID)
	} else {
		cacheKey = fmt.Sprintf("file:user:%d:folder:%d", userID, *parentFolderID)
	}

	err := r.cache.Get(ctx, cacheKey, files)
	if err == nil {
		return files, nil
	} else {
		logger.Error("获取缓存数据发生错误", zap.Uint64("id", userID), zap.Error(err))
	}

	query := r.db.Where("user_id = ?", userID)

	if parentFolderID == nil {
		query = query.Where("parent_folder_id IS NULL") // 查找根目录
	} else {
		query = query.Where("parent_folder_id = ?", *parentFolderID) // 查找指定文件夹
	}

	// 优先显示文件夹，然后按文件名排序
	err = query.Order("is_folder DESC, file_name ASC").Find(&files).Error
	if err != nil {
		log.Printf("Error finding files for user %d in folder %v: %v", userID, parentFolderID, err)
		return nil, err
	}

	// 将从数据库中获取的数据存入 Redis 缓存
	r.cache.Set(ctx, cacheKey, files, r.cacheTTL)

	return files, nil
}

// FindFileByMD5Hash 根据 MD5Hash 查找文件
func (r *fileRepository) FindFileByMD5Hash(md5Hash string) (*models.File, error) {
	ctx := context.Background()

	// 尝试从Redis缓存中获取
	var file models.File
	cacheKey := fmt.Sprintf("file:md5:%s", md5Hash)
	err := r.cache.Get(ctx, cacheKey, &file)
	if err == nil {
		return &file, nil
	} else {
		logger.Error("获取文件缓存数据发生错误", zap.String("md5", md5Hash), zap.Error(err))
	}

	// 注意：这里我们可能需要查询的是那些非文件夹且状态正常的文件的 MD5Hash
	err = r.db.Where("md5_hash = ? AND is_folder = 0 AND status = 1", md5Hash).First(&file).Error
	if err != nil {
		log.Printf("Error finding file by MD5 hash %s: %v", md5Hash, err)
		return nil, err
	}

	// 将从数据库中获取的数据存入 Redis 缓存
	r.cache.Set(ctx, cacheKey, file, r.cacheTTL)

	return &file, nil
}

func (r *fileRepository) FindDeletedFilesByUserID(userID uint64) ([]models.File, error) {
	ctx := context.Background()
	// 尝试从Redis缓存中获取
	var files []models.File

	cacheKey := fmt.Sprintf("file:deleted:user:%d:", userID)
	err := r.cache.Get(ctx, cacheKey, files)
	if err == nil {
		return files, nil
	} else {
		logger.Error("获取缓存数据发生错误", zap.Uint64("id", userID), zap.Error(err))
	}

	if err := r.db.Unscoped().Where("user_id = ?", userID).Where("deleted_at IS NOT NULL").Find(&files).Error; err != nil {
		return nil, err
	}

	// 将从数据库中获取的数据存入 Redis 缓存
	r.cache.Set(ctx, cacheKey, files, r.cacheTTL)
	return files, nil
}

// 未使用函数均没设置redis相关的逻辑,也没有再update函数中添加删除缓存逻辑
// 获取用户所有文件 (不包括文件夹，或者可以根据IsFolder过滤) (未使用)
func (r *fileRepository) FindByUserID(userID uint64) ([]models.File, error) {
	ctx := context.Background()

	// 尝试从Redis缓存中获取
	var files []models.File
	cacheKey := fmt.Sprintf("file:user_id:%d", userID)
	err := r.cache.Get(ctx, cacheKey, files)
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
	err := r.db.Save(file).Error
	if err == nil {
		ctx := context.Background()
		//删除单文件缓存
		r.cache.Del(ctx,
			fmt.Sprintf("file:id:%d", file.ID),
			fmt.Sprintf("file:md5:%v", file.MD5Hash),
		)
		//删除列表缓存
		if file.ParentFolderID == nil {
			r.cache.Del(ctx, fmt.Sprintf("files:user:%d:folder:root", file.UserID)) // 父目录列表
		} else {
			r.cache.Del(ctx, fmt.Sprintf("files:user:%d:folder:%d", file.UserID, *file.ParentFolderID)) // 父目录列表
		}
	}
	return err
}

// 软删除文件,设置DeletedAt字段
func (r *fileRepository) SoftDelete(id uint64) error {
	file, err := r.FindByID(id) // 这里 FindByID 会从缓存或DB获取，以获取文件详细信息
	if err != nil {
		return err
	}
	if file == nil {
		return errors.New("文件不存在")
	}

	// GORM 的 Delete 方法在模型中包含 gorm.DeletedAt 时，默认执行软删除
	err = r.db.Delete(&models.File{}, id).Error
	if err != nil {
		return fmt.Errorf("软删除文件失败: %w", err)
	}

	ctx := context.Background()
	//删除单文件缓存
	r.cache.Del(ctx,
		fmt.Sprintf("file:id:%d", file.ID),
		fmt.Sprintf("file:md5:%v", file.MD5Hash),
	)
	//删除列表缓存
	if file.ParentFolderID == nil {
		r.cache.Del(ctx, fmt.Sprintf("files:user:%d:folder:root", file.UserID)) // 父目录列表
	} else {
		r.cache.Del(ctx, fmt.Sprintf("files:user:%d:folder:%d", file.UserID, *file.ParentFolderID)) // 父目录列表
	}

	return nil
}

// 永久删除文件
func (r *fileRepository) PermanentDelete(id uint64) error {
	file, err := r.FindByID(id) // 获取文件信息以便删除相关缓存
	if err != nil {
		return err
	}
	if file == nil {
		return errors.New("文件不存在")
	}

	// 永久删除数据库记录
	err = r.db.Unscoped().Delete(&models.File{}, id).Error // Unscoped() 绕过软删除
	if err != nil {
		return fmt.Errorf("永久删除文件失败: %w", err)
	}

	ctx := context.Background()
	//删除单文件缓存
	r.cache.Del(ctx,
		fmt.Sprintf("file:id:%d", file.ID),
		fmt.Sprintf("file:md5:%v", file.MD5Hash),
	)
	//删除列表缓存
	if file.ParentFolderID == nil {
		r.cache.Del(ctx, fmt.Sprintf("files:user:%d:folder:root", file.UserID)) // 父目录列表
	} else {
		r.cache.Del(ctx, fmt.Sprintf("files:user:%d:folder:%d", file.UserID, *file.ParentFolderID)) // 父目录列表
	}

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
