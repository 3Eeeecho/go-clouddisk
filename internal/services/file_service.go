package services

import (
	"errors"
	"fmt"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
	"gorm.io/gorm"
)

type FileService interface {
	GetFilesByUserID(userID uint64, parentFolderID uint64) ([]models.File, error)
	// AddFile(userID uint64, filename, originalName, mimeType, storagePath, hash string, filesize uint64, parentFolderID uint64, isFolder bool) (*models.File, error)
	// DeleteFile(userID uint64, fileID uint64) error
	// GetFileForDownload(userID uint64, fileID uint64) (*models.File, error) // 获取文件信息用于下载
	// CreateFolder(userID uint64, folderName string, parentFolderID uint64) (*models.File, error)
	// CheckFileExistenceByHash(hash string) (*models.File, error) // 根据哈希值检查文件是否存在，用于秒传
}

type fileService struct {
	fileRepo repositories.FileRepository
	userRepo repositories.UserRepository
}

var _ FileService = (*fileService)(nil)

// NewFileService 创建一个新的文件服务实例
func NewFileService(fileRepo repositories.FileRepository, userRepo repositories.UserRepository) FileService {
	return &fileService{
		fileRepo: fileRepo,
		userRepo: userRepo,
	}
}

// GetFilesByUserID 获取用户在指定文件夹下的文件和文件夹列表
func (s *fileService) GetFilesByUserID(userID uint64, parentFolderID uint64) ([]models.File, error) {
	// 简单检查父文件夹是否存在（如果 parentFolderID 不为 0）
	if parentFolderID != 0 {
		parentFolder, err := s.fileRepo.FindByID(parentFolderID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, errors.New("parent folder not found")
			}
			return nil, fmt.Errorf("failed to check parent folder: %w", err)
		}
		// 确保父文件夹属于当前用户且确实是文件夹
		if parentFolder.ID != userID || parentFolder.IsFolder != 1 {
			return nil, errors.New("invalid parent folder or not a folder")
		}
	}

	files, err := s.fileRepo.FindByUserIDAndParentFolderID(userID, parentFolderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get files for user %d in folder %d: %w", userID, parentFolderID, err)
	}
	return files, nil
}
