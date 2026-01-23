package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	emunetv1 "github.com/emunet/emunet-operator/api/v1"
	"github.com/emunet/emunet-operator/internal/redis"
)

type Server struct {
	client client.Client
	redis  *redis.Client
	router *mux.Router
}

type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

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

func NewServer(client client.Client, redisClient *redis.Client) *Server {
	s := &Server{
		client: client,
		redis:  redisClient,
		router: mux.NewRouter(),
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	api := s.router.PathPrefix("/api/v1").Subrouter()

	api.HandleFunc("/emunets", s.listEmuNets).Methods("GET")
	api.HandleFunc("/emunets/{namespace}/{name}", s.getEmuNet).Methods("GET")
	api.HandleFunc("/emunets/{namespace}/{name}/status", s.getEmuNetStatus).Methods("GET")
	api.HandleFunc("/emunets/{namespace}/{name}/pods", s.listPods).Methods("GET")
	api.HandleFunc("/emunets/{namespace}/{name}/pods/{podName}", s.getPod).Methods("GET")
	api.HandleFunc("/ebpf/entry/by-pods", s.handleEBPFEntryByPods).Methods("POST")
	api.HandleFunc("/ebpf/entry/by-pods", s.handleEBPFEntryDeleteByPods).Methods("DELETE")
	api.HandleFunc("/health", s.healthCheck).Methods("GET")
}

func (s *Server) GetRouter() *mux.Router {
	return s.router
}

func (s *Server) listEmuNets(w http.ResponseWriter, r *http.Request) {
	emunetList := &emunetv1.EmuNetList{}
	if err := s.client.List(r.Context(), emunetList); err != nil {
		s.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sendSuccess(w, emunetList.Items)
}

func (s *Server) getEmuNet(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]

	emunet := &emunetv1.EmuNet{}
	if err := s.client.Get(r.Context(), types.NamespacedName{Name: name, Namespace: namespace}, emunet); err != nil {
		s.sendError(w, http.StatusNotFound, err.Error())
		return
	}

	s.sendSuccess(w, emunet)
}

func (s *Server) getEmuNetStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]

	redisStatus, err := s.redis.GetEmuNetStatus(r.Context(), namespace, name)
	if err == nil && redisStatus != nil {
		s.sendSuccess(w, redisStatus)
		return
	}

	emunet := &emunetv1.EmuNet{}
	if err := s.client.Get(r.Context(), types.NamespacedName{Name: name, Namespace: namespace}, emunet); err != nil {
		s.sendError(w, http.StatusNotFound, err.Error())
		return
	}

	s.sendSuccess(w, emunet.Status)
}

func (s *Server) listPods(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]

	pods, err := s.redis.ListPodStatuses(r.Context(), namespace, name)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.sendSuccess(w, pods)
}

func (s *Server) getPod(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]
	podName := vars["podName"]

	pod, err := s.redis.GetPodStatus(r.Context(), namespace, name, podName)
	if err != nil {
		s.sendError(w, http.StatusNotFound, err.Error())
		return
	}

	s.sendSuccess(w, pod)
}

func (s *Server) healthCheck(w http.ResponseWriter, r *http.Request) {
	s.sendSuccess(w, map[string]string{"status": "healthy"})
}

func (s *Server) sendSuccess(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(Response{
		Success: true,
		Data:    data,
	})
}

func (s *Server) sendError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(Response{
		Success: false,
		Error:   message,
	})
}

