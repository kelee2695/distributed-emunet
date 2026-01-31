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
	Mode          string // "gen", "clean", "churn"
	MasterURL     string
	Namespace     string
	EmuNetName    string
	TotalRequests int
	Concurrency   int
	Interval      time.Duration // [新增] 更新间隔 (例如 10ms)
}

type PodInfo struct {
	PodName  string `json:"podName"`
	NodeName string `json:"nodeName"`
	PodIP    string `json:"podIP"`
	Phase    string `json:"phase"`
}

type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data"`
	Error   string      `json:"error"`
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

	// 针对极高并发的 Client 配置
	client := &http.Client{
		Timeout: 5 * time.Second, // 快速超时
		Transport: &http.Transport{
			MaxIdleConns:        cfg.Concurrency * 4, // 增大连接池
			MaxIdleConnsPerHost: cfg.Concurrency * 2,
			IdleConnTimeout:     90 * time.Second,
			DisableKeepAlives:   false,
		},
	}

	switch cfg.Mode {
	case "gen":
		doGenerate(client, cfg)
	case "clean":
		doClean(client, cfg)
	case "churn": // [新增模式]
		doChurn(client, cfg)
	default:
		log.Fatalf("未知模式: %s (可选: gen, clean, churn)", cfg.Mode)
	}
}

func parseFlags() Config {
	cfg := Config{}
	flag.StringVar(&cfg.Mode, "mode", "gen", "模式: 'gen'(一次性), 'clean'(清理), 'churn'(持续高频抖动)")
	flag.StringVar(&cfg.MasterURL, "url", "http://localhost:8082", "Master API 地址")
	flag.StringVar(&cfg.Namespace, "ns", "default", "Namespace")
	flag.StringVar(&cfg.EmuNetName, "name", "emunet-example", "EmuNet Name")
	flag.IntVar(&cfg.TotalRequests, "count", 1000, "总请求数 (仅gen模式)")
	flag.IntVar(&cfg.Concurrency, "workers", 50, "并发 Worker 数")
	flag.DurationVar(&cfg.Interval, "interval", 10*time.Millisecond, "churn模式下的循环间隔 (默认10ms)")
	flag.Parse()
	return cfg
}

// ================= 持续抖动模式 (Churn) =================

