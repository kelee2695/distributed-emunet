package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	ebpftc "ebpf-test/ebpf-tc"
)

// Node 代表一个动态生成的测试节点
type Node struct {
	ID           int
	NsName       string
	VethName     string
	HostVethName string
	IP           string
	MAC          [6]byte
	HostIfindex  uint32
}

var (
	numNodes     int
	simVeth1Name = "sim-veth1"
	simVeth2Name = "sim-veth2"
)

func main() {
	// 1. 解析命令行参数 -n
	flag.IntVar(&numNodes, "n", 2, "指定参与测试的命名空间数量 (默认: 2，最大: 254)")
	flag.Parse()

	if numNodes < 1 || numNodes > 254 {
		fmt.Printf("错误: 节点数量必须在 1 到 254 之间 (当前指定: %d)\n", numNodes)
		return
	}

	fmt.Printf("=== 创建网络环境 (节点数: %d) ===\n", numNodes)

	cleanupAll(numNodes)
	defer cleanupAll(numNodes)

	// 2. 创建并配置底层网络结构
	nodes, err := setupTestInterfaces(numNodes)
	if err != nil {
		fmt.Printf("创建测试环境失败: %v\n", err)
		return
	}

	fmt.Println("网络环境创建完成!")
	fmt.Println("")
	fmt.Println("资源清单:")
	fmt.Printf("  模拟器桥接网卡: %s <-> %s\n", simVeth1Name, simVeth2Name)
	for _, n := range nodes {
		fmt.Printf("  [Node %d] NS: %s | IP: %-12s | MAC: %02x:%02x:%02x:%02x:%02x:%02x | Veth: %s <-> %s (Ifindex: %d)\n",
			n.ID, n.NsName, n.IP,
			n.MAC[0], n.MAC[1], n.MAC[2], n.MAC[3], n.MAC[4], n.MAC[5],
			n.VethName, n.HostVethName, n.HostIfindex)
	}

	fmt.Println("")
	fmt.Println("=== 初始化 eBPF 程序 ===")

	// 3. 初始化并加载所有的 eBPF 程序
	fmt.Println("[1] 初始化 src_redirect_to_sim...")
	srcSim, err := ebpftc.NewSrcRedirectSim()
	if err != nil {
		fmt.Printf("初始化 src_redirect_to_sim 失败: %v\n", err)
		return
	}
	defer srcSim.Close()

	fmt.Println("[2] 初始化 sim_redirect_to_dst...")
	dstSim, err := ebpftc.NewSimRedirectDst()
	if err != nil {
		fmt.Printf("初始化 sim_redirect_to_dst 失败: %v\n", err)
		return
	}
	defer dstSim.Close()

	fmt.Println("[3] 初始化 TC 抓包程序...")
	tcCap, err := ebpftc.NewTcPacketCapture()
	if err != nil {
		fmt.Printf("初始化 TC 抓包程序失败: %v\n", err)
		return
	}
	defer tcCap.Close()

	fmt.Println("[4] 初始化 Dummy XDP 程序...")
	dummyXdp, err := ebpftc.NewXdpDummy()
	if err != nil {
		fmt.Printf("初始化 Dummy XDP 失败: %v\n", err)
		return
	}
	defer dummyXdp.Close()

	// 4. 获取模拟器桥接网卡的 ifindex
	simVeth1, _ := netlink.LinkByName(simVeth1Name)
	simVeth2, _ := netlink.LinkByName(simVeth2Name)
	simVeth1Ifindex := uint32(simVeth1.Attrs().Index)

	// 5. 挂载并在 Map 中配置所有节点的信息
	fmt.Println("[5] 配置 eBPF Map 及全局路由...")

	// 配置发送端的去向（统一发往 simVeth1）
	if err := srcSim.SetSimIfindex(simVeth1Ifindex); err != nil {
		fmt.Printf("设置 sim_config_map 失败: %v\n", err)
		return
	}
	if err := srcSim.AddDevmapEntry(simVeth1Ifindex); err != nil {
		fmt.Printf("设置 xdp_src_redirect_map 失败: %v\n", err)
		return
	}

	// 将接收端挂载到 simVeth2
	if err := dstSim.Attach(simVeth2); err != nil {
		fmt.Printf("挂载 sim_redirect_to_dst 到 sim-veth2 失败: %v\n", err)
		return
	}

	fmt.Printf("[6] 为 %d 个节点执行挂载与 Map 注入...\n", numNodes)
	for _, n := range nodes {
		// 写入 MAC 表 (用于单播)
		if err := dstSim.SetMacEntry(n.MAC, n.HostIfindex); err != nil {
			fmt.Printf("设置 mac_table (Node %d) 失败: %v\n", n.ID, err)
			return
		}

		// 写入 TX 端口池 (用于广播/组播 devmap)
		if err := dstSim.AddTxPort(n.HostIfindex); err != nil {
			fmt.Printf("设置 tx_ports (Node %d) 失败: %v\n", n.ID, err)
			return
		}

		// 将 srcSim XDP 挂载到宿主机端 Veth
		hostIface, _ := netlink.LinkByName(n.HostVethName)
		if err := srcSim.Attach(hostIface); err != nil {
			fmt.Printf("挂载 srcSim 到 %s 失败: %v\n", n.HostVethName, err)
			return
		}

		// 进入命名空间内部，挂载 TC 和 Dummy XDP
		if err := attachTcToNsInterface(n.NsName, n.VethName, tcCap, dummyXdp); err != nil {
			fmt.Printf("在命名空间 %s 中挂载抓包程序失败: %v\n", n.NsName, err)
			return
		}
	}

	fmt.Println("")
	fmt.Println("============================================")
	fmt.Printf("eBPF 动态组网配置完成! (共 %d 节点)\n", numNodes)
	fmt.Println("============================================")
	fmt.Println("")
	fmt.Println("按 Enter 退出并清理环境...")
	fmt.Scanln()
}

