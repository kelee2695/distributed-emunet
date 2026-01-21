package cnifunc

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/containernetworking/cni/pkg/skel"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/vishvananda/netlink"

	"EMU_CNI/cni-plugin/cni-network"
	"EMU_CNI/tools/config"
	ebpftc "EMU_CNI/tools/ebpf/ebpf-tc"
)

// CheckResult 定义CNI CHECK命令的输出结果
type CheckResult struct {
	CNIVersion        string   `json:"cniVersion"`                  // CNI版本
	SupportedVersions []string `json:"supportedVersions,omitempty"` // 支持的版本列表
}

// NewCheckResult 构造并返回一个 CheckResult 实例
func NewCheckResult(cniVersion string, supportedVersions []string) *CheckResult {
	return &CheckResult{
		CNIVersion:        cniVersion,
		SupportedVersions: supportedVersions,
	}
}

// OutputResult 将任意可JSON序列化的结果结构体转为JSON并输出到stdout
func OutputResult[T any](result T) error {
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal result to json failed: %v", err)
	}
	// 写入stdout并追加换行
	if _, err := os.Stdout.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write result to stdout failed: %v", err)
	}
	return nil
}

// EmuCNIPlugin 实现了CNI插件的命令接口
type EmuCNIPlugin struct {
	ResultOutputPath string // 结果输出路径
	BridgName        string // (废弃) 网桥名称，保留字段兼容结构体
}

// GetResultOutputPath 返回结果输出路径
func (e *EmuCNIPlugin) GetResultOutputPath() string {
	return e.ResultOutputPath
}

