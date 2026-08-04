//go:build linux
// +build linux

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go srcredirect ../ebpf-tc-sim/src_redirect_to_sim.c -- -I../ebpf-tc-sim -g
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go simredirect ../ebpf-tc-sim/sim_redirect_to_dst.c -- -I../ebpf-tc-sim -g
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go tcpcap ../ebpf-tc-sim/tc_packet_capture.c -- -I../ebpf-tc-sim -g
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go dummyxdp ../ebpf-tc-sim/xdp_dummy.c -- -I../ebpf-tc-sim -g

package ebpftc

import (
	"fmt"
	"os"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/rlimit"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	srcSimPinPath    = "/sys/fs/bpf/tc_emu/src_sim"
	srcSimMapPinPath = "/sys/fs/bpf/tc_emu/src_sim/maps"
	simPinPath       = "/sys/fs/bpf/tc_emu/sim"
	simMapPinPath    = "/sys/fs/bpf/tc_emu/sim/maps"
)

// DevmapValue DEVMAP_HASH 的 Value 结构
type DevmapValue struct {
	IfIndex uint32
	Pad     uint32
}

// ============================================================
// src_redirect_to_sim 程序 (独立模块)
// ============================================================

type SrcRedirectSim struct {
	objs *srcredirectObjects
}

func NewSrcRedirectSim() (*SrcRedirectSim, error) {
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("移除内存限制失败: %v", err)
	}

	// 创建 pin 目录
	if err := os.MkdirAll(srcSimMapPinPath, 0755); err != nil {
		return nil, fmt.Errorf("创建 pin 目录失败: %v", err)
	}

	objs := &srcredirectObjects{}
	if err := loadSrcredirectObjects(objs, &ebpf.CollectionOptions{
		Maps: ebpf.MapOptions{
			PinPath: srcSimMapPinPath,
		},
	}); err != nil {
		return nil, fmt.Errorf("加载 src_redirect_to_sim 失败: %v", err)
	}

	return &SrcRedirectSim{objs: objs}, nil
}

func (s *SrcRedirectSim) Close() {
	if s.objs != nil {
		s.objs.Close()
		s.objs = nil
	}
}

func (s *SrcRedirectSim) Attach(iface netlink.Link) error {
	if s.objs == nil || s.objs.SrcRedirectToSim == nil {
		return fmt.Errorf("src_redirect_to_sim 程序未加载")
	}
	return netlink.LinkSetXdpFd(iface, s.objs.SrcRedirectToSim.FD())
}

func (s *SrcRedirectSim) Detach(iface netlink.Link) error {
	return netlink.LinkSetXdpFd(iface, -1)
}

func (s *SrcRedirectSim) SetSimIfindex(ifindex uint32) error {
	if s.objs == nil || s.objs.SimConfigMap == nil {
		return fmt.Errorf("src_redirect_to_sim 未加载")
	}
	return s.objs.SimConfigMap.Put(uint32(0), ifindex)
}

func (s *SrcRedirectSim) DelSimIfindex() error {
	if s.objs == nil || s.objs.SimConfigMap == nil {
		return fmt.Errorf("src_redirect_to_sim 未加载")
	}
	return s.objs.SimConfigMap.Delete(uint32(0))
}

func (s *SrcRedirectSim) AddDevmapEntry(ifindex uint32) error {
	if s.objs == nil || s.objs.XdpSrcRedirectMap == nil {
		return fmt.Errorf("src_redirect_to_sim 未加载")
	}
	devmapVal := DevmapValue{IfIndex: ifindex}
	return s.objs.XdpSrcRedirectMap.Put(ifindex, devmapVal)
}

func (s *SrcRedirectSim) DelDevmapEntry(ifindex uint32) error {
	if s.objs == nil || s.objs.XdpSrcRedirectMap == nil {
		return fmt.Errorf("src_redirect_to_sim 未加载")
	}
	return s.objs.XdpSrcRedirectMap.Delete(ifindex)
}

// ============================================================
// sim_redirect_to_dst 程序 (独立模块)
// ============================================================

type SimRedirectDst struct {
	objs *simredirectObjects
}

func NewSimRedirectDst() (*SimRedirectDst, error) {
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("移除内存限制失败: %v", err)
	}

	// 创建 pin 目录
	if err := os.MkdirAll(simMapPinPath, 0755); err != nil {
		return nil, fmt.Errorf("创建 pin 目录失败: %v", err)
	}

	objs := &simredirectObjects{}
	if err := loadSimredirectObjects(objs, &ebpf.CollectionOptions{
		Maps: ebpf.MapOptions{
			PinPath: simMapPinPath,
		},
	}); err != nil {
		return nil, fmt.Errorf("加载 sim_redirect_to_dst 失败: %v", err)
	}

	return &SimRedirectDst{objs: objs}, nil
}

