package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controlleropts "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	emunetv1 "github.com/emunet/emunet-operator/master/controller/api/v1"
	"github.com/emunet/emunet-operator/pkg/redis"
)

type EmuNetReconciler struct {
	client.Client
	Scheme                  *runtime.Scheme
	Redis                   *redis.Client
	PodCreateBatchSize      int
	PodDeleteBatchSize      int
	FullStatusSyncPeriod    time.Duration
	MaxConcurrentReconciles int

	statusWriteMu            sync.Mutex
	lastFullStatusWrite      map[string]time.Time
	lastFullStatusGeneration map[string]int64
}

const (
	EmuNetFinalizer     = "emunet.emunet.io/finalizer"
	EmuNetLabelKey      = "emunet.emunet.io/name"
	EmuNetGroupLabelKey = "emunet.emunet.io/image-group"

	// 轮询间隔：未就绪时快，就绪后慢
	SyncPeriodFast        = 3 * time.Second
	SyncPeriodSlow        = 30 * time.Second
	DefaultPodCreateBatch = 100
	DefaultPodDeleteBatch = 100
	DefaultFullStatusSync = 15 * time.Second
)

type podSyncResult struct {
	Created  int
	Updated  int
	Deleted  int
	Requeue  bool
	Existing int
	Desired  int
	PodMap   map[string]*corev1.Pod
}

// Reconcile is the main loop
func (r *EmuNetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	emunet := &emunetv1.EmuNet{}
	if err := r.Get(ctx, req.NamespacedName, emunet); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get emunet")
		return ctrl.Result{}, err
	}

	// Handle Deletion
	if !emunet.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, req.NamespacedName, emunet)
	}

	// Add Finalizer if missing
	if !controllerutil.ContainsFinalizer(emunet, EmuNetFinalizer) {
		controllerutil.AddFinalizer(emunet, EmuNetFinalizer)
		if err := r.Update(ctx, emunet); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// 1. Sync Pods (Create/Update/Delete)
	syncResult, err := r.syncPods(ctx, emunet)
	if err != nil {
		logger.Error(err, "failed to sync pods")
		return ctrl.Result{}, err
	}

	// 2. Update Status (K8s Status & Redis Cache)
	// 这个方法现在返回一个 bool，指示系统是否已经完全 Ready
	isReady, err := r.updateStatus(ctx, emunet, syncResult)
	if err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	if syncResult.Requeue {
		logger.Info("pod sync is throttled; requeueing", "created", syncResult.Created, "updated", syncResult.Updated, "deleted", syncResult.Deleted, "existing", syncResult.Existing, "desired", syncResult.Desired)
		return ctrl.Result{RequeueAfter: SyncPeriodFast}, nil
	}

	// 3. 智能轮询 (Smart Polling)
	if isReady {
		return ctrl.Result{RequeueAfter: SyncPeriodSlow}, nil
	}
	return ctrl.Result{RequeueAfter: SyncPeriodFast}, nil
}

func (r *EmuNetReconciler) syncPods(ctx context.Context, emunet *emunetv1.EmuNet) (podSyncResult, error) {
	existingPods := &corev1.PodList{}
	listOptions := []client.ListOption{
		client.InNamespace(emunet.Namespace),
		client.MatchingLabels{EmuNetLabelKey: emunet.Name},
	}

	if err := r.List(ctx, existingPods, listOptions...); err != nil {
		return podSyncResult{}, err
	}

	existingPodMap := make(map[string]*corev1.Pod)
	for i := range existingPods.Items {
		existingPodMap[existingPods.Items[i].Name] = &existingPods.Items[i]
	}

	desiredPods := make(map[string]bool)
	result := podSyncResult{
		Existing: len(existingPodMap),
		Desired:  int(desiredReplicaCount(emunet)),
		PodMap:   existingPodMap,
	}

	// Reconcile desired state
	for groupIdx, imageGroup := range emunet.Spec.ImageGroups {
		for podIdx := int32(0); podIdx < imageGroup.Replicas; podIdx++ {
			podName := fmt.Sprintf("%s-group%d-%d", emunet.Name, groupIdx, podIdx)
			desiredPods[podName] = true

			if existingPod, exists := existingPodMap[podName]; exists {
				if existingPod.Spec.Containers[0].Image != imageGroup.Image {
					if err := r.updateExistingPod(ctx, existingPod, imageGroup.Image); err != nil {
						return result, err
					}
					result.Updated++
				}
			} else {
				if result.Created >= r.podCreateBatchSize() {
					result.Requeue = true
					continue
				}
				if err := r.createNewPod(ctx, emunet, groupIdx, podIdx, imageGroup); err != nil {
					return result, err
				}
				result.Created++
			}
		}
	}

	// Cleanup extraneous pods
	for podName, pod := range existingPodMap {
		if !desiredPods[podName] && pod.DeletionTimestamp == nil {
			if result.Deleted >= r.podDeleteBatchSize() {
				result.Requeue = true
				continue
			}
			if err := r.Delete(ctx, pod); err != nil && !errors.IsNotFound(err) {
				return result, err
			}
			result.Deleted++
		}
	}

	return result, nil
}

