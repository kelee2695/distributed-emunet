package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/cilium/ebpf"
	"github.com/gorilla/mux"

	"github.com/emunet/emunet-operator/internal/controller"
	"github.com/emunet/emunet-operator/pkg"
)

type Server struct {
	router         *mux.Router
	podInfoStore   *controller.PodInfoStore
	semaphore      chan struct{}
	metrics        *ServerMetrics
	ebpfMap        *ebpf.Map
	ebpfMapMutex   sync.RWMutex
	ebpfMapLoaded  bool
	ebpfMapLoadErr error
}

type ServerMetrics struct {
	mu                 sync.RWMutex
	totalRequests      int64
	activeRequests     int64
	successfulRequests int64
	failedRequests     int64
	timeoutRequests    int64
}

type PodInfoRequest struct {
	PodName string `json:"podName"`
	Ifindex int    `json:"ifindex"`
	SrcMac  string `json:"srcMac"`
}

type PodInfoResponse struct {
	Status  string              `json:"status"`
	Message string              `json:"message"`
	Data    *controller.PodInfo `json:"data,omitempty"`
}

type Response struct {
	Status  string  `json:"status"`
	Message string  `json:"message"`
	Data    ApiData `json:"data,omitempty"`
}

type ApiData struct {
	Operation string `json:"operation"`
	Result    string `json:"result"`
	Details   string `json:"details"`
}

