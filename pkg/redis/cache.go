package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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

func (c *Client) GetAgentNetworkInfoBatch(ctx context.Context, podNames []string) (map[string]*PodStatus, error) {
	result := make(map[string]*PodStatus, len(podNames))
	if len(podNames) == 0 {
		return result, nil
	}

	pipe := c.client.Pipeline()
	cmds := make(map[string]*redis.StringCmd, len(podNames))
	for _, podName := range podNames {
		key := fmt.Sprintf("agent:network:%s", podName)
		cmds[podName] = pipe.Get(ctx, key)
	}

	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, err
	}

	for podName, cmd := range cmds {
		data, err := cmd.Result()
		if err != nil {
			continue
		}
		var pod PodStatus
		if json.Unmarshal([]byte(data), &pod) == nil {
			result[podName] = &pod
		}
	}

	return result, nil
}

func (c *Client) SaveStatusBatch(ctx context.Context, emunet *EmuNetStatus, pods []PodStatus, summary *EmuNetSummary) error {
	pipe := c.client.Pipeline()

	key := fmt.Sprintf("emunet:%s:%s", emunet.Namespace, emunet.Name)
	data, err := json.Marshal(compactEmuNetStatus(emunet))
	if err != nil {
		return err
	}
	pipe.Set(ctx, key, data, DefaultTTL)

	if summary != nil {
		summaryKey := fmt.Sprintf("emunet:%s:%s:summary", emunet.Namespace, emunet.Name)
		summaryData, err := json.Marshal(summary)
		if err != nil {
			return err
		}
		pipe.Set(ctx, summaryKey, summaryData, DefaultTTL)
	}

	indexKey := podIndexKey(emunet.Namespace, emunet.Name)
	legacyIndexKey := legacyPodIndexKey(emunet.Namespace, emunet.Name)
	currentPods := make(map[string]struct{}, len(pods))
	oldPodNames := c.listKnownPodNames(ctx, emunet.Namespace, emunet.Name)

	for index, pod := range pods {
		podData, marshalErr := json.Marshal(pod)
		if marshalErr != nil {
			continue
		}
		if pod.PodName == "" {
			continue
		}

		podKey := fmt.Sprintf("emunet:%s:%s:pod:%s", emunet.Namespace, emunet.Name, pod.PodName)
		pipe.Set(ctx, podKey, podData, DefaultTTL)
		lookupKey := fmt.Sprintf("pod_lookup:%s", pod.PodName)
		pipe.Set(ctx, lookupKey, podData, DefaultTTL)
		pipe.ZAdd(ctx, indexKey, redis.Z{Score: float64(index), Member: pod.PodName})
		currentPods[pod.PodName] = struct{}{}
	}

	for _, podName := range oldPodNames {
		if _, ok := currentPods[podName]; ok {
			continue
		}
		pipe.Del(ctx, fmt.Sprintf("emunet:%s:%s:pod:%s", emunet.Namespace, emunet.Name, podName))
		pipe.Del(ctx, fmt.Sprintf("pod_lookup:%s", podName))
		pipe.Del(ctx, fmt.Sprintf("agent:network:%s", podName))
		pipe.ZRem(ctx, indexKey, podName)
	}

	pipe.Expire(ctx, indexKey, DefaultTTL)
	pipe.Del(ctx, legacyIndexKey)

	_, err = pipe.Exec(ctx)
	return err
}

func (c *Client) SaveSummary(ctx context.Context, summary *EmuNetSummary) error {
	if summary == nil {
		return nil
	}

	summaryKey := fmt.Sprintf("emunet:%s:%s:summary", summary.Namespace, summary.Name)
	summaryData, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, summaryKey, summaryData, DefaultTTL).Err()
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

