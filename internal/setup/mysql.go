package setup

import (
	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var DB *gorm.DB // 全局数据库连接实例

// InitMySQL 初始化 MySQL 数据库连接
func InitMySQL(cfg *config.MySQLConfig) {
	var err error
	DB, err = gorm.Open(mysql.Open(cfg.DSN), &gorm.Config{})
	if err != nil {
		logger.Fatal("Failed to connect to MySQL database", zap.Error(err))
	}

	sqlDB, err := DB.DB()
	if err != nil {
		logger.Fatal("Failed to get generic database object from GORM", zap.Error(err))
	}

	// 设置连接池参数
	sqlDB.SetMaxIdleConns(10)  // 最大空闲连接数
	sqlDB.SetMaxOpenConns(100) // 最大打开连接数
	// sqlDB.SetConnMaxLifetime(time.Hour) // 连接最大复用时间

	logger.Info("成功连接MySQL数据库!")

	// 自动迁移数据库表结构
	AutoMigrate()
}

// AutoMigrate 自动迁移数据库表结构
func AutoMigrate() {
	err := DB.AutoMigrate(
		&models.User{},
		&models.File{},
		&models.Share{},
		&models.UploadTask{},
	)
	if err != nil {
		logger.Fatal("Failed to auto migrate database tables", zap.Error(err))
	}
	logger.Info("Database tables migrated successfully!")
}

// CloseMySQLDB 关闭数据库连接
func CloseMySQLDB() {
	if DB != nil {
		sqlDB, err := DB.DB()
		if err != nil {
			logger.Error("Error getting generic database object to close", zap.Error(err))
			return
		}
		err = sqlDB.Close()
		if err != nil {
			logger.Error("Error closing MySQL database connection", zap.Error(err))
		} else {
			logger.Info("MySQL database connection closed.")
		}
	}
}