func NewServer() *Server {
	s := &Server{
		router:         mux.NewRouter(),
		podInfoStore:   controller.GetGlobalPodInfoStore(),
		semaphore:      make(chan struct{}, 1000),
		metrics:        &ServerMetrics{},
		ebpfMap:        nil,
		ebpfMapLoaded:  false,
		ebpfMapLoadErr: nil,
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	s.router.HandleFunc("/api/podinfo/{podName}", s.handlePodInfo).Methods("GET", "DELETE")
	s.router.HandleFunc("/api/podinfo/add", s.handlePodInfoAdd).Methods("POST")
	s.router.HandleFunc("/api/ebpf/entry", s.handleEBPFEntry).Methods("POST", "DELETE")
	s.router.HandleFunc("/health", s.handleHealth).Methods("GET")
}

func (s *Server) recordRequestStart() {
	s.metrics.mu.Lock()
	s.metrics.totalRequests++
	s.metrics.activeRequests++
	s.metrics.mu.Unlock()
}

func (s *Server) recordRequestEnd(success bool, timeout bool) {
	s.metrics.mu.Lock()
	s.metrics.activeRequests--
	if success {
		s.metrics.successfulRequests++
	} else {
		s.metrics.failedRequests++
	}
	if timeout {
		s.metrics.timeoutRequests++
	}
	s.metrics.mu.Unlock()
}

func (s *Server) getMetrics() (total, active, success, failed, timeout int64) {
	s.metrics.mu.RLock()
	defer s.metrics.mu.RUnlock()
	return s.metrics.totalRequests, s.metrics.activeRequests, s.metrics.successfulRequests, s.metrics.failedRequests, s.metrics.timeoutRequests
}

func (s *Server) GetRouter() *mux.Router {
	return s.router
}

func (s *Server) handlePodInfo(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	s.recordRequestStart()
	defer func() {
		s.recordRequestEnd(true, false)
		duration := time.Since(startTime)
		total, active, success, failed, timeout := s.getMetrics()
		fmt.Printf("[METRICS] Total: %d, Active: %d, Success: %d, Failed: %d, Timeout: %d, Duration: %v\n",
			total, active, success, failed, timeout, duration)
	}()

	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	case <-time.After(10 * time.Second):
		s.recordRequestEnd(false, true)
		http.Error(w, "Server busy, request queued timeout", http.StatusServiceUnavailable)
		return
	}

	vars := mux.Vars(r)
	podName := vars["podName"]

	fmt.Printf("[REQUEST] %s %s (PodName: %s)\n", r.Method, r.URL.Path, podName)

	if r.Method == "GET" {
		podInfo, exists := s.getPodInfo(podName)
		if exists {
			response := PodInfoResponse{
				Status:  "success",
				Message: "Pod info retrieved",
				Data:    podInfo,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else {
			http.Error(w, "Pod not found", http.StatusNotFound)
		}
	} else if r.Method == "DELETE" {
		_, exists := s.getPodInfo(podName)
		if exists {
			s.deletePodInfo(podName)
			fmt.Printf("[DELETE] Pod info deleted: %s\n", podName)
		} else {
			fmt.Printf("[DELETE] Pod info not found (idempotent): %s\n", podName)
		}
		response := Response{
			Status:  "success",
			Message: "Pod info deleted",
			Data: ApiData{
				Operation: "delete",
				Result:    "success",
				Details:   fmt.Sprintf("Deleted pod info for %s", podName),
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	} else {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePodInfoAdd(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	s.recordRequestStart()
	defer func() {
		duration := time.Since(startTime)
		total, active, success, failed, timeout := s.getMetrics()
		fmt.Printf("[METRICS] Total: %d, Active: %d, Success: %d, Failed: %d, Timeout: %d, Duration: %v\n",
			total, active, success, failed, timeout, duration)
	}()

	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	case <-time.After(10 * time.Second):
		s.recordRequestEnd(false, true)
		http.Error(w, "Server busy, request queued timeout", http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	fmt.Printf("[REQUEST] %s %s\n", r.Method, r.URL.Path)

	if r.Method == "POST" {
		var req PodInfoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.recordRequestEnd(false, false)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.PodName == "" {
			s.recordRequestEnd(false, false)
			http.Error(w, "Pod name is required", http.StatusBadRequest)
			return
		}

		if req.SrcMac == "" {
			s.recordRequestEnd(false, false)
			http.Error(w, "MAC address is required", http.StatusBadRequest)
			return
		}

		var srcMac [6]byte
		_, err := fmt.Sscanf(req.SrcMac, "%02x:%02x:%02x:%02x:%02x:%02x",
			&srcMac[0], &srcMac[1], &srcMac[2], &srcMac[3], &srcMac[4], &srcMac[5])
		if err != nil {
			s.recordRequestEnd(false, false)
			http.Error(w, "Invalid MAC address format", http.StatusBadRequest)
			return
		}

		select {
		case <-ctx.Done():
			s.recordRequestEnd(false, true)
			http.Error(w, "Request timeout", http.StatusRequestTimeout)
			return
		default:
			s.addPodInfo(req.PodName, req.Ifindex, req.SrcMac)
		}

		s.recordRequestEnd(true, false)
		response := Response{
			Status:  "success",
			Message: "Pod info added",
			Data: ApiData{
				Operation: "add",
				Result:    "success",
				Details:   fmt.Sprintf("Added pod info for %s", req.PodName),
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	} else {
		s.recordRequestEnd(false, false)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (s *Server) getPodInfo(podName string) (*controller.PodInfo, bool) {
	info, exists := s.podInfoStore.Get(podName)
	if !exists {
		return nil, false
	}
	return info.(*controller.PodInfo), true
}

func (s *Server) addPodInfo(podName string, ifindex int, srcMac string) {
	s.podInfoStore.Set(podName, &controller.PodInfo{
		PodName: podName,
		Ifindex: ifindex,
		SrcMac:  srcMac,
	})
}

func (s *Server) deletePodInfo(podName string) {
	s.podInfoStore.Delete(podName)
}

func (s *Server) loadEBPFMap() error {
	s.ebpfMapMutex.Lock()
	defer s.ebpfMapMutex.Unlock()

	if s.ebpfMapLoaded {
		return s.ebpfMapLoadErr
	}

	ebpfMap, err := pkg.LoadEBPFMap(pkg.DefaultEBPFMapPath)
	if err != nil {
		s.ebpfMapLoadErr = err
		s.ebpfMapLoaded = true
		return err
	}

	s.ebpfMap = ebpfMap
	s.ebpfMapLoaded = true
	s.ebpfMapLoadErr = nil
	fmt.Printf("[INFO] eBPF map loaded successfully from %s\n", pkg.DefaultEBPFMapPath)
	return nil
}

func (s *Server) getEBPFMap() (*ebpf.Map, error) {
	s.ebpfMapMutex.RLock()
	if s.ebpfMapLoaded {
		s.ebpfMapMutex.RUnlock()
		return s.ebpfMap, s.ebpfMapLoadErr
	}
	s.ebpfMapMutex.RUnlock()

	err := s.loadEBPFMap()
	if err != nil {
		return nil, err
	}

	return s.ebpfMap, nil
}

func (s *Server) handleEBPFEntry(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	s.recordRequestStart()
	defer func() {
		duration := time.Since(startTime)
		total, active, success, failed, timeout := s.getMetrics()
		fmt.Printf("[METRICS] Total: %d, Active: %d, Success: %d, Failed: %d, Timeout: %d, Duration: %v\n",
			total, active, success, failed, timeout, duration)
	}()

	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	case <-time.After(10 * time.Second):
		s.recordRequestEnd(false, true)
		http.Error(w, "Server busy, request queued timeout", http.StatusServiceUnavailable)
		return
	}

	fmt.Printf("[REQUEST] %s %s\n", r.Method, r.URL.Path)

	if r.Method == "POST" {
		ebpfMap, err := s.getEBPFMap()
		if err != nil {
			s.recordRequestEnd(false, false)
			http.Error(w, fmt.Sprintf("eBPF map not available: %v", err), http.StatusServiceUnavailable)
			return
		}

		var req struct {
			Ifindex         uint32 `json:"ifindex"`
			SrcMac          string `json:"srcMac"`
			ThrottleRateBps uint32 `json:"throttleRateBps"`
			Delay           uint32 `json:"delay"`
			LossRate        uint32 `json:"lossRate"`
			Jitter          uint32 `json:"jitter"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.recordRequestEnd(false, false)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := pkg.AddEBPFEntry(ebpfMap, req.Ifindex, req.SrcMac, req.ThrottleRateBps, req.Delay, req.LossRate, req.Jitter); err != nil {
			s.recordRequestEnd(false, false)
			http.Error(w, fmt.Sprintf("Failed to add eBPF entry: %v", err), http.StatusInternalServerError)
			return
		}

		s.recordRequestEnd(true, false)
		response := Response{
			Status:  "success",
			Message: "eBPF entry added",
			Data: ApiData{
				Operation: "add",
				Result:    "success",
				Details:   fmt.Sprintf("Added eBPF entry for ifindex %d, mac %s", req.Ifindex, req.SrcMac),
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	} else if r.Method == "DELETE" {
		ebpfMap, err := s.getEBPFMap()
		if err != nil {
			s.recordRequestEnd(false, false)
			http.Error(w, fmt.Sprintf("eBPF map not available: %v", err), http.StatusServiceUnavailable)
			return
		}

		var req struct {
			Ifindex uint32 `json:"ifindex"`
			SrcMac  string `json:"srcMac"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.recordRequestEnd(false, false)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := pkg.DeleteEBPFEntry(ebpfMap, req.Ifindex, req.SrcMac); err != nil {
			s.recordRequestEnd(false, false)
			http.Error(w, fmt.Sprintf("Failed to delete eBPF entry: %v", err), http.StatusInternalServerError)
			return
		}

		s.recordRequestEnd(true, false)
		response := Response{
			Status:  "success",
			Message: "eBPF entry deleted",
			Data: ApiData{
				Operation: "delete",
				Result:    "success",
				Details:   fmt.Sprintf("Deleted eBPF entry for ifindex %d, mac %s", req.Ifindex, req.SrcMac),
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	} else {
		s.recordRequestEnd(false, false)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
