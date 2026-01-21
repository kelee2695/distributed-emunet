package vxlan

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

type VXLANConfig struct {
	Name       string // VXLAN接口名称
	VNI        int    // VXLAN网络标识符
	RemoteIP   net.IP // 对端物理IP地址（点对点模式）
	Group      net.IP // 多播组地址（多播模式）
	BridgeName string // 已存在的网桥名称
	Device     string // 底层物理网卡名称
	Port       int    // VXLAN端口
	Learning   bool   // 是否启用MAC学习
	GBP        bool   // 是否启用组策略
	Ageing     int    // MAC地址老化时间
	TTL        int    // 多播TTL
	MTU        int    // 接口MTU
}

func CreateVXLAN(config VXLANConfig) error {
	bridge, err := netlink.LinkByName(config.BridgeName)
	if err != nil {
		return fmt.Errorf("failed to find bridge %s: %w", config.BridgeName, err)
	}
	_, ok := bridge.(*netlink.Bridge)
	if !ok {
		return fmt.Errorf("link %s is not a bridge", config.BridgeName)
	}
	_, err = netlink.LinkByName(config.Name)
	if err == nil {
		return nil
	}
	parentLink, err := netlink.LinkByName(config.Device)
	if err != nil {
		return fmt.Errorf("failed to find device %s: %w", config.Device, err)
	}
	parentIndex := parentLink.Attrs().Index

	vxlan, err := createVXLANInterface(config, parentIndex)
	if err != nil {
		return fmt.Errorf("failed to create VXLAN %s: %w", config.Name, err)
	}
	if err := netlink.LinkSetMaster(vxlan, bridge); err != nil {
		return fmt.Errorf("failed to set VXLAN %s master to bridge %s: %w", config.Name, config.BridgeName, err)
	}

	return nil
}

func createVXLANInterface(config VXLANConfig, parentIndex int) (*netlink.Vxlan, error) {
	// 设置默认值
	if config.Port == 0 {
		config.Port = 4789
	}
	if config.MTU == 0 {
		config.MTU = 1450
	}
	if config.Ageing == 0 {
		config.Ageing = 300
	}
	if config.TTL == 0 {
		config.TTL = 16
	}
	// 创建VXLAN配置
	vxlan := &netlink.Vxlan{
		LinkAttrs: netlink.LinkAttrs{
			Name: config.Name,
			MTU:  config.MTU,
		},
		VxlanId:      config.VNI,
		Port:         config.Port,
		VtepDevIndex: parentIndex,
		Learning:     config.Learning,
		GBP:          config.GBP,
		Age:          config.Ageing,
		TTL:          config.TTL,
	}
	// 设置对端IP或多播组
	if config.RemoteIP != nil {
		vxlan.Group = config.RemoteIP
	} else if config.Group != nil {
		vxlan.Group = config.Group
	}
	// 添加VXLAN接口
	if err := netlink.LinkAdd(vxlan); err != nil {
		return nil, fmt.Errorf("failed to add VXLAN %s: %v", config.Name, err)
	}

	// 启动VXLAN接口
	if err := netlink.LinkSetUp(vxlan); err != nil {
		// 清理已创建的接口
		_ = netlink.LinkDel(vxlan)
		return nil, fmt.Errorf("failed to set VXLAN %s up: %v", config.Name, err)
	}
	return vxlan, nil
}

// DeleteVXLAN 删除VXLAN接口
func DeleteVXLAN(vxlanName string) error {
	// 1. 获取VXLAN接口
	link, err := netlink.LinkByName(vxlanName)
	if err != nil {
		return fmt.Errorf("failed to find VXLAN %s: %w", vxlanName, err)
	}

	// 2. 删除VXLAN接口
	if err := netlink.LinkDel(link); err != nil {
		return fmt.Errorf("failed to delete VXLAN %s: %w", vxlanName, err)
	}
	return nil
}

// CreateBridge 创建基础网桥设备
func CreateBridge(ifName string, mtu int) error {
	// 检查网桥是否已存在
	if _, err := netlink.LinkByName(ifName); err == nil {
		return fmt.Errorf("bridge %s already exists", ifName)
	}

	bridge := &netlink.Bridge{
		LinkAttrs: netlink.LinkAttrs{
			Name: ifName,
			MTU:  mtu,
		},
	}

	if err := netlink.LinkAdd(bridge); err != nil {
		return fmt.Errorf("failed to add bridge %s: %v", ifName, err)
	}

	return nil
}

