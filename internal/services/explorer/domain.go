package explorer

import (
	"errors"
	"fmt"
	"strings"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// FileDomainService 文件领域服务，处理文件相关的业务逻辑
type FileDomainService interface {
	// 文件验证
	ValidateFile(userID uint64, file *models.File) error
	ValidateFolder(userID uint64, folder *models.File) error
	CheckFile(userID uint64, fileID uint64) (*models.File, error)
	CheckDirectory(userID uint64, folderID *uint64) (*models.File, error)
	CheckDeletedFile(userID uint64, fileID uint64) (*models.File, error)

	// 文件名处理
	ResolveFileNameConflict(userID uint64, parentFolderID *uint64, fileName string, currentFileID uint64, isFolder uint8) (string, error)

	// 文件收集
	CollectAllNormalFiles(userID uint64, fileID uint64) ([]models.File, error)
	CollectAllFiles(userID uint64, fileID uint64) ([]models.File, error)
	collectChildrenRecursively(userID uint64, folderID uint64) ([]models.File, error)

	// 路径处理
	GetRelativePathInZip(rootFolder *models.File, file *models.File) string
}

// FileRepository 接口，用于依赖注入
type FileRepository interface {
	FindByID(id uint64) (*models.File, error)
	FindByUserIDAndParentFolderID(userID uint64, parentFolderID *uint64) ([]models.File, error)
	FindChildrenByPathPrefix(userID uint64, pathPrefix string) ([]models.File, error)
}

type fileDomainService struct {
	fileRepo FileRepository
}

// NewFileDomainService 创建文件领域服务实例
func NewFileDomainService(fileRepo FileRepository) FileDomainService {
	return &fileDomainService{
		fileRepo: fileRepo,
	}
}

// ValidateFile 只检查文件状态和权限,不返回文件
func (s *fileDomainService) ValidateFile(userID uint64, file *models.File) error {
	if file == nil {
		return errors.New("file is nil")
	}

	if file.UserID != userID {
		logger.Warn("File access denied",
			zap.Uint64("fileID", file.ID),
			zap.Uint64("userID", userID),
			zap.Uint64("ownerID", file.UserID))
		return errors.New("access denied: file does not belong to user")
	}

	if file.Status != 1 {
		logger.Warn("File is not in normal status",
			zap.Uint64("fileID", file.ID),
			zap.Uint8("status", file.Status))
		return errors.New("file is not available")
	}

	return nil
}

// ValidateFolder 只检查目录状态和权限,不返回目录文件
func (s *fileDomainService) ValidateFolder(userID uint64, folder *models.File) error {
	if err := s.ValidateFile(userID, folder); err != nil {
		return err
	}

	if folder.IsFolder != 1 {
		return errors.New("not a directory")
	}

	return nil
}

// CheckFile 检查文件状态和权限,并返回正常状态的文件
func (s *fileDomainService) CheckFile(userID uint64, fileID uint64) (*models.File, error) {
	// 验证根文件夹是否存在且属于用户
	file, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("DownloadFolder: Folder not found in DB", zap.Uint64("folderID", file.ID))
			return nil, errors.New("folder not found")
		}
		logger.Error("DownloadFolder: Error retrieving folder from DB", zap.Uint64("folderID", file.ID), zap.Error(err))
		return nil, fmt.Errorf("failed to retrieve folder from DB: %w", err)
	}

	if file == nil {
		return nil, errors.New("file is nil")
	}

	if file.UserID != userID {
		logger.Warn("File access denied",
			zap.Uint64("fileID", file.ID),
			zap.Uint64("userID", userID),
			zap.Uint64("ownerID", file.UserID))
		return nil, errors.New("access denied: file does not belong to user")
	}

	if file.Status != 1 {
		logger.Warn("File is not in normal status",
			zap.Uint64("fileID", file.ID),
			zap.Uint8("status", file.Status))
		return nil, errors.New("file is not available")
	}

	return file, nil
}

// CheckDirectory 检查目录状态和权限,并返回正常状态的目录
func (s *fileDomainService) CheckDirectory(userID uint64, folderID *uint64) (*models.File, error) {
	//如果是根目录,无需检查直接返回
	if folderID == nil {
		return nil, nil
	}

	folder, err := s.CheckFile(userID, *folderID)
	if err != nil {
		return nil, err
	}

	if folder.IsFolder != 1 {
		return nil, errors.New("not a directory")
	}

	return folder, nil
}

// CheckDeletedFile 检查并返回已经被软删除的文件
func (s *fileDomainService) CheckDeletedFile(userID uint64, fileID uint64) (*models.File, error) {
	// 获取要恢复的文件/文件夹记录
	file, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("RestoreFile: File ID not found for user", zap.Uint64("fileID", fileID), zap.Uint64("userID", userID))
			return nil, errors.New("file or folder not found")
		}
		logger.Error("RestoreFile: Error retrieving file ID", zap.Uint64("fileID", fileID), zap.Uint64("userID", userID), zap.Error(err))
		return nil, fmt.Errorf("failed to retrieve file: %w", err)
	}

	// 权限检查
	if file.UserID != userID {
		logger.Warn("RestoreFile: Access denied for file", zap.Uint64("fileID", file.ID), zap.Uint64("userID", userID), zap.Uint64("ownerID", file.UserID))
		return nil, errors.New("access denied: file or folder does not belong to user")
	}

	// 检查文件是否在回收站中
	// 只有 deleted_at 不为空，且 status 为 0，才认为是回收站中的文件
	if !file.DeletedAt.Valid || file.Status == 1 {
		return nil, errors.New("file or folder is not in the recycle bin")
	}

	return file, nil
}

