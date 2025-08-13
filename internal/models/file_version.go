package models

import (
	"time"

	"gorm.io/gorm"
)

// FileVersion 对应 file_versions 表，用于存储文件的历史版本
type FileVersion struct {
	ID        uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	FileID    uint64         `gorm:"not null;index" json:"file_id"` // 关联到 files 表的主键
	Version   uint           `gorm:"not null" json:"version"`
	Size      uint64         `gorm:"not null" json:"size"`
	OssKey    string         `gorm:"type:varchar(255);not null" json:"oss_key"`
	VersionID string         `gorm:"type:varchar(128);not null" json:"version_id"` // MinIO 返回的版本 ID
	CreatedAt time.Time      `gorm:"autoCreateTime" json:"created_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`

	File *File `gorm:"foreignKey:FileID" json:"-"`
}

// TableName 指定 GORM 使用的表名
func (FileVersion) TableName() string {
	return "file_versions"
}
