package repositories

import (
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"gorm.io/gorm"
)

type FileVersionRepository interface {
	Create(fileVersion *models.FileVersion) error

	FindByID(id uint64) (*models.FileVersion, error)
	FindByFileID(fileID uint64) ([]models.FileVersion, error)
	FindLatestVersion(fileID uint64) (*models.FileVersion, error)
	FindByVersion(versionNum uint64) (*models.FileVersion, error)
	FindByVersionID(versionID string) (*models.FileVersion, error)
	FindFileVersions(fileID uint64) ([]models.FileVersion, error)

	Delete(id uint64) error
	DeleteFile(fileID uint64) error
	DeleteVersion(fileID uint64, versionID string) error
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

func (r *fileVersionRepository) FindByID(id uint64) (*models.FileVersion, error) {
	var version models.FileVersion
	err := r.db.First(&version, id).Error
	return &version, err
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

func (r *fileVersionRepository) FindByVersion(versionNum uint64) (*models.FileVersion, error) {
	var version models.FileVersion
	err := r.db.Where("version = ?", versionNum).Order("version desc").First(&version).Error
	return &version, err
}

func (r *fileVersionRepository) FindByVersionID(versionID string) (*models.FileVersion, error) {
	var version models.FileVersion
	err := r.db.Where("version_id = ?", versionID).Order("version desc").First(&version).Error
	return &version, err
}

func (r *fileVersionRepository) FindFileVersions(fileID uint64) ([]models.FileVersion, error) {
	var versions []models.FileVersion
	err := r.db.Where("file_id = ?", fileID).Find(&versions).Error
	return versions, err
}

func (r *fileVersionRepository) Delete(id uint64) error {
	return r.db.Delete(&models.FileVersion{}, id).Error
}

func (r *fileVersionRepository) DeleteFile(fileID uint64) error {
	return r.db.Where("file_id = ?", fileID).Delete(&models.FileVersion{}).Error
}

func (r *fileVersionRepository) DeleteVersion(fileID uint64, versionID string) error {
	return r.db.Where("file_id = ? AND version_id = ?", fileID, versionID).Delete(&models.FileVersion{}).Error
}
