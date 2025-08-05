package share

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
	"github.com/3Eeeecho/go-clouddisk/internal/services/explorer"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

type ShareService interface {
	CreateShare(ctx context.Context, userID uint64, fileID uint64, password *string, expiresInMinutes *int) (*models.Share, error)
	GetShareByUUID(ctx context.Context, uuid string, providedPassword *string) (*models.Share, error)
	ListUserShares(userID uint64, page, pageSize int) ([]models.Share, int64, error)
	RevokeShare(userID uint64, shareID uint64) error
	GetSharedFileContent(ctx context.Context, share *models.Share) (io.ReadCloser, error)   // 获取分享文件的内容
	GetSharedFolderContent(ctx context.Context, share *models.Share) (io.ReadCloser, error) // 获取分享文件夹的压缩包内容
}

type shareService struct {
	shareRepo     repositories.ShareRepository
	fileRepo      repositories.FileRepository
	fileService   explorer.FileService // 引入 FileService，用于复用文件内容获取和文件夹打包逻辑
	domainService explorer.FileDomainService
	cfg           *config.Config
}

// NewShareService creates a new ShareService instance.
func NewShareService(shareRepo repositories.ShareRepository, fileRepo repositories.FileRepository, fileService explorer.FileService, domainService explorer.FileDomainService, cfg *config.Config) ShareService {
	return &shareService{
		shareRepo:     shareRepo,
		fileRepo:      fileRepo,
		fileService:   fileService,
		domainService: domainService,
		cfg:           cfg,
	}
}

func (s *shareService) CreateShare(ctx context.Context, userID uint64, fileID uint64, password *string, expiresInMinutes *int) (*models.Share, error) {
	// 1. 验证文件/文件夹是否存在且属于用户
	file, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		return nil, fmt.Errorf("文件或文件夹不存在或访问受限: %w", err)
	}
	if file.UserID != userID {
		return nil, errors.New("无权分享此文件或文件夹")
	}
	if file.Status != 1 || file.DeletedAt.Valid { // 检查文件状态，例如不在回收站
		return nil, errors.New("文件或文件夹状态异常，无法分享")
	}

	// 2. 检查是否已经为该文件创建了活动分享链接
	existingShare, err := s.shareRepo.FindByFileIDAndUserID(fileID, userID)
	if err != nil {
		return nil, fmt.Errorf("检查现有分享链接失败: %w", err)
	}
	if existingShare != nil {
		// 可以选择返回现有链接，或者报错不允许重复分享
		logger.Warn("CreateShare: 文件已存在有效分享链接",
			zap.Uint64("fileID", fileID), zap.Uint64("shareID", existingShare.ID))
		return existingShare, errors.New("此文件/文件夹已存在有效分享链接，请勿重复创建")
	}

	newShare := &models.Share{
		UUID:   uuid.New().String(), // 生成唯一的分享UUID
		UserID: userID,
		FileID: fileID,
		Status: 1, // 初始状态为激活
	}

	// 3. 处理密码
	if password != nil && *password != "" {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
		if err != nil {
			logger.Error("CreateShare: 密码哈希失败", zap.Error(err))
			return nil, fmt.Errorf("密码处理失败: %w", err)
		}
		hashedPassStr := string(hashedPassword)
		newShare.Password = &hashedPassStr
	}

	// 4. 处理过期时间
	if expiresInMinutes != nil && *expiresInMinutes > 0 {
		expiresAt := time.Now().Add(time.Duration(*expiresInMinutes) * time.Minute)
		newShare.ExpiresAt = &expiresAt
	}

	// 5. 保存分享链接到数据库
	if err := s.shareRepo.Create(newShare); err != nil {
		logger.Error("CreateShare: 创建分享链接记录失败", zap.Error(err))
		return nil, fmt.Errorf("创建分享链接失败: %w", err)
	}

	logger.Info("CreateShare: 分享链接创建成功",
		zap.Uint64("shareID", newShare.ID),
		zap.String("shareUUID", newShare.UUID),
		zap.Uint64("fileID", fileID))
	return newShare, nil
}
func (s *shareService) GetShareByUUID(ctx context.Context, uuid string, providedPassword *string) (*models.Share, error) {
	logger.Debug("GetShareByUUID called", zap.String("uuid", uuid))

	share, err := s.shareRepo.FindByUUID(uuid)
	if err != nil {
		return nil, fmt.Errorf("获取分享链接失败: %w", err)
	}
	if share == nil {
		return nil, errors.New("分享链接不存在或已失效")
	}

	// 1. 检查分享状态
	if share.Status != 1 {
		return nil, errors.New("分享链接已失效或被撤销")
	}

	// 2. 检查过期时间
	if share.ExpiresAt != nil && time.Now().After(*share.ExpiresAt) {
		// 标记为过期并更新数据库 (可选，可以异步处理或定期清理)
		share.Status = 0          // 设置为过期
		s.shareRepo.Update(share) // 尝试更新状态
		return nil, errors.New("分享链接已过期")
	}

	// 3. 检查密码
	if share.Password != nil && *share.Password != "" {
		if providedPassword == nil || *providedPassword == "" {
			return nil, errors.New("该分享链接需要密码")
		}
		if err := bcrypt.CompareHashAndPassword([]byte(*share.Password), []byte(*providedPassword)); err != nil {
			return nil, errors.New("分享密码不正确")
		}
	}

	// 增加访问次数 (可选，异步处理更佳，防止阻塞)
	go func() {
		share.AccessCount++
		if err := s.shareRepo.Update(share); err != nil {
			logger.Error("GetShareByUUID: 更新分享访问次数失败", zap.Uint64("shareID", share.ID), zap.Error(err))
		}
	}()

	logger.Info("GetShareByUUID: 分享链接访问成功", zap.Uint64("shareID", share.ID))
	return share, nil
}

