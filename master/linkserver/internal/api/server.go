package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	emunetv1 "github.com/emunet/emunet-operator/master/controller/api/v1"
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

type PingByPodsRequest struct {
	Namespace      string `json:"namespace,omitempty"`
	Pod1           string `json:"pod1"`
	Pod2           string `json:"pod2"`
	Count          int    `json:"count,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
}

type PingResult struct {
	SourcePod      string `json:"sourcePod"`
	TargetPod      string `json:"targetPod"`
	TargetIP       string `json:"targetIP"`
	Count          int    `json:"count"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
	Stdout         string `json:"stdout"`
	Stderr         string `json:"stderr,omitempty"`
}

type EmuNetApplyRequest struct {
	Namespace     string             `json:"namespace"`
	Name          string             `json:"name"`
	TotalReplicas int32              `json:"totalReplicas"`
	ImageGroups   []EmuNetImageGroup `json:"imageGroups"`
	Selector      map[string]string  `json:"selector,omitempty"`
}

type EmuNetImageGroup struct {
	Image    string `json:"image"`
	Replicas int32  `json:"replicas"`
}

type EmuNetSummary struct {
	Namespace       string             `json:"namespace"`
	Name            string             `json:"name"`
	TotalReplicas   int32              `json:"totalReplicas"`
	ReadyReplicas   int32              `json:"readyReplicas"`
	DesiredReplicas int32              `json:"desiredReplicas"`
	ImageGroups     []EmuNetImageGroup `json:"imageGroups"`
	ObservedGen     int64              `json:"observedGen"`
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
	redis     *redis.Client
	k8sClient client.Client
	clientset kubernetes.Interface
	restCfg   *rest.Config
	router    *mux.Router
	logger    *zap.Logger
	webDir    string

	ctx    context.Context
	cancel context.CancelFunc
}

// NewMasterServer 初始化
func NewMasterServer(redisClient *redis.Client, k8sClient client.Client, clientset kubernetes.Interface, restCfg *rest.Config, logger *zap.Logger, webDir string) *MasterServer {
	ctx, cancel := context.WithCancel(context.Background())

	s := &MasterServer{
		redis:     redisClient,
		k8sClient: k8sClient,
		clientset: clientset,
		restCfg:   restCfg,
		logger:    logger,
		router:    mux.NewRouter(),
		webDir:    webDir,
		ctx:       ctx,
		cancel:    cancel,
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

	v1.HandleFunc("/ping/by-pods", s.handlePingByPods).Methods("POST")

	v1.HandleFunc("/ebpf/entry/by-pods", s.handleRuleCreate).Methods("POST")
	v1.HandleFunc("/ebpf/entry/by-pods", s.handleRuleDelete).Methods("DELETE")

	v1.HandleFunc("/emunets", s.listEmuNets).Methods("GET")
	v1.HandleFunc("/emunets", s.applyEmuNet).Methods("POST")
	v1.HandleFunc("/emunets/{namespace}/{name}", s.getEmuNet).Methods("GET")
	v1.HandleFunc("/emunets/{namespace}/{name}", s.applyEmuNet).Methods("PUT")
	v1.HandleFunc("/emunets/{namespace}/{name}", s.deleteEmuNet).Methods("DELETE")
	v1.HandleFunc("/emunets/{namespace}/{name}/stop", s.deleteEmuNet).Methods("POST")
	v1.HandleFunc("/emunets/{namespace}/{name}/pods", s.listPodsFromCache).Methods("GET")

	if s.webDir != "" {
		s.router.PathPrefix("/").Handler(http.FileServer(http.Dir(s.webDir)))
	}
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

func (s *MasterServer) handlePingByPods(w http.ResponseWriter, r *http.Request) {
	var req PingByPodsRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}
	if req.Namespace == "" {
		req.Namespace = "default"
	}
	if req.Pod1 == "" || req.Pod2 == "" {
		s.sendError(w, http.StatusBadRequest, "pod1 and pod2 are required")
		return
	}
	if req.Pod1 == req.Pod2 {
		s.sendError(w, http.StatusBadRequest, "pod1 and pod2 must be different")
		return
	}
	if req.Count <= 0 {
		req.Count = 4
	}
	if req.Count > 10 {
		req.Count = 10
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 2
	}
	if req.TimeoutSeconds > 10 {
		req.TimeoutSeconds = 10
	}

	targetInfo, err := s.redis.GetPodInfoDirectly(r.Context(), req.Pod2)
	if err != nil || targetInfo == nil || targetInfo.PodIP == "" {
		s.sendError(w, http.StatusNotFound, "target Pod IP not found in cache")
		return
	}

	sourcePod := &corev1.Pod{}
	if err := s.k8sClient.Get(r.Context(), types.NamespacedName{Namespace: req.Namespace, Name: req.Pod1}, sourcePod); err != nil {
		if apierrors.IsNotFound(err) {
			s.sendError(w, http.StatusNotFound, "source Pod not found")
			return
		}
		s.logger.Error("failed to get source pod", zap.Error(err))
		s.sendError(w, http.StatusInternalServerError, "Failed to get source Pod")
		return
	}
	if sourcePod.Status.Phase != corev1.PodRunning {
		s.sendError(w, http.StatusPreconditionFailed, "source Pod is not running")
		return
	}
	if len(sourcePod.Spec.Containers) == 0 {
		s.sendError(w, http.StatusPreconditionFailed, "source Pod has no containers")
		return
	}

	stdout, stderr, err := s.execPing(r.Context(), sourcePod, targetInfo.PodIP, req.Count, req.TimeoutSeconds)
	result := PingResult{
		SourcePod:      req.Pod1,
		TargetPod:      req.Pod2,
		TargetIP:       targetInfo.PodIP,
		Count:          req.Count,
		TimeoutSeconds: req.TimeoutSeconds,
		Stdout:         stdout,
		Stderr:         stderr,
	}
	if err != nil {
		s.logger.Warn("ping command failed", zap.Error(err), zap.String("stdout", stdout), zap.String("stderr", stderr))
		s.sendError(w, http.StatusBadGateway, fmt.Sprintf("Ping failed: %v\n%s", err, stderr))
		return
	}
	s.sendSuccess(w, result)
}

func (s *MasterServer) execPing(ctx context.Context, pod *corev1.Pod, targetIP string, count int, timeoutSeconds int) (string, string, error) {
	command := []string{
		"ping",
		"-c", fmt.Sprintf("%d", count),
		"-W", fmt.Sprintf("%d", timeoutSeconds),
		targetIP,
	}
	req := s.clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: pod.Spec.Containers[0].Name,
			Command:   command,
			Stdout:    true,
			Stderr:    true,
		}, k8sscheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(s.restCfg, http.MethodPost, req.URL())
	if err != nil {
		return "", "", err
	}

	var stdout, stderr bytes.Buffer
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	return stdout.String(), stderr.String(), err
}

