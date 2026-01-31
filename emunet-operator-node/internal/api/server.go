package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
	"fmt"

	"github.com/cilium/ebpf"
	"github.com/gorilla/mux"

	"github.com/emunet/emunet-operator/internal/redis"
	"github.com/emunet/emunet-operator/pkg"
)

// ==========================================
// 1. 本地存储结构 (嵌入式定义)
// ==========================================

type PodInfo struct {
	PodName string `json:"podName"`
	Ifindex int    `json:"ifindex"`
	SrcMac  string `json:"srcMac"`
}

// PodInfoStore 使用 sync.Map 实现高性能并发读写
type PodInfoStore struct {
	data sync.Map
}

func (s *PodInfoStore) Set(podName string, info *PodInfo) {
	s.data.Store(podName, info)
}

func (s *PodInfoStore) Get(podName string) (*PodInfo, bool) {
	val, ok := s.data.Load(podName)
	if !ok {
		return nil, false
	}
	return val.(*PodInfo), true
}

func (s *PodInfoStore) Delete(podName string) {
	s.data.Delete(podName)
}

// ==========================================
// 2. Server 定义
// ==========================================

type AgentServer struct {
	router         *mux.Router
	podInfoStore   *PodInfoStore // 直接使用上方定义的本地结构
	redis          *redis.Client
	semaphore      chan struct{}
	metrics        *ServerMetrics
	ebpfMap        *ebpf.Map
	ebpfMapMutex   sync.RWMutex
	ebpfMapLoaded  bool
	ebpfMapLoadErr error
}

type ServerMetrics struct {
	totalRequests      int64
	activeRequests     int64
	successfulRequests int64
	failedRequests     int64
	timeoutRequests    int64
}

// NewServer 初始化
func NewServer(redisClient *redis.Client) *AgentServer {
	s := &AgentServer{
		router:       mux.NewRouter(),
		podInfoStore: &PodInfoStore{}, // 初始化本地 Store
		redis:        redisClient,
		// 并发控制：根据机器核数调整，例如 2000
		semaphore: make(chan struct{}, 2000),
		metrics:   &ServerMetrics{},
	}
	s.setupRoutes()
	return s
}

func (s *AgentServer) setupRoutes() {
	// eBPF 核心路径
	s.router.HandleFunc("/api/ebpf/entry", s.handleEBPFEntry).Methods("POST", "DELETE")

	// Pod Info 路径
	s.router.HandleFunc("/api/podinfo/add", s.handlePodInfoAdd).Methods("POST")
	s.router.HandleFunc("/api/podinfo/{podName}", s.handlePodInfo).Methods("GET", "DELETE")
	s.router.HandleFunc("/health", s.handleHealth).Methods("GET")
}

// GetRouter 供 main.go 调用
func (s *AgentServer) GetRouter() *mux.Router {
	return s.router
}

// ==========================================
// 3. Metrics (Atomic)
// ==========================================

func (s *AgentServer) recordRequestStart() {
	atomic.AddInt64(&s.metrics.totalRequests, 1)
	atomic.AddInt64(&s.metrics.activeRequests, 1)
}

func (s *AgentServer) recordRequestEnd(success bool, timeout bool) {
	atomic.AddInt64(&s.metrics.activeRequests, -1)
	if success {
		atomic.AddInt64(&s.metrics.successfulRequests, 1)
	} else {
		atomic.AddInt64(&s.metrics.failedRequests, 1)
	}
	if timeout {
		atomic.AddInt64(&s.metrics.timeoutRequests, 1)
	}
}

// ==========================================
// 4. Handlers
// ==========================================

