# -----------------------------------------------------------------------------
# 阶段 1: 编译环境 (Builder)
# -----------------------------------------------------------------------------
FROM golang:1.24-alpine AS builder

# 设置构建参数
ARG TARGETOS
ARG TARGETARCH

# 设置环境变量
# 1. GOTOOLCHAIN=auto: 关键配置！允许 Go 自动下载 go.mod 中指定的更高版本 (例如 1.24.6)
# 2. CGO_ENABLED=0: 禁用 CGO 以生成静态链接二进制文件
# 3. GOPROXY: 配置国内代理加速下载
ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=auto \
    GOOS=${TARGETOS:-linux} \
    GOARCH=${TARGETARCH:-amd64} \
    GOPROXY=https://goproxy.cn,direct

WORKDIR /workspace

# 1. 预先拷贝依赖文件，利用 Docker 缓存
COPY go.mod go.sum ./

# 下载依赖
# 由于设置了 GOTOOLCHAIN=auto，如果发现版本不匹配，这一步会自动先下载新版 Go，然后再下载依赖
RUN go mod download

# 2. 拷贝源代码
COPY . .

# 3. 执行编译
# -a: 强制重新编译
# -o controller: 输出文件名为 controller
RUN go build -a -o controller cmd/main.go


# -----------------------------------------------------------------------------
# 阶段 2: 运行环境 (Runner)
# -----------------------------------------------------------------------------
FROM alpine:test

WORKDIR /

# 安装基础依赖
# ca-certificates: 必须，否则 Controller 无法通过 HTTPS 连接 K8s API Server
# tzdata: 可选，用于时区设置
RUN apk add --no-cache ca-certificates tzdata

# 从编译阶段拷贝二进制文件
COPY --from=builder /workspace/controller .

# 暴露端口 (对应 main.go 中的默认配置)
# 8081: Health Probe (:8081)
# 8443: Metrics Server (:8443)
EXPOSE 8081 8443

# 启动命令
ENTRYPOINT ["/controller"]