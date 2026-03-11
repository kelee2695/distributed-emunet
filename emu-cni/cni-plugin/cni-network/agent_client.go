package cni_network

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// PodInfoRequest 定义向agent发送的Pod信息请求
type PodInfoRequest struct {
	PodName string `json:"podName"` // Pod名称
	Ifindex int    `json:"ifindex"` // 网卡ifindex
	SrcMac  string `json:"srcMac"`  // 源MAC地址
}

// AgentClient 定义agent服务客户端
type AgentClient struct {
	BaseURL string
	Timeout time.Duration
}

// NewAgentClient 创建一个新的agent客户端
func NewAgentClient() *AgentClient {
	return &AgentClient{
		BaseURL: "http://localhost:12345",
		Timeout: 5 * time.Second,
	}
}

// AddPodInfo 向agent服务添加PodInfo
func (ac *AgentClient) AddPodInfo(podName string, ifindex int, srcMac string) error {
	req := PodInfoRequest{
		PodName: podName,
		Ifindex: ifindex,
		SrcMac:  srcMac,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %v", err)
	}

	client := &http.Client{Timeout: ac.Timeout}
	httpReq, err := http.NewRequest("POST", ac.BaseURL+"/api/podinfo/add", bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("创建HTTP请求失败: %v", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("发送HTTP请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("调用agent服务失败，状态码: %d", resp.StatusCode)
	}

	return nil
}

// DeletePodInfo 从agent服务删除PodInfo
func (ac *AgentClient) DeletePodInfo(podName string) error {
	client := &http.Client{Timeout: ac.Timeout}
	httpReq, err := http.NewRequest("DELETE", ac.BaseURL+"/api/podinfo/"+podName, nil)
	if err != nil {
		return fmt.Errorf("创建HTTP请求失败: %v", err)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("发送HTTP请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("调用agent服务失败，状态码: %d", resp.StatusCode)
	}

	return nil
}
