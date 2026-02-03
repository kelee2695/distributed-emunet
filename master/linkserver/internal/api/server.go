package api

import (
	"bytes"
	"context"
	"emunet/linkserver/internal/redis"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

const (
	AgentPort    = 12345   // Agent 监听端口
	JobQueueSize = 1000000 // 任务队列缓冲区大小
	WorkerCount  = 1000    // 并发 Worker 数量
)

// =================================================================================
// 1. 数据结构定义 (Types)
// =================================================================================

// AgentJob 定义发送给 Agent 的异步任务
type AgentJob struct {
	TargetNodeIP string
	Method       string
	Payload      []byte
}

// Request/Response DTOs
type EBPFEntryByPodsRequest struct {
	Pod1            string `json:"pod1"`
	Pod2            string `json:"pod2"`
	ThrottleRateBps uint32 `json:"throttleRateBps"`
	Delay           uint32 `json:"delay"`
	LossRate        uint32 `json:"lossRate"`
	Jitter          uint32 `json:"jitter"`
}

type EBPFEntryDeleteByPodsRequest struct {
	Pod1 string `json:"pod1"`
	Pod2 string `json:"pod2"`
}

type AgentRequest struct {
	Ifindex         uint32 `json:"ifindex"`
	SrcMac          string `json:"srcMac"`
	ThrottleRateBps uint32 `json:"throttleRateBps,omitempty"`
	Delay           uint32 `json:"delay,omitempty"`
	LossRate        uint32 `json:"lossRate,omitempty"`
	Jitter          uint32 `json:"jitter,omitempty"`
}

type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// =================================================================================
// 2. Server 核心与生命周期 (Core & Lifecycle)
// =================================================================================

type MasterServer struct {
	redis      *redis.Client
	router     *mux.Router
	logger     *zap.Logger
	httpClient *http.Client

	// 异步任务系统
	jobQueue chan AgentJob
	wg       sync.WaitGroup // 用于优雅退出等待
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewMasterServer 初始化
func NewMasterServer(redisClient *redis.Client, logger *zap.Logger) *MasterServer {
	ctx, cancel := context.WithCancel(context.Background())

	s := &MasterServer{
		redis:  redisClient,
		logger: logger,
		router: mux.NewRouter(),
		ctx:    ctx,
		cancel: cancel,

		// 缓冲队列：应对 Churn 模式下的突发流量
		jobQueue: make(chan AgentJob, JobQueueSize),

		// 极致优化的 HTTP Client
		httpClient: &http.Client{
			Timeout: 5 * time.Second, // 快速失败原则
			Transport: &http.Transport{
				MaxIdleConns:        WorkerCount * 2, // 匹配 Worker 数量
				MaxIdleConnsPerHost: 100,             // 单个 Node 的最大连接数
				IdleConnTimeout:     90 * time.Second,
				DisableKeepAlives:   false,
			},
		},
	}

	// 启动路由和后台进程
	s.setupRoutes()
	s.startWorkers()

	return s
}

// Stop 优雅关闭
func (s *MasterServer) Stop() {
	s.logger.Info("Stopping Master Server, waiting for workers...")
	close(s.jobQueue) // 关闭通道，Worker 处理完剩余数据后会自动退出
	s.wg.Wait()       // 等待所有 Worker 归队
	s.cancel()        // 取消上下文
	s.logger.Info("Master Server stopped gracefully.")
}

func (s *MasterServer) GetRouter() *mux.Router {
	return s.router
}

func (s *MasterServer) setupRoutes() {
	// API 版本控制
	v1 := s.router.PathPrefix("/api/v1").Subrouter()

	// --- Group A: 系统探针 ---
	v1.HandleFunc("/health", s.healthCheck).Methods("GET")

	// --- Group B: 控制平面 (Control Plane) - 高频、写操作 ---
	// 负责规则的下发、更新、删除。要求极致性能。
	v1.HandleFunc("/ebpf/entry/by-pods", s.handleRuleCreate).Methods("POST")
	v1.HandleFunc("/ebpf/entry/by-pods", s.handleRuleDelete).Methods("DELETE")

	// --- Group C: 查询平面 (Query Plane) - 低频、读操作 ---
	// 负责查询当前的状态、拓扑信息。直接查 Redis。
	v1.HandleFunc("/emunets/{namespace}/{name}/pods", s.listPodsFromCache).Methods("GET")

	// 占位接口 (按需实现)
	v1.HandleFunc("/emunets", s.notImplemented).Methods("GET")
}

// =================================================================================
// 3. Worker 系统 (Async Dispatcher)
// =================================================================================

func (s *MasterServer) startWorkers() {
	s.logger.Info("Starting background workers", zap.Int("count", WorkerCount))

	for i := 0; i < WorkerCount; i++ {
		s.wg.Add(1)
		go func(workerID int) {
			defer s.wg.Done()

			// 循环直到 jobQueue 关闭
			for job := range s.jobQueue {
				s.executeAgentJob(job)
			}
		}(i)
	}
}

func (s *MasterServer) executeAgentJob(job AgentJob) {
	url := fmt.Sprintf("http://%s:%d/api/ebpf/entry", job.TargetNodeIP, AgentPort)

	req, err := http.NewRequest(job.Method, url, bytes.NewBuffer(job.Payload))
	if err != nil {
		s.logger.Error("Failed to create request", zap.Error(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		// 生产环境建议：此处应加入重试机制或死信队列 (DLQ)
		// s.logger.Warn("Failed to send to agent", zap.String("node", job.TargetNodeIP), zap.Error(err))
		return
	}

	// 关键：读取并丢弃 Body，确保 TCP 连接能被复用
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// =================================================================================
// 4. Group B: 控制平面 Handlers (高频核心逻辑)
// =================================================================================

func (s *MasterServer) handleRuleCreate(w http.ResponseWriter, r *http.Request) {
	var req EBPFEntryByPodsRequest

	// 使用 Strict Decoding 防止字段拼写错误
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}

	if req.Pod1 == "" || req.Pod2 == "" {
		s.sendError(w, http.StatusBadRequest, "pod1 and pod2 are required")
		return
	}

	// 1. O(1) Redis Lookup
	// 移除了 K8s Client，完全依赖 Controller 同步到 Redis 的数据
	pod1Info, err1 := s.redis.GetPodInfoDirectly(r.Context(), req.Pod1)
	pod2Info, err2 := s.redis.GetPodInfoDirectly(r.Context(), req.Pod2)

	if err1 != nil || err2 != nil || pod1Info == nil || pod2Info == nil {
		s.sendError(w, http.StatusNotFound, "Pod info not found in cache. Is the Pod running?")
		return
	}

	// 2. 校验节点和 MAC
	if pod1Info.NodeName == "" || pod2Info.NodeName == "" || pod1Info.MACAddress == "" || pod2Info.MACAddress == "" {
		s.sendError(w, http.StatusPreconditionFailed, "Pod metadata incomplete (missing Node or MAC)")
		return
	}

	// 3. 构造双向规则
	// 规则 A: 告诉 Node2，来自 Pod1 (MAC1) 的包要限制
	job1Payload, _ := json.Marshal(AgentRequest{
		Ifindex:         uint32(pod2Info.VethIfIndex),
		SrcMac:          pod1Info.MACAddress,
		ThrottleRateBps: req.ThrottleRateBps,
		Delay:           req.Delay,
		LossRate:        req.LossRate,
		Jitter:          req.Jitter,
	})

	// 规则 B: 告诉 Node1，来自 Pod2 (MAC2) 的包要限制
	job2Payload, _ := json.Marshal(AgentRequest{
		Ifindex:         uint32(pod1Info.VethIfIndex),
		SrcMac:          pod2Info.MACAddress,
		ThrottleRateBps: req.ThrottleRateBps,
		Delay:           req.Delay,
		LossRate:        req.LossRate,
		Jitter:          req.Jitter,
	})

	// 4. 非阻塞入队 (Fast Fail)
	if !s.enqueueJob(pod2Info.NodeName, "POST", job1Payload) {
		s.sendError(w, http.StatusServiceUnavailable, "Job queue overloaded")
		return
	}
	s.enqueueJob(pod1Info.NodeName, "POST", job2Payload) // Best effort for the second one

	s.sendSuccess(w, map[string]string{"status": "queued", "tx_id": fmt.Sprintf("%d", time.Now().UnixNano())})
}

func (s *MasterServer) handleRuleDelete(w http.ResponseWriter, r *http.Request) {
	var req EBPFEntryDeleteByPodsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	pod1Info, err1 := s.redis.GetPodInfoDirectly(r.Context(), req.Pod1)
	pod2Info, err2 := s.redis.GetPodInfoDirectly(r.Context(), req.Pod2)

	// 幂等性：如果缓存里没这 Pod，说明可能已经被删除了，直接返回成功
	if err1 != nil || err2 != nil || pod1Info == nil || pod2Info == nil {
		s.sendSuccess(w, "Cleanup skipped: Pod not found")
		return
	}

	// 构造删除 Payload (只包含识别 Key)
	del1Payload, _ := json.Marshal(AgentRequest{
		Ifindex: uint32(pod2Info.VethIfIndex),
		SrcMac:  pod1Info.MACAddress,
	})
	del2Payload, _ := json.Marshal(AgentRequest{
		Ifindex: uint32(pod1Info.VethIfIndex),
		SrcMac:  pod2Info.MACAddress,
	})

	if !s.enqueueJob(pod2Info.NodeName, "DELETE", del1Payload) {
		s.sendError(w, http.StatusServiceUnavailable, "Job queue overloaded")
		return
	}
	s.enqueueJob(pod1Info.NodeName, "DELETE", del2Payload)

	s.sendSuccess(w, map[string]string{"status": "queued"})
}

// 辅助函数：安全入队
func (s *MasterServer) enqueueJob(nodeIP, method string, payload []byte) bool {
	select {
	case s.jobQueue <- AgentJob{TargetNodeIP: nodeIP, Method: method, Payload: payload}:
		return true
	default:
		s.logger.Warn("Job queue full, dropping request", zap.String("target", nodeIP))
		return false
	}
}

// =================================================================================
// 5. Group C: 查询平面 Handlers (只读操作)
// =================================================================================

func (s *MasterServer) listPodsFromCache(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	ns := vars["namespace"]
	name := vars["name"]

	// 直接从 Redis 集合中读取，这要求 Controller 维护好 "emunet:pods:{ns}:{name}" 这个 Set
	pods, err := s.redis.ListPodStatuses(r.Context(), ns, name)
	if err != nil {
		s.logger.Error("Redis list error", zap.Error(err))
		s.sendError(w, http.StatusInternalServerError, "Failed to retrieve pod list")
		return
	}

	s.sendSuccess(w, pods)
}

func (s *MasterServer) healthCheck(w http.ResponseWriter, r *http.Request) {
	// 简单的健康检查，可以增加 Redis Ping 检查
	if err := s.redis.Ping(r.Context()); err != nil {
		s.sendError(w, http.StatusServiceUnavailable, "Redis disconnected")
		return
	}
	s.sendSuccess(w, map[string]string{"status": "healthy", "worker_count": fmt.Sprintf("%d", WorkerCount)})
}

func (s *MasterServer) notImplemented(w http.ResponseWriter, r *http.Request) {
	s.sendError(w, http.StatusNotImplemented, "API endpoint not implemented or deprecated")
}

// =================================================================================
// 6. HTTP Helper Functions
// =================================================================================

func (s *MasterServer) sendSuccess(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(Response{Success: true, Data: data})
}

func (s *MasterServer) sendError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(Response{Success: false, Error: message})
}