// updateStatus returns (isReady, error)
func (r *EmuNetReconciler) updateStatus(ctx context.Context, emunet *emunetv1.EmuNet, syncResult podSyncResult) (bool, error) {
	logger := log.FromContext(ctx)
	podMap := syncResult.PodMap

	if podMap == nil {
		podMap = map[string]*corev1.Pod{}
	}

	// [关键] 从 Redis 拉取最新 MAC 信息
	imageGroupStatus, totalReady, allMacsFound := r.calculateImageGroupStatus(ctx, emunet, podMap)
	summary := buildRedisSummary(emunet, imageGroupStatus, totalReady)
	desiredReplicas := desiredReplicaCount(emunet)

	// 1. Update K8s Status (Optimistic Locking via Patch)
	newStatus := emunet.Status.DeepCopy()
	newStatus.ReadyReplicas = totalReady
	newStatus.DesiredReplicas = desiredReplicas
	newStatus.ImageGroupStatus = compactImageGroupStatus(imageGroupStatus)
	newStatus.ObservedGen = emunet.Generation

	// Only patch if something changed
	if !equality.Semantic.DeepEqual(emunet.Status, *newStatus) {
		patch := client.MergeFrom(emunet.DeepCopy())
		emunet.Status = *newStatus
		if err := r.Status().Patch(ctx, emunet, patch); err != nil {
			return false, err
		}
		logger.Info("updated emunet status", "ready", totalReady, "target", desiredReplicas)
	}

	// 2. Update Redis
	// 将合并了 (Agent MAC) + (K8s IP) 的完整信息写回 Redis (写到 pod_lookup)
	redisStatus := &redis.EmuNetStatus{
		Name:             emunet.Name,
		Namespace:        emunet.Namespace,
		ReadyReplicas:    totalReady,
		DesiredReplicas:  desiredReplicas,
		ObservedGen:      emunet.Generation,
		ImageGroupStatus: convertImageGroupStatus(imageGroupStatus),
		LastUpdated:      time.Now(),
	}

	forceFullStatusWrite := syncResult.Created > 0 || syncResult.Updated > 0 || syncResult.Deleted > 0
	if err := r.saveStatusToRedis(ctx, emunet, redisStatus, summary, forceFullStatusWrite); err != nil {
		logger.Error(err, "failed to update redis cache")
	}

	// 判断系统是否完全就绪
	isFullyReady := (totalReady == desiredReplicas) && allMacsFound
	return isFullyReady, nil
}

func (r *EmuNetReconciler) saveStatusToRedis(ctx context.Context, emunet *emunetv1.EmuNet, status *redis.EmuNetStatus, summary *redis.EmuNetSummary, forceFull bool) error {
	if !r.shouldWriteFullStatus(emunet, forceFull) {
		return r.Redis.SaveSummary(ctx, summary)
	}

	var allPods []redis.PodStatus
	for _, group := range status.ImageGroupStatus {
		for _, pod := range group.PodStatuses {
			allPods = append(allPods, pod)
		}
	}
	return r.Redis.SaveStatusBatch(ctx, status, allPods, summary)
}

func (r *EmuNetReconciler) shouldWriteFullStatus(emunet *emunetv1.EmuNet, force bool) bool {
	r.statusWriteMu.Lock()
	defer r.statusWriteMu.Unlock()

	if r.lastFullStatusWrite == nil {
		r.lastFullStatusWrite = map[string]time.Time{}
	}
	if r.lastFullStatusGeneration == nil {
		r.lastFullStatusGeneration = map[string]int64{}
	}

	key := fmt.Sprintf("%s/%s", emunet.Namespace, emunet.Name)
	now := time.Now()
	period := r.fullStatusSyncPeriod()
	if force || r.lastFullStatusGeneration[key] != emunet.Generation || now.Sub(r.lastFullStatusWrite[key]) >= period {
		r.lastFullStatusWrite[key] = now
		r.lastFullStatusGeneration[key] = emunet.Generation
		return true
	}
	return false
}

func (r *EmuNetReconciler) podCreateBatchSize() int {
	if r.PodCreateBatchSize > 0 {
		return r.PodCreateBatchSize
	}
	return DefaultPodCreateBatch
}

func (r *EmuNetReconciler) podDeleteBatchSize() int {
	if r.PodDeleteBatchSize > 0 {
		return r.PodDeleteBatchSize
	}
	return DefaultPodDeleteBatch
}

