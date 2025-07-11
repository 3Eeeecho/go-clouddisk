package models

import (
	"time"
)

// ShareLink 对应 share_links 表
type ShareLink struct {
	ID               uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	FileID           uint64     `gorm:"not null" json:"file_id"`
	ShareCode        string     `gorm:"type:varchar(32);unique;not null" json:"share_code"`
	Password         *string    `gorm:"type:varchar(255);default:null" json:"-"` // - 表示不输出到 JSON
	ExpireTime       *time.Time `gorm:"default:null" json:"expire_time"`
	MaxDownloads     *uint32    `gorm:"type:int unsigned;default:null" json:"max_downloads"`
	CurrentDownloads uint32     `gorm:"type:int unsigned;not null;default:0" json:"current_downloads"`
	CreatedAt        time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt        time.Time  `gorm:"autoUpdateTime" json:"updated_at"`

	// 定义 GORM 关联，方便预加载
	File *File `gorm:"foreignKey:FileID"`
}

// TableName 指定 GORM 使用的表名
func (ShareLink) TableName() string {
	return "share_links"
}
