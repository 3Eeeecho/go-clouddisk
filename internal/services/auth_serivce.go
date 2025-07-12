package services

import (
	"errors"
	"fmt"
	"log"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/utils"
	"github.com/3Eeeecho/go-clouddisk/internal/repositories"
)

type AuthService interface {
	RegisterUser(username, password, email string) (*models.User, error)
	// LoginUser(username, password string) (string, error) // 占位
}

type authService struct {
	userRepo repositories.UserRepository
	// jwtService JWTService // 占位
}

// 确保authService实现了AuthService的方法
var _ AuthService = (*authService)(nil)

func NewAuthService(userRepo repositories.UserRepository) AuthService {
	return &authService{
		userRepo: userRepo,
		//jwtService: jwtService,
	}
}

func (s *authService) RegisterUser(username, password, email string) (*models.User, error) {
	//检查用户名是否存在
	existingUser, err := s.userRepo.GetUserByUsername(username)
	if err != nil {
		return nil, fmt.Errorf("failed to check username existence: %w", err)
	}
	if existingUser != nil {
		return nil, errors.New("username already exists")
	}

	//检查邮箱是否存在
	existingUser, err = s.userRepo.GetUserByEmail(email)
	if err != nil {
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

// func (s *authService)LoginUser(username, password string) (string, error){}
