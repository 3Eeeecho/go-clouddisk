package repositories

import (
	"log"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"gorm.io/gorm"
)

type UserRepository interface {
	CreateUser(user *models.User) error
	GetUserByUsername(username string) (*models.User, error)
	GetUserByEmail(email string) (*models.User, error)
	GetUserByID(id uint64) (*models.User, error) // 新增根据ID获取用户
	UpdateUser(user *models.User) error          // 新增更新用户
}

type userRepository struct {
	db *gorm.DB
}

var _ UserRepository = (*userRepository)(nil)

// NewUserRepository 创建一个新的 UserRepository 实例
func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepository{db: db}
}

func (r *userRepository) CreateUser(user *models.User) error {
	err := r.db.Create(user).Error
	if err != nil {
		log.Printf("Error creating user: %v", err)
		return err
	}
	return nil
}
func (r *userRepository) GetUserByUsername(username string) (*models.User, error) {
	var user models.User
	err := r.db.Where("username = ?", username).First(&user).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil // 用户不存在，返回 nil
		}
		log.Printf("Error getting user by username %s: %v", username, err)
		return nil, err
	}
	return &user, nil
}
func (r *userRepository) GetUserByEmail(email string) (*models.User, error) {
	var user models.User
	err := r.db.Where("email = ?", email).First(&user).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil // 用户不存在，返回 nil
		}
		log.Printf("Error getting user by email %s: %v", email, err)
		return nil, err
	}
	return &user, nil
}
func (r *userRepository) GetUserByID(id uint64) (*models.User, error) {
	var user models.User
	err := r.db.Where("id = ?", id).First(&user).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil // 用户不存在，返回 nil
		}
		log.Printf("Error getting user by ID %d: %v", id, err)
		return nil, err
	}
	return &user, nil
}
func (r *userRepository) UpdateUser(user *models.User) error {
	err := r.db.Save(user).Error
	if err != nil {
		log.Printf("Error updating user %d: %v", user.ID, err)
		return err
	}
	return nil
}
