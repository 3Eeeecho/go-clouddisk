package repositories

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/xerr"
	"github.com/go-sql-driver/mysql"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type UserRepository interface {
	CreateUser(ctx context.Context, user *models.User) error
	GetUserByUsername(ctx context.Context, username string) (*models.User, error)
	GetUserByEmail(ctx context.Context, email string) (*models.User, error)
	GetUserByID(ctx context.Context, id uint64) (*models.User, error)
	UpdateUser(ctx context.Context, user *models.User) error
}

type userRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepository{db: db}
}

func (r *userRepository) CreateUser(ctx context.Context, user *models.User) error {
	if err := r.db.WithContext(ctx).Create(user).Error; err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			logger.Error("Failed to create user due to duplicate key", zap.String("username", user.Username), zap.String("email", user.Email), zap.Error(err))
			if strings.Contains(mysqlErr.Message, "for key 'users.username'") {
				return fmt.Errorf("user repository: %w", xerr.ErrUserAlreadyExists)
			}
			if strings.Contains(mysqlErr.Message, "for key 'users.email'") {
				return fmt.Errorf("user repository: %w", xerr.ErrEmailAlreadyExists)
			}
		}
		logger.Error("Error creating user", zap.Error(err))
		return fmt.Errorf("user repository: failed to create user: %w", err)
	}
	return nil
}

func (r *userRepository) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	var user models.User
	err := r.db.WithContext(ctx).Where("username = ?", username).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("user repository: %w", xerr.ErrUserNotFound)
		}
		logger.Error("Error getting user by username", zap.String("username", username), zap.Error(err))
		return nil, fmt.Errorf("user repository: failed to get user by username: %w", err)
	}
	return &user, nil
}

func (r *userRepository) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	var user models.User
	err := r.db.WithContext(ctx).Where("email = ?", email).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("user repository: %w", xerr.ErrUserNotFound)
		}
		logger.Error("Error getting user by email", zap.String("email", email), zap.Error(err))
		return nil, fmt.Errorf("user repository: failed to get user by email: %w", err)
	}
	return &user, nil
}

func (r *userRepository) GetUserByID(ctx context.Context, id uint64) (*models.User, error) {
	var user models.User
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("user repository: %w", xerr.ErrUserNotFound)
		}
		logger.Error("Error getting user by ID", zap.Uint64("id", id), zap.Error(err))
		return nil, fmt.Errorf("user repository: failed to get user by ID: %w", err)
	}
	return &user, nil
}

func (r *userRepository) UpdateUser(ctx context.Context, user *models.User) error {
	err := r.db.WithContext(ctx).Save(user).Error
	if err != nil {
		logger.Error("Error updating user", zap.Uint64("id", user.ID), zap.Error(err))
		return fmt.Errorf("user repository: failed to update user: %w", err)
	}
	return nil
}
