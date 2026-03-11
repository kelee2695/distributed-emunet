package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"go.uber.org/zap"

	"github.com/emunet/emunet-operator/pkg/redis"
)

const (
	AgentPort = 12345
)

// =================================================================================
// 1. 数据结构定义 (Types)
// =================================================================================

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

type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// =================================================================================
// 2. Server 核心与生命周期 (Core & Lifecycle)
// =================================================================================

type MasterServer struct {
	redis  *redis.Client
	router *mux.Router
	logger *zap.Logger

	ctx    context.Context
	cancel context.CancelFunc
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
	}

	s.setupRoutes()

	return s
}

// Stop 优雅关闭
func (s *MasterServer) Stop() {
	s.logger.Info("Stopping Master Server...")
	s.cancel()
	s.logger.Info("Master Server stopped gracefully.")
}

func (s *MasterServer) GetRouter() *mux.Router {
	return s.router
}

func (s *MasterServer) setupRoutes() {
	v1 := s.router.PathPrefix("/api/v1").Subrouter()

	v1.HandleFunc("/health", s.healthCheck).Methods("GET")

	v1.HandleFunc("/ebpf/entry/by-pods", s.handleRuleCreate).Methods("POST")
	v1.HandleFunc("/ebpf/entry/by-pods", s.handleRuleDelete).Methods("DELETE")

	v1.HandleFunc("/emunets/{namespace}/{name}/pods", s.listPodsFromCache).Methods("GET")

	v1.HandleFunc("/emunets", s.notImplemented).Methods("GET")
}

// =================================================================================
// 4. Group B: 控制平面 Handlers (高频核心逻辑)
// =================================================================================

func (s *MasterServer) handleRuleCreate(w http.ResponseWriter, r *http.Request) {
	var req EBPFEntryByPodsRequest

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

	pod1Info, err1 := s.redis.GetPodInfoDirectly(r.Context(), req.Pod1)
	pod2Info, err2 := s.redis.GetPodInfoDirectly(r.Context(), req.Pod2)

	if err1 != nil || err2 != nil || pod1Info == nil || pod2Info == nil {
		s.sendError(w, http.StatusNotFound, "Pod info not found in cache. Is the Pod running?")
		return
	}

	if pod1Info.NodeName == "" || pod2Info.NodeName == "" || pod1Info.MACAddress == "" || pod2Info.MACAddress == "" {
		s.sendError(w, http.StatusPreconditionFailed, "Pod metadata incomplete (missing Node or MAC)")
		return
	}

	payload1 := &redis.EBPFCommandPayload{
		Ifindex:         uint32(pod2Info.VethIfIndex),
		SrcMac:          pod1Info.MACAddress,
		ThrottleRateBps: req.ThrottleRateBps,
		Delay:           req.Delay,
		LossRate:        req.LossRate,
		Jitter:          req.Jitter,
	}

	payload2 := &redis.EBPFCommandPayload{
		Ifindex:         uint32(pod1Info.VethIfIndex),
		SrcMac:          pod2Info.MACAddress,
		ThrottleRateBps: req.ThrottleRateBps,
		Delay:           req.Delay,
		LossRate:        req.LossRate,
		Jitter:          req.Jitter,
	}

	if err := s.redis.PublishEBPFCommand(r.Context(), pod2Info.NodeName, "POST", payload1); err != nil {
		s.sendError(w, http.StatusInternalServerError, "Failed to publish command: "+err.Error())
		return
	}

	if err := s.redis.PublishEBPFCommand(r.Context(), pod1Info.NodeName, "POST", payload2); err != nil {
		s.logger.Warn("Failed to publish second command", zap.Error(err))
	}

	s.sendSuccess(w, map[string]string{"status": "published", "tx_id": fmt.Sprintf("%d", time.Now().UnixNano())})
}

func (s *MasterServer) handleRuleDelete(w http.ResponseWriter, r *http.Request) {
	var req EBPFEntryDeleteByPodsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	pod1Info, err1 := s.redis.GetPodInfoDirectly(r.Context(), req.Pod1)
	pod2Info, err2 := s.redis.GetPodInfoDirectly(r.Context(), req.Pod2)

	if err1 != nil || err2 != nil || pod1Info == nil || pod2Info == nil {
		s.sendSuccess(w, "Cleanup skipped: Pod not found")
		return
	}

	payload1 := &redis.EBPFCommandPayload{
		Ifindex: uint32(pod2Info.VethIfIndex),
		SrcMac:  pod1Info.MACAddress,
	}

	payload2 := &redis.EBPFCommandPayload{
		Ifindex: uint32(pod1Info.VethIfIndex),
		SrcMac:  pod2Info.MACAddress,
	}

	if err := s.redis.PublishEBPFCommand(r.Context(), pod2Info.NodeName, "DELETE", payload1); err != nil {
		s.sendError(w, http.StatusInternalServerError, "Failed to publish command: "+err.Error())
		return
	}

	if err := s.redis.PublishEBPFCommand(r.Context(), pod1Info.NodeName, "DELETE", payload2); err != nil {
		s.logger.Warn("Failed to publish second command", zap.Error(err))
	}

	s.sendSuccess(w, map[string]string{"status": "published"})
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
	if err := s.redis.Ping(r.Context()); err != nil {
		s.sendError(w, http.StatusServiceUnavailable, "Redis disconnected")
		return
	}
	s.sendSuccess(w, map[string]string{"status": "healthy"})
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
