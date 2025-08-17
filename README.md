# Go-CloudDisk

Go-CloudDisk 是一个使用 Go 语言开发的高性能、支持分布式对象存储的云网盘项目。

## ✨ 核心功能

### 已实现
- **用户认证**: 基于 JWT 的用户注册和登录。
- **文件操作**: 支持文件的上传、下载、重命名、移动。
- **秒传功能**: 通过计算文件 MD5 实现秒传，避免重复上传。
- **分块上传/断点续传**: 支持大文件的高效、可靠上传。
- **回收站**: 提供文件的软删除和恢复功能。
- **文件夹操作**: 支持创建文件夹和文件夹下载。
- **多存储后端**: 支持阿里云 OSS 和 MinIO 作为对象存储后端。
- **日志与配置**: 使用 Zap 进行结构化日志记录，Viper 进行配置管理。
- **API 文档**: 通过 Swagger 提供交互式 API 文档。

### 计划中
- **文件分享**: 生成带密码或有效期的分享链接。
- **缩略图生成**: 为图片和视频文件自动生成缩略图。
- **用户配额管理**: 限制每个用户的可用存储空间。
- **全文搜索**: 基于 Elasticsearch 实现文件名或内容搜索。
- **WebSocket 通知**: 实现文件上传完成等实时消息通知。

## 🛠️ 技术栈

- **后端框架**: Gin
- **数据库**: MySQL
- **ORM**: GORM
- **缓存**: Redis
- **对象存储**: Aliyun OSS, MinIO
- **日志**: Zap
- **配置管理**: Viper
- **API 文档**: Swagger

## 🚀 快速开始

### 1. 环境准备
- Go 1.18+
- MySQL 5.7+
- Redis
- Docker & Docker Compose (推荐)

### 2. 克隆项目
```bash
git clone https://github.com/3Eeeecho/go-clouddisk.git
cd go-clouddisk
```

### 3. 配置
复制 `configs/config.yml.example` 并重命名为 `configs/config.yml`，然后根据你的本地环境修改以下配置：
- `mysql`: 数据库连接信息。
- `redis`: Redis 连接信息。
- `storage`: 对象存储配置 (阿里云 OSS 或 MinIO)。

### 4. 运行项目

#### 使用 Docker Compose (推荐)
```bash
docker-compose up -d
```
该命令将启动应用容器以及所有依赖的服务 (MySQL, Redis 等)。

#### 本地直接运行
```bash
# 安装依赖
go mod tidy

# 运行
go run cmd/main.go
```

### 5. 访问
- **应用服务**: `http://localhost:8080`
- **API 文档**: `http://localhost:8080/swagger/index.html`