func (r *EmuNetReconciler) fullStatusSyncPeriod() time.Duration {
	if r.FullStatusSyncPeriod > 0 {
		return r.FullStatusSyncPeriod
	}
	return DefaultFullStatusSync
}

func (r *EmuNetReconciler) handleDeletion(ctx context.Context, nn types.NamespacedName, emunet *emunetv1.EmuNet) (ctrl.Result, error) {
	if err := r.Redis.DeleteEmuNetStatus(ctx, nn.Namespace, nn.Name); err != nil {
		log.FromContext(ctx).Error(err, "failed to cleanup redis status")
	}

	controllerutil.RemoveFinalizer(emunet, EmuNetFinalizer)
	if err := r.Update(ctx, emunet); err != nil {
		return ctrl.Result{}, err
	}
	r.forgetStatusWriteState(nn)

	return ctrl.Result{}, nil
}

func (r *EmuNetReconciler) forgetStatusWriteState(nn types.NamespacedName) {
	r.statusWriteMu.Lock()
	defer r.statusWriteMu.Unlock()

	key := fmt.Sprintf("%s/%s", nn.Namespace, nn.Name)
	delete(r.lastFullStatusWrite, key)
	delete(r.lastFullStatusGeneration, key)
}

// --- Helpers ---

// calculateImageGroupStatus 返回：Status 列表, Ready 数量, 是否所有 Running Pod 的 MAC 都找到了
func (r *EmuNetReconciler) calculateImageGroupStatus(ctx context.Context, emunet *emunetv1.EmuNet, podMap map[string]*corev1.Pod) ([]emunetv1.ImageGroupStatus, int32, bool) {
	var imageGroupStatus []emunetv1.ImageGroupStatus
	var totalReady int32
	allMacsFound := true
	desiredPodNames := desiredPodNames(emunet)
	agentInfo, err := r.Redis.GetAgentNetworkInfoBatch(ctx, desiredPodNames)
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to read agent network cache")
		agentInfo = map[string]*redis.PodStatus{}
	}

	for groupIdx, imageGroup := range emunet.Spec.ImageGroups {
		groupStatus := emunetv1.ImageGroupStatus{
			Image:           imageGroup.Image,
			DesiredReplicas: imageGroup.Replicas,
			PodStatuses:     make([]emunetv1.PodStatus, 0, imageGroup.Replicas),
		}

		for podIdx := int32(0); podIdx < imageGroup.Replicas; podIdx++ {
			podName := fmt.Sprintf("%s-group%d-%d", emunet.Name, groupIdx, podIdx)

			podStatus := emunetv1.PodStatus{
				PodName:     podName,
				Image:       imageGroup.Image,
				Phase:       corev1.PodPending,
				Ready:       false,
				LastUpdated: metav1.Now(),
			}

			// 2. 更新 K8s 实时状态
			if pod, exists := podMap[podName]; exists {
				podStatus.Phase = pod.Status.Phase
				podStatus.PodIP = pod.Status.PodIP
				podStatus.NodeName = pod.Spec.NodeName
				podStatus.Ready = isPodReady(pod)
				podStatus.Message = getPodMessage(pod)

				// 3. [核心修改] 尝试从 Agent 专用 Key 读取 MAC/IfIndex
				// 使用批量读取的 agent:network:* 缓存，避免大规模 Pod 时逐个 Redis 往返。
				if redisInfo := agentInfo[podName]; redisInfo != nil {
					if redisInfo.MACAddress != "" {
						podStatus.MACAddress = redisInfo.MACAddress
					}
					if redisInfo.VethIfIndex != 0 {
						podStatus.VethIfIndex = redisInfo.VethIfIndex
					}
				}

				// 检查是否缺失 MAC
				if podStatus.Phase == corev1.PodRunning && podStatus.MACAddress == "" {
					allMacsFound = false
				}
			}

			if podStatus.Ready {
				groupStatus.ReadyReplicas++
				totalReady++
			}
			groupStatus.PodStatuses = append(groupStatus.PodStatuses, podStatus)
		}
		imageGroupStatus = append(imageGroupStatus, groupStatus)
	}
	return imageGroupStatus, totalReady, allMacsFound
}

func desiredReplicaCount(emunet *emunetv1.EmuNet) int32 {
	var total int32
	for _, group := range emunet.Spec.ImageGroups {
		total += group.Replicas
	}
	return total
}

func desiredPodNames(emunet *emunetv1.EmuNet) []string {
	podNames := make([]string, 0, desiredReplicaCount(emunet))
	for groupIdx, imageGroup := range emunet.Spec.ImageGroups {
		for podIdx := int32(0); podIdx < imageGroup.Replicas; podIdx++ {
			podNames = append(podNames, fmt.Sprintf("%s-group%d-%d", emunet.Name, groupIdx, podIdx))
		}
	}
	return podNames
}

