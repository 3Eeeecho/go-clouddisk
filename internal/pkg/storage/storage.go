package storage

import (
	"context"
	"errors"
	"io"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
)

// StorageService 定义了通用的文件存储操作接口
type StorageService interface {
	// 上传文件到指定存储桶，返回存储对象的信息或错误
	PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, contentType string) (PutObjectResult, error)
	// 从指定存储桶下载文件，返回一个读取器和对象信息
	GetObject(ctx context.Context, bucketName, objectName string) (GetObjectResult, error)
	// 从指定存储桶删除指定版本文件
	RemoveObject(ctx context.Context, bucketName, objectName, VersionID string) error
	// 从指定存储桶删除所有版本文件
	RemoveObjects(ctx context.Context, bucketName, objectName string) error
	// 检查存储桶是否存在
	IsBucketExist(ctx context.Context, bucketName string) (bool, error)
	// 创建存储桶
	MakeBucket(ctx context.Context, bucketName string) error
	// 获取对象的公开访问URL（如果支持）
	GetObjectURL(bucketName, objectName string) string

	// --- 分块上传方法 ---

	// InitMultiPartUpload 初始化分块上传, 返回 uploadID
	InitMultiPartUpload(ctx context.Context, bucketName, objectName string, opts PutObjectOptions) (string, error)

	// UploadPart 上传文件的一个分块
	UploadPart(ctx context.Context, bucketName, objectName, uploadID string, reader io.Reader, partNumber int, partSize int64) (UploadPartResult, error)

	// CompleteMultiPartUpload 完成分块上传
	CompleteMultiPartUpload(ctx context.Context, bucketName, objectName, uploadID string, parts []UploadPartResult) (PutObjectResult, error)

	// AbortMultiPartUpload 中止分块上传
	AbortMultiPartUpload(ctx context.Context, bucketName, objectName, uploadID string) error

	// ListObjectParts 列出已上传的分块
	ListObjectParts(ctx context.Context, bucketName, objectName, uploadID string) ([]UploadPartResult, error)

	//获取上传的ObjectName
	GetUploadObjName(fileHash, fileName string) string

	// IsUploadIDNotFound 检查错误是否是 "upload ID not found" 类型
	IsUploadIDNotFound(err error) bool
}

type PutObjectResult struct {
	Bucket    string
	Key       string
	Size      int64
	ETag      string // 对象哈希值
	VersionID string // 版本 ID
}

type PutObjectOptions struct {
	ContentType string
	// 可根据需要添加其他选项，如用户元数据等
}

type UploadPartResult struct {
	PartNumber int
	ETag       string
}

type GetObjectResult struct {
	Reader   io.ReadCloser // 文件内容读取器，需要在使用后关闭
	Size     int64
	MimeType string
	// 可以添加其他元数据，如文件名等
}

func NewStorageService(cfg *config.Config) (StorageService, error) {
	switch cfg.Storage.Type {
	case "minio":
		return NewMinIOStorageService(&cfg.MinIO)
	case "aliyun_oss":
		return NewAliyunOSSStorageService(&cfg.AliyunOSS)
	default:
		return nil, errors.New("invalid storageType")
	}
}
