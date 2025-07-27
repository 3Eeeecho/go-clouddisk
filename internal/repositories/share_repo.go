package repositories

import (
	"errors"
	"fmt"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"gorm.io/gorm"
)

type ShareRepository interface {
	Create(share *models.Share) error
	FindByUUID(uuid string) (*models.Share, error)
	FindByID(shareID uint64) (*models.Share, error)
	FindByFileIDAndUserID(fileID, userID uint64) (*models.Share, error)
	FindAllByUserID(userID uint64, page, pageSize int) ([]models.Share, int64, error)
	Update(share *models.Share) error
	Delete(id uint64) error // 逻辑删除分享链接
}

type shareRepository struct {
	db *gorm.DB
}

// NewShareRepository 创建新的shareRepository实例
func NewShareRepository(db *gorm.DB) ShareRepository {
	return &shareRepository{db: db}
}

// 创建新的数据库记录
func (r *shareRepository) Create(share *models.Share) error {
	return r.db.Create(share).Error
}

// 根据uuid查找记录
func (r *shareRepository) FindByUUID(uuid string) (*models.Share, error) {
	var share models.Share
	// Preload the associated File model for convenience
	err := r.db.Preload("File").Where("uuid = ? AND status = 1", uuid).First(&share).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil // Return nil, nil if not found
		}
		return nil, fmt.Errorf("查询分享链接失败: %w", err)
	}
	return &share, nil
}

func (r *shareRepository) FindByID(shareID uint64) (*models.Share, error) {
	var share models.Share
	err := r.db.Where("id = ?", shareID).First(&share).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("查询文件分享状态失败: %w", err)
	}
	return &share, nil
}

// 查找特定文件用户是否已分享
func (r *shareRepository) FindByFileIDAndUserID(fileID, userID uint64) (*models.Share, error) {
	var share models.Share
	err := r.db.Where("file_id = ? AND user_id = ? AND status = 1", fileID, userID).First(&share).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil // Not found
		}
		return nil, fmt.Errorf("查询文件分享状态失败: %w", err)
	}
	return &share, nil
}

// 查找特定用户的所有已分享记录
func (r *shareRepository) FindAllByUserID(userID uint64, page, pageSize int) ([]models.Share, int64, error) {
	var shares []models.Share
	var total int64

	offset := (page - 1) * pageSize
	query := r.db.Model(&models.Share{}).Where("user_id = ? AND status = 1", userID)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计分享总数失败: %w", err)
	}

	err := query.Order("created_at desc").Offset(offset).Limit(pageSize).Preload("File").Find(&shares).Error
	if err != nil {
		return nil, 0, fmt.Errorf("查询分享列表失败: %w", err)
	}
	return shares, total, nil
}

// 更新数据库记录
func (r *shareRepository) Update(share *models.Share) error {
	return r.db.Save(share).Error
}

// 软删除记录(设置deleted_at字段)
func (r *shareRepository) Delete(id uint64) error {
	return r.db.Delete(&models.Share{}, id).Error
}
