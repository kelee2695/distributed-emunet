package cnifunc

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/containernetworking/cni/pkg/skel"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"

	cni_network "EMU_CNI/cni-plugin/cni-network"
	"EMU_CNI/tools/config"
	ebpftc "EMU_CNI/tools/ebpf/ebpf-tc"
)

type EmuCNIPlugin struct {
	ResultOutputPath string
}

// WriteResultToFile 将结果记录到本地日志文件以便审计
func (e *EmuCNIPlugin) WriteResultToFile(data []byte) error {
	file, err := os.OpenFile(e.ResultOutputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(data, '\n'))
	return err
}

// Add 实现 CNI ADD 命令
func (e *EmuCNIPlugin) Add(args *skel.CmdArgs) error {
	// 1. 解析基础配置
	n, err := config.LoadNetConf(args.StdinData)
	if err != nil {
		return fmt.Errorf("failed to load netconf: %w", err)
	}

	// 将 prevResult 转换为当前标准格式
	currentResult, err := current.NewResultFromResult(n.PrevResult)
	if err != nil {
		return fmt.Errorf("failed to convert prevResult: %v", err)
	}

	// --- 核心逻辑：跨命名空间抓取 veth 和 MAC 信息 ---
	var (
		hostVethName string
		hostIfIndex  int
		containerMac string
	)

	// 必须锁定线程，防止切换 Namespace 时协程被调度到其他线程
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// 保存主机 Namespace 句柄
	originNs, err := ns.GetCurrentNS()
	if err != nil {
		return fmt.Errorf("failed to get host ns: %v", err)
	}
	defer originNs.Close()

	// 打开并进入容器 Namespace
	targetNs, err := ns.GetNS(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %s: %v", args.Netns, err)
	}
	defer targetNs.Close()

	// [进入容器空间]
	if err := targetNs.Set(); err != nil {
		return fmt.Errorf("failed to enter netns: %v", err)
	}

	// 在容器内查找 eth0 并获取 MAC 与对端 (主机端) Index
	cLink, err := netlink.LinkByName(args.IfName)
	if err != nil {
		originNs.Set() // 失败也要尝试切回
		return fmt.Errorf("container eth0 not found: %v", err)
	}
	containerMac = cLink.Attrs().HardwareAddr.String()
	pIndex := cLink.Attrs().ParentIndex

	// [切回主机空间]
	originNs.Set()

	// 在主机端查找对应的 veth 物理信息
	hLink, err := netlink.LinkByIndex(pIndex)
	if err != nil {
		return fmt.Errorf("host veth not found by index %d: %v", pIndex, err)
	}
	hostVethName = hLink.Attrs().Name
	hostIfIndex = hLink.Attrs().Index

	// --- 业务逻辑：eBPF 与 Agent 交互 ---

	// 1. 初始化并附加 eBPF TC 程序
	if err := ebpftc.Init(); err != nil {
		return fmt.Errorf("ebpf init failed: %v", err)
	}
	if err := ebpftc.AttachTCByName(hostVethName); err != nil {
		return fmt.Errorf("attach eBPF to %s failed: %v", hostVethName, err)
	}

	// 2. 解析 Pod 标识信息
	podName := ""
	if args.Args != "" {
		for _, arg := range strings.Split(args.Args, ";") {
			if strings.HasPrefix(arg, "K8S_POD_NAME=") {
				podName = strings.TrimPrefix(arg, "K8S_POD_NAME=")
				break
			}
		}
	}
	if podName == "" {
		podName = args.ContainerID
	}

	// 3. 注册 Pod 信息到 Agent 服务
	agentClient := cni_network.NewAgentClient()
	if err := agentClient.AddPodInfo(podName, hostIfIndex, containerMac); err != nil {
		// 记录到 stderr，不阻断 CNI 主流程
		fmt.Fprintf(os.Stderr, "emu-cni warning: call agent failed: %v\n", err)
	}

	// --- 结束流程 ---

	// 将主机端接口信息追加到结果中，增强可观测性
	currentResult.Interfaces = append(currentResult.Interfaces, &current.Interface{
		Name: hostVethName,
	})

	// 最终必须将 JSON 结果输出到标准输出，否则 Kubelet 会报错
	finalData, _ := json.Marshal(currentResult)
	fmt.Print(string(finalData))

	// 写入本地审计日志
	e.WriteResultToFile(finalData)

	return nil
}

// Del 实现 CNI DEL 命令
func (e *EmuCNIPlugin) Del(args *skel.CmdArgs) error {
	podName := ""
	if args.Args != "" {
		for _, arg := range strings.Split(args.Args, ";") {
			if strings.HasPrefix(arg, "K8S_POD_NAME=") {
				podName = strings.TrimPrefix(arg, "K8S_POD_NAME=")
				break
			}
		}
	}
	if podName == "" {
		podName = args.ContainerID
	}

	agentClient := cni_network.NewAgentClient()
	if err := agentClient.DeletePodInfo(podName); err != nil {
		fmt.Fprintf(os.Stderr, "emu-cni warning: delete pod info failed: %v\n", err)
	}

	return nil
}

// Check 实现 CNI CHECK 命令
func (e *EmuCNIPlugin) Check(args *skel.CmdArgs) error {
	n, err := config.LoadNetConf(args.StdinData)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(n.PrevResult)
	fmt.Print(string(data))
	return nil
}