func doChurn(client *http.Client, cfg Config) {
	log.Printf("=== [CHURN] 开始持续高频抖动 (间隔: %v) ===", cfg.Interval)

	// 1. 获取并筛选 Pod
	pods, err := fetchPodsViaAPI(client, cfg)
	if err != nil {
		log.Fatalf("获取 Pod 列表失败: %v", err)
	}
	var activePods []PodInfo
	for _, p := range pods {
		if p.Phase == "Running" && p.NodeName != "" {
			activePods = append(activePods, p)
		}
	}
	n := len(activePods)
	if n < 2 {
		log.Fatalf("有效 Pod 数量不足 (%d)", n)
	}

	// 2. 预计算所有链路组合 (只计算一次)
	var allPairs []PodPair
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			allPairs = append(allPairs, PodPair{
				Pod1: activePods[i].PodName,
				Pod2: activePods[j].PodName,
			})
		}
	}
	log.Printf("检测到 %d 个 Pod，共 %d 条链路。正在启动 %d 个 Worker...", n, len(allPairs), cfg.Concurrency)

	// 3. 启动 Workers
	jobs := make(chan PodPair, len(allPairs)*2) // 缓冲区设大一点
	stats := &Stats{}

	targetURL := fmt.Sprintf("%s/api/v1/ebpf/entry/by-pods", cfg.MasterURL)

	for w := 0; w < cfg.Concurrency; w++ {
		go func() {
			// 每个 Worker 独立随机源
			r := rand.New(rand.NewSource(time.Now().UnixNano()))
			for p := range jobs {
				// --- 随机生成大延迟和丢包 ---
				randomDelay := uint32(r.Intn(100000) + 10000) // 10ms ~ 110ms
				randomLoss := uint32(r.Intn(2500) + 500)      // 5% ~ 30%
				randomJitter := uint32(r.Intn(1000) + 100)    // 0.1ms ~ 1.1ms

				reqData := EBPFEntryByPodsRequest{
					Pod1:            p.Pod1,
					Pod2:            p.Pod2,
					ThrottleRateBps: 0,
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

	// 4. 定时器循环 (Ticker Loop)
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	log.Printf("开始循环更新，按 Ctrl+C 停止...")

	// 启动一个监控协程打印 TPS
	go func() {
		lastSuccess := int64(0)
		for range time.Tick(1 * time.Second) {
			curr := atomic.LoadInt64(&stats.Success)
			failed := atomic.LoadInt64(&stats.Failed)
			log.Printf("TPS: %d/s | 总成功: %d | 总失败: %d", curr-lastSuccess, curr, failed)
			lastSuccess = curr
		}
	}()

	for range ticker.C {
		batchStart := time.Now()

		// 将所有链路推入任务队列
		// 如果 Worker 处理不过来，这里会阻塞，导致下一个 Ticker 被延迟（Backpressure）
		for _, p := range allPairs {
			jobs <- p
		}

		elapsed := time.Since(batchStart)
		if elapsed > cfg.Interval {
			// 如果发送一轮的时间超过了间隔，说明系统达到瓶颈
			log.Printf("[警告] 一轮更新耗时 %v (超过设定的 %v)，系统过载，产生延迟。", elapsed, cfg.Interval)
		}
	}
}

func doGenerate(client *http.Client, cfg Config) {
	log.Printf("=== [GEN] 开始随机生成规则 ===")

	// 获取 Pod 列表
	pods, err := fetchPodsViaAPI(client, cfg)
	if err != nil {
		log.Fatalf("获取 Pod 列表失败: %v", err)
	}

	// 过滤有效 Pod
	var activePods []PodInfo
	for _, p := range pods {
		if p.Phase == "Running" && p.NodeName != "" {
			activePods = append(activePods, p)
		}
	}

	if len(activePods) < 2 {
		log.Fatalf("有效 Pod 数量不足 (%d)，无法压测", len(activePods))
	}
	log.Printf("有效种子 Pod 数量: %d, 目标生成请求: %d", len(activePods), cfg.TotalRequests)

	start := time.Now()
	stats := Stats{}
	var wg sync.WaitGroup

	// 创建任务通道
	jobs := make(chan int, cfg.TotalRequests)
	for i := 0; i < cfg.TotalRequests; i++ {
		jobs <- i
	}
	close(jobs)

	targetURL := fmt.Sprintf("%s/api/v1/ebpf/entry/by-pods", cfg.MasterURL)

	// 启动 Worker
	wg.Add(cfg.Concurrency)
	for w := 0; w < cfg.Concurrency; w++ {
		go func() {
			defer wg.Done()
			r := rand.New(rand.NewSource(time.Now().UnixNano()))

			for range jobs {
				p1 := activePods[r.Intn(len(activePods))]
				p2 := activePods[r.Intn(len(activePods))]
				for p1.PodName == p2.PodName {
					p2 = activePods[r.Intn(len(activePods))]
				}

				// 参数: 延迟(10-110ms), 丢包(5-30%), 抖动(0.1-1.1ms)
				reqData := EBPFEntryByPodsRequest{
					Pod1:            p1.PodName,
					Pod2:            p2.PodName,
					ThrottleRateBps: 0,
					Delay:           uint32(r.Intn(100000) + 10000),
					LossRate:        uint32(r.Intn(2500) + 500),
					Jitter:          uint32(r.Intn(1000) + 100),
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

	// 简单的统计报告
	total := stats.Success + stats.Failed
	tps := 0.0
	if time.Since(start).Seconds() > 0 {
		tps = float64(total) / time.Since(start).Seconds()
	}
	fmt.Printf("\n--- 生成模式统计 ---\n耗时: %v\n成功: %d, 失败: %d\nTPS: %.2f\n-------------------\n",
		time.Since(start), stats.Success, stats.Failed, tps)
}

func doClean(client *http.Client, cfg Config) {
	log.Printf("=== [CLEAN] 开始全量暴力清理 ===")

	pods, err := fetchPodsViaAPI(client, cfg)
	if err != nil {
		log.Fatalf("获取 Pod 列表失败: %v", err)
	}

	n := len(pods)
	if n < 2 {
		log.Println("Pod 数量少于 2，无需清理。")
		return
	}

	totalPairs := n * (n - 1) / 2
	log.Printf("检测到 %d 个 Pod，生成 %d 对唯一组合进行清理...", n, totalPairs)

	start := time.Now()
	stats := Stats{}
	var wg sync.WaitGroup

	jobs := make(chan PodPair, 10000)
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
				if err := sendJSON(client, "DELETE", targetURL, reqData); err == nil {
					atomic.AddInt64(&stats.Success, 1)
				} else {
					atomic.AddInt64(&stats.Failed, 1)
				}
			}
		}()
	}

	go func() {
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				jobs <- PodPair{
					Pod1: pods[i].PodName,
					Pod2: pods[j].PodName,
				}
			}
		}
		close(jobs)
	}()

	wg.Wait()

	total := stats.Success + stats.Failed
	tps := 0.0
	if time.Since(start).Seconds() > 0 {
		tps = float64(total) / time.Since(start).Seconds()
	}
	fmt.Printf("\n--- 清理模式统计 ---\n耗时: %v\n成功: %d, 失败: %d\nTPS: %.2f\n-------------------\n",
		time.Since(start), stats.Success, stats.Failed, tps)
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
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func fetchPodsViaAPI(client *http.Client, cfg Config) ([]PodInfo, error) {
	url := fmt.Sprintf("%s/api/v1/emunets/%s/%s/pods", cfg.MasterURL, cfg.Namespace, cfg.EmuNetName)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("JSON解析失败: %v", err)
	}
	if !apiResp.Success {
		return nil, fmt.Errorf("API 错误: %s", apiResp.Error)
	}

	dataBytes, _ := json.Marshal(apiResp.Data)
	var pods []PodInfo
	if err := json.Unmarshal(dataBytes, &pods); err != nil {
		return nil, fmt.Errorf("Pod数据解析失败: %v", err)
	}
	return pods, nil
}
