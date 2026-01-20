package controller

import (
	"sync"
)

type PodInfo struct {
	PodName string `json:"podName"`
	Ifindex int    `json:"ifindex"`
	SrcMac  string `json:"srcMac"`
}

type PodInfoStore struct {
	mu       sync.RWMutex
	podInfos map[string]*PodInfo
}

var globalPodInfoStore *PodInfoStore

func init() {
	globalPodInfoStore = &PodInfoStore{
		podInfos: make(map[string]*PodInfo),
	}
}

func GetGlobalPodInfoStore() *PodInfoStore {
	return globalPodInfoStore
}

func (s *PodInfoStore) Get(podName string) (interface{}, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	info, exists := s.podInfos[podName]
	return info, exists
}

func (s *PodInfoStore) Set(podName string, info *PodInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.podInfos[podName] = info
}

func (s *PodInfoStore) Delete(podName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.podInfos, podName)
}
