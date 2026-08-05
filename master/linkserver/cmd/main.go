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
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	emunetv1 "github.com/emunet/emunet-operator/master/controller/api/v1"
	"github.com/emunet/emunet-operator/master/linkserver/internal/api"
	"github.com/emunet/emunet-operator/pkg/redis"
)

func main() {
	var apiAddr string
	var redisAddr string
	var redisPassword string
	var redisDB int
	var redisPoolSize int
	var redisMinIdleConns int
	var devMode bool
	var webDir string
	var pingConcurrency int

	// 基础配置
	flag.StringVar(&apiAddr, "api-bind-address", ":8082", "The address the REST API endpoint binds to.")
	flag.BoolVar(&devMode, "dev", true, "Enable development mode logging")
	flag.StringVar(&webDir, "web-dir", "web-console", "Static web console directory. Set empty to disable.")
	flag.IntVar(&pingConcurrency, "ping-concurrency", 2, "Maximum concurrent Kubernetes exec ping tests.")

	// Redis 配置
	flag.StringVar(&redisAddr, "redis-addr", "localhost:6379", "Redis server address")
	flag.StringVar(&redisPassword, "redis-password", "", "Redis password")
	flag.IntVar(&redisDB, "redis-db", 0, "Redis database number")
	flag.IntVar(&redisPoolSize, "redis-pool-size", 50, "Redis connection pool size")
	flag.IntVar(&redisMinIdleConns, "redis-min-idle-conns", 5, "Redis minimum idle connections")

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
	redisClient := redis.NewClientWithOptions(redisAddr, redisPassword, redisDB, redis.ClientOptions{
		PoolSize:     redisPoolSize,
		MinIdleConns: redisMinIdleConns,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := redisClient.Ping(ctx); err != nil {
		sugar.Fatalw("failed to connect to Redis", "error", err, "address", redisAddr)
	}
	cancel()
	sugar.Infow("successfully connected to Redis", "address", redisAddr)

	// 3. 初始化 Kubernetes Client
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(emunetv1.AddToScheme(scheme))

	restConfig := ctrl.GetConfigOrDie()
	k8sClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		sugar.Fatalw("failed to initialize Kubernetes client", "error", err)
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		sugar.Fatalw("failed to initialize Kubernetes clientset", "error", err)
	}

	// 4. 初始化业务逻辑
	apiHandler := api.NewMasterServer(redisClient, k8sClient, clientset, restConfig, logger, webDir, pingConcurrency)

	// 5. 启动 HTTP Server
	server := &http.Server{
		Addr:         apiAddr,
		Handler:      apiHandler.GetRouter(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	sugar.Infow("starting REST API server", "address", apiAddr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		sugar.Fatalw("problem running REST API server", "error", err)
	}
}
