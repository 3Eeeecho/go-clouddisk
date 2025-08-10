package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/utils"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type AuthService interface {
	RegisterUser(username, password, email string) (*models.User, error)
	LoginUser(username, password string) (string, error)
}

type authService struct {
	userRepo repositories.UserRepository
	jwtCfg   *config.JWTConfig
}

// 确保authService实现了AuthService的方法
var _ AuthService = (*authService)(nil)

func NewAuthService(userRepo repositories.UserRepository, cfg *config.JWTConfig) AuthService {
	return &authService{
		userRepo: userRepo,
		jwtCfg:   cfg,
	}
}

func (s *authService) RegisterUser(username, password, email string) (*models.User, error) {
	// 检查用户名是否存在
	_, err := s.userRepo.GetUserByUsername(context.Background(), username)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.Error("failed to check username existence", zap.String("username", username), zap.Error(err))
		return nil, fmt.Errorf("auth service: failed to check username: %w", xerr.ErrDatabaseError)
	}
	if err == nil {
		logger.Warn("Registration failed because user already exists", zap.String("username", username))
		return nil, fmt.Errorf("auth service: %w", xerr.ErrUserAlreadyExists)
	}

	// 检查邮箱是否存在
	_, err = s.userRepo.GetUserByEmail(context.Background(), email)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.Error("failed to check email existence", zap.String("email", email), zap.Error(err))
		return nil, fmt.Errorf("auth service: failed to check email: %w", xerr.ErrDatabaseError)
	}
	if err == nil {
		logger.Warn("Registration failed because email already exists", zap.String("email", email))
		return nil, fmt.Errorf("auth service: %w", xerr.ErrEmailAlreadyExists)
	}

	// 哈希密码
	hashedPassword, err := utils.HashPassword(password)
	if err != nil {
		logger.Error("failed to hash password", zap.Error(err))
		return nil, fmt.Errorf("auth service: failed to hash password: %w", err)
	}

	// 创建用户模型
	user := &models.User{
		Username:     username,
		PasswordHash: hashedPassword,
		Email:        email,
		TotalSpace:   1073741824, // 默认给每个新用户 1GB 空间
		UsedSpace:    0,
		Status:       1,
	}

	// 调用 Repository 层
	if err := s.userRepo.CreateUser(context.Background(), user); err != nil {
		// 在 Service 层可以判断 Repository 返回的错误类型
		// 这里假设 repository 层可能返回特定的业务错误
		if xerr.Is(err, xerr.ErrUserAlreadyExists) {
			logger.Warn("Registration failed because user already exists", zap.String("username", username))
			return nil, fmt.Errorf("auth service: %w", xerr.ErrUserAlreadyExists)
		}
		// 如果是其他错误，也包裹并传递
		logger.Error("failed to create user in database", zap.String("username", username), zap.Error(err))
		return nil, fmt.Errorf("auth service: failed to register user: %w", err)
	}

	logger.Info("User registered successfully", zap.String("username", user.Username))
	return user, nil
}

func (s *authService) LoginUser(identifier, password string) (string, error) {
	var user *models.User
	var err error

	// 尝试通过用户名或邮箱查找用户
	user, err = s.userRepo.GetUserByUsername(context.Background(), identifier)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 如果用户名未找到，尝试通过邮箱查找
		user, err = s.userRepo.GetUserByEmail(context.Background(), identifier)
	}

	// 处理查找用户过程中可能发生的错误
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warn("Login failed: user not found", zap.String("identifier", identifier))
			return "", fmt.Errorf("auth service: %w", xerr.ErrUserNotFound)
		}
		logger.Error("Login failed: error getting user", zap.String("identifier", identifier), zap.Error(err))
		return "", fmt.Errorf("auth service: failed to get user: %w", xerr.ErrDatabaseError)
	}

	// 验证密码
	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	if err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			logger.Warn("Login failed: invalid credentials", zap.String("identifier", identifier))
			return "", fmt.Errorf("auth service: %w", xerr.ErrInvalidCredentials)
		}
		logger.Error("Login failed: failed to compare password", zap.String("identifier", identifier), zap.Error(err))
		return "", fmt.Errorf("auth service: failed to compare password: %w", err)
	}

	// 生成JWT Token
	tokenString, err := utils.GenerateToken(
		user.ID,
		user.Username,
		user.Email,
		s.jwtCfg.SecretKey,
		s.jwtCfg.Issuer,
		s.jwtCfg.ExpiresIn,
	)
	if err != nil {
		logger.Error("Login failed: failed to generate token", zap.String("username", user.Username), zap.Error(err))
		return "", fmt.Errorf("auth service: failed to generate token: %w", err)
	}

	logger.Info("User logged in successfully", zap.String("username", user.Username))
	return tokenString, nil
}
