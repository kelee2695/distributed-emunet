package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	// 仅引入必要的内部包
	"github.com/emunet/emunet-operator/internal/api"
	"github.com/emunet/emunet-operator/internal/redis"
)

func main() {
	var apiAddr string
	var redisAddr string
	var redisPassword string
	var redisDB int

	// 1. 配置参数
	// Agent 默认监听 12345
	flag.StringVar(&apiAddr, "api-bind-address", ":12345", "The address the Agent API endpoint binds to.")

	// Redis 配置 (Agent 通过 Service DNS 连接)
	flag.StringVar(&redisAddr, "redis-addr", "emunet-redis.default.svc.cluster.local:6379", "The address of the Redis server")
	flag.StringVar(&redisPassword, "redis-password", "", "The password of the Redis server")
	flag.IntVar(&redisDB, "redis-db", 0, "The Redis database index")

	flag.Parse()

	// 2. 初始化日志 (生产环境建议 JSON 格式)
	config := zap.NewProductionConfig()
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	zapLog, _ := config.Build()
	defer zapLog.Sync()
	logger := zapLog.Sugar()

	logger.Infow("Starting EmuNet Agent", "version", "v1.0", "address", apiAddr)

	// 3. 权限检查 (必须 Root 才能操作 eBPF)
	if os.Geteuid() != 0 {
		logger.Warn("WARNING: Agent is NOT running as root. eBPF operations will likely fail!")
	}

	// 4. 初始化 Redis 客户端
	// Agent 启动时必须连接上 Redis，否则无法上报自己的存在
	logger.Infow("Connecting to Redis", "addr", redisAddr)
	redisClient := redis.NewClient(redisAddr, redisPassword, redisDB)

	// 健康检查: 确保 Redis 通畅
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := redisClient.Ping(ctx); err != nil {
		cancel()
		logger.Fatalw("Failed to connect to Redis", "error", err)
	}
	cancel()
	logger.Info("Connected to Redis successfully")

	// 5. 初始化 Agent Server
	// 注入 Redis 客户端，移除所有 K8s 相关依赖
	agentServer := api.NewServer(redisClient)

	// 6. 配置 HTTP Server
	server := &http.Server{
		Addr:         apiAddr,
		Handler:      agentServer.GetRouter(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 7. 启动服务 (Goroutine)
	go func() {
		logger.Info("Listening for HTTP requests...")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalw("Agent server crashed", "error", err)
		}
	}()

	// 8. 优雅退出 (Graceful Shutdown)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// 阻塞等待信号
	sig := <-stop
	logger.Infow("Shutting down Agent...", "signal", sig)

	// 这里的 Context 控制关闭超时时间
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	// 关闭 HTTP 服务
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Errorw("Server forced to shutdown", "error", err)
	}

	// 关闭 Redis 连接
	if err := redisClient.Close(); err != nil {
		logger.Errorw("Error closing Redis connection", "error", err)
	}

	logger.Info("Agent exited cleanly")
}
