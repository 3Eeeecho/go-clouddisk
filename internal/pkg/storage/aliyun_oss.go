package storage

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"go.uber.org/zap"
)

//TODO 待完善文件,后续考虑完善

type AliyunOSSStorageService struct {
	client *oss.Client
	cfg    *config.AliyunOSSConfig // 阿里云OSS的配置信息
}

// NewAliyunOSSStorageService 创建并返回一个 AliyunOSSStorageService 实例
func NewAliyunOSSStorageService(cfg *config.AliyunOSSConfig) (*AliyunOSSStorageService, error) {
	// OSS Endpoint 应该包含 http:// 或 https:// 前缀
	ossClient, err := oss.New(cfg.Endpoint, cfg.AccessKeyID, cfg.SecretAccessKey)
	if err != nil {
		logger.Error("初始化阿里云OSS客户端失败", zap.Error(err))
		return nil, fmt.Errorf("无法初始化阿里云OSS客户端: %w", err)
	}
	logger.Info("阿里云OSS客户端初始化成功", zap.String("endpoint", cfg.Endpoint))
	return &AliyunOSSStorageService{
		client: ossClient,
		cfg:    cfg,
	}, nil
}

// PutObject 实现 StorageService 接口的 PutObject 方法
func (s *AliyunOSSStorageService) PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, contentType string) (PutObjectResult, error) {
	bucket, err := s.client.Bucket(bucketName)
	if err != nil {
		return PutObjectResult{}, fmt.Errorf("获取OSS存储桶失败: %w", err)
	}

	options := []oss.Option{
		oss.ContentType(contentType),
	}
	// objectSize 参数在 PutObjectFromStream 中会被忽略，OSS SDK 会自动计算
	err = bucket.PutObject(objectName, reader, options...)
	if err != nil {
		return PutObjectResult{}, fmt.Errorf("阿里云OSS上传文件失败: %w", err)
	}

	// 上传后，如果需要 ETag 和 Size，可能需要再次 StatObject
	// 简单起见，这里可以假设上传成功，如果需要更精确的 info，可以考虑GetObjectMeta
	// 实际项目中，更常见的是在PutObjectOptions中设置Header，或者依赖OSS的API响应
	// 假设我们只关心基本的成功信息
	return PutObjectResult{
		Bucket: bucketName,
		Key:    objectName,
		Size:   objectSize, // 暂时使用传入的尺寸，因为PutObject本身不直接返回
		// ETag:  如果需要精确ETag，需要调用GetObjectMeta
	}, nil
}

// GetObject 实现 StorageService 接口的 GetObject 方法
func (s *AliyunOSSStorageService) GetObject(ctx context.Context, bucketName, objectName, versionID string) (GetObjectResult, error) {
	bucket, err := s.client.Bucket(bucketName)
	if err != nil {
		return GetObjectResult{}, fmt.Errorf("获取OSS存储桶失败: %w", err)
	}

	var opts []oss.Option
	if versionID != "" {
		opts = append(opts, oss.VersionId(versionID))
	}

	reader, err := bucket.GetObject(objectName, opts...)
	if err != nil {
		return GetObjectResult{}, fmt.Errorf("阿里云OSS获取文件失败: %w", err)
	}

	// 获取对象元数据以获取Size和MimeType
	props, err := bucket.GetObjectDetailedMeta(objectName)
	if err != nil {
		logger.Warn("获取OSS对象元数据失败", zap.String("object", objectName), zap.Error(err))
	}

	size := int64(0)

	if val := props.Get(oss.HTTPHeaderContentLength); val != "" {
		size, _ = strconv.ParseInt(val, 10, 64)
	}
	mimeType := ""
	if mt := props.Get(oss.HTTPHeaderContentType); mt != "" {
		mimeType = mt
	}

	return GetObjectResult{
		Reader:   reader,
		Size:     size,
		MimeType: mimeType,
	}, nil
}

// RemoveObject 实现 StorageService 接口的 RemoveObject 方法
func (s *AliyunOSSStorageService) RemoveObject(ctx context.Context, bucketName, objectName, VersionID string) error {
	bucket, err := s.client.Bucket(bucketName)
	if err != nil {
		return fmt.Errorf("获取OSS存储桶失败: %w", err)
	}
	err = bucket.DeleteObject(objectName)
	if err != nil {
		return fmt.Errorf("阿里云OSS删除文件失败: %w", err)
	}
	return nil
}

// 从指定存储桶删除所有版本文件
func (s *AliyunOSSStorageService) RemoveObjects(ctx context.Context, bucketName, objectName string) error {

	return nil
}

