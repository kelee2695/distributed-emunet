package ebpfxdp

// 1. Generate 指令
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -type xdp_md bpf ../ebpf-xdp-c/xdp_bpf.c

import (
	"fmt"
	"os"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/rlimit"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
)

// 默认的 eBPF 程序和map的pin路径
const (
	defaultDir        = "/sys/fs/bpf/xdp_bridge"
	defaultPinPath    = "/sys/fs/bpf/xdp_bridge/program"
	defaultMapPinPath = "/sys/fs/bpf/xdp_bridge/maps"
)

// Init 初始化 eBPF XDP 程序
func Init() error {
	// 1. 幂等性检查
	prog, err := ebpf.LoadPinnedProgram(defaultPinPath, nil)
	if err == nil {
		prog.Close()
		return nil
	}
	if !os.IsNotExist(err) {
		fmt.Printf("警告: 检查 Pin 程序时发生意外错误: %v\n", err)
	}

	// 2. 移除内存锁定限制
	if err := rlimit.RemoveMemlock(); err != nil {
		return fmt.Errorf("移除内存锁定限制失败: %v", err)
	}

	// 3. 创建 Pin 目录
	if err := os.MkdirAll(defaultMapPinPath, os.ModePerm); err != nil {
		return fmt.Errorf("创建 BPF Pin 目录失败: %v", err)
	}

	// 4. 配置加载选项
	loadOpts := &ebpf.CollectionOptions{}
	loadOpts.Maps = ebpf.MapOptions{
		PinPath: defaultMapPinPath,
	}

	// 5. 加载编译后的 eBPF 对象
	var objs bpfObjects
	if err := loadBpfObjects(&objs, loadOpts); err != nil {
		return fmt.Errorf("加载 eBPF 对象失败: %v", err)
	}
	defer objs.Close()

	// 6. 将程序 Pin 到文件系统
	if err := objs.XdpL2FwdProg.Pin(defaultPinPath); err != nil {
		return fmt.Errorf("无法将 eBPF 程序 pin 到 %s: %v", defaultPinPath, err)
	}

	return nil
}

// Close 关闭 eBPF XDP 程序
func Close() error {
	err := os.RemoveAll(defaultDir)
	if err != nil {
		return fmt.Errorf("清理 eBPF Pin 目录失败: %v", err)
	}
	return nil
}

// IsXDPAttached 检查 XDP 程序是否已附加到指定的 netlink.Link 网络接口
func IsXDPAttached(iface netlink.Link) (bool, error) {
	if iface == nil || iface.Attrs() == nil {
		return false, fmt.Errorf("无效的网络接口")
	}

	currentLink, err := netlink.LinkByIndex(iface.Attrs().Index)
	if err != nil {
		return false, fmt.Errorf("无法获取接口最新状态: %v", err)
	}

	xdpState := currentLink.Attrs().Xdp
	if xdpState == nil {
		return false, nil
	}

	if xdpState.Attached || xdpState.ProgId > 0 {
		return true, nil
	}

	return false, nil
}

// IsXDPAttachedByName 通过接口名称检查 XDP 程序是否已附加
func IsXDPAttachedByName(ifname string) (bool, error) {
	iface, err := netlink.LinkByName(ifname)
	if err != nil {
		return false, fmt.Errorf("查找网络接口 %q 失败: %v", ifname, err)
	}
	return IsXDPAttached(iface)
}

// AttachXDP 将 XDP 程序附加到指定的 netlink.Link 网络接口
func AttachXDP(iface netlink.Link) error {
	prog, err := ebpf.LoadPinnedProgram(defaultPinPath, nil)
	if err != nil {
		return fmt.Errorf("eBPF XDP 程序未初始化或未 pin 住: %v", err)
	}
	defer prog.Close()

	// 尝试驱动模式
	err = netlink.LinkSetXdpFdWithFlags(iface, prog.FD(), nl.XDP_FLAGS_DRV_MODE)
	if err == nil {
		return nil
	}

	// 回退到通用模式 (veth 必需)
	err = netlink.LinkSetXdpFdWithFlags(iface, prog.FD(), nl.XDP_FLAGS_SKB_MODE)
	if err != nil {
		return fmt.Errorf("挂载 XDP 程序失败 (Driver 和 Generic 模式均失败): %v", err)
	}

	return nil
}

// AttachXDPByName 通过接口名称附加
func AttachXDPByName(ifname string) error {
	iface, err := netlink.LinkByName(ifname)
	if err != nil {
		return fmt.Errorf("查找网络接口 %q 失败: %v", ifname, err)
	}
	return AttachXDP(iface)
}

