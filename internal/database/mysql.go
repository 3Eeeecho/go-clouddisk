package database

import (
	"log"

	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var DB *gorm.DB // 全局数据库连接实例

// InitMySQL 初始化 MySQL 数据库连接
func InitMySQL(cfg *config.MySQLConfig) {
	var err error
	DB, err = gorm.Open(mysql.Open(cfg.DSN), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to MySQL database: %v", err)
	}

	sqlDB, err := DB.DB()
	if err != nil {
		log.Fatalf("Failed to get generic database object from GORM: %v", err)
	}

	// 设置连接池参数
	sqlDB.SetMaxIdleConns(10)  // 最大空闲连接数
	sqlDB.SetMaxOpenConns(100) // 最大打开连接数
	// sqlDB.SetConnMaxLifetime(time.Hour) // 连接最大复用时间

	log.Println("MySQL database connected successfully!")

	// 自动迁移数据库表结构
	AutoMigrate()
}

// AutoMigrate 自动迁移数据库表结构
func AutoMigrate() {
	err := DB.AutoMigrate(
		&models.User{},
		&models.File{},
		//&models.ShareLink{}, // 如果您决定包含分享功能
	)
	if err != nil {
		log.Fatalf("Failed to auto migrate database tables: %v", err)
	}
	log.Println("Database tables migrated successfully!")
}

// CloseMySQLDB 关闭数据库连接
func CloseMySQLDB() {
	if DB != nil {
		sqlDB, err := DB.DB()
		if err != nil {
			log.Printf("Error getting generic database object to close: %v", err)
			return
		}
		err = sqlDB.Close()
		if err != nil {
			log.Printf("Error closing MySQL database connection: %v", err)
		} else {
			log.Println("MySQL database connection closed.")
		}
	}
}
