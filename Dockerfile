# Dockerfile (在您的 Go 项目根目录)
# 使用 Go 镜像作为构建阶段
FROM golang:1.23.5 AS builder

ENV CGO_ENABLED=0 \
    GOPROXY=https://goproxy.cn,direct

# 设置工作目录
WORKDIR /app

# 复制 Go 模块依赖文件并下载依赖
# 这一步应该在复制所有代码之前，以利用 Docker 层的缓存
COPY go.mod go.sum ./
RUN go mod download

# 复制整个项目代码
COPY . .

# 构建 Go 应用
# 确保您的 main 包是 cmd/server
# CGO_ENABLED=0 禁用 CGO，生成静态链接的二进制文件，使其独立于系统库
# GOOS=linux 指定目标操作系统，确保在 Linux 容器中运行
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/server ./cmd/server

# --- 构建运行阶段镜像 ---
FROM alpine:latest

# 安装必要的工具，例如ca-certificates用于HTTPS连接
# --no-cache 避免在镜像中保留 apk 缓存，减少镜像大小
RUN apk --no-cache add ca-certificates
ENV TZ=Asia/Shanghai
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone
# 设置工作目录
WORKDIR /app

# 从构建阶段复制编译好的应用二进制文件
COPY --from=builder /app/server .

# 复制配置文件到镜像中
COPY configs/config.yml .

# 暴露应用端口（仅为文档声明）
EXPOSE 8080

# 运行应用程序
# 使用 exec 形式，使应用程序成为容器的 PID 1，以便正确处理信号
CMD ["./server"]