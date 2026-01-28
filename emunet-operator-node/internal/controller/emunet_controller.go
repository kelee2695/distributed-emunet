package controller

import (
	"context"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	emunetv1 "github.com/emunet/emunet-operator/api/v1"
)

type EmuNetReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	PodInfoStore interface {
		Get(podName string) (interface{}, bool)
	}
	NodeName string
}

func (r *EmuNetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	emunet := &emunetv1.EmuNet{}
	if err := r.Get(ctx, req.NamespacedName, emunet); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("EmuNet not found, ignoring")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get EmuNet")
		return ctrl.Result{}, err
	}

	if err := r.updateStatus(ctx, req.NamespacedName); err != nil {
		if errors.IsConflict(err) {
			logger.Info("conflict detected, will retry")
			return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
		}
		logger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *EmuNetReconciler) updateStatus(ctx context.Context, namespacedName types.NamespacedName) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		emunet := &emunetv1.EmuNet{}
		if err := r.Get(ctx, namespacedName, emunet); err != nil {
			if errors.IsNotFound(err) {
				return nil
			}
			return err
		}

		original := emunet.DeepCopy()

		pods, err := r.getPods(ctx, emunet)
		if err != nil {
			return err
		}

		totalReady := int32(0)

		r.initializeImageGroupStatus(emunet)

		for i := range emunet.Spec.ImageGroups {
			r.updateImageGroupInfo(emunet, i)

			for podIdx := int32(0); podIdx < emunet.Spec.ImageGroups[i].Replicas; podIdx++ {
				podName := r.generatePodName(emunet, i, podIdx)
				if pod, found := r.findPodByName(pods, podName); found {
					if pod.Spec.NodeName == r.NodeName {
						r.updatePodStatus(emunet, i, pod, podName)
					}

					if podReady(pod) {
						totalReady++
					}
				}
			}
		}

		emunet.Status.ReadyReplicas = totalReady
		emunet.Status.ObservedGen = emunet.Generation

		patch := client.MergeFrom(original)
		if err := r.Status().Patch(ctx, emunet, patch); err != nil {
			if errors.IsNotFound(err) {
				return nil
			}
			return err
		}

		return nil
	})
}

// getPods 获取指定命名空间中符合 selector 的 Pod 列表
func (r *EmuNetReconciler) getPods(ctx context.Context, emunet *emunetv1.EmuNet) (*corev1.PodList, error) {
	pods := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(emunet.Namespace),
	}

	// 使用 selector 过滤 Pod
	if emunet.Spec.Selector == nil {
		return nil, fmt.Errorf("selector is required")
	}

	selector, err := metav1.LabelSelectorAsSelector(emunet.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("invalid selector: %v", err)
	}
	listOpts = append(listOpts, client.MatchingLabelsSelector{Selector: selector})

	if err := r.List(ctx, pods, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list pods: %v", err)
	}

	return pods, nil
}

// initializeImageGroupStatus 初始化 ImageGroupStatus 数组
func (r *EmuNetReconciler) initializeImageGroupStatus(emunet *emunetv1.EmuNet) {
	if len(emunet.Status.ImageGroupStatus) != len(emunet.Spec.ImageGroups) {
		if len(emunet.Status.ImageGroupStatus) < len(emunet.Spec.ImageGroups) {
			// 添加缺失的 ImageGroupStatus
			for i := len(emunet.Status.ImageGroupStatus); i < len(emunet.Spec.ImageGroups); i++ {
				emunet.Status.ImageGroupStatus = append(emunet.Status.ImageGroupStatus, emunetv1.ImageGroupStatus{
					Image:           emunet.Spec.ImageGroups[i].Image,
					DesiredReplicas: emunet.Spec.ImageGroups[i].Replicas,
					ReadyReplicas:   0,
					PodStatuses:     []emunetv1.PodStatus{},
				})
			}
		} else {
			// 截断多余的 ImageGroupStatus
			emunet.Status.ImageGroupStatus = emunet.Status.ImageGroupStatus[:len(emunet.Spec.ImageGroups)]
		}
	}
}

