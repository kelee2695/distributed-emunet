package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	emunetv1 "github.com/emunet/emunet-operator/api/v1"
	"github.com/emunet/emunet-operator/internal/redis"
)

type EmuNetReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Redis  *redis.Client
}

const (
	EmuNetFinalizer     = "emunet.emunet.io/finalizer"
	EmuNetLabelKey      = "emunet.emunet.io/name"
	EmuNetGroupLabelKey = "emunet.emunet.io/image-group"
)

func (r *EmuNetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("reconciling", "namespace", req.Namespace, "name", req.Name)

	emunet := &emunetv1.EmuNet{}
	if err := r.Get(ctx, req.NamespacedName, emunet); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("emunet not found, ignoring")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get emunet")
		return ctrl.Result{}, err
	}

	if !emunet.DeletionTimestamp.IsZero() {
		logger.Info("emunet is being deleted")
		return r.handleDeletion(ctx, req.NamespacedName)
	}

	if !controllerutil.ContainsFinalizer(emunet, EmuNetFinalizer) {
		logger.Info("adding finalizer")
		if err := r.addFinalizer(ctx, req.NamespacedName); err != nil {
			logger.Error(err, "failed to add finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	logger.Info("syncing pods")
	if err := r.syncPods(ctx, req.NamespacedName); err != nil {
		logger.Error(err, "failed to sync pods")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, err
	}

	logger.Info("updating status")
	if err := r.updateStatus(ctx, req.NamespacedName); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{RequeueAfter: 2 * time.Second}, err
	}

	logger.Info("reconciliation completed successfully")
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *EmuNetReconciler) addFinalizer(ctx context.Context, namespacedName types.NamespacedName) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latestEmuNet := &emunetv1.EmuNet{}
		if err := r.Get(ctx, namespacedName, latestEmuNet); err != nil {
			return err
		}

		if !controllerutil.ContainsFinalizer(latestEmuNet, EmuNetFinalizer) {
			original := latestEmuNet.DeepCopy()
			controllerutil.AddFinalizer(latestEmuNet, EmuNetFinalizer)
			patch := client.MergeFrom(original)
			return r.Patch(ctx, latestEmuNet, patch)
		}
		return nil
	})
}

func (r *EmuNetReconciler) getPodListOptions(emunet *emunetv1.EmuNet) []client.ListOption {
	var listOptions []client.ListOption
	listOptions = append(listOptions, client.InNamespace(emunet.Namespace))
	if emunet.Spec.Selector != nil {
		if selector, err := metav1.LabelSelectorAsSelector(emunet.Spec.Selector); err == nil {
			listOptions = append(listOptions, client.MatchingLabelsSelector{Selector: selector})
		} else {
			// Fallback to using default label if selector conversion fails
			listOptions = append(listOptions, client.MatchingLabels{EmuNetLabelKey: emunet.Name})
		}
	} else {
		listOptions = append(listOptions, client.MatchingLabels{EmuNetLabelKey: emunet.Name})
	}
	return listOptions
}

// updateExistingPod updates an existing pod's image if it doesn't match the desired image
func (r *EmuNetReconciler) updateExistingPod(ctx context.Context, emunet *emunetv1.EmuNet, pod *corev1.Pod, desiredImage string) error {
	if pod.Spec.Containers[0].Image != desiredImage {
		if pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			return retry.RetryOnConflict(retry.DefaultRetry, func() error {
				latestPod := &corev1.Pod{}
				if err := r.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: emunet.Namespace}, latestPod); err != nil {
					return err
				}
				if len(latestPod.Spec.Containers) == 0 {
					return fmt.Errorf("pod has no containers")
				}
				original := latestPod.DeepCopy()
				latestPod.Spec.Containers[0].Image = desiredImage
				patch := client.MergeFrom(original)
				return r.Patch(ctx, latestPod, patch)
			})
		}
	}
	return nil
}

// createNewPod creates a new pod based on the provided image group and index
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
			Containers: []corev1.Container{
				{
					Name:  "emunet-pod",
					Image: imageGroup.Image,
				},
			},
		},
	}

	// Add selector matchLabels to pod labels if specified
	if emunet.Spec.Selector != nil && emunet.Spec.Selector.MatchLabels != nil {
		for key, value := range emunet.Spec.Selector.MatchLabels {
			newPod.Labels[key] = value
		}
	}

	if err := ctrl.SetControllerReference(emunet, newPod, r.Scheme); err != nil {
		return err
	}

	return r.Create(ctx, newPod)
}

