package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
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
	Image       string    `json:"image"`
	PodIP       string    `json:"podIP"`
	NodeName    string    `json:"nodeName"`
	Phase       string    `json:"phase"`
	Ready       bool      `json:"ready"`
	Message     string    `json:"message"`
	MACAddress  string    `json:"macAddress"`
	VethIfIndex int       `json:"vethIfIndex"`
	LastUpdated time.Time `json:"lastUpdated"`
}

func NewClient(addr string, password string, db int) *Client {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
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

func (c *Client) SaveEmuNetStatus(ctx context.Context, status *EmuNetStatus) error {
	key := fmt.Sprintf("emunet:%s:%s", status.Namespace, status.Name)

	data, err := json.Marshal(status)
	if err != nil {
		return err
	}

	return c.client.Set(ctx, key, data, 24*time.Hour).Err()
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

func (c *Client) ListEmuNetStatuses(ctx context.Context) ([]*EmuNetStatus, error) {
	pattern := "emunet:*"
	var cursor uint64
	var statuses []*EmuNetStatus

	for {
		keys, nextCursor, err := c.client.Scan(ctx, cursor, pattern, 0).Result()
		if err != nil {
			return nil, err
		}

		for _, key := range keys {
			data, err := c.client.Get(ctx, key).Result()
			if err != nil {
				continue
			}

			var status EmuNetStatus
			if err := json.Unmarshal([]byte(data), &status); err != nil {
				continue
			}

			statuses = append(statuses, &status)
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return statuses, nil
}

func (c *Client) DeleteEmuNetStatus(ctx context.Context, namespace, name string) error {
	key := fmt.Sprintf("emunet:%s:%s", namespace, name)

	pattern := fmt.Sprintf("emunet:%s:%s:pod:*", namespace, name)
	var cursor uint64
	for {
		keys, nextCursor, err := c.client.Scan(ctx, cursor, pattern, 0).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			c.client.Del(ctx, keys...)
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return c.client.Del(ctx, key).Err()
}

func (c *Client) SavePodStatus(ctx context.Context, namespace, name string, pod *PodStatus) error {
	key := fmt.Sprintf("emunet:%s:%s:pod:%s", namespace, name, pod.PodName)

	data, err := json.Marshal(pod)
	if err != nil {
		return err
	}

	return c.client.Set(ctx, key, data, 24*time.Hour).Err()
}

func (c *Client) GetPodStatus(ctx context.Context, namespace, name, podName string) (*PodStatus, error) {
	key := fmt.Sprintf("emunet:%s:%s:pod:%s", namespace, name, podName)

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

func (c *Client) ListPodStatuses(ctx context.Context, namespace, name string) ([]PodStatus, error) {
	pattern := fmt.Sprintf("emunet:%s:%s:pod:*", namespace, name)
	var cursor uint64
	var pods []PodStatus

	for {
		keys, nextCursor, err := c.client.Scan(ctx, cursor, pattern, 0).Result()
		if err != nil {
			return nil, err
		}

		for _, key := range keys {
			data, err := c.client.Get(ctx, key).Result()
			if err != nil {
				continue
			}

			var pod PodStatus
			if err := json.Unmarshal([]byte(data), &pod); err != nil {
				continue
			}

			pods = append(pods, pod)
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return pods, nil
}
