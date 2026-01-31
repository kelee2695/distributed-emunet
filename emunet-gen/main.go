package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// ================= 配置与结构体 =================

type Config struct {
	Mode          string // "gen" or "clean"
	MasterURL     string
	Namespace     string
	EmuNetName    string
	TotalRequests int
	Concurrency   int
}

type PodInfo struct {
	PodName  string `json:"podName"`
	NodeName string `json:"nodeName"`
}

type EBPFEntryByPodsRequest struct {
	Pod1            string `json:"pod1"`
	Pod2            string `json:"pod2"`
	ThrottleRateBps uint32 `json:"throttleRateBps"`
	Delay           uint32 `json:"delay"`
	LossRate        uint32 `json:"lossRate"`
	Jitter          uint32 `json:"jitter"`
}

type EBPFEntryDeleteByPodsRequest struct {
	Pod1 string `json:"pod1"`
	Pod2 string `json:"pod2"`
}

// 简单的任务结构
type PodPair struct {
	Pod1 string
	Pod2 string
}

type Stats struct {
	Success int64
	Failed  int64
}

// ================= 主函数 =================

func main() {
	cfg := parseFlags()
	rand.Seed(time.Now().UnixNano())

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        cfg.Concurrency,
			MaxIdleConnsPerHost: cfg.Concurrency,
		},
	}

	switch cfg.Mode {
	case "gen":
		doGenerate(client, cfg)
	case "clean":
		doClean(client, cfg)
	default:
		log.Fatalf("未知模式: %s (请使用 'gen' 或 'clean')", cfg.Mode)
	}
}

func parseFlags() Config {
	cfg := Config{}
	flag.StringVar(&cfg.Mode, "mode", "gen", "运行模式: 'gen' (生成规则) 或 'clean' (暴力清空所有规则)")
	flag.StringVar(&cfg.MasterURL, "url", "http://localhost:8082", "Master API 地址")
	flag.StringVar(&cfg.Namespace, "ns", "default", "EmuNet Namespace")
	flag.StringVar(&cfg.EmuNetName, "name", "emunet-example", "EmuNet Name")
	flag.IntVar(&cfg.TotalRequests, "count", 1000, "生成的规则总数 (仅 gen 模式)")
	flag.IntVar(&cfg.Concurrency, "workers", 20, "并发线程数")
	flag.Parse()
	return cfg
}

// ================= 生成逻辑 (Generate) =================

func doGenerate(client *http.Client, cfg Config) {
	log.Printf("=== [GEN] 开始随机生成规则 ===")

	pods, err := fetchPods(client, cfg)
	if err != nil {
		log.Fatalf("获取 Pod 列表失败: %v", err)
	}
	if len(pods) < 2 {
		log.Fatalf("Pod 数量不足 (%d)，无法压测", len(pods))
	}
	log.Printf("种子 Pod 数量: %d, 目标生成请求: %d", len(pods), cfg.TotalRequests)

	start := time.Now()
	stats := Stats{}
	var wg sync.WaitGroup

	jobs := make(chan int, cfg.TotalRequests)
	for i := 0; i < cfg.TotalRequests; i++ {
		jobs <- i
	}
	close(jobs)

	targetURL := fmt.Sprintf("%s/api/v1/ebpf/entry/by-pods", cfg.MasterURL)

	wg.Add(cfg.Concurrency)
	for w := 0; w < cfg.Concurrency; w++ {
		go func() {
			defer wg.Done()
			for range jobs {
				p1 := pods[rand.Intn(len(pods))]
				p2 := pods[rand.Intn(len(pods))]
				if p1.PodName == p2.PodName {
					p2 = pods[(rand.Intn(len(pods))+1)%len(pods)]
				}

				// 延迟: 10ms ~ 110ms
				randomDelay := uint32(rand.Intn(10000) + 1000)
				// 丢包: 5% ~ 30%
				randomLoss := uint32(rand.Intn(2500) + 500)
				// 抖动: 0.1ms ~ 1.1ms
				randomJitter := uint32(rand.Intn(100) + 10)

				reqData := EBPFEntryByPodsRequest{
					Pod1:            p1.PodName,
					Pod2:            p2.PodName,
					ThrottleRateBps: 10000000,
					Delay:           randomDelay,
					LossRate:        randomLoss,
					Jitter:          randomJitter,
				}

				if err := sendJSON(client, "POST", targetURL, reqData); err == nil {
					atomic.AddInt64(&stats.Success, 1)
				} else {
					atomic.AddInt64(&stats.Failed, 1)
				}
			}
		}()
	}
	wg.Wait()

	printReport("生成压测", stats, time.Since(start))
}

