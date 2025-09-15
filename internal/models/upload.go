package models

import "time"

// UploadInitRequest 定义了初始化分片上传的请求体
type UploadInitRequest struct {
	FileName string `json:"fileName" binding:"required"`
	FileHash string `json:"fileHash" binding:"required"`
}

// UploadInitResponse 定义了初始化分片上传的响应体
type UploadInitResponse struct {
	FileExists    bool             `json:"fileExists"`
	UploadID      string           `json:"uploadID"`
	UploadedParts []UploadPartInfo `json:"uploadedParts"`
}

// UploadPartInfo 包含了已上传分块的信息
type UploadPartInfo struct {
	PartNumber int    `json:"partNumber"`
	ETag       string `json:"eTag"`
}

// UploadChunkRequest 定义了上传分片的请求参数（通常来自查询参数）
type UploadChunkRequest struct {
	UploadID    string `form:"uploadID" binding:"required"`
	ChunkNumber int    `form:"chunkNumber" binding:"required"`
	ChunkSize   int64  `form:"chunkSize" binding:"required"`
	FileHash    string `form:"fileHash" binding:"required"`
	FileName    string `form:"fileName" binding:"required"`
}

// UploadCompleteRequest 定义了完成分片上传的请求体
type UploadCompleteRequest struct {
	UploadID       string  `json:"uploadID" binding:"required"`
	FileHash       string  `json:"fileHash" binding:"required"`
	FileName       string  `json:"fileName" binding:"required"`
	MimeType       string  `json:"mimeType"`
	ParentFolderID *uint64 `json:"parentFolderID"`
	UploadMode     string  `json:"uploadMode"` // "version" or "rename"
}

// MultipartUpload 对应数据库中的 multipart_uploads 表，用于持久化分片上传任务
type MultipartUpload struct {
	ID         uint64 `gorm:"primarykey"`
	FileHash   string `gorm:"type:varchar(255);not null;index:idx_file_hash,unique"`
	UploadID   string `gorm:"type:varchar(255);not null"`
	ObjectName string `gorm:"type:varchar(1024);not null"`
	UserID     uint64 `gorm:"not null;index"`
	Status     string `gorm:"type:varchar(20);not null;default:'in_progress'"` // in_progress, completed, aborted
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (MultipartUpload) TableName() string {
	return "multipart_uploads"
}
