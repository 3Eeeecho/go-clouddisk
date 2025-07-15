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
	// CheckFileExistenceByHash(hash string) (*models.File, error) // 根据哈希值检查文件是否存在，用于秒传
}

type fileService struct {
	fileRepo repositories.FileRepository
	userRepo repositories.UserRepository
	cfg      *config.Config
}

var _ FileService = (*fileService)(nil)

// NewFileService 创建一个新的文件服务实例
func NewFileService(fileRepo repositories.FileRepository, userRepo repositories.UserRepository, cfg *config.Config) FileService {
	return &fileService{
		fileRepo: fileRepo,
		userRepo: userRepo,
		cfg:      cfg,
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
			return errors.New("file or folder not found")
		}
		return fmt.Errorf("failed to retrieve file from DB: %w", err)
	}

	if file.UserID != userID {
		return errors.New("access denied: file or folder does not belong to user")
	}

	//执行软删除
	err = s.fileRepo.Delete(fileID)
	if err != nil {
		return fmt.Errorf("failed to soft delete file or folder: %w", err)
	}

	return nil
}
