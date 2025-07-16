package services

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type FileService interface {
	GetFilesByUserID(userID uint64, parentFolderID *uint64) ([]models.File, error)
	AddFile(userID uint64, originalName, mimeType string, filesize uint64, parentFolderID *uint64, fileContent io.Reader) (*models.File, error)
	CreateFolder(userID uint64, folderName string, parentFolderID *uint64) (*models.File, error)
	GetFileForDownload(userID uint64, fileID uint64) (*models.File, io.ReadCloser, error) // 获取文件信息用于下载
	SoftDeleteFile(userID uint64, fileID uint64) error
	PermanentDeleteFile(userID uint64, fileID uint64) error
	ListRecycleBinFiles(userID uint64) ([]models.File, error)
	RestoreFile(userID uint64, fileID uint64) error // 从回收站恢复文件
	// CheckFileExistenceByHash(hash string) (*models.File, error) // 根据哈希值检查文件是否存在，用于秒传
}

type fileService struct {
	fileRepo repositories.FileRepository
	userRepo repositories.UserRepository
	cfg      *config.Config
	db       *gorm.DB
}

var _ FileService = (*fileService)(nil)

// NewFileService 创建一个新的文件服务实例
func NewFileService(fileRepo repositories.FileRepository, userRepo repositories.UserRepository, cfg *config.Config, db *gorm.DB) FileService {
	return &fileService{
		fileRepo: fileRepo,
		userRepo: userRepo,
		cfg:      cfg,
		db:       db,
	}
}

