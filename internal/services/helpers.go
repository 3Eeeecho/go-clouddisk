package services

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// collectFilesForDeletion 辅助函数：收集需要删除的文件和文件夹（包括其所有子项）
// 这里使用 BFS (广度优先搜索) 遍历，避免深层递归导致的栈溢出
// 返回一个切片，包含所有需要处理的 models.File 记录
func (s *fileService) collectFilesForDeletion(rootFileID uint64, userID uint64) ([]models.File, error) {
	logger.Debug("collectFilesForDeletion called", zap.Uint64("rootFileID", rootFileID), zap.Uint64("userID", userID))
	rootFile, err := s.fileRepo.FindByID(rootFileID)
	if err != nil {
		logger.Error("collectFilesForDeletion: root file not found", zap.Uint64("rootFileID", rootFileID), zap.Error(err))
		return nil, err // 错误会在调用方处理
	}
	filesToDelete := []models.File{*rootFile} // 包含根文件/文件夹自身

	// 如果是文件夹，查找所有子项
	if rootFile.IsFolder == 1 {
		// 使用队列进行 BFS
		queue := []uint64{rootFileID}
		processedIDs := make(map[uint64]bool) // 避免重复处理
		processedIDs[rootFileID] = true

		for len(queue) > 0 {
			currentFolderID := queue[0]
			queue = queue[1:]

			logger.Debug("collectFilesForDeletion: processing folder", zap.Uint64("currentFolderID", currentFolderID))
			var children []models.File
			// 注意：这里需要确保能够查到所有状态的子项，包括 Status 为 0 的，因为它们也应该被软删除或更新 deleted_at
			// FindByUserIDAndParentFolderID 默认只查 status=1，这里需要更灵活的查询
			childQuery := s.db.Unscoped().Where("user_id = ?", userID)
			if currentFolderID == 0 {
				childQuery = childQuery.Where("parent_folder_id IS NULL")
			} else {
				childQuery = childQuery.Where("parent_folder_id = ?", currentFolderID)
			}

			err = childQuery.Find(&children).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				logger.Error("collectFilesForDeletion: failed to retrieve children", zap.Uint64("folderID", currentFolderID), zap.Error(err))
				return nil, fmt.Errorf("failed to retrieve folder children: %w", err)
			}

			for _, child := range children {
				if !processedIDs[child.ID] {
					logger.Debug("collectFilesForDeletion: found child", zap.Uint64("childID", child.ID), zap.String("childName", child.FileName))
					filesToDelete = append(filesToDelete, child)
					processedIDs[child.ID] = true
					if child.IsFolder == 1 {
						queue = append(queue, child.ID)
					}
				}
			}
		}
	}
	logger.Info("collectFilesForDeletion finished", zap.Int("totalFiles", len(filesToDelete)), zap.Uint64("rootFileID", rootFileID))
	return filesToDelete, nil
}

// resolveFileNameConflict 检查指定父文件夹下是否有命名冲突，并返回一个不冲突的文件名。
// 如果存在冲突，它会自动在文件名后添加 (1), (2) 等后缀。
// currentFileID: 当前正在操作的文件/文件夹的 ID。在重命名自身时，需要排除它与自身名称的冲突检查。
// isFolder: 指示待检查的项是文件夹 (1) 还是文件 (0)。
func (s *fileService) resolveFileNameConflict(userID uint64, parentFolderID *uint64, originalProposedName string, currentFileID uint64, isFolder uint8) (string, error) {
	logger.Debug("resolveFileNameConflict called", zap.Uint64("userID", userID), zap.Any("parentFolderID", parentFolderID), zap.String("originalProposedName", originalProposedName), zap.Uint64("currentFileID", currentFileID), zap.Uint8("isFolder", isFolder))
	existingFiles, err := s.fileRepo.FindByUserIDAndParentFolderID(userID, parentFolderID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.Error("resolveFileNameConflict: Failed to check existing files for naming conflict",
			zap.Uint64("userID", userID),
			zap.Any("parentFolderID", parentFolderID),
			zap.Error(err))
		return "", fmt.Errorf("failed to check folder contents for naming conflict: %w", err)
	}

	proposedFileName := originalProposedName
	for counter := 1; ; counter++ {
		isConflict := false
		for _, existingFile := range existingFiles {
			// 冲突判断条件：
			// 1. 文件名相同 (proposedFileName)
			// 2. 文件类型相同 (都是文件或都是文件夹)
			// 3. 不是当前正在操作的文件/文件夹本身 (如果 currentFileID 为 0 表示是新创建，则无需排除)
			if existingFile.FileName == proposedFileName &&
				existingFile.IsFolder == isFolder &&
				(currentFileID == 0 || existingFile.ID != currentFileID) {
				isConflict = true
				break
			}
		}
		if !isConflict {
			break // 没有冲突，找到可用名称
		}

		// 存在冲突，生成新文件名
		ext := filepath.Ext(originalProposedName) // 总是基于原始提议的名称提取扩展名
		nameWithoutExt := originalProposedName[:len(originalProposedName)-len(ext)]
		proposedFileName = fmt.Sprintf("%s(%d)%s", nameWithoutExt, counter, ext)
		logger.Debug("resolveFileNameConflict: conflict found, trying new name", zap.String("proposedFileName", proposedFileName))
	}

	// 如果文件名被修改了，记录日志
	if proposedFileName != originalProposedName {
		logger.Info("FileNameConflictResolved",
			zap.String("originalProposedName", originalProposedName),
			zap.String("finalName", proposedFileName),
			zap.Uint64("userID", userID),
			zap.Any("parentFolderID", parentFolderID))
	}
	logger.Debug("resolveFileNameConflict finished", zap.String("finalName", proposedFileName))
	return proposedFileName, nil
}

