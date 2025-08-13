package models

// DeleteFileTask 定义了要发布到 RabbitMQ 的文件删除任务的消息体
type DeleteFileTask struct {
	FileID    uint64 `json:"file_id"`
	UserID    uint64 `json:"user_id"`
	OssKey    string `json:"oss_key"`
	VersionID string `json:"version_id,omitempty"`
}
