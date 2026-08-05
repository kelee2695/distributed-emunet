package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/emunet/emunet-operator/node/emunet-node-agent/internal/api"
	"github.com/emunet/emunet-operator/node/emunet-node-agent/pkg"
	"github.com/emunet/emunet-operator/pkg/redis"
)

func main() {
	var apiAddr string
	var redisAddr string
	var redisPassword string
	var redisDB int
	var nodeName string
	var redisPoolSize int
	var redisMinIdleConns int
	var maxConcurrent int
	var podInfoWorkers int
	var podInfoQueueSize int

	flag.StringVar(&apiAddr, "api-bind-address", ":12345", "The address the Agent API endpoint binds to.")
	flag.StringVar(&redisAddr, "redis-addr", "emunet-redis.default.svc.cluster.local:6379", "The address of the Redis server")
	flag.StringVar(&redisPassword, "redis-password", "", "The password of the Redis server")
	flag.IntVar(&redisDB, "redis-db", 0, "The Redis database index")
	flag.StringVar(&nodeName, "node-name", "", "The name of the node this agent is running on")
	flag.IntVar(&redisPoolSize, "redis-pool-size", 20, "Redis connection pool size")
	flag.IntVar(&redisMinIdleConns, "redis-min-idle-conns", 2, "Redis minimum idle connections")
	flag.IntVar(&maxConcurrent, "max-concurrent", 64, "Maximum concurrent eBPF HTTP requests")
	flag.IntVar(&podInfoWorkers, "podinfo-workers", 4, "Workers used to write Pod network info to Redis")
	flag.IntVar(&podInfoQueueSize, "podinfo-queue-size", 2048, "Queue size for Pod network info Redis writes")

	flag.Parse()

	// 2. 初始化日志 (生产环境建议 JSON 格式)
	config := zap.NewProductionConfig()
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	zapLog, _ := config.Build()
	defer zapLog.Sync()
	logger := zapLog.Sugar()

	logger.Infow("Starting EmuNet Agent", "version", "v1.0", "address", apiAddr, "node", nodeName)

	if nodeName == "" {
		logger.Fatal("node-name is required")
	}

	if os.Geteuid() != 0 {
		logger.Warn("WARNING: Agent is NOT running as root. eBPF operations will likely fail!")
	}

	logger.Infow("Connecting to Redis", "addr", redisAddr)
	redisClient := redis.NewClientWithOptions(redisAddr, redisPassword, redisDB, redis.ClientOptions{
		PoolSize:     redisPoolSize,
		MinIdleConns: redisMinIdleConns,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := redisClient.Ping(ctx); err != nil {
		cancel()
		logger.Fatalw("Failed to connect to Redis", "error", err)
	}
	cancel()
	logger.Info("Connected to Redis successfully")

	agentServer := api.NewServer(redisClient, maxConcurrent, podInfoWorkers, podInfoQueueSize)

	consumer := redis.NewControlBusConsumer(
		redisClient.GetClient(),
		fmt.Sprintf("agent-%s", nodeName),
		nodeName,
		handleControlCommand(agentServer),
	)

	if err := consumer.Start(); err != nil {
		logger.Fatalw("Failed to start control bus consumer", "error", err)
	}
	logger.Info("Control bus consumer started")

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

	sig := <-stop
	logger.Infow("Shutting down Agent...", "signal", sig)

	consumer.Stop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Errorw("Server forced to shutdown", "error", err)
	}

	if err := redisClient.Close(); err != nil {
		logger.Errorw("Error closing Redis connection", "error", err)
	}

	logger.Info("Agent exited cleanly")
}

func handleControlCommand(server *api.AgentServer) redis.CommandHandler {
	return func(ctx context.Context, command *redis.ControlCommand) error {
		var payload redis.EBPFCommandPayload
		if err := json.Unmarshal([]byte(command.Payload), &payload); err != nil {
			return err
		}

		ebpfMap, err := server.GetEBPFMap()
		if err != nil {
			return err
		}

		switch command.CommandType {
		case "POST":
			return pkg.AddEBPFEntry(ebpfMap, payload.Ifindex, payload.SrcMac, payload.ThrottleRateBps, payload.Delay, payload.LossRate, payload.Jitter)
		case "DELETE":
			return pkg.DeleteEBPFEntry(ebpfMap, payload.Ifindex, payload.SrcMac)
		default:
			return fmt.Errorf("unknown command type: %s", command.CommandType)
		}
	}
}