// 检查目标文件是否处于正常的可操作状态
func (s *fileService) checkFile(fileToCheck *models.File) error {
	if fileToCheck == nil {
		return xerr.ErrFileNotFound
	}

	var existingFile models.File
	if err := s.db.Where("id = ?", fileToCheck.ID).First(&existingFile).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			logger.Warn("file not exist", zap.Uint64("file_id", fileToCheck.ID))
			return fmt.Errorf("%w: ID %d", xerr.ErrFileNotFound, fileToCheck.ID) // 使用 errors.Wrap
		}
		return fmt.Errorf("database query error for file %d: %w", fileToCheck.ID, err)
	}

	if fileToCheck.DeletedAt.Valid {
		return fmt.Errorf("%w: file %d is deleted", xerr.ErrFileUnavailable, fileToCheck.ID)
	}

	return nil
}

// CheckDirectory 是一个辅助函数，用于检查一个文件是否是有效的目录
func (s *fileService) checkDirectory(dirToCheck *models.File) error {
	if err := s.checkFile(dirToCheck); err != nil {
		return err // 如果文件本身不可用，直接返回
	}
	// 根据你的 models.File.IsFolder 定义：1 为文件夹
	if dirToCheck.IsFolder == 0 { // Changed from !dirToCheck.IsFolder to dirToCheck.IsFolder == 0
		return fmt.Errorf("file %d is not a directory", dirToCheck.ID)
	}
	return nil
}

func (s *fileService) ValidateParentFolder(userID uint64, parentFolderID *uint64) (*models.File, error) {
	if parentFolderID == nil {
		// 如果 parentFolderID 为 nil，表示是根目录，根目录无需验证（或在其他地方处理根目录逻辑）
		// 在这种情况下，可以返回nil，表示没有特定的父文件夹对象需要返回，但验证通过
		return nil, nil
	}

	parentFolder, err := s.fileRepo.FindByID(*parentFolderID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("ValidateParentFolder: Parent folder not found",
				zap.Uint64("parentFolderID", *parentFolderID),
				zap.Error(err))
			return nil, errors.New("parent folder not found")
		}
		logger.Error("ValidateParentFolder: Failed to check parent folder",
			zap.Uint64("parentFolderID", *parentFolderID),
			zap.Error(err))
		return nil, fmt.Errorf("failed to check parent folder: %w", err)
	}

	// 确保父文件夹属于当前用户
	if parentFolder.UserID != userID {
		logger.Warn("ValidateParentFolder: Access denied - Parent folder does not belong to user",
			zap.Uint64("attemptedUserID", userID),
			zap.Uint64("parentFolderID", parentFolder.ID),
			zap.Uint64("parentFolderOwnerID", parentFolder.UserID))
		return nil, errors.New("access denied: parent folder does not belong to you")
	}

	// 确保目标是文件夹
	if parentFolder.IsFolder != 1 {
		logger.Warn("ValidateParentFolder: Invalid parent - Not a folder",
			zap.Uint64("parentFolderID", parentFolder.ID),
			zap.Uint8("isFolderValue", parentFolder.IsFolder))
		return nil, errors.New("invalid parent: target ID is not a folder")
	}

	return parentFolder, nil
}