func (s *SimRedirectDst) Close() {
	if s.objs != nil {
		s.objs.Close()
		s.objs = nil
	}
}

func (s *SimRedirectDst) Attach(iface netlink.Link) error {
	if s.objs == nil || s.objs.SimRedirectToDst == nil {
		return fmt.Errorf("sim_redirect_to_dst 程序未加载")
	}
	return netlink.LinkSetXdpFd(iface, s.objs.SimRedirectToDst.FD())
}

func (s *SimRedirectDst) Detach(iface netlink.Link) error {
	return netlink.LinkSetXdpFd(iface, -1)
}

func (s *SimRedirectDst) SetMacEntry(mac [6]byte, ifindex uint32) error {
	if s.objs == nil || s.objs.MacTable == nil {
		return fmt.Errorf("sim_redirect_to_dst 未加载")
	}
	return s.objs.MacTable.Put(mac, ifindex)
}

func (s *SimRedirectDst) DelMacEntry(mac [6]byte) error {
	if s.objs == nil || s.objs.MacTable == nil {
		return fmt.Errorf("sim_redirect_to_dst 未加载")
	}
	return s.objs.MacTable.Delete(mac)
}

func (s *SimRedirectDst) AddTxPort(ifindex uint32) error {
	if s.objs == nil || s.objs.TxPorts == nil {
		return fmt.Errorf("sim_redirect_to_dst 未加载")
	}
	devmapVal := DevmapValue{IfIndex: ifindex}
	return s.objs.TxPorts.Put(ifindex, devmapVal)
}

func (s *SimRedirectDst) DelTxPort(ifindex uint32) error {
	if s.objs == nil || s.objs.TxPorts == nil {
		return fmt.Errorf("sim_redirect_to_dst 未加载")
	}
	return s.objs.TxPorts.Delete(ifindex)
}

// ============================================================
// xdp_dummy 程序 (独立模块)
// ============================================================

type XdpDummy struct {
	objs *dummyxdpObjects
}

func NewXdpDummy() (*XdpDummy, error) {
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("移除内存限制失败: %v", err)
	}

	objs := &dummyxdpObjects{}
	// Dummy XDP 不需要 Map，直接传 nil 即可
	if err := loadDummyxdpObjects(objs, nil); err != nil {
		return nil, fmt.Errorf("加载 xdp_dummy 失败: %v", err)
	}

	return &XdpDummy{objs: objs}, nil
}

func (d *XdpDummy) Close() {
	if d.objs != nil {
		d.objs.Close()
		d.objs = nil
	}
}

func (d *XdpDummy) Attach(iface netlink.Link) error {
	if d.objs == nil || d.objs.XdpDummy == nil {
		return fmt.Errorf("xdp_dummy 程序未加载")
	}
	return netlink.LinkSetXdpFd(iface, d.objs.XdpDummy.FD())
}

func (d *XdpDummy) Detach(iface netlink.Link) error {
	return netlink.LinkSetXdpFd(iface, -1)
}

// ============================================================
// 全局清理函数
// ============================================================

func ClearEbpf(iface netlink.Link) error {
	_ = netlink.LinkSetXdpFd(iface, -1)

	clsactAttrs := netlink.QdiscAttrs{
		LinkIndex: iface.Attrs().Index,
		Handle:    netlink.MakeHandle(0xffff, 0),
		Parent:    netlink.HANDLE_CLSACT,
	}
	clsact := &netlink.GenericQdisc{
		QdiscAttrs: clsactAttrs,
		QdiscType:  "clsact",
	}
	_ = netlink.QdiscDel(clsact)

	return nil
}

func CleanupAllResources() {
	os.RemoveAll("/sys/fs/bpf/tc_emu")
	os.RemoveAll("/sys/fs/bpf/sim_config_map")
	os.RemoveAll("/sys/fs/bpf/xdp_src_redirect_map")
	os.RemoveAll("/sys/fs/bpf/mac_table")
	os.RemoveAll("/sys/fs/bpf/tx_ports")
}

// ============================================================
// 辅助函数
// ============================================================