func (s *Server) handleEBPFEntryByPods(w http.ResponseWriter, r *http.Request) {
	var req EBPFEntryByPodsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	if req.Pod1 == "" || req.Pod2 == "" {
		s.sendError(w, http.StatusBadRequest, "pod1 and pod2 are required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	emunetList := &emunetv1.EmuNetList{}
	if err := s.client.List(ctx, emunetList); err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to list EmuNets: %v", err))
		return
	}

	var pod1Info, pod2Info *redis.PodStatus
	var pod1Namespace, pod2Namespace string
	var pod1EmuNet, pod2EmuNet string

	for _, emunet := range emunetList.Items {
		pods, err := s.redis.ListPodStatuses(ctx, emunet.Namespace, emunet.Name)
		if err != nil {
			continue
		}

		for _, pod := range pods {
			if pod.PodName == req.Pod1 {
				pod1Info = &pod
				pod1Namespace = emunet.Namespace
				pod1EmuNet = emunet.Name
			}
			if pod.PodName == req.Pod2 {
				pod2Info = &pod
				pod2Namespace = emunet.Namespace
				pod2EmuNet = emunet.Name
			}
		}
	}

	if pod1Info == nil {
		s.sendError(w, http.StatusNotFound, fmt.Sprintf("Pod '%s' not found in Redis", req.Pod1))
		return
	}

	if pod2Info == nil {
		s.sendError(w, http.StatusNotFound, fmt.Sprintf("Pod '%s' not found in Redis", req.Pod2))
		return
	}

	if pod1Info.VethIfIndex == 0 {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("Pod '%s' has no valid ifindex", req.Pod1))
		return
	}

	if pod2Info.VethIfIndex == 0 {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("Pod '%s' has no valid ifindex", req.Pod2))
		return
	}

	if pod1Info.MACAddress == "" {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("Pod '%s' has no MAC address", req.Pod1))
		return
	}

	if pod2Info.MACAddress == "" {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("Pod '%s' has no MAC address", req.Pod2))
		return
	}

	if pod1Info.NodeName == "" {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("Pod '%s' has no node name", req.Pod1))
		return
	}

	if pod2Info.NodeName == "" {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("Pod '%s' has no node name", req.Pod2))
		return
	}

	var results []map[string]interface{}

	ebpfReq1 := EBPFEntryRequest{
		Ifindex:         uint32(pod2Info.VethIfIndex),
		SrcMac:          pod1Info.MACAddress,
		ThrottleRateBps: req.ThrottleRateBps,
		Delay:           req.Delay,
		LossRate:        req.LossRate,
		Jitter:          req.Jitter,
	}

	reqBody1, err := json.Marshal(ebpfReq1)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to marshal request: %v", err))
		return
	}

	targetURL1 := fmt.Sprintf("http://%s:12345/api/ebpf/entry", pod2Info.NodeName)
	httpReq1, err := http.NewRequestWithContext(ctx, "POST", targetURL1, bytes.NewBuffer(reqBody1))
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create request: %v", err))
		return
	}

	httpReq1.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp1, err := client.Do(httpReq1)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to send request to node %s: %v", pod2Info.NodeName, err))
		return
	}
	defer resp1.Body.Close()

	respBody1, err := io.ReadAll(resp1.Body)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to read response: %v", err))
		return
	}

	if resp1.StatusCode != http.StatusOK {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Node %s returned error: %s", pod2Info.NodeName, string(respBody1)))
		return
	}

	results = append(results, map[string]interface{}{
		"direction": fmt.Sprintf("%s -> %s", req.Pod1, req.Pod2),
		"node":      pod2Info.NodeName,
		"ifindex":   pod2Info.VethIfIndex,
		"srcMac":    pod1Info.MACAddress,
		"status":    "success",
	})

	ebpfReq2 := EBPFEntryRequest{
		Ifindex:         uint32(pod1Info.VethIfIndex),
		SrcMac:          pod2Info.MACAddress,
		ThrottleRateBps: req.ThrottleRateBps,
		Delay:           req.Delay,
		LossRate:        req.LossRate,
		Jitter:          req.Jitter,
	}

	reqBody2, err := json.Marshal(ebpfReq2)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to marshal request: %v", err))
		return
	}

	targetURL2 := fmt.Sprintf("http://%s:12345/api/ebpf/entry", pod1Info.NodeName)
	httpReq2, err := http.NewRequestWithContext(ctx, "POST", targetURL2, bytes.NewBuffer(reqBody2))
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create request: %v", err))
		return
	}

	httpReq2.Header.Set("Content-Type", "application/json")

	resp2, err := client.Do(httpReq2)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to send request to node %s: %v", pod1Info.NodeName, err))
		return
	}
	defer resp2.Body.Close()

	respBody2, err := io.ReadAll(resp2.Body)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to read response: %v", err))
		return
	}

	if resp2.StatusCode != http.StatusOK {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Node %s returned error: %s", pod1Info.NodeName, string(respBody2)))
		return
	}

	results = append(results, map[string]interface{}{
		"direction": fmt.Sprintf("%s -> %s", req.Pod2, req.Pod1),
		"node":      pod1Info.NodeName,
		"ifindex":   pod1Info.VethIfIndex,
		"srcMac":    pod2Info.MACAddress,
		"status":    "success",
	})

	s.sendSuccess(w, map[string]interface{}{
		"message": "修改链路参数成功",
		"pod1": map[string]interface{}{
			"pod":       req.Pod1,
			"node":      pod1Info.NodeName,
			"ifindex":   pod1Info.VethIfIndex,
			"mac":       pod1Info.MACAddress,
			"namespace": pod1Namespace,
			"emunet":    pod1EmuNet,
		},
		"pod2": map[string]interface{}{
			"pod":       req.Pod2,
			"node":      pod2Info.NodeName,
			"ifindex":   pod2Info.VethIfIndex,
			"mac":       pod2Info.MACAddress,
			"namespace": pod2Namespace,
			"emunet":    pod2EmuNet,
		},
		"parameters": map[string]interface{}{
			"throttleRateBps": req.ThrottleRateBps,
			"delay":           req.Delay,
			"lossRate":        req.LossRate,
			"jitter":          req.Jitter,
		},
		"results": results,
	})
}