// GetFilesByUserID 获取用户在指定文件夹下的文件和文件夹列表
func (s *fileService) GetFilesByUserID(userID uint64, parentFolderID *uint64) ([]models.File, error) {
	// 简单检查父文件夹是否存在（如果 parentFolderID 不为 0）
	if parentFolderID != nil {
		parentFolder, err := s.fileRepo.FindByID(*parentFolderID)
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

func (s *fileService) AddFile(userID uint64, originalName, mimeType string, filesize uint64, parentFolderID *uint64, fileContent io.Reader) (*models.File, error) {
	// 1. 检查父文件夹是否存在和权限
	if parentFolderID != nil {
		parentFolder, err := s.fileRepo.FindByID(*parentFolderID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, errors.New("parent folder not found")
			}
			return nil, fmt.Errorf("failed to check parent folder: %w", err)
		}
		if parentFolder.UserID != userID || parentFolder.IsFolder != 1 {
			return nil, errors.New("invalid parent folder or not a folder")
		}
	}

	// 2. 计算文件内容的哈希值，用于去重
	md5Hasher := md5.New()
	md5CopyBytes, err := io.Copy(md5Hasher, fileContent)
	if err != nil {
		return nil, fmt.Errorf("failed to compute file MD5 hash: %w", err)
	}
	fileMD5Hash := hex.EncodeToString(md5Hasher.Sum(nil))
	log.Printf("File %s (original size %d) MD5 calculated. Bytes read for MD5: %d", originalName, filesize, md5CopyBytes) // 添加日志

	// 重要：将 fileContent 的读取位置重置到文件开头，以便再次读取用于写入物理存储
	// 确保 fileContent 实现了 io.Seeker 接口（在 Handler 中我们使用了临时文件，它实现了）
	if seeker, ok := fileContent.(io.Seeker); ok {
		_, err := seeker.Seek(0, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to reset file reader position: %w", err)
		}
	} else {
		// 理论上不会发生，因为 Handler 中已经确保了是可 Seek 的临时文件
		return nil, errors.New("fileContent reader is not seekable, cannot re-read for storage")
	}

	// 3. 检查文件是否已存在（秒传逻辑）
	existingFileByMD5, err := s.fileRepo.FindFileByMD5Hash(fileMD5Hash)
	if err == nil && existingFileByMD5 != nil {
		// 文件内容已存在，执行秒传：直接创建新的文件记录指向旧的物理文件
		// 注意：这里的 UUID 和 OssKey/OssBucket 应该引用现有的物理文件信息
		// 我们假设 existingFileByMD5 包含了正确的 OSS 相关信息
		newFileRecord := &models.File{
			UUID:           uuid.New().String(), // 每个文件记录有自己的 UUID
			UserID:         userID,
			ParentFolderID: parentFolderID,
			FileName:       originalName, // 用户原始文件名
			IsFolder:       0,            // 0表示文件
			Size:           existingFileByMD5.Size,
			MimeType:       &mimeType,
			OssBucket:      existingFileByMD5.OssBucket, // 引用现有物理文件的 bucket
			OssKey:         existingFileByMD5.OssKey,    // 引用现有物理文件的 key
			MD5Hash:        &fileMD5Hash,
			Status:         1,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		if err := s.fileRepo.Create(newFileRecord); err != nil {
			log.Printf("Error creating new file record for existing file %s: %v", originalName, err) // 添加日志
			return nil, fmt.Errorf("failed to create new file record for existing file: %w", err)
		}
		log.Printf("Fast upload successful for file %s. New record ID: %d", originalName, newFileRecord.ID) // 添加日志
		return newFileRecord, nil                                                                           //秒传成功
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		// 查找文件 MD5 时发生其他数据库错误
		log.Printf("Error checking existing file by MD5 hash %s: %v", fileMD5Hash, err) // 添加日志
		return nil, fmt.Errorf("failed to check existing file by MD5 hash: %w", err)
	}

	// 4. 文件内容不存在，需要上传到物理存储 (MinIO/S3 或本地)
	// 生成新的 UUID 作为文件在OSS中的唯一标识
	newUUID := uuid.New().String()

	//提取原始拓展名
	extension := filepath.Ext(originalName)

	// 组合 OssKey：UUID + 扩展名 (为了防止重复哈希文件名的扩展名问题，使用 UUID 更稳定)
	// 或者，你也可以使用 MD5Hash + 扩展名：storageFileName := fileMD5Hash + extension
	// 这里我倾向于使用 UUID，因为 MD5Hash 仅仅是文件内容的哈希，而 UUID 是全局唯一标识。
	// 但如果主要目标是秒传，那么 MD5Hash 作为文件名的一部分更直观。
	// 考虑实际存储路径的唯一性，MD5Hash 加上原始扩展名是合理的选择。
	storageFileName := fileMD5Hash + extension
	ossKey := storageFileName // 将存储文件名作为 OssKey

	// 构造本地存储路径,使用 OssKey 作为本地文件名
	localPath := filepath.Join(s.cfg.Storage.LocalBasePath, storageFileName)
	log.Printf("Attempting to save physical file to: %s", localPath) // 添加日志

	// 确保存储目录存在
	err = os.MkdirAll(filepath.Dir(localPath), 0755)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	out, err := os.Create(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create file on disk at %s: %w", localPath, err)
	}
	defer out.Close()

	// 从 fileContent 读取并写入到物理文件
	writtenBytes, err := io.Copy(out, fileContent)
	log.Printf("Physical file %s written successfully. Actual bytes written: %d", localPath, writtenBytes)
	if err != nil {
		os.Remove(localPath) // 写入失败，删除不完整文件
		return nil, fmt.Errorf("failed to write file to disk: %w", err)
	}

	// 5. 将文件元数据记录到数据库
	fileRecord := &models.File{
		UUID:           newUUID,
		UserID:         userID,
		ParentFolderID: parentFolderID,
		FileName:       originalName,
		IsFolder:       0, // 0表示文件
		Size:           uint64(writtenBytes),
		MimeType:       &mimeType,               // 传入指针
		OssBucket:      &s.cfg.MinIO.BucketName, // 假设使用 MinIO 的 bucket name，即使是本地存储
		OssKey:         &ossKey,                 // 传入指针
		MD5Hash:        &fileMD5Hash,            // 传入指针
		Status:         1,                       // 正常状态
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := s.fileRepo.Create(fileRecord); err != nil {
		os.Remove(localPath) // 如果数据库记录失败，删除已经写入的物理文件
		return nil, fmt.Errorf("failed to save file record to database: %w", err)
	}

	return fileRecord, nil
}

func (s *fileService) CreateFolder(userID uint64, folderName string, parentFolderID *uint64) (*models.File, error) {
	// 1. 检查父文件夹是否存在和权限 (与 AddFile 类似)
	if parentFolderID != nil {
		parentFolder, err := s.fileRepo.FindByID(*parentFolderID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, errors.New("parent folder not found")
			}
			return nil, fmt.Errorf("failed to check parent folder: %w", err)
		}
		if parentFolder.UserID != userID || parentFolder.IsFolder != 1 {
			return nil, errors.New("invalid parent folder or not a folder")
		}
	}

	// 2. 检查同一父文件夹下是否已存在同名文件夹
	// 这是一个简单的检查，更严谨的实现可能需要查询所有子文件和文件夹的名字
	existingFiles, err := s.fileRepo.FindByUserIDAndParentFolderID(userID, parentFolderID)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing folders: %w", err)
	}
	for _, file := range existingFiles {
		if file.IsFolder == 1 && file.FileName == folderName {
			return nil, errors.New("folder with this name already exists in the current directory")
		}
	}

	// 3. 创建文件夹记录
	folder := &models.File{
		UUID:           uuid.New().String(), // 文件夹也需要一个 UUID
		UserID:         userID,
		ParentFolderID: parentFolderID,
		FileName:       folderName,
		IsFolder:       1,   // 1 表示文件夹
		Size:           0,   // 文件夹大小为 0
		MimeType:       nil, // 文件夹没有 MimeType
		OssBucket:      nil, // 文件夹没有 OssBucket
		OssKey:         nil, // 文件夹没有 OssKey
		MD5Hash:        nil, // 文件夹没有 MD5Hash
		Status:         1,   // 正常状态
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := s.fileRepo.Create(folder); err != nil {
		return nil, fmt.Errorf("failed to create folder record: %w", err)
	}

	return folder, nil
}

func (s *fileService) GetFileForDownload(userID uint64, fileID uint64) (*models.File, io.ReadCloser, error) {
	file, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("File ID %d not found in DB.", fileID) // 添加日志
			return nil, nil, errors.New("file not found")
		}
		log.Printf("Error retrieving file ID %d from DB: %v", fileID, err) // 添加日志
		return nil, nil, fmt.Errorf("failed to retrieve file from DB: %w", err)
	}

	// 权限检查：确保文件属于当前用户
	if file.UserID != userID {
		log.Printf("Access denied for file ID %d. User %d tried to access file of user %d.", file.ID, userID, file.UserID) // 添加日志
		return nil, nil, errors.New("access denied: file does not belong to user")
	}

	// 检查是否是文件 (不是文件夹)
	if file.IsFolder == 1 {
		log.Printf("Access denied for file ID %d. User %d tried to access file of user %d.", file.ID, userID, file.UserID) // 添加日志
		return nil, nil, errors.New("cannot download a folder")
	}

	// 检查文件状态是否正常 (例如，不在回收站)
	if file.Status != 1 {
		log.Printf("Attempted to download folder ID %d (Name: %s).", file.ID, file.FileName) // 添加日志
		return nil, nil, errors.New("file is not available for download")
	}

	// 从本地存储路径打开文件
	// 注意：这里我们假设 OssKey 已经包含了完整的文件名（MD5Hash + 扩展名）
	// 并且 s.cfg.Storage.LocalBasePath 已经设置正确
	localFilePath := filepath.Join(s.cfg.Storage.LocalBasePath, *file.OssKey)
	log.Printf("Opening physical file for download from: %s", localFilePath) // 添加日志
	fileReader, err := os.Open(localFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("Physical file %s not found on disk for DB record ID %d. Error: %v", localFilePath, file.ID, err) // 添加日志
			return nil, nil, errors.New("physical file not found on disk")
		}
		log.Printf("Error opening physical file %s for download: %v", localFilePath, err) // 添加日志
		return nil, nil, fmt.Errorf("failed to open physical file for download: %w", err)
	}
	log.Printf("Physical file %s opened successfully. Returning file and reader.", localFilePath) // 添加日志

	return file, fileReader, nil
}