// setupTestInterfaces 动态创建所需数量的命名空间和 veth-pair，并返回包含所有详细信息的 Node 切片
func setupTestInterfaces(k int) ([]Node, error) {
	var nodes []Node

	fmt.Println("  -> 创建模拟器桥接网卡 (sim-veth1 <-> sim-veth2)...")
	simVeth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: simVeth1Name},
		PeerName:  simVeth2Name,
	}
	if err := netlink.LinkAdd(simVeth); err != nil {
		return nil, fmt.Errorf("创建 simVeth 失败: %v", err)
	}
	exec.Command("ip", "link", "set", simVeth1Name, "up").Run()
	exec.Command("ip", "link", "set", simVeth2Name, "up").Run()

	for i := 1; i <= k; i++ {
		node := Node{
			ID:           i,
			NsName:       fmt.Sprintf("sim-ns%d", i),
			VethName:     fmt.Sprintf("veth-ns%d", i),
			HostVethName: fmt.Sprintf("veth-ns%d-host", i),
			IP:           fmt.Sprintf("10.0.0.%d/24", i),
		}

		fmt.Printf("  -> 初始化节点 %d (%s)...\n", i, node.NsName)

		// 1. 创建 Network Namespace
		if err := exec.Command("ip", "netns", "add", node.NsName).Run(); err != nil {
			return nil, fmt.Errorf("创建 netns %s 失败: %v", node.NsName, err)
		}

		// 2. 创建 veth-pair
		veth := &netlink.Veth{
			LinkAttrs: netlink.LinkAttrs{Name: node.HostVethName},
			PeerName:  node.VethName,
		}
		if err := netlink.LinkAdd(veth); err != nil {
			return nil, fmt.Errorf("创建 veth %s 失败: %v", node.HostVethName, err)
		}

		// 3. 将一端移动到 namespace 中
		if err := moveToNs(node.VethName, node.NsName); err != nil {
			return nil, fmt.Errorf("移动 %s 到 %s 失败: %v", node.VethName, node.NsName, err)
		}

		// 4. 启动网卡
		exec.Command("ip", "link", "set", node.HostVethName, "up").Run()
		exec.Command("ip", "netns", "exec", node.NsName, "ip", "link", "set", node.VethName, "up").Run()

		// 5. 分配 IP 地址
		exec.Command("ip", "netns", "exec", node.NsName, "ip", "addr", "add", node.IP, "dev", node.VethName).Run()

		// 6. 收集生成的属性 (Ifindex 和 MAC 地址)
		hostIface, err := netlink.LinkByName(node.HostVethName)
		if err != nil {
			return nil, fmt.Errorf("获取网卡 %s 失败: %v", node.HostVethName, err)
		}
		node.HostIfindex = uint32(hostIface.Attrs().Index)
		node.MAC = getMacFromNs(node.NsName, node.VethName)

		nodes = append(nodes, node)
	}

	return nodes, nil
}

