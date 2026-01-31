package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// Key TTL (Time To Live) to prevent stale data leaking
	DefaultTTL = 24 * time.Hour
)

type Client struct {
	client *redis.Client
}

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
	MACAddress  string    `json:"macAddress,omitempty"`  // Agent updates this
	VethIfIndex int       `json:"vethIfIndex,omitempty"` // Agent updates this
	LastUpdated time.Time `json:"lastUpdated"`
}

// NewClient creates a redis client.
// addr: "localhost:6379" or "redis-service.default.svc:6379"
func NewClient(addr string, password string, db int) *Client {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
		// Connection pool optimization
		PoolSize:     100,
		MinIdleConns: 10,
	})

	return &Client{
		client: rdb,
	}
}

func (c *Client) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

func (c *Client) Close() error {
	return c.client.Close()
}

// ==========================================
// Agent Operations (Targeted Updates)
// ==========================================

// UpdatePodNetworkInfo [核心修改] Agent 写入专用的 Key，防止被 Master 覆盖
// Agent 写入: agent:network:{podName}
func (c *Client) UpdatePodNetworkInfo(ctx context.Context, podName string, mac string, ifIndex int) error {
	pod := PodStatus{
		PodName:     podName,
		MACAddress:  mac,
		VethIfIndex: ifIndex,
		LastUpdated: time.Now(),
	}

	data, err := json.Marshal(pod)
	if err != nil {
		return err
	}

	// 使用专用前缀 "agent:network:"
	key := fmt.Sprintf("agent:network:%s", podName)
	return c.client.Set(ctx, key, data, DefaultTTL).Err()
}

// GetAgentNetworkInfo [新增] Master 从专用 Key 读取 Agent 上报的数据
func (c *Client) GetAgentNetworkInfo(ctx context.Context, podName string) (*PodStatus, error) {
	key := fmt.Sprintf("agent:network:%s", podName)
	data, err := c.client.Get(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	var pod PodStatus
	if err := json.Unmarshal([]byte(data), &pod); err != nil {
		return nil, err
	}
	return &pod, nil
}

// ==========================================
// Master Operations (Batch & Hierarchy)
// ==========================================

// SaveStatusBatch uses Redis Pipeline to save everything in 1 RTT.
// Master 写入: pod_lookup:{podName} (这是合并了 IP 和 MAC 的完整数据)
func (c *Client) SaveStatusBatch(ctx context.Context, emunet *EmuNetStatus, pods []PodStatus) error {
	pipe := c.client.Pipeline()

	// 1. Save EmuNet Status
	key := fmt.Sprintf("emunet:%s:%s", emunet.Namespace, emunet.Name)
	data, err := json.Marshal(emunet)
	if err != nil {
		return err
	}
	pipe.Set(ctx, key, data, DefaultTTL)

	// 2. Save Pods and Indices
	indexKey := fmt.Sprintf("emunet:%s:%s:pods", emunet.Namespace, emunet.Name)

	for _, pod := range pods {
		podData, err := json.Marshal(pod)
		if err != nil {
			continue
		}

		// A. Hierarchical Key
		podKey := fmt.Sprintf("emunet:%s:%s:pod:%s", emunet.Namespace, emunet.Name, pod.PodName)
		pipe.Set(ctx, podKey, podData, DefaultTTL)

		// B. Lookup Key (pod_lookup:podname) - Enables O(1) global lookup
		// API Server 读取这个 Key
		if pod.PodName != "" {
			lookupKey := fmt.Sprintf("pod_lookup:%s", pod.PodName)
			pipe.Set(ctx, lookupKey, podData, DefaultTTL)
		}

		// C. Add to Index Set
		pipe.SAdd(ctx, indexKey, pod.PodName)
	}

	// Refresh Index TTL
	pipe.Expire(ctx, indexKey, DefaultTTL)

	_, err = pipe.Exec(ctx)
	return err
}

func (c *Client) SaveEmuNetStatus(ctx context.Context, status *EmuNetStatus) error {
	key := fmt.Sprintf("emunet:%s:%s", status.Namespace, status.Name)
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, key, data, DefaultTTL).Err()
}

func (c *Client) GetEmuNetStatus(ctx context.Context, namespace, name string) (*EmuNetStatus, error) {
	key := fmt.Sprintf("emunet:%s:%s", namespace, name)
	data, err := c.client.Get(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	var status EmuNetStatus
	if err := json.Unmarshal([]byte(data), &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// DeleteEmuNetStatus Optimized: Uses the Index Set to delete efficiently without SCAN
func (c *Client) DeleteEmuNetStatus(ctx context.Context, namespace, name string) error {
	pipe := c.client.Pipeline()

	// 1. Get all pods from Index
	indexKey := fmt.Sprintf("emunet:%s:%s:pods", namespace, name)
	podNames, _ := c.client.SMembers(ctx, indexKey).Result()

	// 2. Delete all Pod specific keys
	for _, podName := range podNames {
		pipe.Del(ctx, fmt.Sprintf("emunet:%s:%s:pod:%s", namespace, name, podName))
		pipe.Del(ctx, fmt.Sprintf("pod_lookup:%s", podName))
		// Optional: We can also cleanup agent data, but TTL handles it usually
		pipe.Del(ctx, fmt.Sprintf("agent:network:%s", podName))
	}

	// 3. Delete EmuNet keys
	mainKey := fmt.Sprintf("emunet:%s:%s", namespace, name)
	pipe.Del(ctx, mainKey)
	pipe.Del(ctx, indexKey)

	_, err := pipe.Exec(ctx)
	return err
}

// ==========================================
// Shared Operations
// ==========================================

// GetPodInfoDirectly is the O(1) lookup method for the Master API
// It reads from "pod_lookup:{podName}" which contains merged data
func (c *Client) GetPodInfoDirectly(ctx context.Context, podName string) (*PodStatus, error) {
	key := fmt.Sprintf("pod_lookup:%s", podName)
	data, err := c.client.Get(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	var pod PodStatus
	if err := json.Unmarshal([]byte(data), &pod); err != nil {
		return nil, err
	}
	return &pod, nil
}

// ListPodStatuses Optimized: Uses SMEMBERS instead of SCAN
func (c *Client) ListPodStatuses(ctx context.Context, namespace, name string) ([]PodStatus, error) {
	indexKey := fmt.Sprintf("emunet:%s:%s:pods", namespace, name)
	podNames, err := c.client.SMembers(ctx, indexKey).Result()
	if err != nil {
		return nil, err
	}

	if len(podNames) == 0 {
		return []PodStatus{}, nil
	}

	pipe := c.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(podNames))

	for i, podName := range podNames {
		key := fmt.Sprintf("emunet:%s:%s:pod:%s", namespace, name, podName)
		cmds[i] = pipe.Get(ctx, key)
	}

	_, err = pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, err
	}

	var pods []PodStatus
	for _, cmd := range cmds {
		data, err := cmd.Result()
		if err == nil {
			var pod PodStatus
			if json.Unmarshal([]byte(data), &pod) == nil {
				pods = append(pods, pod)
			}
		}
	}

	return pods, nil
}

// Deprecated: Use SaveStatusBatch instead
func (c *Client) SavePodStatus(ctx context.Context, namespace, name string, pod *PodStatus) error {
	return c.SaveStatusBatch(ctx, &EmuNetStatus{Namespace: namespace, Name: name}, []PodStatus{*pod})
}

func (c *Client) GetPodStatus(ctx context.Context, namespace, name, podName string) (*PodStatus, error) {
	return c.GetPodInfoDirectly(ctx, podName)
}