// IsBucketExist 实现 StorageService 接口的 IsBucketExist 方法
func (s *AliyunOSSStorageService) IsBucketExist(ctx context.Context, bucketName string) (bool, error) {
	found, err := s.client.IsBucketExist(bucketName)
	if err != nil {
		return false, fmt.Errorf("检查阿里云OSS存储桶存在性失败: %w", err)
	}
	return found, nil
}

// MakeBucket 实现 StorageService 接口的 MakeBucket 方法
func (s *AliyunOSSStorageService) MakeBucket(ctx context.Context, bucketName string) error {
	// 阿里云OSS创建桶时可能需要ACL，这里默认使用默认ACL (公共读写，私有等)
	// 根据你的实际需求，可能需要添加 oss.ACL() 选项
	err := s.client.CreateBucket(bucketName)
	if err != nil {
		// 检查是否是桶已存在错误
		// 正确的类型断言和错误检查
		if ossErr, ok := err.(oss.ServiceError); ok && (ossErr.Code == "BucketAlreadyExists" || ossErr.Code == "BucketAlreadyOwnedByYou") {
			logger.Info("阿里云OSS存储桶已存在，无需创建", zap.String("bucket", bucketName))
			return nil
		}
		return fmt.Errorf("创建阿里云OSS存储桶失败: %w", err)
	}
	logger.Info("阿里云OSS存储桶创建成功", zap.String("bucket", bucketName))
	return nil
}

// GetObjectURL 获取对象的公开访问URL (如果桶是公开的)
// 注意：如果桶是私有的，需要生成预签名URL
func (s *AliyunOSSStorageService) GetObjectURL(bucketName, objectName string) string {
	// 阿里云OSS的URL通常是 bucketName.endpoint/objectName
	// 例如：your-bucket.oss-cn-hangzhou.aliyuncs.com/your-object
	// 需要拼接协议头
	scheme := "http://"
	if s.cfg.UseSSL { // 假设你配置中有 UseSSL 字段
		scheme = "https://"
	}
	// 截取 endpoint 中的域名部分，如果它包含 http/https
	endpoint := s.cfg.Endpoint
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")

	return fmt.Sprintf("%s%s.%s/%s", scheme, bucketName, endpoint, objectName)
}

// PreSignGetObjectURL 为下载生成预签名URL (Aliyun OSS 支持)
func (s *AliyunOSSStorageService) PreSignGetObjectURL(ctx context.Context, bucketName, objectName string, expiry time.Duration) (string, error) {
	bucket, err := s.client.Bucket(bucketName)
	if err != nil {
		return "", fmt.Errorf("获取OSS存储桶失败: %w", err)
	}

	// SignURL 默认是 GET 方法
	signedURL, err := bucket.SignURL(objectName, oss.HTTPGet, int64(expiry.Seconds()))
	if err != nil {
		return "", fmt.Errorf("生成阿里云OSS预签名URL失败: %w", err)
	}
	return signedURL, nil
}

// --- 分块上传实现 (待定) ---

func (s *AliyunOSSStorageService) InitMultiPartUpload(ctx context.Context, bucketName, objectName string, opts PutObjectOptions) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (s *AliyunOSSStorageService) UploadPart(ctx context.Context, bucketName, objectName, uploadID string, reader io.Reader, partNumber int, partSize int64) (UploadPartResult, error) {
	return UploadPartResult{}, fmt.Errorf("not implemented")
}

func (s *AliyunOSSStorageService) CompleteMultiPartUpload(ctx context.Context, bucketName, objectName, uploadID string, parts []UploadPartResult) (PutObjectResult, error) {
	return PutObjectResult{}, fmt.Errorf("not implemented")
}

func (s *AliyunOSSStorageService) AbortMultiPartUpload(ctx context.Context, bucketName, objectName, uploadID string) error {
	return fmt.Errorf("not implemented")
}

func (s *AliyunOSSStorageService) GetUploadObjName(fileHash, fileName string) string {
	return fmt.Sprintf("uploads/%s", fileName)
}

func (s *AliyunOSSStorageService) ListObjectParts(ctx context.Context, bucketName, objectName, uploadID string) ([]UploadPartResult, error) {
	return nil, nil
}

func (s *AliyunOSSStorageService) IsUploadIDNotFound(err error) bool {
	if err == nil {
		return false
	}
	// Aliyun OSS a "NoSuchUpload" error code when the upload ID does not exist.
	if ossErr, ok := err.(oss.ServiceError); ok && ossErr.Code == "NoSuchUpload" {
		return true
	}
	return false
}