// attachTcToNsInterface 在指定命名空间内的接口上挂载 TC 和 Dummy XDP 程序
func attachTcToNsInterface(nsName, ifaceName string, tcCap *ebpftc.TcPacketCapture, dummyXdp *ebpftc.XdpDummy) error {
	// 打开命名空间
	nsPath := fmt.Sprintf("/var/run/netns/%s", nsName)
	nsFd, err := os.Open(nsPath)
	if err != nil {
		return fmt.Errorf("打开命名空间失败: %v", err)
	}
	defer nsFd.Close()

	// 保存当前网络命名空间
	origNsFd, err := os.Open("/proc/self/ns/net")
	if err != nil {
		return fmt.Errorf("保存当前命名空间失败: %v", err)
	}
	defer origNsFd.Close()

	// 切换到目标命名空间
	if err := unix.Setns(int(nsFd.Fd()), unix.CLONE_NEWNET); err != nil {
		return fmt.Errorf("切换命名空间失败: %v", err)
	}

	// 在目标命名空间内获取接口
	iface, err := netlink.LinkByName(ifaceName)
	if err != nil {
		// 恢复原命名空间
		unix.Setns(int(origNsFd.Fd()), unix.CLONE_NEWNET)
		return fmt.Errorf("获取接口失败: %v", err)
	}

	// 挂载 Dummy XDP (接住底层的 xdp_frame 并放行给协议栈)
	if err := dummyXdp.Attach(iface); err != nil {
		unix.Setns(int(origNsFd.Fd()), unix.CLONE_NEWNET)
		return fmt.Errorf("挂载 Dummy XDP 程序失败: %v", err)
	}

	// 挂载 TC 程序
	if err := tcCap.Attach(iface); err != nil {
		unix.Setns(int(origNsFd.Fd()), unix.CLONE_NEWNET)
		return fmt.Errorf("挂载 TC 程序失败: %v", err)
	}

	// 恢复原命名空间
	if err := unix.Setns(int(origNsFd.Fd()), unix.CLONE_NEWNET); err != nil {
		return fmt.Errorf("恢复命名空间失败: %v", err)
	}

	return nil
}

func moveToNs(ifaceName, nsName string) error {
	iface, err := netlink.LinkByName(ifaceName)
	if err != nil {
		return err
	}
	fd, err := os.Open(fmt.Sprintf("/var/run/netns/%s", nsName))
	if err != nil {
		return err
	}
	defer fd.Close()
	return netlink.LinkSetNsFd(iface, int(fd.Fd()))
}

// cleanupAll 动态清理指定数量的命名空间资源
func cleanupAll(k int) {
	fmt.Println("清理已有资源...")
	exec.Command("ip", "link", "del", simVeth1Name).Run()

	for i := 1; i <= k; i++ {
		nsName := fmt.Sprintf("sim-ns%d", i)
		hostVeth := fmt.Sprintf("veth-ns%d-host", i)

		exec.Command("ip", "netns", "del", nsName).Run()
		exec.Command("ip", "link", "del", hostVeth).Run()
	}

	ebpftc.CleanupAllResources()
}

// getMacFromNs 获取命名空间内网卡的 MAC 地址（字节数组格式）
func getMacFromNs(nsName, ifaceName string) [6]byte {
	var mac [6]byte
	out, err := exec.Command("ip", "netns", "exec", nsName, "cat", "/sys/class/net/"+ifaceName+"/address").Output()
	if err != nil {
		fmt.Printf("获取 %s 内 %s 的 MAC 地址失败: %v\n", nsName, ifaceName, err)
		return mac
	}

	// 解析 MAC 地址格式: "xx:xx:xx:xx:xx:xx\n"
	macStr := string(out)
	var b0, b1, b2, b3, b4, b5 uint8
	_, err = fmt.Sscanf(macStr, "%02x:%02x:%02x:%02x:%02x:%02x",
		&b0, &b1, &b2, &b3, &b4, &b5)
	if err == nil {
		mac = [6]byte{b0, b1, b2, b3, b4, b5}
	}

	return mac
}