// BridgeExists 检查指定名称的网桥是否存在
func BridgeExists(ifName string) (bool, error) {
	_, err := netlink.LinkByName(ifName)
	if err != nil {
		if _, ok := err.(netlink.LinkNotFoundError); ok {
			return false, nil
		}
		return false, fmt.Errorf("failed to query bridge %s: %w", ifName, err)
	}
	return true, nil
}

// SetBridgeUp 启动指定的bridge接口
func SetBridgeUp(ifName string) error {
	// 通过名称查找bridge接口
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("failed to find bridge %s: %w", ifName, err)
	}

	// 启动bridge
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("failed to set bridge %s up: %w", ifName, err)
	}

	return nil
}

// SetBridgeDown 关闭指定的bridge接口
func SetBridgeDown(ifName string) error {
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("failed to find bridge %s: %w", ifName, err)
	}

	if err := netlink.LinkSetDown(link); err != nil {
		return fmt.Errorf("failed to set bridge %s down: %w", ifName, err)
	}

	return nil
}

// IsBridgeUp 检查指定名称的网桥是否已启动
func IsBridgeUp(ifName string) (bool, error) {
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return false, fmt.Errorf("failed to find bridge %s: %w", ifName, err)
	}

	// 检查网桥状态标志位中的 UP 标志
	return link.Attrs().Flags&net.FlagUp != 0, nil
}

// VethPairInfo 保存 veth pair 两端的信息
type VethPairInfo struct {
	HostIfName      string
	HostMAC         string
	ContainerIfName string
	ContainerMAC    string
}

