package models

// UploadInitRequest 定义了初始化上传的请求体
type UploadInitRequest struct {
	FileName string `json:"file_name" binding:"required"`
	FileHash string `json:"file_hash" binding:"required"`
}

// UploadInitResponse 定义了初始化上传的响应体
type UploadInitResponse struct {
	FileExists bool   `json:"file_exists"`
	UploadID   string `json:"upload_id,omitempty"` // 仅在 file_exists 为 false 时返回
}

// UploadChunkRequest 定义了上传块的请求体
// 注意：实际的块数据是通过 multipart form 的文件部分传输的
type UploadChunkRequest struct {
	FileHash    string `form:"file_hash" binding:"required"`
	FileName    string `form:"file_name" binding:"required"`
	UploadID    string `form:"upload_id" binding:"required"`
	ChunkNumber int    `form:"chunk_number" binding:"required"`
	ChunkSize   int64  `form:"chunk_size" binding:"required"`
	FileSize    int64  `form:"file_size" binding:"required"`
}

// UploadCompleteRequest 定义了完成上传的请求体
type UploadCompleteRequest struct {
	FileHash       string  `json:"file_hash" binding:"required"`
	FileName       string  `json:"file_name" binding:"required"`
	UploadID       string  `json:"upload_id" binding:"required"`
	ParentFolderID *uint64 `json:"parent_folder_id"`
}

// ListPartsRequest 定义了查询已上传分块的请求体
type ListPartsRequest struct {
	UploadID string `json:"upload_id" binding:"required"`
}

// ListPartsResponse 定义了查询已上传分块的响应体
type ListPartsResponse struct {
	UploadedParts []int `json:"uploaded_parts"`
}
