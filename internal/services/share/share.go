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

// ShareService 定义了文件分享服务需要实现的接口
type ShareService interface {
	// CreateShare 创建一个新的文件分享链接
	CreateShare(ctx context.Context, userID uint64, fileID uint64, password *string, expiresInMinutes *int) (*models.Share, error)
	// GetShareByUUID 通过分享UUID获取分享详情，并验证密码
	GetShareByUUID(ctx context.Context, uuid string, providedPassword *string) (*models.Share, error)
	// ListUserShares 列出指定用户创建的所有分享链接
	ListUserShares(userID uint64, page, pageSize int) ([]models.Share, int64, error)
	// RevokeShare 撤销一个分享链接
	RevokeShare(userID uint64, shareID uint64) error
	// GetSharedFileContent 获取分享文件的内容读取器
	GetSharedFileContent(ctx context.Context, share *models.Share) (io.ReadCloser, error)
	// GetSharedFolderContent 获取分享文件夹（打包成zip）的内容读取器
	GetSharedFolderContent(ctx context.Context, share *models.Share) (io.ReadCloser, error)
	GetSharedFilePresignedURL(ctx context.Context, share *models.Share) (string, error)
}

// shareService 是 ShareService 接口的具体实现
type shareService struct {
	shareRepo     repositories.ShareRepository // 分享数据仓库，用于数据库操作
	fileRepo      repositories.FileRepository  // 文件数据仓库
	fileService   explorer.FileService         // 文件核心服务，用于复用文件内容获取和文件夹打包逻辑
	domainService explorer.FileDomainService   // 文件领域服务，处理文件相关的业务规则
	cfg           *config.Config               // 全局配置
}

// NewShareService 创建一个新的 ShareService 实例
func NewShareService(shareRepo repositories.ShareRepository, fileRepo repositories.FileRepository, fileService explorer.FileService, domainService explorer.FileDomainService, cfg *config.Config) ShareService {
	return &shareService{
		shareRepo:     shareRepo,
		fileRepo:      fileRepo,
		fileService:   fileService,
		domainService: domainService,
		cfg:           cfg,
	}
}