func GetIfaceMac(iface netlink.Link) ([6]byte, error) {
	var mac [6]byte
	attrs := iface.Attrs()
	if len(attrs.HardwareAddr) != 6 {
		return mac, fmt.Errorf("无效的 MAC 地址")
	}
	copy(mac[:], attrs.HardwareAddr)
	return mac, nil
}

func GetIfaceIfindex(iface netlink.Link) uint32 {
	return uint32(iface.Attrs().Index)
}

// ============================================================
// TC Packet Capture 程序 (独立模块)
// ============================================================

const (
	tcCapPinPath    = "/sys/fs/bpf/tc_emu/tc_cap"
	tcCapMapPinPath = "/sys/fs/bpf/tc_emu/tc_cap/maps"
)

type TcPacketCapture struct {
	objs *tcpcapObjects
}

func NewTcPacketCapture() (*TcPacketCapture, error) {
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("移除内存限制失败: %v", err)
	}

	// 创建 pin 目录
	if err := os.MkdirAll(tcCapMapPinPath, 0755); err != nil {
		return nil, fmt.Errorf("创建 pin 目录失败: %v", err)
	}

	objs := &tcpcapObjects{}
	if err := loadTcpcapObjects(objs, &ebpf.CollectionOptions{
		Maps: ebpf.MapOptions{
			PinPath: tcCapMapPinPath,
		},
	}); err != nil {
		return nil, fmt.Errorf("加载 tc_packet_capture 失败: %v", err)
	}

	// 初始化数据包计数器
	if err := objs.PacketCount.Put(uint32(0), uint64(0)); err != nil {
		objs.Close()
		return nil, fmt.Errorf("初始化 packet_count 失败: %v", err)
	}

	return &TcPacketCapture{objs: objs}, nil
}

func (t *TcPacketCapture) Close() {
	if t.objs != nil {
		t.objs.Close()
		t.objs = nil
	}
}

func (t *TcPacketCapture) Attach(iface netlink.Link) error {
	if t.objs == nil || t.objs.TcPacketCapture == nil {
		return fmt.Errorf("tc_packet_capture 程序未加载")
	}

	// 1. 准备 clsact qdisc
	clsact := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: iface.Attrs().Index,
			Handle:    netlink.MakeHandle(0xffff, 0),
			Parent:    netlink.HANDLE_CLSACT,
		},
		QdiscType: "clsact",
	}

	// 尝试删除旧的并添加新的
	_ = netlink.QdiscDel(clsact)
	if err := netlink.QdiscAdd(clsact); err != nil {
		return fmt.Errorf("添加 clsact qdisc 失败: %v", err)
	}

	// 2. 创建并挂载 BpfFilter 到 Ingress
	filter := &netlink.BpfFilter{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: iface.Attrs().Index,
			// 直接使用常量即可，不要套 MakeHandle 避免溢出
			Parent:   netlink.HANDLE_MIN_INGRESS,
			Handle:   netlink.MakeHandle(0, 1),
			Protocol: unix.ETH_P_ALL,
		},
		Fd:           t.objs.TcPacketCapture.FD(),
		Name:         "tc_packet_capture",
		DirectAction: true, // 启用 direct-action，使 TC_ACT_OK 等返回值生效
	}

	if err := netlink.FilterAdd(filter); err != nil {
		return fmt.Errorf("添加 TC filter 失败: %v", err)
	}

	return nil
}

func (t *TcPacketCapture) Detach(iface netlink.Link) error {
	// 删除 clsact qdisc（会自动删除所有 filter）
	clsactAttrs := netlink.QdiscAttrs{
		LinkIndex: iface.Attrs().Index,
		Handle:    netlink.MakeHandle(0xffff, 0),
		Parent:    netlink.HANDLE_CLSACT,
	}
	clsact := &netlink.GenericQdisc{
		QdiscAttrs: clsactAttrs,
		QdiscType:  "clsact",
	}

	return netlink.QdiscDel(clsact)
}

func (t *TcPacketCapture) GetPacketCount() (uint64, error) {
	if t.objs == nil || t.objs.PacketCount == nil {
		return 0, fmt.Errorf("tc_packet_capture 程序未加载")
	}

	var count uint64
	if err := t.objs.PacketCount.Lookup(uint32(0), &count); err != nil {
		return 0, fmt.Errorf("读取 packet_count 失败: %v", err)
	}

	return count, nil
}

func (t *TcPacketCapture) ResetPacketCount() error {
	if t.objs == nil || t.objs.PacketCount == nil {
		return fmt.Errorf("tc_packet_capture 程序未加载")
	}

	return t.objs.PacketCount.Put(uint32(0), uint64(0))
}
