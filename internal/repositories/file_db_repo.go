package repositories

import (
	"errors"
	"fmt"
	"log"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// dbFileRepository is the implementation of FileRepository that interacts directly with the database.
type dbFileRepository struct {
	db *gorm.DB
}

// NewDBFileRepository creates a new DBFileRepository instance.
func NewDBFileRepository(db *gorm.DB) FileRepository {
	return &dbFileRepository{
		db: db,
	}
}

func (r *dbFileRepository) Create(file *models.File) error {
	err := r.db.Create(file).Error
	if err != nil {
		logger.Error("Create: Failed to create file in DB", zap.Error(err), zap.Uint64("userID", file.UserID), zap.String("fileName", file.FileName))
		return fmt.Errorf("failed to create file: %w", err)
	}
	return nil
}

func (r *dbFileRepository) FindByID(id uint64) (*models.File, error) {
	var file models.File
	err := r.db.Unscoped().First(&file, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, xerr.ErrFileNotFound // 文件未找到
		}
		return nil, fmt.Errorf("file not found: %w", err)
	}
	return &file, nil
}

func (r *dbFileRepository) FindByUserIDAndParentFolderID(userID uint64, parentFolderID *uint64) ([]models.File, error) {
	var dbFiles []models.File
	query := r.db.Where("user_id = ?", userID)

	if parentFolderID == nil {
		query = query.Where("parent_folder_id IS NULL") // 查找根目录
	} else {
		query = query.Where("parent_folder_id = ?", *parentFolderID) // 查找指定文件夹
	}

	// 优先显示文件夹，然后按文件名排序
	err := query.Order("is_folder DESC, file_name ASC").Find(&dbFiles).Error
	if err != nil {
		logger.Error("Error finding files from DB", zap.Uint64("userID", userID), zap.Any("parentFolderID", parentFolderID), zap.Error(err))
		return nil, fmt.Errorf("failed to find files: %w", err)
	}
	return dbFiles, nil
}

func (r *dbFileRepository) FindFileByMD5Hash(md5Hash string) (*models.File, error) {
	var file models.File
	err := r.db.Where("md5_hash = ? AND is_folder = 0 AND status = 1", md5Hash).First(&file).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, xerr.ErrFileNotFound // 文件未找到
		}
		log.Printf("Error finding file by MD5 hash %s: %v", md5Hash, err)
		return nil, err
	}
	return &file, nil
}

func (r *dbFileRepository) FindDeletedFilesByUserID(userID uint64) ([]models.File, error) {
	var dbFiles []models.File
	err := r.db.Unscoped().Where("user_id = ?", userID).Where("deleted_at IS NOT NULL").Order("deleted_at DESC").Find(&dbFiles).Error
	if err != nil {
		logger.Error("Error finding deleted files from DB", zap.Uint64("userID", userID), zap.Error(err))
		return nil, fmt.Errorf("查询已删除文件列表失败: %w", err)
	}
	return dbFiles, nil
}

func (r *dbFileRepository) FindByUUID(uuid string) (*models.File, error) {
	var file models.File
	err := r.db.Where("uuid = ?", uuid).First(&file).Error
	if err != nil {
		log.Printf("Error finding file by UUID %s: %v", uuid, err)
		return nil, err
	}
	return &file, nil
}

func (r *dbFileRepository) FindByOssKey(ossKey string) (*models.File, error) {
	var file models.File
	err := r.db.Where("oss_key = ?", ossKey).First(&file).Error
	if err != nil {
		log.Printf("Error finding file by OssKey %s: %v", ossKey, err)
		return nil, err
	}
	return &file, nil
}

func (r *dbFileRepository) FindByFileName(userID uint64, parentFolderID *uint64, fileName string) (*models.File, error) {
	var file models.File
	query := r.db.Where("user_id = ? AND file_name = ?", userID, fileName)
	if parentFolderID == nil {
		query = query.Where("parent_folder_id IS NULL")
	} else {
		query = query.Where("parent_folder_id = ?", *parentFolderID)
	}
	err := query.First(&file).Error
	return &file, err
}

func (r *dbFileRepository) FindByPath(path string) (*models.File, error) {
	var file models.File
	err := r.db.Where("storage_path = ?", path).First(&file).Error
	if err != nil {
		log.Printf("Error finding file by path %s: %v", path, err)
		return nil, err
	}
	return &file, nil
}

func (r *dbFileRepository) Update(file *models.File) error {
	err := r.db.Save(file).Error
	if err != nil {
		logger.Error("Update: Failed to update file in DB", zap.Error(err), zap.Uint64("fileID", file.ID), zap.Uint64("userID", file.UserID))
		return fmt.Errorf("failed to update file: %w", err)
	}
	return nil
}

func (r *dbFileRepository) SoftDelete(id uint64) error {
	return r.db.Delete(&models.File{}, id).Error
}

func (r *dbFileRepository) PermanentDelete(tx *gorm.DB, fileID uint64) error {
	err := tx.Unscoped().Delete(&models.File{}, fileID).Error
	if err != nil {
		return fmt.Errorf("failed to permanently delete file: %w", err)
	}
	return nil
}

func (r *dbFileRepository) FindChildrenByPathPrefix(userID uint64, pathPrefix string) ([]models.File, error) {
	var files []models.File
	err := r.db.Where("user_id = ? AND path LIKE ?", userID, pathPrefix+"%").Find(&files).Error
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (r *dbFileRepository) UpdateFilesPathInBatch(userID uint64, oldPathPrefix, newPathPrefix string) error {
	return r.db.Model(&models.File{}).
		Where("user_id = ? AND path LIKE ?", userID, oldPathPrefix+"%").
		Update("path", gorm.Expr("REPLACE(path, ?, ?)", oldPathPrefix, newPathPrefix)).Error
}

func (r *dbFileRepository) UpdateFileStatus(fileID uint64, status uint8) error {
	if err := r.db.Model(&models.File{}).Where("id = ?", fileID).Update("status", status).Error; err != nil {
		logger.Error("UpdateFileStatus: Failed to update file status in DB", zap.Uint64("fileID", fileID), zap.Uint8("status", status), zap.Error(err))
		return fmt.Errorf("failed to update file status: %w", err)
	}
	return nil
}

func (r *dbFileRepository) CountFilesInStorage(ossKey string, md5Hash string, excludeFileID uint64) (int64, error) {
	var count int64
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
