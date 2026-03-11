package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

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

	key := fmt.Sprintf("agent:network:%s", podName)
	return c.client.Set(ctx, key, data, DefaultTTL).Err()
}

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

func (c *Client) SaveStatusBatch(ctx context.Context, emunet *EmuNetStatus, pods []PodStatus) error {
	pipe := c.client.Pipeline()

	key := fmt.Sprintf("emunet:%s:%s", emunet.Namespace, emunet.Name)
	data, err := json.Marshal(emunet)
	if err != nil {
		return err
	}
	pipe.Set(ctx, key, data, DefaultTTL)

	indexKey := fmt.Sprintf("emunet:%s:%s:pods", emunet.Namespace, emunet.Name)

	for _, pod := range pods {
		podData, marshalErr := json.Marshal(pod)
		if marshalErr != nil {
			continue
		}

		podKey := fmt.Sprintf("emunet:%s:%s:pod:%s", emunet.Namespace, emunet.Name, pod.PodName)
		pipe.Set(ctx, podKey, podData, DefaultTTL)

		if pod.PodName != "" {
			lookupKey := fmt.Sprintf("pod_lookup:%s", pod.PodName)
			pipe.Set(ctx, lookupKey, podData, DefaultTTL)
		}

		pipe.SAdd(ctx, indexKey, pod.PodName)
	}

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

func (c *Client) DeleteEmuNetStatus(ctx context.Context, namespace, name string) error {
	pipe := c.client.Pipeline()

	indexKey := fmt.Sprintf("emunet:%s:%s:pods", namespace, name)
	podNames, _ := c.client.SMembers(ctx, indexKey).Result()

	for _, podName := range podNames {
		pipe.Del(ctx, fmt.Sprintf("emunet:%s:%s:pod:%s", namespace, name, podName))
		pipe.Del(ctx, fmt.Sprintf("pod_lookup:%s", podName))
		pipe.Del(ctx, fmt.Sprintf("agent:network:%s", podName))
	}

	mainKey := fmt.Sprintf("emunet:%s:%s", namespace, name)
	pipe.Del(ctx, mainKey)
	pipe.Del(ctx, indexKey)

	_, err := pipe.Exec(ctx)
	return err
}

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