// WriteResultToFile 将结果写入文件
func (e *EmuCNIPlugin) WriteResultToFile(data []byte) error {
	outputPath := e.GetResultOutputPath()
	// 以追加模式打开或创建文件
	file, err := os.OpenFile(outputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	// 写入数据并追加换行
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// Add 实现了CNI插件的ADD命令
func (e *EmuCNIPlugin) Add(args *skel.CmdArgs) error {
	n, err := config.LoadNetConf(args.StdinData)
	if err != nil {
		return fmt.Errorf("unable to parse CNI configuration %q: %w", string(args.StdinData), err)
	}

	// 转换为current.Result
	currentResult, err := current.NewResultFromResult(n.PrevResult)
	if err != nil {
		return fmt.Errorf("could not convert prevResult to current version: %v", err)
	}
	// 初始化ebpf-tc程序
	if err := ebpftc.Init(); err != nil {
		return fmt.Errorf("initialize ebpf-tc program failed: %v", err)
	}

	// 将ebpf-tc程序附加到主机端veth接口
	if err := ebpftc.AttachTCByName(currentResult.Interfaces[0].Name); err != nil {
		return fmt.Errorf("attach ebpf-tc program to veth interface failed: %v", err)
	}

	// 从网卡名称获取ifindex
	vethName := currentResult.Interfaces[0].Name
	iface, err := netlink.LinkByName(vethName)
	if err != nil {
		// 记录错误但不影响主流程
		return fmt.Errorf("获取网卡 %s 的ifindex失败: %v", vethName, err)
	} else {
		// 从args.Args中解析Pod名称
		podName := ""
		if args.Args != "" {
			// 解析args.Args，格式为: K8S_POD_NAME=podname;K8S_POD_NAMESPACE=namespace;...
			argsMap := make(map[string]string)
			for _, arg := range strings.Split(args.Args, ";") {
				parts := strings.SplitN(arg, "=", 2)
				if len(parts) == 2 {
					argsMap[parts[0]] = parts[1]
				}
			}
			// 优先使用K8S_POD_NAME
			podName = argsMap["K8S_POD_NAME"]
		}
		
		// 如果没有找到Pod名称，使用ContainerID作为备选
		if podName == "" {
			podName = args.ContainerID
		}

		// 获取Pod的MAC地址
		podMac := ""
		if len(currentResult.Interfaces) > 1 {
			podMac = currentResult.Interfaces[1].Mac
		}

		// 使用cni_network包调用agent服务
		agentClient := cni_network.NewAgentClient()
		if err := agentClient.AddPodInfo(podName, iface.Attrs().Index, podMac); err != nil {
			// 记录错误但不影响主流程
			return fmt.Errorf("调用agent网络服务失败: %v", err)	
		}
	}

	// 结果输出至stdout，供kubelet/runtime读取
	if err := OutputResult(n.PrevResult); err != nil {
		return fmt.Errorf("output result failed: %v", err)
	}

	// 业务行为记录
	data, err := json.Marshal(currentResult)
	if err == nil {
		if err := e.WriteResultToFile(data); err != nil {
			// 记录日志失败不应影响CNI主流程
		}
	}

	return nil
}

// Check 实现了CNI插件的CHECK命令
func (e *EmuCNIPlugin) Check(args *skel.CmdArgs) error {
	n, err := config.LoadNetConf(args.StdinData)
	if err != nil {
		return fmt.Errorf("unable to parse CNI configuration %q: %w", string(args.StdinData), err)
	}

	// 结果输出至stdout，供kubelet/runtime读取
	if err := OutputResult(n.PrevResult); err != nil {
		return fmt.Errorf("output result failed: %v", err)
	}

	// 业务行为记录
	data, err := json.Marshal(n.PrevResult)
	if err == nil {
		if err := e.WriteResultToFile(data); err != nil {
			// 记录日志失败不应影响CNI主流程
		}
	}

	return nil
}

// Del 实现了CNI插件的DEL命令
func (e *EmuCNIPlugin) Del(args *skel.CmdArgs) error {
	// // 解析输入参数
	// cfg := config.NewConfig(args)
	// cfg.SetCNICommand("Del")

	// // 计算veth名称
	// vethName := "veth" + args.ContainerID[:8]

	// // 清理网络资源
	// vethToDelete, err := netlink.LinkByName(vethName)
	// if err != nil {
	// 	// 如果veth不存在，视为清理成功(幂等性)
	// 	if _, ok := err.(netlink.LinkNotFoundError); ok {
	// 		return nil
	// 	}
	// 	return fmt.Errorf("failed to find veth %s: %v", vethName, err)
	// }

	// // 从veth接口上卸载ebpf-tc程序
	// if err := ebpftc.DetachTC(vethToDelete); err != nil {
	// 	// 卸载失败不应阻止删除veth接口，但需要记录错误
	// 	fmt.Fprintf(os.Stderr, "从veth接口卸载ebpf-tc程序失败: %v\n", err)
	// }

	// // 删除veth pair
	// if err := netlink.LinkDel(vethToDelete); err != nil {
	// 	return fmt.Errorf("failed to delete veth %s: %v", vethName, err)
	// }

	// 调用agent网络服务，删除PodInfo
	podName := ""
	if args.Args != "" {
		// 解析args.Args，格式为: K8S_POD_NAME=podname;K8S_POD_NAMESPACE=namespace;...
		argsMap := make(map[string]string)
		for _, arg := range strings.Split(args.Args, ";") {
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) == 2 {
				argsMap[parts[0]] = parts[1]
			}
		}
		// 优先使用K8S_POD_NAME
		podName = argsMap["K8S_POD_NAME"]
	}
	
	// 如果没有找到Pod名称，使用ContainerID作为备选
	if podName == "" {
		podName = args.ContainerID
	}

	// 使用cni_network包调用agent服务删除PodInfo
	if podName != "" {
		agentClient := cni_network.NewAgentClient()
		if err := agentClient.DeletePodInfo(podName); err != nil {
			// 记录错误但不影响主流程
			return fmt.Errorf("调用agent网络服务删除PodInfo失败: %v", err)	
		}
	}

	return nil
}
