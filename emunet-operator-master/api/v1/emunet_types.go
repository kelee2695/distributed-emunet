/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ImageGroup struct {
	Image    string `json:"image"`
	Replicas int32  `json:"replicas"`
}

type EmuNetSpec struct {
	TotalReplicas int32                 `json:"totalReplicas"`
	ImageGroups   []ImageGroup          `json:"imageGroups"`
	Selector      *metav1.LabelSelector `json:"selector,omitempty"`
}

type PodStatus struct {
	PodName     string          `json:"podName"`
	Image       string          `json:"image"`
	MACAddress  string          `json:"macAddress"`
	NodeName    string          `json:"nodeName"`
	VethIfIndex int             `json:"vethIfIndex"`
	Phase       corev1.PodPhase `json:"phase"`
	PodIP       string          `json:"podIP,omitempty"`
	Ready       bool            `json:"ready"`
	Message     string          `json:"message,omitempty"`
	LastUpdated metav1.Time     `json:"lastUpdated"`
}

type ImageGroupStatus struct {
	Image           string      `json:"image"`
	DesiredReplicas int32       `json:"desiredReplicas"`
	ReadyReplicas   int32       `json:"readyReplicas"`
	PodStatuses     []PodStatus `json:"podStatuses"`
}

type EmuNetStatus struct {
	ReadyReplicas    int32              `json:"readyReplicas"`
	DesiredReplicas  int32              `json:"desiredReplicas"`
	ObservedGen      int64              `json:"observedGen"`
	ImageGroupStatus []ImageGroupStatus `json:"imageGroupStatus"`
	Conditions       []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// EmuNet is the Schema for the emunets API
type EmuNet struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of EmuNet
	// +required
	Spec EmuNetSpec `json:"spec"`

	// status defines the observed state of EmuNet
	// +optional
	Status EmuNetStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// EmuNetList contains a list of EmuNet
type EmuNetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []EmuNet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EmuNet{}, &EmuNetList{})
}