// AddVethPair 创建veth pair，将一端移动到容器网络命名空间，另一端连接到网桥
// hostVeth: 主机端veth名称
// containerVeth: 容器端veth名称
// bridgeName: 网桥名称
// netnsPath: 容器网络命名空间路径
// mtu: 网卡的MTU值
// 返回包含两端接口名和MAC地址的结构体
func AddVethPair(hostVeth, containerVeth, bridgeName, netnsPath string, mtu int) (*VethPairInfo, error) {
	info := &VethPairInfo{
		HostIfName:      hostVeth,
		ContainerIfName: containerVeth,
	}

	// 检查并删除已存在的veth接口
	existingLink, err := netlink.LinkByName(hostVeth)
	if err == nil {
		// 接口存在，删除它
		if err := netlink.LinkDel(existingLink); err != nil {
			return nil, fmt.Errorf("failed to delete existing veth %s: %w", hostVeth, err)
		}
	}

	// 生成随机名称用于容器端veth
	randomBytes := make([]byte, 4)
	if _, err := rand.Read(randomBytes); err != nil {
		return nil, fmt.Errorf("failed to generate random name: %w", err)
	}
	tempContainerVeth := "veth" + hex.EncodeToString(randomBytes)

	// 创建veth pair
	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name: hostVeth,
			MTU:  mtu,
		},
		PeerName: tempContainerVeth,
	}

	// 添加veth pair
	if err := netlink.LinkAdd(veth); err != nil {
		return nil, fmt.Errorf("failed to create veth pair %s-%s: %w", hostVeth, tempContainerVeth, err)
	}

	// 获取主机端veth链接
	hostVethLink, err := netlink.LinkByName(hostVeth)
	if err != nil {
		return nil, fmt.Errorf("failed to find host veth %s: %w", hostVeth, err)
	}
	info.HostMAC = hostVethLink.Attrs().HardwareAddr.String()

	// 获取容器端veth链接
	containerVethLink, err := netlink.LinkByName(tempContainerVeth)
	if err != nil {
		return nil, fmt.Errorf("failed to find container veth %s: %w", tempContainerVeth, err)
	}
	info.ContainerMAC = containerVethLink.Attrs().HardwareAddr.String()

	// 获取容器网络命名空间
	containerNs, err := ns.GetNS(netnsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get container netns %s: %w", netnsPath, err)
	}
	defer containerNs.Close()

	// 将容器端veth移动到容器网络命名空间
	if err := netlink.LinkSetNsFd(containerVethLink, int(containerNs.Fd())); err != nil {
		return nil, fmt.Errorf("failed to move veth %s to container netns: %w", tempContainerVeth, err)
	}

	// 启动主机端veth（在连接到网桥之前）
	if err := netlink.LinkSetUp(hostVethLink); err != nil {
		return nil, fmt.Errorf("failed to set host veth %s up: %w", hostVeth, err)
	}

	// 在容器网络命名空间中配置容器端veth
	err = containerNs.Do(func(_ ns.NetNS) error {
		// 获取容器端veth链接（现在在容器命名空间中）
		vethInContainer, err := netlink.LinkByName(tempContainerVeth)
		if err != nil {
			return fmt.Errorf("failed to find veth %s in container netns: %w", tempContainerVeth, err)
		}

		// 重命名为目标名称
		if err := netlink.LinkSetName(vethInContainer, containerVeth); err != nil {
			return fmt.Errorf("failed to rename veth %s to %s: %w", tempContainerVeth, containerVeth, err)
		}

		// 重新获取重命名后的链接
		vethInContainer, err = netlink.LinkByName(containerVeth)
		if err != nil {
			return fmt.Errorf("failed to find veth %s after rename: %w", containerVeth, err)
		}

		// 启动容器端veth
		if err := netlink.LinkSetUp(vethInContainer); err != nil {
			return fmt.Errorf("failed to set veth %s up in container: %w", containerVeth, err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// 获取网桥链接
	bridgeLink, err := netlink.LinkByName(bridgeName)
	if err != nil {
		return nil, fmt.Errorf("failed to find bridge %s: %w", bridgeName, err)
	}

	// 将主机端veth连接到网桥
	if err := netlink.LinkSetMaster(hostVethLink, bridgeLink.(*netlink.Bridge)); err != nil {
		return nil, fmt.Errorf("failed to attach veth %s to bridge %s: %w", hostVeth, bridgeName, err)
	}

	return info, nil
}

func getIPFromID(id string) string {
	// 确保ID长度足够
	if len(id) < 2 {
		id = "02"
	}
	// 取最后两个字符
	lastTwo := id[len(id)-2:]
	// 十六进制转十进制
	num := 0
	if _, err := fmt.Sscanf(lastTwo, "%x", &num); err != nil {
		num = 2
	}
	// 边界处理
	if num == 0 || num >= 255 {
		num = 2
	}
	return fmt.Sprintf("10.88.0.%d", num)
}

// AddVethPairWithIP 创建veth pair，将一端移动到容器网络命名空间并配置IP，另一端连接到网桥
// hostVeth: 主机端veth名称
// containerVeth: 容器端veth名称
// bridgeName: 网桥名称
// netnsPath: 容器网络命名空间路径
// mtu: 网卡的MTU值
// containerIP: 分配给容器端veth的IP地址（CIDR格式，如 10.88.0.2/24）
// 返回包含两端接口名和MAC地址的结构体
func AddVethPairWithIP(hostVeth, containerVeth, bridgeName, netnsPath string, mtu int) (*VethPairInfo, string, error) {
	info, err := AddVethPair(hostVeth, containerVeth, bridgeName, netnsPath, mtu)
	if err != nil {
		return nil, "", err
	}

	ip := getIPFromID(hostVeth)

	// 将IP地址添加到容器端veth网卡
	containerNs, err := ns.GetNS(netnsPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get container netns %s: %w", netnsPath, err)
	}
	defer containerNs.Close()

	err = containerNs.Do(func(_ ns.NetNS) error {
		link, err := netlink.LinkByName(containerVeth)
		if err != nil {
			return fmt.Errorf("failed to find veth %s in container netns: %w", containerVeth, err)
		}
		ipNet, err := netlink.ParseIPNet(ip + "/24")
		if err != nil {
			return fmt.Errorf("failed to parse IP %s/24: %w", ip, err)
		}
		if err := netlink.AddrAdd(link, &netlink.Addr{IPNet: ipNet}); err != nil {
			return fmt.Errorf("failed to add IP %s to veth %s: %w", ip, containerVeth, err)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}

	return info, ip, nil
}

// DeleteVethPair 删除指定的 veth pair（主机端接口）
// 如果接口不存在，返回 nil
func DeleteVethPair(hostIfName string) error {
	link, err := netlink.LinkByName(hostIfName)
	if err != nil {
		if _, ok := err.(netlink.LinkNotFoundError); ok {
			return nil // 接口不存在，视为成功
		}
		return fmt.Errorf("failed to find veth %s: %w", hostIfName, err)
	}

	// 确保是 veth 类型
	if _, ok := link.(*netlink.Veth); !ok {
		return fmt.Errorf("interface %s is not a veth device", hostIfName)
	}

	// 删除接口
	if err := netlink.LinkDel(link); err != nil {
		return fmt.Errorf("failed to delete veth %s: %w", hostIfName, err)
	}
	return nil
}