func (s *AgentServer) handleEBPFEntry(w http.ResponseWriter, r *http.Request) {
	s.recordRequestStart()
	success := false
	isTimeout := false

	defer func() {
		s.recordRequestEnd(success, isTimeout)
	}()

	// 快速失败机制
	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	case <-time.After(100 * time.Millisecond):
		isTimeout = true
		http.Error(w, "Server busy", http.StatusServiceUnavailable)
		return
	}

	ebpfMap, err := s.getEBPFMap()
	if err != nil {
		http.Error(w, "eBPF map error", http.StatusServiceUnavailable)
		return
	}

	if r.Method == "POST" {
		var req struct {
			Ifindex         uint32 `json:"ifindex"`
			SrcMac          string `json:"srcMac"`
			ThrottleRateBps uint32 `json:"throttleRateBps"`
			Delay           uint32 `json:"delay"`
			LossRate        uint32 `json:"lossRate"`
			Jitter          uint32 `json:"jitter"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if err := pkg.AddEBPFEntry(ebpfMap, req.Ifindex, req.SrcMac, req.ThrottleRateBps, req.Delay, req.LossRate, req.Jitter); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		success = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success"}`))

	} else if r.Method == "DELETE" {
		var req struct {
			Ifindex uint32 `json:"ifindex"`
			SrcMac  string `json:"srcMac"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if err := pkg.DeleteEBPFEntry(ebpfMap, req.Ifindex, req.SrcMac); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		success = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success"}`))
	}
}

func (s *AgentServer) handlePodInfoAdd(w http.ResponseWriter, r *http.Request) {
    s.recordRequestStart()

    var req struct {
        PodName string `json:"podName"`
        Ifindex int    `json:"ifindex"`
        SrcMac  string `json:"srcMac"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        s.recordRequestEnd(false, false)
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    // 1. 存入本地内存 Store
    info := &PodInfo{
        PodName: req.PodName,
        Ifindex: req.Ifindex,
        SrcMac:  req.SrcMac,
    }
    s.podInfoStore.Set(req.PodName, info)

    // 2. 异步写入 Redis (增加调试日志!!!)
    go func(name, mac string, idx int) {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        
        // --- 修改开始 ---
        err := s.redis.UpdatePodNetworkInfo(ctx, name, mac, idx)
        if err != nil {
            // 这里会打印具体的错误原因，例如 DNS 解析失败、连接超时等
            fmt.Printf("[ERROR] Failed to update Redis for pod %s: %v\n", name, err)
        } else {
            fmt.Printf("[DEBUG] Successfully wrote to Redis: lookup:%s -> %s\n", name, mac)
        }
        // --- 修改结束 ---
        
    }(req.PodName, req.SrcMac, req.Ifindex)

    s.recordRequestEnd(true, false)
    w.WriteHeader(http.StatusOK)
    w.Write([]byte(`{"status":"success"}`))
}

func (s *AgentServer) handlePodInfo(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	podName := vars["podName"]

	if r.Method == "GET" {
		info, exists := s.podInfoStore.Get(podName)
		if exists {
			json.NewEncoder(w).Encode(info)
		} else {
			http.Error(w, "Not found", http.StatusNotFound)
		}
	} else if r.Method == "DELETE" {
		s.podInfoStore.Delete(podName)
		w.WriteHeader(http.StatusOK)
	}
}

func (s *AgentServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"healthy"}`))
}

// ==========================================
// 5. EBPF Helpers
// ==========================================

func (s *AgentServer) loadEBPFMap() error {
	s.ebpfMapMutex.Lock()
	defer s.ebpfMapMutex.Unlock()
	if s.ebpfMapLoaded {
		return s.ebpfMapLoadErr
	}
	ebpfMap, err := pkg.LoadEBPFMap(pkg.DefaultEBPFMapPath)
	s.ebpfMapLoadErr = err
	s.ebpfMapLoaded = true
	if err == nil {
		s.ebpfMap = ebpfMap
	}
	return err
}

func (s *AgentServer) getEBPFMap() (*ebpf.Map, error) {
	s.ebpfMapMutex.RLock()
	if s.ebpfMapLoaded {
		defer s.ebpfMapMutex.RUnlock()
		return s.ebpfMap, s.ebpfMapLoadErr
	}
	s.ebpfMapMutex.RUnlock()
	err := s.loadEBPFMap()
	if err != nil {
		return nil, err
	}
	return s.ebpfMap, nil
}
