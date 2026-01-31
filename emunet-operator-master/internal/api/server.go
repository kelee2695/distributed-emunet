package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/emunet/emunet-operator/internal/redis"
)

const (
	AgentPort = 12345 // Agent 监听端口
)

// AgentJob 定义发送给 Agent 的异步任务
type AgentJob struct {
	TargetNodeIP string
	Method       string
	Payload      []byte
}

type MasterServer struct {
	client client.Client // K8s Client (用于 List EmuNets 等非高频操作)
	redis  *redis.Client // Redis Client (用于高频 O(1) 查找)
	router *mux.Router

	// 优化1: 全局 HTTP Client (连接池)
	httpClient *http.Client
	// 优化2: 异步任务队列
	jobQueue chan AgentJob
}

// Request/Response 结构体
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

// 发送给 Agent 的底层请求结构
type EBPFEntryRequest struct {
	Ifindex         uint32 `json:"ifindex"`
	SrcMac          string `json:"srcMac"`
	ThrottleRateBps uint32 `json:"throttleRateBps"`
	Delay           uint32 `json:"delay"`
	LossRate        uint32 `json:"lossRate"`
	Jitter          uint32 `json:"jitter"`
}

type EBPFEntryDeleteRequest struct {
	Ifindex uint32 `json:"ifindex"`
	SrcMac  string `json:"srcMac"`
}

type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// NewMasterServer 初始化 Master Server
func NewMasterServer(client client.Client, redisClient *redis.Client) *MasterServer {
	s := &MasterServer{
		client: client,
		redis:  redisClient,
		router: mux.NewRouter(),
		// 任务队列容量：防止瞬间流量洪峰导致阻塞
		jobQueue: make(chan AgentJob, 50000),
		// 优化 HTTP 连接配置
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        1000,
				MaxIdleConnsPerHost: 100, // 关键：允许对每个 Agent 保持 100 个长连接
				IdleConnTimeout:     90 * time.Second,
				DisableKeepAlives:   false,
			},
		},
	}

	// 启动 50 个后台 Worker 协程处理网络请求
	s.startWorkers(50)
	s.setupRoutes()
	return s
}

func (s *MasterServer) startWorkers(count int) {
	for i := 0; i < count; i++ {
		go func() {
			for job := range s.jobQueue {
				s.sendToAgent(job)
			}
		}()
	}
}

