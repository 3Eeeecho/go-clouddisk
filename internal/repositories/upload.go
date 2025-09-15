package repositories

import (
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"gorm.io/gorm"
)

// MultipartUploadRepository 定义了分片上传任务的数据库操作接口
type MultipartUploadRepository interface {
	// FindByFileHash 根据文件哈希查找进行中的上传任务
	FindByFileHash(fileHash string, userID uint64) (*models.MultipartUpload, error)
	// Create 创建一个新的分片上传任务记录
	Create(upload *models.MultipartUpload) error
	// UpdateStatus 更新指定 uploadID 的任务状态
	UpdateStatus(uploadID string, status string) error
}

type dbMultipartUploadRepository struct {
	db *gorm.DB
}

// NewDBMultipartUploadRepository 创建一个新的 MultipartUploadRepository 实例
func NewDBMultipartUploadRepository(db *gorm.DB) MultipartUploadRepository {
	return &dbMultipartUploadRepository{db: db}
}

func (r *dbMultipartUploadRepository) FindByFileHash(fileHash string, userID uint64) (*models.MultipartUpload, error) {
	var upload models.MultipartUpload
	err := r.db.Where("file_hash = ? AND user_id = ? AND status = ?", fileHash, userID, "in_progress").First(&upload).Error
	if err != nil {
		return nil, err
	}
	return &upload, nil
}

func (r *dbMultipartUploadRepository) Create(upload *models.MultipartUpload) error {
	return r.db.Create(upload).Error
}

func (r *dbMultipartUploadRepository) UpdateStatus(uploadID string, status string) error {
	return r.db.Model(&models.MultipartUpload{}).Where("upload_id = ?", uploadID).Update("status", status).Error
}
