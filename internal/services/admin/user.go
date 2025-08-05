package admin

import (
	"errors"
	"fmt"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
	"go.uber.org/zap"
)

type UserService interface {
	GetUserProfile(userID uint64) (*models.User, error)
}

type userService struct {
	userRepo repositories.UserRepository
}

var _ UserService = (*userService)(nil)

func NewUserService(userRepo repositories.UserRepository) UserService {
	return &userService{userRepo: userRepo}
}

func (s *userService) GetUserProfile(userID uint64) (*models.User, error) {
	user, err := s.userRepo.GetUserByID(userID)
	if err != nil {
		logger.Error("GetUserProfile: Error retrieving user from DB",
			zap.Uint64("userID", userID),
			zap.Error(err))
		return nil, fmt.Errorf("failed to retrieve user profile: %w", err)
	}
	if user == nil { // userRepo.FindByID returns nil, nil if not found
		logger.Warn("GetUserProfile: User not found", zap.Uint64("userID", userID))
		return nil, errors.New("user not found")
	}

	// 可以在这里添加额外的业务逻辑，例如：
	// - 过滤掉敏感信息 (密码等 GORM tag 已经处理了)
	// - 组合其他数据 (例如用户空间使用情况)

	logger.Info("GetUserProfile: User profile retrieved successfully", zap.Uint64("userID", userID))
	return user, nil
}
