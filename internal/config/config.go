package config

import (
	"log"
	"strings"
	"time"

	"github.com/spf13/viper" // 导入 Viper
)

// Config 结构体包含所有应用的配置
type Config struct {
	Server        ServerConfig        `mapstructure:"server"` // `mapstructure` 标签用于Viper绑定结构体
	MySQL         MySQLConfig         `mapstructure:"mysql"`
	Redis         RedisConfig         `mapstructure:"redis"`
	MinIO         MinIOConfig         `mapstructure:"minio"`
	AliyunOSS     AliyunOSSConfig     `mapstructure:"aliyun_oss"`
	RabbitMQ      RabbitMQConfig      `mapstructure:"rabbitmq"`
	JWT           JWTConfig           `mapstructure:"jwt"`
	Storage       StorageConfig       `mapstructure:"storageconfig"`
	Log           LogConfig           `mapstructure:"log"`
	Elasticsearch ElasticsearchConfig `mapstructure:"elasticsearch"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port string `mapstructure:"port"`
}

// MySQLConfig 数据库配置
type MySQLConfig struct {
	DSN string `mapstructure:"dsn"`
}

// RedisConfig Redis配置
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// MinIOConfig MinIO配置
type MinIOConfig struct {
	Endpoint        string `mapstructure:"endpoint"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	SecretAccessKey string `mapstructure:"secret_access_key"`
	UseSSL          bool   `mapstructure:"use_ssl"`
	BucketName      string `mapstructure:"bucket_name"`
}

type AliyunOSSConfig struct {
	Endpoint        string `mapstructure:"endpoint"` // 例如: oss-cn-hangzhou.aliyuncs.com
	AccessKeyID     string `mapstructure:"access_key_id"`
	SecretAccessKey string `mapstructure:"secret_access_key"`
	BucketName      string `mapstructure:"bucket_name"`
	UseSSL          bool   `mapstructure:"use_ssl"` // OSS SDK 默认是HTTPS，但为了明确
}

// RabbitMQConfig RabbitMQ配置
type RabbitMQConfig struct {
	URL string `mapstructure:"url"`
}

// JWTConfig JWT配置
type JWTConfig struct {
	SecretKey          string        `mapstructure:"secret_key"`
	ExpiresIn          time.Duration `mapstructure:"expires_in"` // 使用 time.Duration 更清晰
	RefreshExpireHours time.Duration `mapstructure:"refresh_expire_hours"`
	Issuer             string        `mapstructure:"issuer"`
}

type StorageConfig struct {
	LocalBasePath      string `mapstructure:"local_base_path"`
	Type               string `mapstructure:"type"`
	PresignedURLExpiry int    `mapstructure:"presigned_url_expiry"` // 预签名URL有效期（分钟）
}

// zap日志配置
type LogConfig struct {
	OutputPath string `mapstructure:"output_path"`
	ErrorPath  string `mapstructure:"error_path"`
	Level      string `mapstructure:"level"`
}

// ElasticsearchConfig 定义 Elasticsearch 连接配置
type ElasticsearchConfig struct {
	Addresses []string `mapstructure:"addresses"`
	Username  string   `mapstructure:"username"`
	Password  string   `mapstructure:"password"`
	// CloudID   string   `mapstructure:"cloud_id"`
	// APIKey    string   `mapstructure:"api_key"`
}

var AppConfig *Config // 全局应用配置实例

// LoadConfig 加载配置
func LoadConfig() (*Config, error) {
	viper.SetConfigName("config")             // 配置文件名 (不带扩展名)
	viper.SetConfigType("yaml")               // 配置文件类型
	viper.AddConfigPath(".")                  // 在当前目录查找配置文件
	viper.AddConfigPath("./configs")          // 也可以添加其他路径，例如 ./configs/
	viper.AddConfigPath("/etc/go-clouddisk/") // 生产环境常见路径

	// 读取环境变量，环境变量名将自动转换为大写，并用下划线替换点
	// 例如：SERVER.PORT 对应环境变量 SERVER_PORT
	viper.SetEnvPrefix("GO_CLOUD_DISK") // 设置环境变量前缀，例如 GO_CLOUD_DISK_SERVER_PORT
	viper.AutomaticEnv()                // 自动绑定环境变量

	// 替换环境变量中的点为下划线，例如 "SERVER.PORT" 对应 "SERVER_PORT"
	// 确保Viper能正确映射如 MYSQL_DSN 到 mysql.dsn
	replacer := strings.NewReplacer(".", "_")
	viper.SetEnvKeyReplacer(replacer)

	// 1. 设置默认值 (如果配置文件和环境变量中都没有，则使用这些默认值)
	// viper.SetDefault("server.port", "8080")
	// viper.SetDefault("mysql.dsn", "root:root@tcp(mysql:3306)/clouddisk_db?charset=utf8mb4&parseTime=True&loc=Local")
	// viper.SetDefault("redis.addr", "redis:6379")
	// viper.SetDefault("redis.password", "")
	// viper.SetDefault("redis.db", 0)
	// viper.SetDefault("minio.endpoint", "minio:9000")
	// viper.SetDefault("minio.access_key_id", "minioadmin")
	// viper.SetDefault("minio.secret_access_key", "minioadmin")
	// viper.SetDefault("minio.use_ssl", false)
	// viper.SetDefault("minio.bucket_name", "go-clouddisk-bucket")
	// viper.SetDefault("rabbitmq.url", "amqp://guest:guest@rabbitmq:5672/")
	// viper.SetDefault("jwt.secret_key", "MTg5ODg2OTE1MjAyNS8zLzEyIDE4OjU0OjU3")
	// viper.SetDefault("jwt.expires_in", 60*time.Minute)           // 1小时
	// viper.SetDefault("jwt.refresh_expire_hours", 24*7*time.Hour) // 7天
	// viper.SetDefault("jwt.issuer", "go-clouddisk")
	// viper.SetDefault("storage.local_base_path", "./uploads/data")
	// ...其他默认配置

	// 2. 读取配置文件
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// 配置文件未找到，但这不是致命错误，因为我们可以依赖环境变量或默认值
			log.Println("Warning: config file not found, using environment variables or default values.")
			return nil, err
		} else {
			// 其他读取错误，例如配置文件格式错误
			log.Fatalf("Fatal error reading config file: %s \n", err)
			return nil, err
		}
	}

	// 3. 将读取到的配置绑定到结构体
	AppConfig = &Config{}
	if err := viper.Unmarshal(AppConfig); err != nil {
		log.Fatalf("Fatal error unmarshaling config: %s \n", err)
		return nil, err
	}

	log.Println("Configuration loaded successfully with Viper.")
	return AppConfig, nil
}
