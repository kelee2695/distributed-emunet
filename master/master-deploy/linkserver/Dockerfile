# -----------------------------------------------------------------------------
# 阶段 1: 编译环境 (Builder)
# -----------------------------------------------------------------------------
FROM golang:1.24-alpine AS builder

# 设置构建参数
ARG TARGETOS
ARG TARGETARCH

# 环境变量配置
# GOTOOLCHAIN=auto: 自动下载 go.mod 要求的 Go 版本
# CGO_ENABLED=0: 静态编译
ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=auto \
    GOOS=${TARGETOS:-linux} \
    GOARCH=${TARGETARCH:-amd64} \
    GOPROXY=https://goproxy.cn,direct

WORKDIR /workspace

# 1. 缓存依赖
COPY go.mod go.sum ./
RUN go mod download

# 2. 拷贝源码
COPY . .

# 3. 编译
# 对应 Makefile 中的: go build -o bin/linkserver cmd/main.go
RUN go build -a -o linkserver cmd/main.go


# -----------------------------------------------------------------------------
# 阶段 2: 运行环境 (Runner)
# -----------------------------------------------------------------------------
FROM alpine:test

WORKDIR /

# 安装基础依赖 (API 服务通常需要 CA 证书和时区)
RUN apk add --no-cache ca-certificates tzdata

# 从编译阶段拷贝二进制文件
COPY --from=builder /workspace/linkserver .

# 暴露端口 (仅用于文档说明)
EXPOSE 8082

# 启动命令
ENTRYPOINT ["/linkserver"]