package admin

import (
	"errors"
	"fmt"
	"log"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/utils"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type AuthService interface {
	RegisterUser(username, password, email string) (*models.User, error)
	LoginUser(username, password string) (string, error)
}

type authService struct {
	userRepo repositories.UserRepository
	cfg      *config.Config
}

// 确保authService实现了AuthService的方法
var _ AuthService = (*authService)(nil)

func NewAuthService(userRepo repositories.UserRepository, cfg *config.Config) AuthService {
	return &authService{
		userRepo: userRepo,
		cfg:      cfg,
	}
}

func (s *authService) RegisterUser(username, password, email string) (*models.User, error) {
	//检查用户名是否存在
	existingUser, err := s.userRepo.GetUserByUsername(username)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("failed to check username existence: %w", err)
	}
	if existingUser != nil {
		return nil, errors.New("username already exists")
	}

	//检查邮箱是否存在
	existingUser, err = s.userRepo.GetUserByEmail(email)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("failed to check email existence: %w", err)
	}
	if existingUser != nil {
		return nil, errors.New("email already exists")
	}

	//哈希密码
	hashedPassword, err := utils.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	//创建用户模型
	user := &models.User{
		Username:     username,
		PasswordHash: hashedPassword,
		Email:        email,
		TotalSpace:   1073741824, // 默认给每个新用户 1GB 空间
		UsedSpace:    0,
		Status:       1,
	}

	err = s.userRepo.CreateUser(user)
	if err != nil {
		return nil, fmt.Errorf("failed to create user in database: %w", err)
	}

	log.Printf("User registered successfully: %s", user.Username)
	return user, nil
}

func (s *authService) LoginUser(identifier, password string) (string, error) {
	var user *models.User // 声明 user 为指针类型，初始化为 nil
	var err error

	// 尝试通过用户名查找用户
	user, err = s.userRepo.GetUserByUsername(identifier)
	if err != nil { // 如果用户名查找失
		if !errors.Is(err, gorm.ErrRecordNotFound) { // 如果不是 "记录未找到" 错误，而是其他数据库错误
			return "", fmt.Errorf("failed to get user by username: %w", err)
		}
		// 如果是 gorm.ErrRecordNotFound，继续尝试通过邮箱查找
		user, err = s.userRepo.GetUserByEmail(identifier)
		if err != nil { // 如果邮箱查找也失败
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return "", errors.New("user not found") // 用户名和邮箱都未找到
			}
			return "", fmt.Errorf("failed to get user by email: %w", err) // 其他邮箱查找错误
		}
	}

	//验证密码
	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	if err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return "", errors.New("invalid credentials") // 密码不匹配
		}
		return "", fmt.Errorf("failed to compare password: %w", err)
	}

	//生成JWT Token
	// 调用 utils 包中的 GenerateToken 函数来生成 JWT Token
	tokenString, err := utils.GenerateToken(
		user.ID,
		user.Username,
		user.Email,
		s.cfg.JWT.SecretKey,
		s.cfg.JWT.Issuer,
		s.cfg.JWT.ExpiresIn,
	)
	if err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}

	return tokenString, nil
}
