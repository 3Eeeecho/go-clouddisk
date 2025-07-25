package models

import (
	"time"

	"gorm.io/gorm"
)

// User 对应 users 表
type User struct {
	ID           uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	Username     string `gorm:"type:varchar(64);unique;not null" json:"username"`
	PasswordHash string `gorm:"type:varchar(255);not null" json:"-"` // - 表示不输出到 JSON
	Email        string `gorm:"type:varchar(255);unique;not null" json:"email"`
	TotalSpace   uint64 `gorm:"type:bigint unsigned;not null;default:0" json:"total_space"`
	UsedSpace    uint64 `gorm:"type:bigint unsigned;not null;default:0" json:"used_space"`
	Status       uint8  `gorm:"type:tinyint unsigned;not null;default:1" json:"status"`

	CreatedAt time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

// TableName 指定 GORM 使用的表名
func (User) TableName() string {
	return "users"
}