// SoftDeleteFile 软删除文件或文件夹
func (s *fileService) SoftDeleteFile(userID uint64, fileID uint64) error {
	file, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("SoftDeleteFile: File ID %d not found for user %d.", fileID, userID)
			return errors.New("file or folder not found")
		}
		log.Printf("SoftDeleteFile: Error retrieving file ID %d for user %d: %v", fileID, userID, err)
		return fmt.Errorf("failed to retrieve file: %w", err)
	}

	if file.UserID != userID {
		log.Printf("SoftDeleteFile: Access denied for file ID %d. User %d tried to soft-delete file of user %d.", fileID, userID, file.UserID)
		return errors.New("access denied: file or folder does not belong to user")
	}

	// 检查是否已经是软删除状态
	if file.Status == 0 {
		return errors.New("file or folder is already soft-deleted")
	}

	// 获取所有需要删除的文件或文件夹及其所有子项
	filesToDelete, err := s.collectFilesForDeletion(fileID, userID)
	if err != nil {
		log.Printf("SoftDeleteFile: Failed to collect files for soft deletion for file ID %d: %v", fileID, err)
		return fmt.Errorf("failed to collect files for soft deletion: %w", err)
	}

	// 开启事务
	tx := s.db.Begin()
	if tx.Error != nil {
		log.Printf("SoftDeleteFile: Failed to begin transaction: %v", tx.Error)
		return fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}() // 确保 panic 时回滚

	// 在事务中批量更新所有待删除文件的状态和 deleted_at
	for _, fileToUpdate := range filesToDelete {
		// 确保文件属于当前用户 (已在前面检查，这里是双重保险)
		if fileToUpdate.UserID != userID {
			tx.Rollback()
			log.Printf("SoftDeleteFile: Attempted to delete file ID %d (user %d) not belonging to current user %d during batch deletion.", fileToUpdate.ID, fileToUpdate.UserID, userID)
			return errors.New("internal error: file ownership mismatch during batch deletion")
		}

		// 执行 GORM 的软删除操作，这将同时更新 status 字段为 0 和 deleted_at 字段为当前时间
		// GORM 的 Delete 方法在模型有 DeletedAt 字段时会自动进行软删除。
		// 手动设置 fileToUpdate.Status = 0，并让 GORM 的 Delete 自动处理 DeletedAt。
		err = tx.Unscoped().Model(&models.File{}).Where("id = ?", fileToUpdate.ID).Updates(map[string]interface{}{
			"status":     0,
			"deleted_at": time.Now(),
		}).Error

		if err != nil {
			tx.Rollback()
			log.Printf("SoftDeleteFile: Failed to soft-delete file ID %d in DB transaction: %v", fileToUpdate.ID, err)
			return fmt.Errorf("failed to soft-delete file in transaction: %w", err)
		}
		log.Printf("SoftDeleteFile: File ID %d soft-deleted in DB transaction.", fileToUpdate.ID)
	}

	// 提交事务
	err = tx.Commit().Error
	if err != nil {
		log.Printf("SoftDeleteFile: Failed to commit transaction: %v", err)
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("SoftDeleteFile: File/Folder ID %d and its contents soft-deleted successfully.", fileID)
	return nil
}

