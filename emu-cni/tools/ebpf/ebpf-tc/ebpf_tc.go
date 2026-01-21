package ebpftc

// 1. Generate 指令
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go bpf ../ebpf-tc-c/tc_bpf.c

import (
	"fmt"
	"os"
	"golang.org/x/sys/unix"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/rlimit"
	"github.com/vishvananda/netlink"
)

const (
	defaultDir        = "/sys/fs/bpf/tc_emu"
	defaultPinPath    = "/sys/fs/bpf/tc_emu/program"
	defaultMapPinPath = "/sys/fs/bpf/tc_emu/maps"
)

// ClearEbpf 清理网络接口上的 eBPF 程序和 TC 组件
func ClearEbpf(iface netlink.Link) error {
	// 移除 clsact qdisc（包含入口和出口过滤器）
	clsactAttrs := netlink.QdiscAttrs{
		LinkIndex: iface.Attrs().Index,
		Handle:    netlink.MakeHandle(0xffff, 0),
		Parent:    netlink.HANDLE_CLSACT,
	}
	clsact := &netlink.GenericQdisc{
		QdiscAttrs: clsactAttrs,
		QdiscType:  "clsact",
	}
	
	// 尝试删除 clsact qdisc，如果失败则忽略错误
	_ = netlink.QdiscDel(clsact)

	// 移除根节点的 fq qdisc
	fqAttrs := netlink.QdiscAttrs{
		LinkIndex: iface.Attrs().Index,
		Handle:    netlink.MakeHandle(0x123, 0),
		Parent:    netlink.HANDLE_ROOT,
	}
	fq := &netlink.Fq{
		QdiscAttrs: fqAttrs,
	}
	// 尝试删除 fq qdisc，如果失败则忽略错误
	_ = netlink.QdiscDel(fq)

	// 尝试删除所有根节点上的 qdisc
	rootAttrs := netlink.QdiscAttrs{
		LinkIndex: iface.Attrs().Index,
		Parent:    netlink.HANDLE_ROOT,
	}
	rootQdisc := &netlink.GenericQdisc{
		QdiscAttrs: rootAttrs,
	}
	// 尝试删除根 qdisc，如果失败则忽略错误
	_ = netlink.QdiscDel(rootQdisc)

	return nil
}


func CreateFQdisc(iface netlink.Link) (*netlink.Fq, error) {
	//tc qdisc add dev wlp2s0 root fq ce_threshold 4ms
	attrs := netlink.QdiscAttrs{
		LinkIndex: iface.Attrs().Index,
		Handle:    netlink.MakeHandle(0x123, 0),
		Parent:    netlink.HANDLE_ROOT,
	}

	//fq := netlink.NewFq(attrs)

	fq := &netlink.Fq{
		QdiscAttrs: attrs,
		Pacing:     0,
	}

	if err := netlink.QdiscAdd(fq); err != nil {
		return nil, fmt.Errorf("添加 fq qdisc 失败: %v", err)
	}
	return fq, nil
}

func CreateClsactQdisc(iface netlink.Link) (*netlink.GenericQdisc, error) {
	attrs := netlink.QdiscAttrs{
		LinkIndex: iface.Attrs().Index,
		Handle:    netlink.MakeHandle(0xffff, 0),
		Parent:    netlink.HANDLE_CLSACT,
	}

	qdisc := &netlink.GenericQdisc{
		QdiscAttrs: attrs,
		QdiscType:  "clsact",
	}

	if err := netlink.QdiscAdd(qdisc); err != nil {
		return nil, fmt.Errorf("添加 clsact qdisc 失败: %v", err)
	}
	return qdisc, nil
}


func CreateTCBpfFilter(iface netlink.Link, progFd int, parent uint32, name string) (*netlink.BpfFilter, error) {
	filterAttrs := netlink.FilterAttrs{
		LinkIndex: iface.Attrs().Index,
		Parent:    parent,
		Handle:    netlink.MakeHandle(0, 1),
		Protocol:  unix.ETH_P_ALL,
		Priority:  1,
	}

	filter := &netlink.BpfFilter{
		FilterAttrs:  filterAttrs,
		Fd:           progFd,
		Name:         name,
		DirectAction: true,
	}

	if err := netlink.FilterAdd(filter); err != nil {
		return nil, fmt.Errorf("添加 bpf filter 失败: %v", err)
	}
	return filter, nil
}

// Init 初始化 eBPF XDP 程序
func Init() error {
	// 1. 幂等性检查
	prog, err := ebpf.LoadPinnedProgram(defaultPinPath, nil)
	if err == nil {
		prog.Close()
		return nil
	}
	// if !os.IsNotExist(err) {
	// 	return fmt.Errorf("检查 Pin 程序时发生意外错误: %v", err)
	// }

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
	if err := objs.LossBps.Pin(defaultPinPath); err != nil {
		return fmt.Errorf("无法将 eBPF 程序 pin 到 %s: %v", defaultPinPath, err)
	}

	// 7. 更新程序的延迟函数指针
	err = objs.Progs.Update(uint32(0), uint32(objs.DelayJitter.FD()), ebpf.UpdateAny)
	if err != nil {
		return fmt.Errorf("更新 eBPF 程序延迟函数指针失败: %v", err)
	}

	return nil
}

// Close 关闭 eBPF TC 程序	
func Close() error {
	err := os.RemoveAll(defaultDir)
	if err != nil {
		return fmt.Errorf("清理 eBPF Pin 目录失败: %v", err)
	}
	return nil
}

// AttachTC 将 XDP 程序附加到指定的 netlink.Link 网络接口
func AttachTC(iface netlink.Link) error {	
	prog, err := ebpf.LoadPinnedProgram(defaultPinPath, nil)
	if err != nil {
		return fmt.Errorf("eBPF TC 程序未初始化或未 pin 住: %v", err)
	}
	defer prog.Close()

	// Create clsact qdisc
	if _, err := CreateClsactQdisc(iface); err != nil {
		return fmt.Errorf("Create clsact qdisc failed: %v", err)
	}
	
	// Create fq qdisc
	if _, err := CreateFQdisc(iface); err != nil {
		return fmt.Errorf("Create fq qdisc failed: %v", err)
	}

	// 固定使用egress方向
	handle := uint32(netlink.HANDLE_MIN_EGRESS)
	
	// Attach bpf program
	if _, err := CreateTCBpfFilter(iface, prog.FD(), handle, "edt_bandwidth"); err != nil {
		return fmt.Errorf("Create bpf filter failed: %v", err)
	}

	return nil
}

// AttachTCByName 通过接口名称附加
func AttachTCByName(ifname string) error {
	iface, err := netlink.LinkByName(ifname)
	if err != nil {
		return fmt.Errorf("查找网络接口 %q 失败: %v", ifname, err)
	}
	return AttachTC(iface)	
}


// DetachTC 从指定的 netlink.Link 网络接口上卸载 TC 程序
func DetachTC(iface netlink.Link) error {
	if err := ClearEbpf(iface); err != nil {
		return fmt.Errorf("清除 eBPF 程序和 TC 组件失败: %v", err)
	}
	return nil
}

// DetachTCByName 通过名称卸载
func DetachTCByName(ifname string) error {
	iface, err := netlink.LinkByName(ifname)
	if err != nil {
		return fmt.Errorf("查找网络接口 %q 失败: %v", ifname, err)
	}
	return DetachTC(iface)
}

