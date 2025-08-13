package repositories

import (
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"gorm.io/gorm"
)

type FileVersionRepository interface {
	Create(fileVersion *models.FileVersion) error
	FindByFileID(fileID uint64) ([]models.FileVersion, error)
	FindLatestVersion(fileID uint64) (*models.FileVersion, error)
	FindByID(id uint64) (*models.FileVersion, error)
	Delete(id uint64) error
}

type fileVersionRepository struct {
	db *gorm.DB
}

func NewFileVersionRepository(db *gorm.DB) FileVersionRepository {
	return &fileVersionRepository{db: db}
}

func (r *fileVersionRepository) Create(fileVersion *models.FileVersion) error {
	return r.db.Create(fileVersion).Error
}

func (r *fileVersionRepository) FindByFileID(fileID uint64) ([]models.FileVersion, error) {
	var versions []models.FileVersion
	err := r.db.Where("file_id = ?", fileID).Order("version desc").Find(&versions).Error
	return versions, err
}

func (r *fileVersionRepository) FindLatestVersion(fileID uint64) (*models.FileVersion, error) {
	var version models.FileVersion
	err := r.db.Where("file_id = ?", fileID).Order("version desc").First(&version).Error
	return &version, err
}

func (r *fileVersionRepository) Delete(id uint64) error {
	return r.db.Delete(&models.FileVersion{}, id).Error
}

func (r *fileVersionRepository) FindByID(id uint64) (*models.FileVersion, error) {
	var version models.FileVersion
	err := r.db.First(&version, id).Error
	return &version, err
}
