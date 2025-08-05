package models

import (
	"time"

	"gorm.io/gorm"
)

// UploadTask 对应 upload_tasks 表，用于管理断点续传任务
type UploadTask struct {
	ID            uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	UploadID      string         `gorm:"type:varchar(64);unique;not null;index" json:"upload_id"`           // 上传任务唯一ID
	UserID        uint64         `gorm:"not null;index" json:"user_id"`                                     // 用户ID
	Filename      string         `gorm:"type:varchar(255);not null" json:"filename"`                        // 文件名
	FileSize      int64          `gorm:"type:bigint;not null" json:"file_size"`                             // 文件总大小
	UploadedSize  int64          `gorm:"type:bigint;not null;default:0" json:"uploaded_size"`               // 已上传的字节数
	TempFilePath  string         `gorm:"type:varchar(500);not null" json:"temp_file_path"`                  // 临时文件路径
	Status        string         `gorm:"type:varchar(20);not null;default:'uploading';index" json:"status"` // uploading, completed, failed
	FinalFilePath *string        `gorm:"type:varchar(500);default:null" json:"final_file_path"`             // 最终文件路径（完成时设置）
	MD5Hash       *string        `gorm:"type:varchar(32);default:null" json:"md5_hash"`                     // 文件MD5哈希（可选，用于去重）
	MimeType      *string        `gorm:"type:varchar(128);default:null" json:"mime_type"`                   // MIME类型
	ExpiredAt     *time.Time     `gorm:"default:null;index" json:"expired_at"`                              // 过期时间（用于清理过期任务）
	CreatedAt     time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt     time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`

	// 定义 GORM 关联
	User *User `gorm:"foreignKey:UserID" json:"-"`
}

// TableName 指定 GORM 使用的表名
func (UploadTask) TableName() string {
	return "upload_tasks"
}
