package repositories

import (
	"errors"
	"log"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"gorm.io/gorm"
)

// FileRepository 定义文件数据访问层接口
type FileRepository interface {
	Create(file *models.File) error
	FindByID(id uint64) (*models.File, error)
	FindByUserID(userID uint64) ([]models.File, error)                                  // 获取用户所有文件
	FindByUserIDAndParentFolderID(userID, parentFolderID uint64) ([]models.File, error) // 获取指定文件夹下的文件
	FindByPath(path string) (*models.File, error)                                       // 根据存储路径查找文件
	Update(file *models.File) error
	Delete(id uint64) error          // 软删除文件
	PermanentDelete(id uint64) error // 永久删除文件
	// 可能还需要其他方法，例如：
	// FindFileByHash(hash string) (*models.File, error)
	// FindFileByOriginalName(userID uint64, originalName string) (*models.File, error)
}

type fileRepository struct {
	db *gorm.DB
}

var _ FileRepository = (*fileRepository)(nil)

// NewFileRepository 创建一个新的 FileRepository 实例
func NewFileRepository(db *gorm.DB) FileRepository {
	return &fileRepository{db: db}
}

func (r *fileRepository) Create(file *models.File) error {
	if err := r.db.Create(file).Error; err != nil {
		log.Printf("Error creating file: %v", err)
		return err
	}
	return nil
}
func (r *fileRepository) FindByID(id uint64) (*models.File, error) {
	var file models.File
	err := r.db.First(&file, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, gorm.ErrRecordNotFound
		}
		log.Printf("Error finding file by ID %d: %v", id, err)
		return nil, err
	}
	return &file, nil
}

// 获取用户所有文件 (不包括文件夹，或者可以根据IsFolder过滤)
func (r *fileRepository) FindByUserID(userID uint64) ([]models.File, error) {
	var files []models.File
	// 查询用户所有文件和文件夹，按创建时间排序，不包括已删除的
	err := r.db.Where("user_id = ?", userID).Order("created_at desc").Find(&files).Error
	if err != nil {
		log.Printf("Error finding files for user %d: %v", userID, err)
		return nil, err
	}
	return files, nil
}

// 获取指定用户在特定父文件夹下的文件和文件夹
func (r *fileRepository) FindByUserIDAndParentFolderID(userID, parentFolderID uint64) ([]models.File, error) {
	var files []models.File
	err := r.db.Where("user_id = ? AND parent_folder_id = ?", userID, parentFolderID).Order("is_folder desc, name asc").Find(&files).Error
	if err != nil {
		log.Printf("Error finding files for user %d in folder %d: %v", userID, parentFolderID, err)
		return nil, err
	}
	return files, nil
}

// 根据存储路径查找文件
func (r *fileRepository) FindByPath(path string) (*models.File, error) {
	var file models.File
	err := r.db.Where("storage_path = ?", path).First(&file).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, gorm.ErrRecordNotFound
		}
		log.Printf("Error finding file by path %s: %v", path, err)
		return nil, err
	}
	return &file, nil
}
func (r *fileRepository) Update(file *models.File) error {
	if err := r.db.Save(file).Error; err != nil {
		log.Printf("Error updating file %d: %v", file.ID, err)
		return err
	}
	return nil
}

// 软删除文件
func (r *fileRepository) Delete(id uint64) error {
	// GORM 的 Delete 方法在模型中包含 gorm.DeletedAt 时，默认执行软删除
	if err := r.db.Delete(&models.File{}, id).Error; err != nil {
		log.Printf("Error soft-deleting file %d: %v", id, err)
		return err
	}
	return nil
}

// 永久删除文件
func (r *fileRepository) PermanentDelete(id uint64) error {
	if err := r.db.Unscoped().Delete(&models.File{}, id).Error; err != nil {
		log.Printf("Error permanent-deleting file %d: %v", id, err)
		return err
	}
	return nil
}