func (s *shareService) ListUserShares(userID uint64, page, pageSize int) ([]models.Share, int64, error) {
	logger.Debug("ListUserShares called", zap.Uint64("userID", userID), zap.Int("page", page), zap.Int("pageSize", pageSize))
	shares, total, err := s.shareRepo.FindAllByUserID(userID, page, pageSize)
	if err != nil {
		logger.Error("ListUserShares: 查询用户分享列表失败", zap.Uint64("userID", userID), zap.Error(err))
		return nil, 0, fmt.Errorf("查询分享列表失败: %w", err)
	}
	return shares, total, nil
}

func (s *shareService) RevokeShare(userID uint64, shareID uint64) error {
	logger.Debug("RevokeShare called", zap.Uint64("userID", userID), zap.Uint64("shareID", shareID))

	share, err := s.shareRepo.FindByID(shareID) // 需要 shareRepo.FindByID 方法
	if err != nil {
		return fmt.Errorf("获取分享链接失败: %w", err)
	}
	if share == nil {
		return errors.New("分享链接不存在")
	}
	if share.UserID != userID {
		return errors.New("无权撤销此分享链接")
	}
	if share.Status == 0 {
		return errors.New("分享链接已失效或已撤销")
	}

	// 逻辑删除 (设置 DeletedAt) 并且更新 Status 为 0
	share.Status = 0
	if err := s.shareRepo.Update(share); err != nil {
		logger.Error("RevokeShare: 撤销分享链接失败", zap.Uint64("shareID", shareID), zap.Error(err))
		return fmt.Errorf("撤销分享链接失败: %w", err)
	}
	if err := s.shareRepo.Delete(shareID); err != nil {
		logger.Error("RevokeShare: 撤销分享链接失败", zap.Uint64("shareID", shareID), zap.Error(err))
		return fmt.Errorf("撤销分享链接失败: %w", err)
	}

	logger.Info("RevokeShare: 分享链接撤销成功", zap.Uint64("shareID", shareID), zap.Uint64("userID", userID))
	return nil
}

func (s *shareService) GetSharedFileContent(ctx context.Context, share *models.Share) (io.ReadCloser, error) {
	if share.File == nil {
		// This should ideally be preloaded by shareRepo.FindByUUID
		file, err := s.fileRepo.FindByID(share.FileID)
		if err != nil {
			return nil, fmt.Errorf("获取分享文件信息失败: %w", err)
		}
		share.File = file
	}

	if share.File.IsFolder == 1 {
		return nil, errors.New("分享的是文件夹，请使用文件夹下载接口")
	}

	// TODO 修复
	// // 复用 FileService 的 GetFileContentReader 方法
	// reader, err := s.domainService.GetFileContentReader(ctx, *share.File)
	// if err != nil {
	// 	logger.Error("GetSharedFileContent: 获取文件内容读取器失败",
	// 		zap.Uint64("fileID", share.File.ID), zap.String("shareUUID", share.UUID), zap.Error(err))
	// 	return nil, fmt.Errorf("获取分享文件内容失败: %w", err)
	// }
	return nil, nil
}

// 获取分享文件的内容
func (s *shareService) GetSharedFolderContent(ctx context.Context, share *models.Share) (io.ReadCloser, error) {
	if share.File == nil {
		file, err := s.fileRepo.FindByID(share.FileID)
		if err != nil {
			return nil, fmt.Errorf("获取分享文件夹信息失败: %w", err)
		}
		share.File = file
	}

	if share.File.IsFolder == 0 {
		return nil, errors.New("分享的是文件，请使用文件下载接口")
	}

	// 复用 FileService 的 DownloadFolder 逻辑，但这里我们只需要 Reader
	_, reader, err := s.fileService.Download(ctx, share.UserID, share.File.ID) // 注意这里传递 share.UserID
	if err != nil {
		logger.Error("GetSharedFolderContent: 打包分享文件夹失败",
			zap.Uint64("folderID", share.File.ID), zap.String("shareUUID", share.UUID), zap.Error(err))
		return nil, fmt.Errorf("打包分享文件夹失败: %w", err)
	}
	return reader, nil
}
