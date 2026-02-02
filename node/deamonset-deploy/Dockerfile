# --- 阶段 1: 编译阶段 ---
FROM golang:1.24-alpine AS builder

# 1. 全局环境变量设置：解决 Go 1.24.2 版本依赖冲突
ENV GOPROXY=https://goproxy.cn,direct
ENV GOTOOLCHAIN=auto
ENV CGO_ENABLED=0
ENV GOOS=linux

# 安装构建 eBPF 和 CNI 插件所需的工具链
RUN apk add --no-cache make gcc musl-dev linux-headers

WORKDIR /app

# 2. 拷贝依赖文件（利用 Docker 缓存层）
COPY debug-cni/go.mod debug-cni/go.sum ./debug-cni/
COPY emu-cni/go.mod emu-cni/go.sum ./emu-cni/
COPY emunet-node-agent/go.mod emunet-node-agent/go.sum ./emunet-node-agent/

# 下载依赖
RUN cd emunet-node-agent && go mod download && \
    cd ../emu-cni && go mod download && \
    cd ../debug-cni && go mod download

# 3. 拷贝完整源码
COPY debug-cni/ ./debug-cni/
COPY emu-cni/ ./emu-cni/
COPY emunet-node-agent/ ./emunet-node-agent/

# 4. 执行静态编译
# 编译 Node Agent
RUN cd emunet-node-agent && \
    go build -ldflags "-s -w" -o ../bin/manager ./cmd/main.go

# 编译 Emu CNI
RUN cd emu-cni && \
    go build -o ../bin/emu-cni ./cmd/emu-cni/main.go

# 编译 Debug CNI（基于你目前的目录结构新增）
RUN cd debug-cni && \
    go build -o ../bin/debug-cni ./main.go


# --- 阶段 2: 运行镜像阶段 ---
FROM alpine:test

# 安装基础网络调试工具
RUN apk add --no-cache ca-certificates iproute2 tzdata && \
    rm -rf /var/cache/apk/*

WORKDIR /app

# 从构建阶段拷贝所有二进制产物
COPY --from=builder /app/bin/manager /app/manager
COPY --from=builder /app/bin/emu-cni /app/emu-cni
COPY --from=builder /app/bin/debug-cni /app/debug-cni

# 赋予执行权限
RUN chmod +x /app/manager /app/emu-cni /app/debug-cni

# 暴露 Agent 服务端口
EXPOSE 12345

# 容器启动时运行 Agent 主程序
ENTRYPOINT ["/app/manager"]