func (r *EmuNetReconciler) handleDeletion(ctx context.Context, namespacedName types.NamespacedName) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	emunet := &emunetv1.EmuNet{}
	if err := r.Get(ctx, namespacedName, emunet); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	pods := &corev1.PodList{}
	listOptions := r.getPodListOptions(emunet)
	if err := r.List(ctx, pods, listOptions...); err != nil {
		return ctrl.Result{}, err
	}

	for _, pod := range pods.Items {
		if err := r.Delete(ctx, &pod); err != nil && !errors.IsNotFound(err) {
			logger.Error(err, "failed to delete pod", "pod", pod.Name)
			return ctrl.Result{}, err
		}
	}

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latestEmuNet := &emunetv1.EmuNet{}
		if err := r.Get(ctx, namespacedName, latestEmuNet); err != nil {
			if errors.IsNotFound(err) {
				return nil
			}
			return err
		}

		if controllerutil.ContainsFinalizer(latestEmuNet, EmuNetFinalizer) {
			original := latestEmuNet.DeepCopy()
			controllerutil.RemoveFinalizer(latestEmuNet, EmuNetFinalizer)
			patch := client.MergeFrom(original)
			return r.Patch(ctx, latestEmuNet, patch)
		}
		return nil
	}); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.Redis.DeleteEmuNetStatus(ctx, namespacedName.Namespace, namespacedName.Name); err != nil {
		logger.Error(err, "failed to delete emunet status from redis", "namespace", namespacedName.Namespace, "name", namespacedName.Name)
	}

	return ctrl.Result{}, nil
}

func (r *EmuNetReconciler) syncPods(ctx context.Context, namespacedName types.NamespacedName) error {
	emunet := &emunetv1.EmuNet{}
	if err := r.Get(ctx, namespacedName, emunet); err != nil {
		return err
	}

	existingPods := &corev1.PodList{}
	listOptions := r.getPodListOptions(emunet)
	if err := r.List(ctx, existingPods, listOptions...); err != nil {
		return err
	}

	existingPodMap := make(map[string]*corev1.Pod)
	for i := range existingPods.Items {
		existingPodMap[existingPods.Items[i].Name] = &existingPods.Items[i]
	}

	desiredPods := make(map[string]bool)

	for groupIdx, imageGroup := range emunet.Spec.ImageGroups {
		for podIdx := int32(0); podIdx < imageGroup.Replicas; podIdx++ {
			podName := fmt.Sprintf("%s-group%d-%d", emunet.Name, groupIdx, podIdx)
			desiredPods[podName] = true

			if existingPod, exists := existingPodMap[podName]; exists {
				if err := r.updateExistingPod(ctx, emunet, existingPod, imageGroup.Image); err != nil {
					return err
				}
				continue
			}

			if err := r.createNewPod(ctx, emunet, groupIdx, podIdx, imageGroup); err != nil {
				return err
			}
		}
	}

	for podName, pod := range existingPodMap {
		if !desiredPods[podName] {
			if err := r.Delete(ctx, pod); err != nil && !errors.IsNotFound(err) {
				return err
			}
		}
	}

	return nil
}

// updateStatus updates the EmuNet status based on current pod states
func (r *EmuNetReconciler) updateStatus(ctx context.Context, namespacedName types.NamespacedName) error {
	logger := log.FromContext(ctx)

	retryPolicy := retry.DefaultRetry
	retryPolicy.Steps = 20
	retryPolicy.Duration = 200 * time.Millisecond
	retryPolicy.Factor = 1.5
	retryPolicy.Jitter = 0.1

	var redisStatus *redis.EmuNetStatus
	var err error

	err = retry.RetryOnConflict(retryPolicy, func() error {
		emunet := &emunetv1.EmuNet{}
		if err := r.Get(ctx, namespacedName, emunet); err != nil {
			logger.Error(err, "failed to get emunet")
			return err
		}

		original := emunet.DeepCopy()

		// Get current pods
		pods := &corev1.PodList{}
		listOptions := r.getPodListOptions(emunet)
		if err := r.List(ctx, pods, listOptions...); err != nil {
			logger.Error(err, "failed to list pods")
			return err
		}

		logger.Info("found pods", "count", len(pods.Items), "namespace", emunet.Namespace, "name", emunet.Name)

		// Create pod map for quick lookup
		podMap := make(map[string]*corev1.Pod)
		for i := range pods.Items {
			podMap[pods.Items[i].Name] = &pods.Items[i]
		}

		// Create existing pod status map for quick lookup
		existingPodStatusMap := make(map[string]emunetv1.PodStatus)
		for _, group := range emunet.Status.ImageGroupStatus {
			for _, podStatus := range group.PodStatuses {
				existingPodStatusMap[podStatus.PodName] = podStatus
			}
		}

		// Calculate image group status
		imageGroupStatus, totalReady := r.calculateImageGroupStatus(ctx, emunet, podMap, existingPodStatusMap)

		// Update EmuNet status
		emunet.Status.ReadyReplicas = totalReady
		emunet.Status.DesiredReplicas = emunet.Spec.TotalReplicas
		emunet.Status.ImageGroupStatus = imageGroupStatus
		emunet.Status.ObservedGen = emunet.Generation

		// Patch status
		patch := client.MergeFrom(original)
		if err := r.Status().Patch(ctx, emunet, patch); err != nil {
			logger.Error(err, "failed to patch emunet status")
			return err
		}

		logger.Info("updated emunet status", "readyReplicas", totalReady, "desiredReplicas", emunet.Spec.TotalReplicas)

		// Prepare redis status
		redisStatus = &redis.EmuNetStatus{
			Name:             emunet.Name,
			Namespace:        emunet.Namespace,
			ReadyReplicas:    totalReady,
			DesiredReplicas:  emunet.Spec.TotalReplicas,
			ObservedGen:      emunet.Generation,
			ImageGroupStatus: convertImageGroupStatus(emunet.Status.ImageGroupStatus),
			LastUpdated:      time.Now(),
		}

		return nil
	})

	if err != nil {
		return err
	}

	// Save status to redis
	if err := r.saveStatusToRedis(ctx, redisStatus); err != nil {
		return err
	}

	return nil
}