func (c *Client) GetEmuNetSummary(ctx context.Context, namespace, name string) (*EmuNetSummary, error) {
	key := fmt.Sprintf("emunet:%s:%s:summary", namespace, name)
	data, err := c.client.Get(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	var summary EmuNetSummary
	if err := json.Unmarshal([]byte(data), &summary); err != nil {
		return nil, err
	}
	return &summary, nil
}

func (c *Client) DeleteEmuNetStatus(ctx context.Context, namespace, name string) error {
	pipe := c.client.Pipeline()

	indexKey := fmt.Sprintf("emunet:%s:%s:pods", namespace, name)
	zsetIndexKey := podIndexKey(namespace, name)
	podNames, _ := c.client.SMembers(ctx, indexKey).Result()
	zsetPodNames, _ := c.client.ZRange(ctx, zsetIndexKey, 0, -1).Result()
	podNames = append(podNames, zsetPodNames...)

	for _, podName := range podNames {
		pipe.Del(ctx, fmt.Sprintf("emunet:%s:%s:pod:%s", namespace, name, podName))
		pipe.Del(ctx, fmt.Sprintf("pod_lookup:%s", podName))
		pipe.Del(ctx, fmt.Sprintf("agent:network:%s", podName))
	}

	mainKey := fmt.Sprintf("emunet:%s:%s", namespace, name)
	summaryKey := fmt.Sprintf("emunet:%s:%s:summary", namespace, name)
	pipe.Del(ctx, mainKey)
	pipe.Del(ctx, summaryKey)
	pipe.Del(ctx, indexKey)
	pipe.Del(ctx, zsetIndexKey)

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
	pods, _, err := c.ListPodStatusesPage(ctx, namespace, name, 0, 0)
	return pods, err
}

func (c *Client) ListPodStatusesPage(ctx context.Context, namespace, name string, offset, limit int) ([]PodStatus, int, error) {
	indexKey := podIndexKey(namespace, name)
	zsetTotal, err := c.client.ZCard(ctx, indexKey).Result()
	if err != nil {
		return nil, 0, err
	}
	if zsetTotal > 0 {
		if offset < 0 {
			offset = 0
		}
		if limit <= 0 {
			limit = int(zsetTotal)
		}
		if offset > int(zsetTotal) {
			offset = int(zsetTotal)
		}
		stop := int64(offset + limit - 1)
		if stop >= zsetTotal {
			stop = zsetTotal - 1
		}
		podNames, err := c.client.ZRange(ctx, indexKey, int64(offset), stop).Result()
		if err != nil {
			return nil, 0, err
		}
		pods, err := c.getPodStatusesByName(ctx, namespace, name, podNames)
		return pods, int(zsetTotal), err
	}

	podNames, err := c.client.SMembers(ctx, legacyPodIndexKey(namespace, name)).Result()
	if err != nil {
		return nil, 0, err
	}
	if len(podNames) == 0 {
		return []PodStatus{}, 0, nil
	}
	sort.Strings(podNames)

	total := len(podNames)
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = total
	}
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	podNames = podNames[offset:end]

	pods, err := c.getPodStatusesByName(ctx, namespace, name, podNames)
	return pods, total, err
}

func (c *Client) getPodStatusesByName(ctx context.Context, namespace, name string, podNames []string) ([]PodStatus, error) {
	if len(podNames) == 0 {
		return []PodStatus{}, nil
	}

	pipe := c.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(podNames))
	for i, podName := range podNames {
		key := fmt.Sprintf("emunet:%s:%s:pod:%s", namespace, name, podName)
		cmds[i] = pipe.Get(ctx, key)
	}

	_, err := pipe.Exec(ctx)
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

func (c *Client) listKnownPodNames(ctx context.Context, namespace, name string) []string {
	seen := map[string]struct{}{}
	for _, read := range []func() ([]string, error){
		func() ([]string, error) { return c.client.ZRange(ctx, podIndexKey(namespace, name), 0, -1).Result() },
		func() ([]string, error) { return c.client.SMembers(ctx, legacyPodIndexKey(namespace, name)).Result() },
	} {
		podNames, err := read()
		if err != nil {
			continue
		}
		for _, podName := range podNames {
			seen[podName] = struct{}{}
		}
	}

	result := make([]string, 0, len(seen))
	for podName := range seen {
		result = append(result, podName)
	}
	sort.Strings(result)
	return result
}

func compactEmuNetStatus(status *EmuNetStatus) *EmuNetStatus {
	compact := *status
	compact.ImageGroupStatus = make([]ImageGroupStatus, 0, len(status.ImageGroupStatus))
	for _, group := range status.ImageGroupStatus {
		compact.ImageGroupStatus = append(compact.ImageGroupStatus, ImageGroupStatus{
			Image:           group.Image,
			DesiredReplicas: group.DesiredReplicas,
			ReadyReplicas:   group.ReadyReplicas,
			PodStatuses:     []PodStatus{},
		})
	}
	return &compact
}

func podIndexKey(namespace, name string) string {
	return fmt.Sprintf("emunet:%s:%s:pod_index", namespace, name)
}

func legacyPodIndexKey(namespace, name string) string {
	return fmt.Sprintf("emunet:%s:%s:pods", namespace, name)
}