// updateImageGroupInfo 更新 ImageGroup 的基本信息
func (r *EmuNetReconciler) updateImageGroupInfo(emunet *emunetv1.EmuNet, index int) {
	if index < len(emunet.Status.ImageGroupStatus) {
		emunet.Status.ImageGroupStatus[index].Image = emunet.Spec.ImageGroups[index].Image
		emunet.Status.ImageGroupStatus[index].DesiredReplicas = emunet.Spec.ImageGroups[index].Replicas
	}
}

// generatePodName 生成 Pod 名称
func (r *EmuNetReconciler) generatePodName(emunet *emunetv1.EmuNet, groupIndex int, podIndex int32) string {
	return fmt.Sprintf("%s-%s-%d", emunet.Name, fmt.Sprintf("group%d", groupIndex), podIndex)
}

// findPodByName 根据名称查找 Pod
func (r *EmuNetReconciler) findPodByName(pods *corev1.PodList, podName string) (*corev1.Pod, bool) {
	for i := range pods.Items {
		if pods.Items[i].Name == podName {
			return &pods.Items[i], true
		}
	}
	return nil, false
}

// updatePodStatus 更新 Pod 状态
func (r *EmuNetReconciler) updatePodStatus(emunet *emunetv1.EmuNet, groupIndex int, pod *corev1.Pod, podName string) {
	if groupIndex >= len(emunet.Status.ImageGroupStatus) {
		return
	}

	podStatuses := &emunet.Status.ImageGroupStatus[groupIndex].PodStatuses

	// 查找是否已有该 Pod 的状态
	for i := range *podStatuses {
		if (*podStatuses)[i].PodName == podName {
			// 更新现有 Pod 状态
			r.updateExistingPodStatus(&(*podStatuses)[i], pod, podName)
			return
		}
	}

	// 创建新的 Pod 状态
	newPodStatus := r.createPodStatus(pod, emunet.Spec.ImageGroups[groupIndex].Image, podName)
	*podStatuses = append(*podStatuses, newPodStatus)
}

// updateExistingPodStatus 更新现有 Pod 状态
func (r *EmuNetReconciler) updateExistingPodStatus(podStatus *emunetv1.PodStatus, pod *corev1.Pod, podName string) {
	// 从 PodInfoStore 获取网络信息
	if podInfo, exists := r.PodInfoStore.Get(podName); exists {
		if info, ok := podInfo.(*PodInfo); ok {
			podStatus.MACAddress = info.SrcMac
			podStatus.VethIfIndex = info.Ifindex
		}
	}

	// 更新 Pod 状态信息
	podStatus.Phase = pod.Status.Phase
	podStatus.PodIP = pod.Status.PodIP
	podStatus.NodeName = pod.Spec.NodeName
	podStatus.Ready = podReady(pod)
	podStatus.Message = podStatusMessage(pod)
	podStatus.LastUpdated = metav1.Now()
}

// createPodStatus 创建新的 Pod 状态
func (r *EmuNetReconciler) createPodStatus(pod *corev1.Pod, image string, podName string) emunetv1.PodStatus {
	podStatus := emunetv1.PodStatus{
		PodName:     podName,
		Image:       image,
		Phase:       pod.Status.Phase,
		PodIP:       pod.Status.PodIP,
		NodeName:    pod.Spec.NodeName,
		Ready:       podReady(pod),
		Message:     podStatusMessage(pod),
		LastUpdated: metav1.Now(),
	}

	// 从 PodInfoStore 获取网络信息
	if podInfo, exists := r.PodInfoStore.Get(podName); exists {
		if info, ok := podInfo.(*PodInfo); ok {
			podStatus.MACAddress = info.SrcMac
			podStatus.VethIfIndex = info.Ifindex
		}
	}

	return podStatus
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
