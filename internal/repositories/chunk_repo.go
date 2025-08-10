package repositories

import (
	"fmt"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/cache"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type ChunkRepository interface {
	FindByID(id uint64) (*models.Chunk, error)
	SaveChunk(chunk *models.Chunk) error
	GetUploadedChunks(fileHash string) ([]models.Chunk, error)
	CountUploadedChunks(fileHash string) (int64, error)
	DeleteChunksByFileHash(fileHash string) error
}

type chunkRepository struct {
	db    *gorm.DB
	cache *cache.RedisCache
}

// NewChunkRepository 创建一个新的 ChunkRepository 实例
func NewChunkRepository(db *gorm.DB, cache *cache.RedisCache) ChunkRepository {
	return &chunkRepository{
		db:    db,
		cache: cache,
	}
}

// FindByID 根据 ID 查找分片
func (r *chunkRepository) FindByID(id uint64) (*models.Chunk, error) {
	var chunk models.Chunk
	err := r.db.First(&chunk, id).Error
	if err != nil {
		logger.Error("FindChunkByID: Failed to find chunk", zap.Uint64("chunk_id", id))
		return nil, fmt.Errorf("chunk repository: failed to find chunk: %w", err)
	}
	return &chunk, nil
}

// SaveChunk 保存或更新分片记录
func (r *chunkRepository) SaveChunk(chunk *models.Chunk) error {
	// 如果是新建记录，GORM 的 Attrs 会自动设置 UploadStatus
	// 如果是已有记录，则不更新，因为我们不需要更新
	result := r.db.Where("file_hash = ? AND chunk_index = ?", chunk.FileHash, chunk.ChunkIndex).
		Attrs(models.Chunk{UploadStatus: 1}).
		FirstOrCreate(&chunk)

	if result.Error != nil {
		logger.Error("SaveChunk: Failed to save chunk record",
			zap.String("fileHash", chunk.FileHash),
			zap.Int("chunkIndex", chunk.ChunkIndex),
			zap.Error(result.Error))
		return fmt.Errorf("failed to save chunk record: %w", result.Error)
	}

	return nil
}

// GetUploadedChunks 根据文件哈希获取所有已上传的分片
func (r *chunkRepository) GetUploadedChunks(fileHash string) ([]models.Chunk, error) {
	var chunks []models.Chunk
	err := r.db.Where("file_hash = ? AND upload_status = 1", fileHash).Find(&chunks).Error
	if err != nil {
		logger.Error("GetUploadedChunks: Failed to get chunks from DB",
			zap.String("fileHash", fileHash),
			zap.Error(err))
		return nil, fmt.Errorf("failed to get uploaded chunks: %w", err)
	}
	return chunks, nil
}

// CountUploadedChunks 根据文件哈希计算已上传的分片总数
func (r *chunkRepository) CountUploadedChunks(fileHash string) (int64, error) {
	var count int64
	err := r.db.Model(&models.Chunk{}).
		Where("file_hash = ? AND upload_status = 1", fileHash).
		Count(&count).Error
	if err != nil {
		logger.Error("CountUploadedChunks: Failed to count chunks from DB",
			zap.String("fileHash", fileHash),
			zap.Error(err))
		return 0, fmt.Errorf("failed to count uploaded chunks: %w", err)
	}
	return count, nil
}

// DeleteChunksByFileHash 删除所有与给定文件哈希相关的分片记录
func (r *chunkRepository) DeleteChunksByFileHash(fileHash string) error {
	err := r.db.Where("file_hash = ?", fileHash).Delete(&models.Chunk{}).Error
	if err != nil {
		logger.Error("DeleteChunksByFileHash: Failed to delete chunks from DB",
			zap.String("fileHash", fileHash),
			zap.Error(err))
		return fmt.Errorf("failed to delete chunk records: %w", err)
	}
	return nil
}