// DetachXDP 从指定的 netlink.Link 网络接口上卸载 XDP 程序
func DetachXDP(iface netlink.Link) error {
	isAttached, err := IsXDPAttached(iface)
	if err != nil {
		return fmt.Errorf("检查挂载状态失败: %v", err)
	}
	if !isAttached {
		return nil
	}

	var errs []error
	// 尝试卸载所有模式
	if err := netlink.LinkSetXdpFdWithFlags(iface, -1, nl.XDP_FLAGS_DRV_MODE); err != nil {
		errs = append(errs, err)
	}
	if err := netlink.LinkSetXdpFdWithFlags(iface, -1, nl.XDP_FLAGS_SKB_MODE); err != nil {
		errs = append(errs, err)
	}

	// 最终检查
	isStillAttached, err := IsXDPAttached(iface)
	if err != nil {
		return fmt.Errorf("卸载后状态检查失败: %v", err)
	}
	if isStillAttached {
		return fmt.Errorf("卸载失败，XDP 程序依然存在")
	}

	return nil
}

// DetachXDPByName 通过名称卸载
func DetachXDPByName(ifname string) error {
	iface, err := netlink.LinkByName(ifname)
	if err != nil {
		return fmt.Errorf("查找网络接口 %q 失败: %v", ifname, err)
	}
	return DetachXDP(iface)
}

// 假设 compiled_bpf 是编译好的对象

// 1. 更新 DEVMAP_HASH (必须步骤)
// 告诉内核：这个 ifindex 是可用于 redirect 的
// 如果不把目标网卡放入 devmap，bpf_redirect_map 会失败并丢包
func AddToDevMap(ifindex uint32) error {
    // 加载pin住的devmap
    devMap, err := ebpf.LoadPinnedMap(defaultMapPinPath+"/tx_ports", nil)
    if err != nil {
        return fmt.Errorf("加载 pin 住的 tx_ports 失败: %v", err)	
    }
    defer devMap.Close()

    // 对于 DEVMAP_HASH, Key 是 ifindex, Value 是 bpf_devmap_val
    // bpf_devmap_val 结构体简单来说只需要填 ifindex (fd设为0)
    key := ifindex
    val := struct {
        IfIndex uint32
        // ProgFd/IfIndex 等字段根据 bpf_devmap_val 定义
        // 在 cilium/ebpf 中通常只要传 IfIndex 即可，底层会自动处理
        Pad uint32 
    }{
        IfIndex: ifindex,
    }
    return devMap.Update(key, val, ebpf.UpdateAny)
}

// 2. 更新 MAC 转发规则
func UpdateFwdRule(macAddr []byte, targetIfIndex uint32) error {
    // 加载pin住的mac_table
    macMap, err := ebpf.LoadPinnedMap(defaultMapPinPath+"/mac_table", nil)
    if err != nil {
        return fmt.Errorf("加载 pin 住的 mac_table 失败: %v", err)
    }
    defer macMap.Close()

    // Key 需要匹配 C 代码中的 struct mac_key
    key := struct{ Mac [6]byte }{}  
    copy(key.Mac[:], macAddr)
    
    return macMap.Update(key, targetIfIndex, ebpf.UpdateAny)
}

// 3. 删除 MAC 转发规则
func DeleteFwdRule(macAddr []byte) error {
    // 加载pin住的mac_table
    macMap, err := ebpf.LoadPinnedMap(defaultMapPinPath+"/mac_table", nil)
    if err != nil {
        return fmt.Errorf("加载 pin 住的 mac_table 失败: %v", err)
    }
    defer macMap.Close()

    // Key 需要匹配 C 代码中的 struct mac_key
    key := struct{ Mac [6]byte }{}  
    copy(key.Mac[:], macAddr)
    
    return macMap.Delete(key)
}

// 4. 从 devmap 中删除接口
func DeleteFromDevMap(ifindex uint32) error {
    // 加载pin住的tx_ports
    devMap, err := ebpf.LoadPinnedMap(defaultMapPinPath+"/tx_ports", nil)
    if err != nil {
        return fmt.Errorf("加载 pin 住的 tx_ports 失败: %v", err)
    }
    defer devMap.Close()

    key := ifindex
    return devMap.Delete(key)
}

// 5. 删除所有指向特定ifindex的转发规则
func DeleteAllFwdRulesByIfIndex(targetIfIndex uint32) error {
    // 加载pin住的mac_table
    macMap, err := ebpf.LoadPinnedMap(defaultMapPinPath+"/mac_table", nil)
    if err != nil {
        return fmt.Errorf("加载 pin 住的 mac_table 失败: %v", err)
    }
    defer macMap.Close()

    // 创建迭代器
    it := macMap.Iterate()
    var key struct{ Mac [6]byte }
    var val uint32

    // 遍历所有条目
    for it.Next(&key, &val) {
        if val == targetIfIndex {
            // 删除匹配的条目
            if err := macMap.Delete(key); err != nil {
                return fmt.Errorf("删除转发规则失败: %v", err)
            }
        }
    }

    // 检查迭代过程中是否有错误
    if err := it.Err(); err != nil {
        return fmt.Errorf("遍历 mac_table 失败: %v", err)
    }

    return nil
}