func (s *fileService) PermanentDeleteFile(userID uint64, fileID uint64) error {
	rootFile, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("PermanentDeleteFile: File ID %d not found for user %d.", fileID, userID)
			return errors.New("file or folder not found")
		}
		log.Printf("PermanentDeleteFile: Error retrieving file ID %d for user %d: %v", fileID, userID, err)
		return fmt.Errorf("failed to retrieve file: %w", err)
	}

	if rootFile.UserID != userID {
		log.Printf("PermanentDeleteFile: Access denied for file ID %d. User %d tried to delete file of user %d.", fileID, userID, rootFile.UserID)
		return errors.New("access denied: file or folder does not belong to user")
	}

	// 收集所有需要永久删除的文件和文件夹
	filesToPermanentlyDelete, err := s.collectFilesForDeletion(fileID, userID)
	if err != nil {
		log.Printf("PermanentDeleteFile: Failed to collect files for permanent deletion for file ID %d: %v", fileID, err)
		return fmt.Errorf("failed to collect files for permanent deletion: %w", err)
	}

	// 开启事务，确保数据库删除和物理文件删除的原子性
	// 这是一个简化的事务处理，实际可能需要更复杂的事务管理器
	tx := s.db.Begin()
	if tx.Error != nil {
		log.Printf("PermanentDeleteFile: Failed to begin transaction: %v", tx.Error)
		return fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	// 在事务中批量删除数据库记录和物理文件
	// 为了确保物理文件删除在数据库记录删除之前进行（避免数据库记录没了但物理文件没删成功的情况）
	// 我们先尝试删除所有物理文件，然后删除数据库记录。
	// 注意：这里需要考虑物理文件删除失败的回滚。
	// 为了简化，我们按顺序处理，任何一个失败就回滚。

	// 对要删除的文件进行排序，确保先删除子项的物理文件，再删除父文件夹的物理文件
	// 这样可以避免在删除子项物理文件时，父文件夹的路径已经不存在的情况
	// 但实际上 os.Remove 只需要完整路径，顺序影响不大，更多是逻辑清晰。
	// 关键是所有物理文件删除成功后再删除数据库记录，或者在循环内按序删除。

	// 更稳健的做法是：先收集所有物理文件路径，在事务外（或独立于DB删除前）尝试删除，
	// 如果所有物理文件都删除成功，再开始DB事务删除DB记录。
	// 但为了原子性，我们保持在事务内，并处理回滚。
	for i := len(filesToPermanentlyDelete) - 1; i >= 0; i-- {
		fileToPermanentlyDelete := filesToPermanentlyDelete[i]

		// 确保文件属于当前用户 (双重检查)
		if fileToPermanentlyDelete.UserID != userID {
			tx.Rollback()
			log.Printf("PermanentDeleteFile: Attempted to delete file ID %d (user %d) not belonging to current user %d during batch permanent deletion.", fileToPermanentlyDelete.ID, fileToPermanentlyDelete.UserID, userID)
			return errors.New("internal error: file ownership mismatch during batch permanent deletion")
		}

		//如果是文件且有 OssKey (即有对应的物理文件)，则删除物理文件
		if fileToPermanentlyDelete.IsFolder == 0 && fileToPermanentlyDelete.OssKey != nil && *fileToPermanentlyDelete.OssKey != "" {
			localFilePath := filepath.Join(s.cfg.Storage.LocalBasePath, *fileToPermanentlyDelete.OssKey)
			err = os.Remove(localFilePath)
			if err != nil {
				tx.Rollback()
				log.Printf("PermanentDeleteFile: Failed to delete physical file %s for record ID %d: %v", localFilePath, fileID, err)
				return fmt.Errorf("failed to delete physical file: %w", err)
			}
			log.Printf("PermanentDeleteFile: Physical file %s deleted.", localFilePath)
		}

		err = tx.Unscoped().Delete(&models.File{}, fileToPermanentlyDelete.ID).Error
		if err != nil {
			tx.Rollback()
			log.Printf("PermanentDeleteFile: Failed to delete file record ID %d from DB via transaction: %v", fileToPermanentlyDelete.ID, err)
			return fmt.Errorf("failed to delete file record: %w", err)
		}
		log.Printf("PermanentDeleteFile: File record ID %d deleted from DB via transaction.", fileToPermanentlyDelete.ID)
	}

	tx.Commit()
	if tx.Error != nil {
		log.Printf("PermanentDeleteFile: Failed to commit transaction: %v", tx.Error)
		return fmt.Errorf("failed to commit transaction: %w", tx.Error)
	}

	log.Printf("PermanentDeleteFile: File ID %d permanently deleted successfully.", fileID)
	return nil
}