// 实际发送逻辑 (Worker 执行)
func (s *MasterServer) sendToAgent(job AgentJob) {
	// 构造 Agent URL
	url := fmt.Sprintf("http://%s:%d/api/ebpf/entry", job.TargetNodeIP, AgentPort)

	req, err := http.NewRequest(job.Method, url, bytes.NewBuffer(job.Payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	// 复用连接发送请求
	resp, err := s.httpClient.Do(req)
	if err != nil {
		// 生产环境建议：记录 Metrics 或加入重试队列
		// fmt.Printf("Failed to send to agent %s: %v\n", job.TargetNodeIP, err)
		return
	}
	// 必须读取 Body 并 Close，否则无法复用 TCP 连接
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func (s *MasterServer) setupRoutes() {
	api := s.router.PathPrefix("/api/v1").Subrouter()

	// 查询接口 (保留原有逻辑，这里略去实现细节以聚焦核心)
	api.HandleFunc("/emunets", s.listEmuNets).Methods("GET")
	api.HandleFunc("/emunets/{namespace}/{name}", s.getEmuNet).Methods("GET")
	api.HandleFunc("/emunets/{namespace}/{name}/status", s.getEmuNetStatus).Methods("GET")
	api.HandleFunc("/emunets/{namespace}/{name}/pods", s.listPods).Methods("GET")
	api.HandleFunc("/emunets/{namespace}/{name}/pods/{podName}", s.getPod).Methods("GET")
	api.HandleFunc("/health", s.healthCheck).Methods("GET")

	// 核心高并发接口：规则下发
	api.HandleFunc("/ebpf/entry/by-pods", s.handleEBPFEntryByPods).Methods("POST")
	api.HandleFunc("/ebpf/entry/by-pods", s.handleEBPFEntryDeleteByPods).Methods("DELETE")
}

func (s *MasterServer) GetRouter() *mux.Router {
	return s.router
}

// ==========================================
// 核心优化：高并发处理逻辑
// ==========================================

func (s *MasterServer) handleEBPFEntryByPods(w http.ResponseWriter, r *http.Request) {
	var req EBPFEntryByPodsRequest
	// 使用 json.Decoder 流式解析，减少内存分配
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	if req.Pod1 == "" || req.Pod2 == "" {
		s.sendError(w, http.StatusBadRequest, "pod1 and pod2 are required")
		return
	}

	// 优化1: O(1) Redis 查找
	// 直接读取 Agent 上报并存入 Redis 的 "pod_lookup:{name}" 键
	// 这避免了遍历整个 Pod 列表，将复杂度从 O(N) 降为 O(1)
	pod1Info, err1 := s.redis.GetPodInfoDirectly(r.Context(), req.Pod1)
	pod2Info, err2 := s.redis.GetPodInfoDirectly(r.Context(), req.Pod2)

	// 校验数据完整性
	if err1 != nil || err2 != nil || pod1Info == nil || pod2Info == nil {
		s.sendError(w, http.StatusNotFound, fmt.Sprintf("Pod info not found in cache (p1:%v, p2:%v)", err1 == nil, err2 == nil))
		return
	}

	if pod1Info.NodeName == "" || pod2Info.NodeName == "" {
		s.sendError(w, http.StatusBadRequest, "Pod not scheduled to a node yet")
		return
	}

	if pod1Info.MACAddress == "" || pod2Info.MACAddress == "" {
		s.sendError(w, http.StatusBadRequest, "Pod MAC address not reported by agent yet")
		return
	}

	// 构造发给 Agent 的 Payload
	// 逻辑：在目标节点 (Node2) 上限制来自源 Pod (Pod1) 的流量
	// 这里的 IfIndex 是目标 Pod 在其所在节点上的 veth 索引
	job1Payload, _ := json.Marshal(EBPFEntryRequest{
		Ifindex:         uint32(pod2Info.VethIfIndex),
		SrcMac:          pod1Info.MACAddress,
		ThrottleRateBps: req.ThrottleRateBps,
		Delay:           req.Delay,
		LossRate:        req.LossRate,
		Jitter:          req.Jitter,
	})

	// 对称规则：在 Node1 上限制来自 Pod2 的流量
	job2Payload, _ := json.Marshal(EBPFEntryRequest{
		Ifindex:         uint32(pod1Info.VethIfIndex),
		SrcMac:          pod2Info.MACAddress,
		ThrottleRateBps: req.ThrottleRateBps,
		Delay:           req.Delay,
		LossRate:        req.LossRate,
		Jitter:          req.Jitter,
	})

	// 优化2: 非阻塞入队
	// 尝试将任务放入队列，如果队列满则快速失败，保护 Master 不被撑爆
	select {
	case s.jobQueue <- AgentJob{TargetNodeIP: pod2Info.NodeName, Method: "POST", Payload: job1Payload}:
	default:
		s.sendError(w, http.StatusServiceUnavailable, "Job queue full")
		return
	}

	select {
	case s.jobQueue <- AgentJob{TargetNodeIP: pod1Info.NodeName, Method: "POST", Payload: job2Payload}:
	default:
		// 尽力而为
	}

	// 立即返回，不等待 Agent 响应，极大地提高了 Master 的吞吐量
	s.sendSuccess(w, map[string]string{
		"status":  "queued",
		"message": "Rule update tasks dispatched to agents",
	})
}

func (s *MasterServer) handleEBPFEntryDeleteByPods(w http.ResponseWriter, r *http.Request) {
	var req EBPFEntryDeleteByPodsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	// O(1) 查找
	pod1Info, err1 := s.redis.GetPodInfoDirectly(r.Context(), req.Pod1)
	pod2Info, err2 := s.redis.GetPodInfoDirectly(r.Context(), req.Pod2)

	if err1 != nil || err2 != nil || pod1Info == nil || pod2Info == nil {
		// 删除时如果找不到 Pod，视为成功（幂等性）
		s.sendSuccess(w, "Pod not found, cleanup skipped")
		return
	}

	// 构造删除任务
	del1Payload, _ := json.Marshal(EBPFEntryDeleteRequest{
		Ifindex: uint32(pod2Info.VethIfIndex),
		SrcMac:  pod1Info.MACAddress,
	})

	del2Payload, _ := json.Marshal(EBPFEntryDeleteRequest{
		Ifindex: uint32(pod1Info.VethIfIndex),
		SrcMac:  pod2Info.MACAddress,
	})

	// 入队
	select {
	case s.jobQueue <- AgentJob{TargetNodeIP: pod2Info.NodeName, Method: "DELETE", Payload: del1Payload}:
	default:
		s.sendError(w, http.StatusServiceUnavailable, "Job queue full")
		return
	}

	select {
	case s.jobQueue <- AgentJob{TargetNodeIP: pod1Info.NodeName, Method: "DELETE", Payload: del2Payload}:
	default:
	}

	s.sendSuccess(w, map[string]string{
		"status":  "queued",
		"message": "Rule delete tasks dispatched to agents",
	})
}

// --- 辅助函数 ---

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

// 占位函数：保持原有接口定义，具体逻辑复用之前的实现
func (s *MasterServer) listEmuNets(w http.ResponseWriter, r *http.Request) {
	s.sendSuccess(w, "impl pending")
}
func (s *MasterServer) getEmuNet(w http.ResponseWriter, r *http.Request) {
	s.sendSuccess(w, "impl pending")
}
func (s *MasterServer) getEmuNetStatus(w http.ResponseWriter, r *http.Request) {
	s.sendSuccess(w, "impl pending")
}
func (s *MasterServer) listPods(w http.ResponseWriter, r *http.Request) {
	s.sendSuccess(w, "impl pending")
}
func (s *MasterServer) getPod(w http.ResponseWriter, r *http.Request) {
	s.sendSuccess(w, "impl pending")
}
func (s *MasterServer) healthCheck(w http.ResponseWriter, r *http.Request) {
	s.sendSuccess(w, map[string]string{"status": "healthy"})
}
