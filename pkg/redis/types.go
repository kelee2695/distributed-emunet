package redis

import "time"

const (
	DefaultTTL = 24 * time.Hour
)

type EmuNetStatus struct {
	Name             string             `json:"name"`
	Namespace        string             `json:"namespace"`
	ReadyReplicas    int32              `json:"readyReplicas"`
	DesiredReplicas  int32              `json:"desiredReplicas"`
	ObservedGen      int64              `json:"observedGen"`
	ImageGroupStatus []ImageGroupStatus `json:"imageGroupStatus"`
	LastUpdated      time.Time          `json:"lastUpdated"`
}

type ImageGroupStatus struct {
	Image           string      `json:"image"`
	DesiredReplicas int32       `json:"desiredReplicas"`
	ReadyReplicas   int32       `json:"readyReplicas"`
	PodStatuses     []PodStatus `json:"podStatuses"`
}

type PodStatus struct {
	PodName     string    `json:"podName"`
	Image       string    `json:"image,omitempty"`
	PodIP       string    `json:"podIP,omitempty"`
	NodeName    string    `json:"nodeName,omitempty"`
	Phase       string    `json:"phase,omitempty"`
	Ready       bool      `json:"ready,omitempty"`
	Message     string    `json:"message,omitempty"`
	MACAddress  string    `json:"macAddress,omitempty"`
	VethIfIndex int       `json:"vethIfIndex,omitempty"`
	LastUpdated time.Time `json:"lastUpdated"`
}

type ControlCommand struct {
	CommandID   string        `json:"commandId"`
	CommandType string        `json:"commandType"`
	TargetNode  string        `json:"targetNode"`
	Payload     string        `json:"payload"`
	Timestamp   time.Time     `json:"timestamp"`
	TTL         time.Duration `json:"ttl"`
}

type EBPFCommandPayload struct {
	Ifindex         uint32 `json:"ifindex"`
	SrcMac          string `json:"srcMac"`
	ThrottleRateBps uint32 `json:"throttleRateBps,omitempty"`
	Delay           uint32 `json:"delay,omitempty"`
	LossRate        uint32 `json:"lossRate,omitempty"`
	Jitter          uint32 `json:"jitter,omitempty"`
}
