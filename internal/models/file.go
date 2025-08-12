package models

import (
	"time"

	"gorm.io/gorm"
)

const (
	StatusDeleted  = 0 // 已删除 (软删除)
	StatusNormal   = 1 // 正常
	StatusBanned   = 2 // 被禁用
	StatusDeleting = 3 // 待删除 (进入异步删除队列)
)

// File 对应 files 表
type File struct {
	ID             uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	UUID           string         `gorm:"type:varchar(36);unique;not null" json:"uuid"` // 文件在OSS中的唯一标识
	UserID         uint64         `gorm:"not null" json:"user_id"`
	ParentFolderID *uint64        `gorm:"default:null" json:"parent_folder_id"` // 父文件夹ID，根目录为 null
	FileName       string         `gorm:"type:varchar(255);not null" json:"filename"`
	Path           string         `gorm:"type:varchar(1024);not null;default:''" json:"path"`        // 逻辑路径
	IsFolder       uint8          `gorm:"type:tinyint unsigned;not null;default:0" json:"is_folder"` // 1:文件夹, 0:文件
	Size           uint64         `gorm:"type:bigint unsigned;not null;default:0" json:"size"`
	MimeType       *string        `gorm:"type:varchar(128);default:null" json:"mime_type"`
	OssBucket      *string        `gorm:"type:varchar(64);default:null" json:"oss_bucket"`
	OssKey         *string        `gorm:"type:varchar(255);default:null" json:"oss_key"`
	MD5Hash        *string        `gorm:"type:varchar(32);default:null" json:"md5_hash"`
	Status         uint8          `gorm:"type:tinyint unsigned;not null;default:1" json:"status"` // 1:正常, 0:回收站
	CreatedAt      time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`

	// 定义 GORM 关联，方便预加载
	User         *User `gorm:"foreignKey:UserID" json:"-"`
	ParentFolder *File `gorm:"foreignKey:ParentFolderID" json:"-"` // 自关联，获取父文件夹信息
}

// TableName 指定 GORM 使用的表名
func (File) TableName() string {
	return "files"
}