func (r *EmuNetReconciler) updateExistingPod(ctx context.Context, pod *corev1.Pod, desiredImage string) error {
	patch := client.MergeFrom(pod.DeepCopy())
	pod.Spec.Containers[0].Image = desiredImage
	return r.Patch(ctx, pod, patch)
}

func (r *EmuNetReconciler) createNewPod(ctx context.Context, emunet *emunetv1.EmuNet, groupIdx int, podIdx int32, imageGroup emunetv1.ImageGroup) error {
	podName := fmt.Sprintf("%s-group%d-%d", emunet.Name, groupIdx, podIdx)
	newPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: emunet.Namespace,
			Labels: map[string]string{
				EmuNetLabelKey:      emunet.Name,
				EmuNetGroupLabelKey: fmt.Sprintf("group%d", groupIdx),
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "emunet-pod",
				Image: imageGroup.Image,
			}},
		},
	}

	if emunet.Spec.Selector != nil {
		for k, v := range emunet.Spec.Selector.MatchLabels {
			newPod.Labels[k] = v
		}
	}

	if err := ctrl.SetControllerReference(emunet, newPod, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, newPod)
}

func convertImageGroupStatus(k8sStatus []emunetv1.ImageGroupStatus) []redis.ImageGroupStatus {
	var redisStatus []redis.ImageGroupStatus
	for _, k8sGroup := range k8sStatus {
		redisStatus = append(redisStatus, redis.ImageGroupStatus{
			Image:           k8sGroup.Image,
			DesiredReplicas: k8sGroup.DesiredReplicas,
			ReadyReplicas:   k8sGroup.ReadyReplicas,
			PodStatuses:     convertPodStatuses(k8sGroup.PodStatuses),
		})
	}
	return redisStatus
}

func convertPodStatuses(k8sPods []emunetv1.PodStatus) []redis.PodStatus {
	var redisPods []redis.PodStatus
	for _, k8sPod := range k8sPods {
		redisPods = append(redisPods, redis.PodStatus{
			PodName:     k8sPod.PodName,
			Image:       k8sPod.Image,
			PodIP:       k8sPod.PodIP,
			NodeName:    k8sPod.NodeName,
			Phase:       string(k8sPod.Phase),
			Ready:       k8sPod.Ready,
			Message:     k8sPod.Message,
			MACAddress:  k8sPod.MACAddress,
			VethIfIndex: k8sPod.VethIfIndex,
			LastUpdated: k8sPod.LastUpdated.Time,
		})
	}
	return redisPods
}

func compactImageGroupStatus(fullStatus []emunetv1.ImageGroupStatus) []emunetv1.ImageGroupStatus {
	compact := make([]emunetv1.ImageGroupStatus, 0, len(fullStatus))
	for _, group := range fullStatus {
		compact = append(compact, emunetv1.ImageGroupStatus{
			Image:           group.Image,
			DesiredReplicas: group.DesiredReplicas,
			ReadyReplicas:   group.ReadyReplicas,
			PodStatuses:     []emunetv1.PodStatus{},
		})
	}
	return compact
}

func buildRedisSummary(emunet *emunetv1.EmuNet, imageGroupStatus []emunetv1.ImageGroupStatus, totalReady int32) *redis.EmuNetSummary {
	var runningReplicas int32
	var macSyncedReplicas int32
	nodes := make(map[string]struct{})

	for _, group := range imageGroupStatus {
		for _, pod := range group.PodStatuses {
			if pod.Phase == corev1.PodRunning {
				runningReplicas++
			}
			if pod.MACAddress != "" && pod.VethIfIndex != 0 {
				macSyncedReplicas++
			}
			if pod.NodeName != "" {
				nodes[pod.NodeName] = struct{}{}
			}
		}
	}

	return &redis.EmuNetSummary{
		Name:              emunet.Name,
		Namespace:         emunet.Namespace,
		DesiredReplicas:   desiredReplicaCount(emunet),
		ReadyReplicas:     totalReady,
		RunningReplicas:   runningReplicas,
		MACSyncedReplicas: macSyncedReplicas,
		NodeCount:         int32(len(nodes)),
		ObservedGen:       emunet.Generation,
		LastUpdated:       time.Now(),
	}
}

func isPodReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func getPodMessage(pod *corev1.Pod) string {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Message
		}
	}
	return ""
}

func (r *EmuNetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	maxConcurrent := r.MaxConcurrentReconciles
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&emunetv1.EmuNet{}).
		Owns(&corev1.Pod{}).
		WithOptions(controlleropts.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}
