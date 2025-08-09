package models

import (
	"time"

	"gorm.io/gorm"
)

// 分片数据表
type Chunk struct {
	ID           uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	FileHash     string         `gorm:"size:32;not null;index:idx_chunk_hash_indx"`
	ChunkIndex   int            `gorm:"not null;index:idx_chunk_hash_indx"`
	UploadStatus int            `gorm:"not null;default:0"` // 0:未上传, 1:已上传
	CreatedAt    time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

// UploadInitRequest 是初始化上传请求体
type UploadInitRequest struct {
	FileName string `json:"file_name" binding:"required"`
	FileHash string `json:"file_hash" binding:"required"`
	FileSize int64  `json:"file_size" binding:"required"`
}

// UploadInitResponse 是初始化上传响应体
type UploadInitResponse struct {
	FileExists     bool  `json:"file_exists"`
	UploadedChunks []int `json:"uploaded_chunks,omitempty"`
}

// UploadChunkRequest 是分片上传请求体
type UploadChunkRequest struct {
	FileHash   string `form:"file_hash" binding:"required"`
	ChunkIndex int    `form:"chunk_index" binding:"required"`
}

// UploadCompleteRequest 是完成上传请求体
type UploadCompleteRequest struct {
	FileName       string  `json:"file_name" binding:"required"`
	FileHash       string  `json:"file_hash" binding:"required"`
	TotalChunks    int     `json:"total_chunks" binding:"required"`
	ParentFolderID *uint64 `json:"parent_folder_id" biniding:"required"`
}

// TableName 指定 GORM 使用的表名
func (Chunk) TableName() string {
	return "chunks"
}
