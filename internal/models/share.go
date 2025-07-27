package models

import (
	"time"

	"gorm.io/gorm"
)

type Share struct {
	ID          uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	UUID        string         `gorm:"type:varchar(36);not null;uniqueIndex" json:"uuid"` // 唯一分享ID，用于生成链接
	UserID      uint64         `gorm:"not null;index" json:"user_id"`                     // 分享者ID
	FileID      uint64         `gorm:"not null;index" json:"file_id"`                     // 被分享的文件或文件夹ID
	Password    *string        `gorm:"type:varchar(255)" json:"password,omitempty"`       // 可选：分享密码的哈希值
	ExpiresAt   *time.Time     `json:"expires_at,omitempty"`                              // 可选：分享链接过期时间
	AccessCount int64          `gorm:"default:0" json:"access_count"`                     // 访问次数（可选）
	Status      int            `gorm:"type:tinyint;default:1" json:"status"`              // 1: 可用, 0: 被取消/过期
	CreatedAt   time.Time      `gorm:"not null" json:"created_at"`
	UpdatedAt   time.Time      `gorm:"not null" json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`

	// 关系File模型预加载
	File *File `gorm:"foreignKey:FileID"` // 关联到文件模型，方便查询文件详情
}

// 指定gorm的表名
func (Share) TableName() string {
	return "shares"
}