// CreateShare 处理创建文件分享链接的业务逻辑
func (s *shareService) CreateShare(ctx context.Context, userID uint64, fileID uint64, password *string, expiresInMinutes *int) (*models.Share, error) {
	// 1. 验证文件或文件夹是否存在，并且是否属于当前用户
	file, err := s.fileRepo.FindByID(fileID)
	if err != nil {
		return nil, fmt.Errorf("文件或文件夹不存在或访问受限: %w", err)
	}
	if file.UserID != userID {
		return nil, errors.New("无权分享此文件或文件夹")
	}
	// 检查文件状态是否正常，例如文件不在回收站中
	if file.Status != 1 || file.DeletedAt.Valid {
		return nil, errors.New("文件或文件夹状态异常，无法分享")
	}

	// 2. 检查该文件是否已经存在一个有效的分享链接
	existingShare, err := s.shareRepo.FindByFileIDAndUserID(fileID, userID)
	if err != nil {
		return nil, fmt.Errorf("检查现有分享链接失败: %w", err)
	}
	if existingShare != nil {
		// 如果已存在，可以选择返回现有链接，或者报错不允许重复分享
		logger.Warn("CreateShare: 文件已存在有效分享链接",
			zap.Uint64("fileID", fileID), zap.Uint64("shareID", existingShare.ID))
		return existingShare, errors.New("此文件/文件夹已存在有效分享链接，请勿重复创建")
	}

	// 构造新的分享记录
	newShare := &models.Share{
		UUID:   uuid.New().String(), // 生成唯一的分享ID
		UserID: userID,
		FileID: fileID,
		Status: 1, // 初始状态为“可用”
	}

	// 3. 如果设置了密码，对密码进行哈希处理
	if password != nil && *password != "" {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
		if err != nil {
			logger.Error("CreateShare: 密码哈希失败", zap.Error(err))
			return nil, fmt.Errorf("密码处理失败: %w", err)
		}
		hashedPassStr := string(hashedPassword)
		newShare.Password = &hashedPassStr
	}

	// 4. 如果设置了过期时间，计算并设置绝对过期时间点
	if expiresInMinutes != nil && *expiresInMinutes > 0 {
		expiresAt := time.Now().Add(time.Duration(*expiresInMinutes) * time.Minute)
		newShare.ExpiresAt = &expiresAt
	}

	// 5. 将新的分享记录保存到数据库
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

// GetShareByUUID 处理获取分享详情的业务逻辑，包含权限校验
func (s *shareService) GetShareByUUID(ctx context.Context, uuid string, providedPassword *string) (*models.Share, error) {
	logger.Debug("GetShareByUUID called", zap.String("uuid", uuid))

	// 从数据库中根据UUID查找分享记录
	share, err := s.shareRepo.FindByUUID(uuid)
	if err != nil {
		return nil, fmt.Errorf("获取分享链接失败: %w", err)
	}
	if share == nil {
		return nil, errors.New("分享链接不存在或已失效")
	}

	// 1. 检查分享状态是否有效
	if share.Status != 1 {
		return nil, errors.New("分享链接已失效或被撤销")
	}

	// 2. 检查分享链接是否已过期
	if share.ExpiresAt != nil && time.Now().After(*share.ExpiresAt) {
		// 如果已过期，可以选择更新数据库中的状态（可以异步处理以优化性能）
		share.Status = 0 // 设置为过期状态
		s.shareRepo.Update(share)
		return nil, errors.New("分享链接已过期")
	}

	// 3. 如果分享链接设有密码，则校验提供的密码
	if share.Password != nil && *share.Password != "" {
		if providedPassword == nil || *providedPassword == "" {
			return nil, errors.New("该分享链接需要密码")
		}
		// 使用 bcrypt 对比哈希值和提供的密码
		if err := bcrypt.CompareHashAndPassword([]byte(*share.Password), []byte(*providedPassword)); err != nil {
			return nil, errors.New("分享密码不正确")
		}
	}

	// 4. 异步增加访问次数，避免阻塞主流程
	go func() {
		share.AccessCount++
		if err := s.shareRepo.Update(share); err != nil {
			logger.Error("GetShareByUUID: 更新分享访问次数失败", zap.Uint64("shareID", share.ID), zap.Error(err))
		}
	}()

	logger.Info("GetShareByUUID: 分享链接访问成功", zap.Uint64("shareID", share.ID))
	return share, nil
}

// ListUserShares 获取指定用户创建的所有分享链接列表（分页）
func (s *shareService) ListUserShares(userID uint64, page, pageSize int) ([]models.Share, int64, error) {
	logger.Debug("ListUserShares called", zap.Uint64("userID", userID), zap.Int("page", page), zap.Int("pageSize", pageSize))
	shares, total, err := s.shareRepo.FindAllByUserID(userID, page, pageSize)
	if err != nil {
		logger.Error("ListUserShares: 查询用户分享列表失败", zap.Uint64("userID", userID), zap.Error(err))
		return nil, 0, fmt.Errorf("查询分享列表失败: %w", err)
	}
	return shares, total, nil
}

// RevokeShare 撤销一个分享链接
func (s *shareService) RevokeShare(userID uint64, shareID uint64) error {
	logger.Debug("RevokeShare called", zap.Uint64("userID", userID), zap.Uint64("shareID", shareID))

	// 1. 查找分享链接是否存在
	share, err := s.shareRepo.FindByID(shareID)
	if err != nil {
		return fmt.Errorf("获取分享链接失败: %w", err)
	}
	if share == nil {
		return errors.New("分享链接不存在")
	}
	// 2. 验证操作者是否为分享的创建者
	if share.UserID != userID {
		return errors.New("无权撤销此分享链接")
	}
	// 3. 检查链接是否已经是失效状态
	if share.Status == 0 {
		return errors.New("分享链接已失效或已撤销")
	}

	// 4. 更新状态并进行逻辑删除
	share.Status = 0
	if err := s.shareRepo.Update(share); err != nil {
		logger.Error("RevokeShare: 更新分享链接状态失败", zap.Uint64("shareID", shareID), zap.Error(err))
		return fmt.Errorf("撤销分享链接失败: %w", err)
	}
	if err := s.shareRepo.Delete(shareID); err != nil {
		logger.Error("RevokeShare: 逻辑删除分享链接失败", zap.Uint64("shareID", shareID), zap.Error(err))
		return fmt.Errorf("撤销分享链接失败: %w", err)
	}

	logger.Info("RevokeShare: 分享链接撤销成功", zap.Uint64("shareID", shareID), zap.Uint64("userID", userID))
	return nil
}

// GetSharedFileContent 获取分享的单个文件的内容读取器
func (s *shareService) GetSharedFileContent(ctx context.Context, share *models.Share) (io.ReadCloser, error) {
	// 如果分享对象中没有文件信息，则从数据库加载
	if share.File == nil {
		file, err := s.fileRepo.FindByID(share.FileID)
		if err != nil {
			return nil, fmt.Errorf("获取分享文件信息失败: %w", err)
		}
		share.File = file
	}

	// 确认分享的是文件而不是文件夹
	if share.File.IsFolder == 1 {
		return nil, errors.New("分享的是文件夹，请使用文件夹下载接口")
	}

	// 复用 FileService 的 Download 方法来获取文件内容的读取器
	_, reader, err := s.fileService.Download(ctx, share.UserID, share.FileID)
	if err != nil {
		logger.Error("GetSharedFileContent: 获取文件内容读取器失败",
			zap.Uint64("fileID", share.File.ID), zap.String("shareUUID", share.UUID), zap.Error(err))
		return nil, fmt.Errorf("获取分享文件内容失败: %w", err)
	}
	return reader, nil
}

// GetSharedFilePresignedURL 获取分享文件的预签名URL
func (s *shareService) GetSharedFilePresignedURL(ctx context.Context, share *models.Share) (string, error) {
	// 确认分享的是文件而不是文件夹
	if share.File.IsFolder == 1 {
		return "", errors.New("分享的是文件夹，不支持生成预签名URL")
	}

	// 调用 fileService 来生成预签名URL
	// 注意：这里传递的是分享创建者 share.UserID，以确保有权限访问文件
	presignedURL, err := s.fileService.GetPresignedURLForDownload(ctx, share.UserID, share.FileID)
	if err != nil {
		logger.Error("GetSharedFilePresignedURL: 生成预签名URL失败",
			zap.Uint64("fileID", share.File.ID),
			zap.String("shareUUID", share.UUID),
			zap.Error(err))
		return "", fmt.Errorf("获取分享文件下载链接失败: %w", err)
	}

	return presignedURL, nil
}

// GetSharedFolderContent 获取分享的文件夹（打包为zip）的内容读取器
func (s *shareService) GetSharedFolderContent(ctx context.Context, share *models.Share) (io.ReadCloser, error) {
	// 如果分享对象中没有文件夹信息，则从数据库加载
	if share.File == nil {
		file, err := s.fileRepo.FindByID(share.FileID)
		if err != nil {
			return nil, fmt.Errorf("获取分享文件夹信息失败: %w", err)
		}
		share.File = file
	}

	// 确认分享的是文件夹而不是文件
	if share.File.IsFolder == 0 {
		return nil, errors.New("分享的是文件，请使用文件下载接口")
	}

	// 复用 FileService 的 Download 方法来处理文件夹打包和获取内容读取器
	// 注意：这里传递的是分享创建者 share.UserID，以确保有权限访问文件夹内容
	_, reader, err := s.fileService.Download(ctx, share.UserID, share.File.ID)
	if err != nil {
		logger.Error("GetSharedFolderContent: 打包分享文件夹失败",
			zap.Uint64("folderID", share.File.ID), zap.String("shareUUID", share.UUID), zap.Error(err))
		return nil, fmt.Errorf("打包分享文件夹失败: %w", err)
	}
	return reader, nil
}
