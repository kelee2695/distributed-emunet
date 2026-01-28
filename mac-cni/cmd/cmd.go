package cmd

import (
	"fmt"
	"net"

	"MAC_CNI/result"
	"MAC_CNI/types"
	"MAC_CNI/vxlan"

	"github.com/containernetworking/cni/pkg/skel"
	cniVersion "github.com/containernetworking/cni/pkg/version"
)

// Cmd provides methods for the CNI ADD, DEL and CHECK commands.
type Cmd struct {
}

// RemoteIP 对端物理IP地址，可通过编译时 -ldflags 覆盖
// 编译时设置: go build -ldflags "-X 'MAC_CNI/cmd.RemoteIP=192.168.1.104'"
var RemoteIP string = "192.168.1.104"

// writeLog 将日志信息写入到 mac-cni.log 文件中
func writeLog(format string, v ...interface{}) error {
	// 打开日志文件，追加模式
	// logFile, err := os.OpenFile("/home/node01/MAC_CNI/mac-cni.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	// if err != nil {
	// 	return fmt.Errorf("unable to open log file: %w", err)
	// }
	// defer logFile.Close()

	// // 设置日志输出到文件
	// log.SetOutput(logFile)
	// log.Printf(format, v...)

	return nil
}

func (c *Cmd) Add(args *skel.CmdArgs) error {
	// configuration load
	n, err := types.LoadNetConf(args.StdinData)
	if err != nil {
		return fmt.Errorf("unable to parse CNI configuration %q: %w", string(args.StdinData), err)
	}
	if err := writeLog("CNI configuration: %+v\n", n); err != nil {
		return err
	}

	// 检查 vxlanBr 网桥是否存在
	exists, err := vxlan.BridgeExists("vxlanBr")
	if err != nil {
		return fmt.Errorf("unable to check bridge vxlanBr: %w", err)
	}
	if !exists {
		if err := vxlan.CreateBridge("vxlanBr", 1500); err != nil {
			return fmt.Errorf("failed to create bridge vxlanBr: %w", err)
		}
	}
	// 检查 vxlanBr 网桥是否已启动
	if up, err := vxlan.IsBridgeUp("vxlanBr"); err != nil {
		return fmt.Errorf("unable to check bridge vxlanBr status: %w", err)
	} else if !up {
		if err := vxlan.SetBridgeUp("vxlanBr"); err != nil {
			return fmt.Errorf("failed to set bridge vxlanBr up: %w", err)
		}
	}
	// 配置 VXLAN 接口
	config := vxlan.VXLANConfig{
		Name:       "vxlan0",                     // VXLAN接口名称
		VNI:        100,                          // VXLAN网络标识符
		RemoteIP:   net.ParseIP(RemoteIP),        // 对端物理IP地址（编译时配置）
		BridgeName: "vxlanBr",                    // 已存在的网桥名称
		Device:     "tailscale0",                     // 底层物理网卡
		Port:       4789,                         // VXLAN端口
		Learning:   true,                         // 启用MAC地址学习
		GBP:        false,                        // 禁用组策略扩展
		Ageing:     300,                          // MAC地址老化时间（秒）
		TTL:        16,                           // 多播TTL
		MTU:        1200,                         // 接口MTU
	}
	// 创建 VXLAN 接口（创建函数内包含检查是否存在逻辑，若存在则直接返回nil）
	if err := vxlan.CreateVXLAN(config); err != nil {
		return fmt.Errorf("failed to create VXLAN: %w", err)
	}
	//创建虚拟网卡对并分配IP
	hostVeth := "veth" + args.ContainerID[:8]
	containerVeth := "eth0"
	info, ip, err := vxlan.AddVethPairWithIP(hostVeth, containerVeth, "vxlanBr", args.Netns, 1500)
	if err != nil {
		return fmt.Errorf("failed to add veth pair with IP: %w", err)
	}

	// 构造 AddResult
	ifaceIdx := 0
	addResult := result.NewAddResult(
		n.CNIVersion,
		result.NewCniInterfaces(
			result.NewCniInterface(hostVeth, info.HostMAC, args.Netns),
			result.NewCniInterface(containerVeth, info.ContainerMAC, args.Netns),
		),
		result.NewIPConfigs(
			result.NewIPConfig("4", net.IPNet{IP: net.ParseIP(ip), Mask: net.CIDRMask(24, 32)}, net.ParseIP(ip), &ifaceIdx),
		),
	)

	// 将 AddResult 序列化为 JSON 并打印到 stdout
	return result.OutputResult(addResult)
}

func (c *Cmd) Del(args *skel.CmdArgs) error {
	// 使用封装的日志函数写入日志
	if err := writeLog("CNI DEL command received for container %s\n", args.ContainerID); err != nil {
		return err
	}

	// 删除 veth pair
	hostVeth := "veth" + args.ContainerID[:8]
	if err := vxlan.DeleteVethPair(hostVeth); err != nil {
		return fmt.Errorf("failed to delete veth pair %s: %w", hostVeth, err)
	}

	return nil
}

func (c *Cmd) Check(args *skel.CmdArgs) error {

	n, err := types.LoadNetConf(args.StdinData)
	if err != nil {
		return fmt.Errorf("unable to parse CNI configuration %q: %w", string(args.StdinData), err)
	}
	// 使用封装的日志函数写入日志
	if err := writeLog("CNI CHECK command received for container %s\n", args.ContainerID); err != nil {
		return err
	}

	// 构造 CheckResult
	checkResult := result.NewCheckResult(
		n.CNIVersion,
		[]string{"0.1.0", "0.2.0", "0.3.0", "0.3.1", "0.4.0", "1.0.0", "1.1.0"},
	)
	// 将 CheckResult 序列化为 JSON 并打印到 stdout
	return result.OutputResult(checkResult)
}

type Option func(cmd *Cmd)

func PluginMain(opts ...Option) {

	cmd := &Cmd{}
	for _, opt := range opts {
		opt(cmd)
	}

	skel.PluginMainFuncs(
		skel.CNIFuncs{
			Add:   cmd.Add,
			Del:   cmd.Del,
			Check: cmd.Check,
		},
		cniVersion.PluginSupports("0.1.0", "0.2.0", "0.3.0", "0.3.1", "0.4.0", "1.0.0", "1.1.0"),
		"MAC CNI plugin",
	)
}