// =================================================================================
// 5. Group C: 查询平面 Handlers (只读操作)
// =================================================================================

func (s *MasterServer) listEmuNets(w http.ResponseWriter, r *http.Request) {
	list := &emunetv1.EmuNetList{}
	if err := s.k8sClient.List(r.Context(), list); err != nil {
		s.logger.Error("failed to list emunets", zap.Error(err))
		s.sendError(w, http.StatusInternalServerError, "Failed to list EmuNet resources")
		return
	}

	items := make([]EmuNetSummary, 0, len(list.Items))
	for _, item := range list.Items {
		items = append(items, summarizeEmuNet(&item))
	}

	s.sendSuccess(w, items)
}

func (s *MasterServer) getEmuNet(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	emunet := &emunetv1.EmuNet{}
	if err := s.k8sClient.Get(r.Context(), types.NamespacedName{
		Namespace: vars["namespace"],
		Name:      vars["name"],
	}, emunet); err != nil {
		if apierrors.IsNotFound(err) {
			s.sendError(w, http.StatusNotFound, "EmuNet not found")
			return
		}
		s.logger.Error("failed to get emunet", zap.Error(err))
		s.sendError(w, http.StatusInternalServerError, "Failed to get EmuNet")
		return
	}
	s.sendSuccess(w, emunet)
}

