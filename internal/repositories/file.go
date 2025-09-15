package repositories

import (
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"gorm.io/gorm"
)

// FileRepository defines the interface for file data access.
type FileRepository interface {
	Create(file *models.File) error
	FindByID(id uint64) (*models.File, error)
	FindByUserIDAndParentFolderID(userID uint64, parentFolderID *uint64) ([]models.File, error)
	FindByPath(path string) (*models.File, error)
	FindByUUID(uuid string) (*models.File, error)
	FindByOssKey(ossKey string) (*models.File, error)
	FindByFileName(userID uint64, parentFolderID *uint64, fileName string) (*models.File, error)
	FindFileByMD5Hash(md5Hash string) (*models.File, error)
	FindDeletedFilesByUserID(userID uint64) ([]models.File, error)
	FindChildrenByPathPrefix(userID uint64, pathPrefix string) ([]models.File, error)
	CountFilesInStorage(ossKey string, md5Hash string, excludeFileID uint64) (int64, error)
	UpdateFilesPathInBatch(userID uint64, oldPathPrefix, newPathPrefix string) error
	Update(file *models.File) error
	SoftDelete(id uint64) error
	PermanentDelete(tx *gorm.DB, fileID uint64) error
	UpdateFileStatus(fileID uint64, status uint8) error
}
