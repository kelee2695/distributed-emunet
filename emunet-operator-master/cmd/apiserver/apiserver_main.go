/*
Copyright 2026.
*/

package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/emunet/emunet-operator/internal/api"
	"github.com/emunet/emunet-operator/internal/redis"
)

func main() {
	var apiAddr string
	var redisAddr string
	var redisPassword string
	var redisDB int
	var devMode bool

	// 基础配置
	flag.StringVar(&apiAddr, "api-bind-address", ":8082", "The address the REST API endpoint binds to.")
	flag.BoolVar(&devMode, "dev", true, "Enable development mode logging")

	// Redis 配置
	flag.StringVar(&redisAddr, "redis-addr", "localhost:6379", "Redis server address")
	flag.StringVar(&redisPassword, "redis-password", "", "Redis password")
	flag.IntVar(&redisDB, "redis-db", 0, "Redis database number")

	flag.Parse()

	// 1. 初始化 Logger (不再依赖 controller-runtime 的 zap)
	logConfig := zap.NewProductionConfig()
	if devMode {
		logConfig = zap.NewDevelopmentConfig()
		logConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	}
	logger, err := logConfig.Build()
	if err != nil {
		os.Exit(1)
	}
	defer logger.Sync()
	sugar := logger.Sugar() // 使用 SugarLogger 方便输出

	sugar.Info("Starting EmuNet Master API Server...")

	// 2. 初始化 Redis
	redisClient := redis.NewClient(redisAddr, redisPassword, redisDB)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := redisClient.Ping(ctx); err != nil {
		sugar.Fatalw("failed to connect to Redis", "error", err, "address", redisAddr)
	}
	cancel()
	sugar.Infow("successfully connected to Redis", "address", redisAddr)

	// 3. 初始化业务逻辑 (不再传递 k8sClient)
	// 注意：你需要同步修改 internal/api/server.go 中的 NewMasterServer 签名
	apiHandler := api.NewMasterServer(redisClient, logger)

	// 4. 启动 HTTP Server
	server := &http.Server{
		Addr:         apiAddr,
		Handler:      apiHandler.GetRouter(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	sugar.Infow("starting REST API server", "address", apiAddr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		sugar.Fatalw("problem running REST API server", "error", err)
	}
}