// ================= 清理逻辑 (Clean) =================

func doClean(client *http.Client, cfg Config) {
	log.Printf("=== [CLEAN] 开始全量暴力清理 ===")

	// 1. 获取最新 Pod 列表
	pods, err := fetchPods(client, cfg)
	if err != nil {
		log.Fatalf("获取 Pod 列表失败: %v", err)
	}

	n := len(pods)
	if n < 2 {
		log.Println("Pod 数量少于 2，无需清理。")
		return
	}

	// 2. 计算所有唯一组合 (排列组合 C(n, 2))
	// 例如 [A, B, C] -> (A,B), (A,C), (B,C)
	totalPairs := n * (n - 1) / 2
	log.Printf("检测到 %d 个 Pod，生成 %d 对唯一组合进行清理...", n, totalPairs)

	start := time.Now()
	stats := Stats{}
	var wg sync.WaitGroup

	// 创建任务通道
	jobs := make(chan PodPair, totalPairs)

	// 3. 启动 Worker
	targetURL := fmt.Sprintf("%s/api/v1/ebpf/entry/by-pods", cfg.MasterURL)

	wg.Add(cfg.Concurrency)
	for w := 0; w < cfg.Concurrency; w++ {
		go func() {
			defer wg.Done()
			for p := range jobs {
				reqData := EBPFEntryDeleteByPodsRequest{
					Pod1: p.Pod1,
					Pod2: p.Pod2,
				}

				// 无论 Master 报错与否（例如规则本身就不存在），我们都发一遍
				if err := sendJSON(client, "DELETE", targetURL, reqData); err == nil {
					atomic.AddInt64(&stats.Success, 1)
				} else {
					// 这里的 Failed 多数情况是因为规则本来就不存在，可以忽略
					atomic.AddInt64(&stats.Failed, 1)
				}
			}
		}()
	}

	// 4. 发送所有组合任务
	// 只需要遍历 i < j 的情况，不需要对称发送，因为 Master API 会处理双向
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			jobs <- PodPair{
				Pod1: pods[i].PodName,
				Pod2: pods[j].PodName,
			}
		}
	}
	close(jobs)

	wg.Wait()
	printReport("全量清理", stats, time.Since(start))
}

// ================= 辅助函数 =================

func sendJSON(client *http.Client, method, url string, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(method, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 为了性能，不读取 Body 也不报错详情
		return fmt.Errorf("Status %d", resp.StatusCode)
	}
	return nil
}

func fetchPods(client *http.Client, cfg Config) ([]PodInfo, error) {
	url := fmt.Sprintf("%s/api/v1/emunets/%s/%s/pods", cfg.MasterURL, cfg.Namespace, cfg.EmuNetName)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var apiResp struct {
		Success bool      `json:"success"`
		Data    []PodInfo `json:"data"`
		Error   string    `json:"error"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}
	if !apiResp.Success {
		return nil, fmt.Errorf(apiResp.Error)
	}
	return apiResp.Data, nil
}

func printReport(title string, stats Stats, duration time.Duration) {
	total := stats.Success + stats.Failed
	tps := 0.0
	if duration.Seconds() > 0 {
		tps = float64(total) / duration.Seconds() // 清理时我们关注总处理能力，包括404
	}
	fmt.Printf("\n--- %s 统计 ---\n", title)
	fmt.Printf("耗时: %v\n", duration)
	fmt.Printf("请求总数: %d\n", total)
	fmt.Printf("TPS: %.2f req/s\n", tps)
	fmt.Println("-----------------------")
}