func (s *fileService) ListRecycleBinFiles(userID uint64) ([]models.File, error) {
	files, err := s.fileRepo.FindDeletedFilesByUserID(userID)
	if err != nil {
		log.Printf("ListRecycleBinFiles: Failed to retrieve deleted files for user %d: %v", userID, err)
		return nil, fmt.Errorf("failed to retrieve recycle bin files: %w", err)
	}
	return files, nil
}

func (s *fileService) RestoreFile(userID uint64, fileID uint64) error {
	// 1. 获取要恢复的文件/文件夹记录
	rootFile, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("RestoreFile: File ID %d not found for user %d.", fileID, userID)
			return errors.New("file or folder not found")
		}
		log.Printf("RestoreFile: Error retrieving file ID %d for user %d: %v", fileID, userID, err)
		return fmt.Errorf("failed to retrieve file: %w", err)
	}

	// 2. 权限检查
	if rootFile.UserID != userID {
		log.Printf("RestoreFile: Access denied for file ID %d. User %d tried to restore file of user %d.", fileID, userID, rootFile.UserID)
		return errors.New("access denied: file or folder does not belong to user")
	}

	// 3. 检查文件是否在回收站中
	// 只有 deleted_at 不为空，且 status 为 0，才认为是回收站中的文件
	if !rootFile.DeletedAt.Valid || rootFile.Status != 0 {
		return errors.New("file or folder is not in the recycle bin")
	}

	// 4. 检查恢复到原始位置是否会引起命名冲突
	// 查找原始父文件夹下所有 '正常' 状态的文件，检查是否有同名文件。
	// 这里使用 FindByUserIDAndParentFolderID，它应该只返回 status=1 的文件。
	originalParentFolderID := rootFile.ParentFolderID
	originalFileName := rootFile.FileName
	proposedFileName := rootFile.FileName
	for counter := 1; ; counter++ {
		existingFiles, err := s.fileRepo.FindByUserIDAndParentFolderID(userID, originalParentFolderID)
		if err != nil {
			log.Printf("RestoreFile: Failed to check existing files in original parent folder %v for user %d: %v", originalParentFolderID, userID, err)
			return fmt.Errorf("failed to check original folder contents: %w", err)
		}

		isConflit := false
		for _, existingFile := range existingFiles {
			// 检查同名文件/文件夹，但要排除掉当前要恢复的文件本身（如果它还没被完全删除）
			// GORM 的软删除机制意味着 rootFile 实际上还在数据库中，只是被标记了。
			// FindByUserIDAndParentFolderID 不会返回已软删除的，所以这里只检查活跃的文件。
			if existingFile.FileName == proposedFileName && existingFile.IsFolder == rootFile.IsFolder {
				//冲突产生
				isConflit = true
				break
			}
		}

		// 没有冲突，找到可用名称
		if !isConflit {
			break
		}

		//存在名字冲突,生成新文件名
		// 分离文件名和扩展名 (如果存在)
		ext := filepath.Ext(originalFileName)
		nameWithoutExt := originalFileName[:len(originalFileName)-len(ext)]
		proposedFileName = fmt.Sprintf("%s(%d)%s", nameWithoutExt, counter, ext)
	}

	// 5.开启事务
	tx := s.db.Begin()
	if tx.Error != nil {
		log.Printf("RestoreFile: Failed to begin transaction: %v", tx.Error)
		return fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	// 6. 收集所有需要恢复的文件和文件夹 (包括子项)
	// collectFilesForDeletion 能够获取根文件及其所有（包括软删除的）子项
	// 注意：这里收集到的 rootFile 还是原始文件名，需要在循环里更新
	filesToRestore, err := s.collectFilesForDeletion(fileID, userID)
	if err != nil {
		tx.Rollback()
		log.Printf("RestoreFile: Failed to collect files for restoration for file ID %d: %v", fileID, err)
		return fmt.Errorf("failed to collect files for restoration: %w", err)
	}

	//批量恢复数据库记录
	for _, fileToUpdate := range filesToRestore {
		// 权限再次检查 (尽管 collectFilesForDeletion 已经过滤了)
		if fileToUpdate.UserID != userID {
			tx.Rollback()
			return errors.New("internal error: file ownership mismatch during batch restoration")
		}

		if fileToUpdate.ID == fileID {
			fileToUpdate.FileName = proposedFileName
		}

		// 恢复操作：将 status 改为 1，清空 deleted_at
		fileToUpdate.Status = 1
		fileToUpdate.DeletedAt = gorm.DeletedAt{}

		err = tx.Save(&fileToUpdate).Error
		if err != nil {
			tx.Rollback()
			log.Printf("RestoreFile: Failed to restore file record ID %d in DB transaction: %v", fileToUpdate.ID, err)
			return fmt.Errorf("failed to restore file in transaction: %w", err)
		}
		log.Printf("RestoreFile: File ID %d restored in DB transaction.", fileToUpdate.ID)
	}

	// 8. 提交事务
	err = tx.Commit().Error
	if err != nil {
		log.Printf("RestoreFile: Failed to commit transaction: %v", err)
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("RestoreFile: File/Folder ID %d restored successfully to its original location (final name: '%s').", fileID, proposedFileName)
	return nil
}

// ---helpers---
// collectFilesForDeletion 辅助函数：收集需要删除的文件和文件夹（包括其所有子项）
// 这里使用 BFS (广度优先搜索) 遍历，避免深层递归导致的栈溢出
// 返回一个切片，包含所有需要处理的 models.File 记录
func (s *fileService) collectFilesForDeletion(rootFileID uint64, userID uint64) ([]models.File, error) {
	rootFile, err := s.fileRepo.FindByID(rootFileID)
	if err != nil {
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

			// 查找当前文件夹下的所有文件和子文件夹 (包括已软删除的，因为要一起永久删除)
			var children []models.File
			// 注意：这里需要确保能够查到所有状态的子项，包括 Status 为 0 的，因为它们也应该被软删除或更新 deleted_at
			// FindByUserIDAndParentFolderID 默认只查 status=1，这里需要更灵活的查询
			childQuery := s.db.Unscoped().Where("user_id = ?", userID)
			if currentFolderID == 0 { // 根目录
				childQuery = childQuery.Where("parent_folder_id IS NULL")
			} else {
				childQuery = childQuery.Where("parent_folder_id = ?", currentFolderID)
			}

			err = childQuery.Find(&children).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				log.Printf("SoftDeleteFile: Failed to retrieve children for folder ID %d: %v", currentFolderID, err)
				return nil, fmt.Errorf("failed to retrieve folder children: %w", err)
			}

			for _, child := range children {
				if !processedIDs[child.ID] {
					filesToDelete = append(filesToDelete, child)
					processedIDs[child.ID] = true
					if child.IsFolder == 1 {
						queue = append(queue, child.ID)
					}
				}
			}
		}
	}
	return filesToDelete, nil
}