// ResolveFileNameConflict 解决文件名冲突
func (s *fileDomainService) ResolveFileNameConflict(userID uint64, parentFolderID *uint64, fileName string, currentFileID uint64, isFolder uint8) (string, error) {
	if fileName == "" {
		return "", errors.New("file name cannot be empty")
	}

	// 获取同级文件列表
	siblingFiles, err := s.fileRepo.FindByUserIDAndParentFolderID(userID, parentFolderID)
	if err != nil {
		logger.Error("Failed to get sibling files for conflict resolution",
			zap.Uint64("userID", userID),
			zap.Any("parentFolderID", parentFolderID),
			zap.Error(err))
		return "", fmt.Errorf("failed to get sibling files: %w", err)
	}

	// 检查是否存在同名文件
	baseName := fileName
	extension := ""

	// 分离文件名和扩展名
	if isFolder == 0 {
		lastDotIndex := strings.LastIndex(fileName, ".")
		if lastDotIndex > 0 {
			baseName = fileName[:lastDotIndex]
			extension = fileName[lastDotIndex:]
		}
	}

	// 检查冲突
	conflictExists := false
	for _, sibling := range siblingFiles {
		if sibling.ID != currentFileID && sibling.FileName == fileName {
			conflictExists = true
			break
		}
	}

	if !conflictExists {
		return fileName, nil
	}

	// 解决冲突：添加数字后缀
	counter := 1
	for {
		newFileName := fmt.Sprintf("%s (%d)%s", baseName, counter, extension)

		// 检查新名称是否冲突
		hasConflict := false
		for _, sibling := range siblingFiles {
			if sibling.ID != currentFileID && sibling.FileName == newFileName {
				hasConflict = true
				break
			}
		}

		if !hasConflict {
			logger.Info("File name conflict resolved",
				zap.String("originalName", fileName),
				zap.String("resolvedName", newFileName))
			return newFileName, nil
		}

		counter++
		if counter > 1000 { // 防止无限循环
			return "", errors.New("unable to resolve file name conflict")
		}
	}
}

func (s *fileDomainService) CollectAllNormalFiles(userID uint64, fileID uint64) ([]models.File, error) {
	allFiles, err := s.CollectAllFiles(userID, fileID)
	if err != nil {
		return nil, err
	}

	var normalFiles []models.File
	for _, file := range allFiles {
		// 跳过文件夹
		if file.IsFolder == 1 {
			continue
		}

		// 验证文件所有权和状态
		// ValidateFile 应该检查文件是否属于用户，以及是否被软删除
		if err := s.ValidateFile(userID, &file); err != nil {
			// 如果文件不可用，记录警告并跳过，而不是返回错误
			logger.Warn("CollectAllNormalFiles: File is not available, skipping",
				zap.Uint64("fileID", file.ID),
				zap.String("fileName", file.FileName),
				zap.Error(err))
			continue
		}

		// 如果文件通过所有检查，则添加到结果列表
		normalFiles = append(normalFiles, file)
	}
	return normalFiles, nil
}

// 优化后的收集子文件方法
// 递归地获取一个文件夹下的所有文件和子文件夹,包括文件自身
func (s *fileDomainService) CollectAllFiles(userID uint64, fileID uint64) ([]models.File, error) {
	// 验证权限并获取根文件
	rootFile, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		logger.Error("failed to get file", zap.Uint64("fileID", fileID))
		return nil, err
	}

	var allFiles []models.File
	allFiles = append(allFiles, *rootFile) // 包含根文件

	// 如果是文件夹，收集所有子项
	if rootFile.IsFolder == 1 {
		children, err := s.collectChildrenRecursively(userID, fileID)
		if err != nil {
			return nil, err
		}
		allFiles = append(allFiles, children...)
	}

	return allFiles, nil
}

// 递归收集子文件（优化版本）
func (s *fileDomainService) collectChildrenRecursively(userID uint64, folderID uint64) ([]models.File, error) {
	var allChildren []models.File
	// 使用队列进行BFS，避免深层递归
	queue := []uint64{folderID}
	processed := make(map[uint64]bool)
	processed[folderID] = true

	for len(queue) > 0 {
		currentID := queue[0]
		queue = queue[1:]

		// 获取当前文件夹的子项
		children, err := s.fileRepo.FindByUserIDAndParentFolderID(userID, &currentID)
		if err != nil {
			return nil, fmt.Errorf("failed to get children of folder %d: %w", currentID, err)
		}

		for _, child := range children {
			if !processed[child.ID] {
				allChildren = append(allChildren, child)
				processed[child.ID] = true

				// 如果是文件夹，加入队列继续处理
				if child.IsFolder == 1 {
					queue = append(queue, child.ID)
				}
			}
		}
	}

	return allChildren, nil
}

// GetRelativePathInZip 获取文件在ZIP中的相对路径
func (s *fileDomainService) GetRelativePathInZip(rootFolder *models.File, file *models.File) string {
	if rootFolder == nil || file == nil {
		return ""
	}

	// 构建根文件夹的完整路径
	rootFullPath := rootFolder.Path + rootFolder.FileName + "/"

	// 构建当前文件的完整路径
	var fileFullPath string
	if file.IsFolder == 1 {
		fileFullPath = file.Path + file.FileName + "/"
	} else {
		fileFullPath = file.Path + file.FileName
	}

	// 计算相对路径
	if strings.HasPrefix(fileFullPath, rootFullPath) {
		relativePath := fileFullPath[len(rootFullPath):]
		return relativePath
	}

	// 如果不在根文件夹下，返回完整路径
	return fileFullPath
}
