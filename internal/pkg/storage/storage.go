package storage

import (
	"context"
	"io"
)

// StorageService 定义了通用的文件存储操作接口
type StorageService interface {
	// 上传文件到指定存储桶，返回存储对象的信息或错误
	PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, contentType string) (PutObjectResult, error)
	// 从指定存储桶下载文件，返回一个读取器和对象信息
	GetObject(ctx context.Context, bucketName, objectName string) (GetObjectResult, error)
	// 从指定存储桶删除文件
	RemoveObject(ctx context.Context, bucketName, objectName string) error
	// 检查存储桶是否存在
	IsBucketExist(ctx context.Context, bucketName string) (bool, error)
	// 创建存储桶
	MakeBucket(ctx context.Context, bucketName string) error
	// 获取对象的公开访问URL（如果支持）
	GetObjectURL(bucketName, objectName string) string
}

type PutObjectResult struct {
	Bucket string
	Key    string
	Size   int64
	ETag   string // 对象哈希值
}

type GetObjectResult struct {
	Reader   io.ReadCloser // 文件内容读取器，需要在使用后关闭
	Size     int64
	MimeType string
	// 可以添加其他元数据，如文件名等
}