func (s *MasterServer) applyEmuNet(w http.ResponseWriter, r *http.Request) {
	var req EmuNetApplyRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}

	vars := mux.Vars(r)
	if vars["namespace"] != "" {
		req.Namespace = vars["namespace"]
	}
	if vars["name"] != "" {
		req.Name = vars["name"]
	}
	if err := normalizeEmuNetRequest(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, err.Error())
		return
	}

	nn := types.NamespacedName{Namespace: req.Namespace, Name: req.Name}
	existing := &emunetv1.EmuNet{}
	err := s.k8sClient.Get(r.Context(), nn, existing)
	if err != nil && !apierrors.IsNotFound(err) {
		s.logger.Error("failed to check emunet", zap.Error(err))
		s.sendError(w, http.StatusInternalServerError, "Failed to check EmuNet")
		return
	}

	spec := emunetv1.EmuNetSpec{
		TotalReplicas: req.TotalReplicas,
		ImageGroups:   convertImageGroups(req.ImageGroups),
	}
	if len(req.Selector) > 0 {
		spec.Selector = &metav1.LabelSelector{MatchLabels: req.Selector}
	}

	if apierrors.IsNotFound(err) {
		emunet := &emunetv1.EmuNet{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "emunet.emunet.io/v1",
				Kind:       "EmuNet",
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: req.Namespace,
				Name:      req.Name,
			},
			Spec: spec,
		}
		if err := s.k8sClient.Create(r.Context(), emunet); err != nil {
			s.logger.Error("failed to create emunet", zap.Error(err))
			s.sendError(w, http.StatusInternalServerError, "Failed to create EmuNet")
			return
		}
		s.sendSuccess(w, map[string]string{"status": "created"})
		return
	}

	existing.Spec = spec
	if err := s.k8sClient.Update(r.Context(), existing); err != nil {
		s.logger.Error("failed to update emunet", zap.Error(err))
		s.sendError(w, http.StatusInternalServerError, "Failed to update EmuNet")
		return
	}
	s.sendSuccess(w, map[string]string{"status": "updated"})
}

func (s *MasterServer) deleteEmuNet(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	emunet := &emunetv1.EmuNet{}
	err := s.k8sClient.Get(r.Context(), types.NamespacedName{
		Namespace: vars["namespace"],
		Name:      vars["name"],
	}, emunet)
	if err != nil {
		if apierrors.IsNotFound(err) {
			s.sendSuccess(w, map[string]string{"status": "already stopped"})
			return
		}
		s.logger.Error("failed to get emunet for delete", zap.Error(err))
		s.sendError(w, http.StatusInternalServerError, "Failed to get EmuNet")
		return
	}
	if err := s.k8sClient.Delete(r.Context(), emunet); err != nil && !apierrors.IsNotFound(err) {
		s.logger.Error("failed to delete emunet", zap.Error(err))
		s.sendError(w, http.StatusInternalServerError, "Failed to stop EmuNet")
		return
	}
	s.sendSuccess(w, map[string]string{"status": "stopping"})
}

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

func normalizeEmuNetRequest(req *EmuNetApplyRequest) error {
	if req.Namespace == "" {
		req.Namespace = "default"
	}
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	if len(req.ImageGroups) == 0 {
		return fmt.Errorf("at least one image group is required")
	}

	var total int32
	for _, group := range req.ImageGroups {
		if group.Image == "" {
			return fmt.Errorf("image is required for every image group")
		}
		if group.Replicas < 0 {
			return fmt.Errorf("replicas cannot be negative")
		}
		total += group.Replicas
	}
	if req.TotalReplicas == 0 {
		req.TotalReplicas = total
	}
	if req.TotalReplicas != total {
		return fmt.Errorf("totalReplicas must equal the sum of image group replicas")
	}
	return nil
}

func convertImageGroups(groups []EmuNetImageGroup) []emunetv1.ImageGroup {
	result := make([]emunetv1.ImageGroup, 0, len(groups))
	for _, group := range groups {
		result = append(result, emunetv1.ImageGroup{
			Image:    group.Image,
			Replicas: group.Replicas,
		})
	}
	return result
}

func summarizeEmuNet(em *emunetv1.EmuNet) EmuNetSummary {
	return EmuNetSummary{
		Namespace:       em.Namespace,
		Name:            em.Name,
		TotalReplicas:   em.Spec.TotalReplicas,
		ReadyReplicas:   em.Status.ReadyReplicas,
		DesiredReplicas: em.Status.DesiredReplicas,
		ImageGroups:     summarizeImageGroups(em.Spec.ImageGroups),
		ObservedGen:     em.Status.ObservedGen,
	}
}

func summarizeImageGroups(groups []emunetv1.ImageGroup) []EmuNetImageGroup {
	result := make([]EmuNetImageGroup, 0, len(groups))
	for _, group := range groups {
		result = append(result, EmuNetImageGroup{
			Image:    group.Image,
			Replicas: group.Replicas,
		})
	}
	return result
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