func (s *Server) handleEBPFEntryDeleteByPods(w http.ResponseWriter, r *http.Request) {
	var req EBPFEntryDeleteByPodsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	if req.Pod1 == "" || req.Pod2 == "" {
		s.sendError(w, http.StatusBadRequest, "pod1 and pod2 are required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	emunetList := &emunetv1.EmuNetList{}
	if err := s.client.List(ctx, emunetList); err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to list EmuNets: %v", err))
		return
	}

	var pod1Info, pod2Info *redis.PodStatus
	var pod1Namespace, pod2Namespace string
	var pod1EmuNet, pod2EmuNet string

	for _, emunet := range emunetList.Items {
		pods, err := s.redis.ListPodStatuses(ctx, emunet.Namespace, emunet.Name)
		if err != nil {
			continue
		}

		for _, pod := range pods {
			if pod.PodName == req.Pod1 {
				pod1Info = &pod
				pod1Namespace = emunet.Namespace
				pod1EmuNet = emunet.Name
			}
			if pod.PodName == req.Pod2 {
				pod2Info = &pod
				pod2Namespace = emunet.Namespace
				pod2EmuNet = emunet.Name
			}
		}
	}

	if pod1Info == nil {
		s.sendError(w, http.StatusNotFound, fmt.Sprintf("Pod '%s' not found in Redis", req.Pod1))
		return
	}

	if pod2Info == nil {
		s.sendError(w, http.StatusNotFound, fmt.Sprintf("Pod '%s' not found in Redis", req.Pod2))
		return
	}

	if pod1Info.VethIfIndex == 0 {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("Pod '%s' has no valid ifindex", req.Pod1))
		return
	}

	if pod2Info.VethIfIndex == 0 {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("Pod '%s' has no valid ifindex", req.Pod2))
		return
	}

	if pod1Info.MACAddress == "" {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("Pod '%s' has no MAC address", req.Pod1))
		return
	}

	if pod2Info.MACAddress == "" {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("Pod '%s' has no MAC address", req.Pod2))
		return
	}

	if pod1Info.NodeName == "" {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("Pod '%s' has no node name", req.Pod1))
		return
	}

	if pod2Info.NodeName == "" {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("Pod '%s' has no node name", req.Pod2))
		return
	}

	var results []map[string]interface{}

	ebpfDeleteReq1 := EBPFEntryDeleteRequest{
		Ifindex: uint32(pod2Info.VethIfIndex),
		SrcMac:  pod1Info.MACAddress,
	}

	reqBody1, err := json.Marshal(ebpfDeleteReq1)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to marshal request: %v", err))
		return
	}

	targetURL1 := fmt.Sprintf("http://%s:12345/api/ebpf/entry", pod2Info.NodeName)
	httpReq1, err := http.NewRequestWithContext(ctx, "DELETE", targetURL1, bytes.NewBuffer(reqBody1))
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create request: %v", err))
		return
	}

	httpReq1.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp1, err := client.Do(httpReq1)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to send request to node %s: %v", pod2Info.NodeName, err))
		return
	}
	defer resp1.Body.Close()

	respBody1, err := io.ReadAll(resp1.Body)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to read response: %v", err))
		return
	}

	if resp1.StatusCode != http.StatusOK {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Node %s returned error: %s", pod2Info.NodeName, string(respBody1)))
		return
	}

	results = append(results, map[string]interface{}{
		"direction": fmt.Sprintf("%s -> %s", req.Pod1, req.Pod2),
		"node":      pod2Info.NodeName,
		"ifindex":   pod2Info.VethIfIndex,
		"srcMac":    pod1Info.MACAddress,
		"status":    "success",
	})

	ebpfDeleteReq2 := EBPFEntryDeleteRequest{
		Ifindex: uint32(pod1Info.VethIfIndex),
		SrcMac:  pod2Info.MACAddress,
	}

	reqBody2, err := json.Marshal(ebpfDeleteReq2)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to marshal request: %v", err))
		return
	}

	targetURL2 := fmt.Sprintf("http://%s:12345/api/ebpf/entry", pod1Info.NodeName)
	httpReq2, err := http.NewRequestWithContext(ctx, "DELETE", targetURL2, bytes.NewBuffer(reqBody2))
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create request: %v", err))
		return
	}

	httpReq2.Header.Set("Content-Type", "application/json")

	resp2, err := client.Do(httpReq2)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to send request to node %s: %v", pod2Info.NodeName, err))
		return
	}
	defer resp2.Body.Close()

	respBody2, err := io.ReadAll(resp2.Body)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to read response: %v", err))
		return
	}

	if resp2.StatusCode != http.StatusOK {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("Node %s returned error: %s", pod2Info.NodeName, string(respBody2)))
		return
	}

	results = append(results, map[string]interface{}{
		"direction": fmt.Sprintf("%s -> %s", req.Pod2, req.Pod1),
		"node":      pod2Info.NodeName,
		"ifindex":   pod2Info.VethIfIndex,
		"srcMac":    pod1Info.MACAddress,
		"status":    "success",
	})

	s.sendSuccess(w, map[string]interface{}{
		"message": "修改链路参数成功",
		"pod1": map[string]interface{}{
			"pod":       req.Pod1,
			"node":      pod1Info.NodeName,
			"ifindex":   pod1Info.VethIfIndex,
			"mac":       pod1Info.MACAddress,
			"namespace": pod1Namespace,
			"emunet":    pod1EmuNet,
		},
		"pod2": map[string]interface{}{
			"pod":       req.Pod2,
			"node":      pod2Info.NodeName,
			"ifindex":   pod2Info.VethIfIndex,
			"mac":       pod2Info.MACAddress,
			"namespace": pod2Namespace,
			"emunet":    pod2EmuNet,
		},
		"results": results,
	})
}
