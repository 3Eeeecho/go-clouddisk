your-simple-cloud-disk/
├── cmd/
│   └── server/
│       └── main.go       # 项目主入口文件，启动Web服务
├── internal/
│   ├── config/           # 配置文件加载、解析（例如：database.yml, redis.yml, oss.yml）
│   │   └── config.go
│   ├── models/           # 数据库模型定义 (GORM结构体)
│   │   ├── user.go
│   │   ├── file.go
│   │   └── share_link.go # 如果有分享功能
│   ├── handlers/         # HTTP请求处理函数 (Gin的Handler)
│   │   ├── auth.go       # 用户认证相关handler (注册、登录、刷新token)
│   │   ├── user.go       # 用户信息相关handler
│   │   ├── file.go       # 文件及文件夹管理handler (上传、下载、列表、删除、移动、重命名)
│   │   └── share.go      # 分享相关handler
│   ├── services/         # 业务逻辑服务层 (聚合多个repo/pkg，处理复杂业务流程)
│   │   ├── auth_service.go
│   │   ├── user_service.go
│   │   ├── file_service.go
│   │   └── share_service.go
│   ├── repositories/     # 数据仓储层 (与数据库、Redis、OSS等数据源交互的封装)
│   │   ├── user_repo.go
│   │   ├── file_repo.go
│   │   ├── redis_repo.go # Redis操作
│   │   └── oss_repo.go   # OSS操作
│   ├── middlewares/      # Gin中间件 (如JWT认证、CORS、日志、限流)
│   │   └── auth_middleware.go
│   ├── router/           # Gin路由注册
│   │   └── router.go
│   ├── utils/            # 通用工具函数 (如密码哈希、UUID生成、文件MD5计算)
│   │   └── password.go
│   │   └── uuid.go
│   │   └── file_helper.go
│   ├── pkg/              # 外部可复用的公共代码包，如果 internal/ 下有需要独立复用的内容可以提到这里
│   │   └── errors/       # 统一错误码和错误信息定义
│   │       └── errors.go
│   │   └── logger/       # 日志工具
│   │       └── logger.go
│   │   └── jwt/          # JWT相关工具
│   │       └── jwt.go
│   └── consumers/        # RabbitMQ消费者（如果需要异步处理）
│       └── file_processor.go # 例如处理文件缩略图生成、MD5计算等异步任务
├── scripts/              # 辅助脚本 (如数据库初始化脚本、部署脚本)
│   └── init_db.sql
├── web/                  # 前端静态文件 (如果前端也包含在项目中)
│   ├── assets/
│   └── index.html
├── .env                  # 环境变量配置文件 (敏感信息，不应提交到Git)
├── Dockerfile            # Docker镜像构建文件
├── docker-compose.yml    # Docker Compose配置 (MySQL, Redis, RabbitMQ, MinIO)
├── go.mod                # Go模块定义文件
├── go.sum                # Go模块校验文件
├── README.md             # 项目说明文档