// calculateImageGroupStatus calculates the status for each image group
func (r *EmuNetReconciler) calculateImageGroupStatus(ctx context.Context, emunet *emunetv1.EmuNet, podMap map[string]*corev1.Pod, existingPodStatusMap map[string]emunetv1.PodStatus) ([]emunetv1.ImageGroupStatus, int32) {
	logger := log.FromContext(ctx)
	var imageGroupStatus []emunetv1.ImageGroupStatus
	var totalReady int32

	for groupIdx, imageGroup := range emunet.Spec.ImageGroups {
		groupStatus := emunetv1.ImageGroupStatus{
			Image:           imageGroup.Image,
			DesiredReplicas: imageGroup.Replicas,
			PodStatuses:     []emunetv1.PodStatus{},
		}

		for podIdx := int32(0); podIdx < imageGroup.Replicas; podIdx++ {
			podName := fmt.Sprintf("%s-group%d-%d", emunet.Name, groupIdx, podIdx)
			podStatus := emunetv1.PodStatus{
				PodName:     podName,
				Image:       imageGroup.Image,
				MACAddress:  "",
				NodeName:    "",
				VethIfIndex: 0,
				Phase:       corev1.PodPending,
				Ready:       false,
				LastUpdated: metav1.Now(),
			}

			if pod, exists := podMap[podName]; exists {
				podStatus.Phase = pod.Status.Phase
				podStatus.PodIP = pod.Status.PodIP
				podStatus.NodeName = pod.Spec.NodeName
				podStatus.Ready = podReady(pod)
				podStatus.Message = podStatusMessage(pod)

				if existingPodStatus, exists := existingPodStatusMap[podName]; exists {
					podStatus.MACAddress = existingPodStatus.MACAddress
					podStatus.VethIfIndex = existingPodStatus.VethIfIndex
				}

				logger.Info("pod status", "podName", podName, "phase", podStatus.Phase, "ready", podStatus.Ready, "macAddress", podStatus.MACAddress, "vethIfIndex", podStatus.VethIfIndex)

				if podStatus.Ready {
					groupStatus.ReadyReplicas++
					totalReady++
				}
			}

			groupStatus.PodStatuses = append(groupStatus.PodStatuses, podStatus)
		}

		imageGroupStatus = append(imageGroupStatus, groupStatus)
	}

	return imageGroupStatus, totalReady
}

// saveStatusToRedis saves the EmuNet status to redis
func (r *EmuNetReconciler) saveStatusToRedis(ctx context.Context, status *redis.EmuNetStatus) error {
	logger := log.FromContext(ctx)

	if err := r.Redis.SaveEmuNetStatus(ctx, status); err != nil {
		logger.Error(err, "failed to save emunet status to redis")
		return err
	}

	logger.Info("saved emunet status to redis", "name", status.Name, "namespace", status.Namespace)

	for _, group := range status.ImageGroupStatus {
		for _, pod := range group.PodStatuses {
			redisPod := pod
			if err := r.Redis.SavePodStatus(ctx, status.Namespace, status.Name, &redisPod); err != nil {
				logger.Error(err, "failed to save pod status to redis", "podName", redisPod.PodName)
				return err
			}
		}
	}

	logger.Info("saved all pod statuses to redis", "count", len(status.ImageGroupStatus))

	return nil
}

func convertImageGroupStatus(k8sStatus []emunetv1.ImageGroupStatus) []redis.ImageGroupStatus {
	var redisStatus []redis.ImageGroupStatus
	for _, k8sGroup := range k8sStatus {
		group := redis.ImageGroupStatus{
			Image:           k8sGroup.Image,
			DesiredReplicas: k8sGroup.DesiredReplicas,
			ReadyReplicas:   k8sGroup.ReadyReplicas,
			PodStatuses:     convertPodStatuses(k8sGroup.PodStatuses),
		}
		redisStatus = append(redisStatus, group)
	}
	return redisStatus
}

func convertPodStatuses(k8sPods []emunetv1.PodStatus) []redis.PodStatus {
	var redisPods []redis.PodStatus
	for _, k8sPod := range k8sPods {
		pod := redis.PodStatus{
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
		}
		redisPods = append(redisPods, pod)
	}
	return redisPods
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func podStatusMessage(pod *corev1.Pod) string {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Message
		}
	}
	return ""
}

func (r *EmuNetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&emunetv1.EmuNet{}).
		Named("emunet").
		Complete(r)
}
