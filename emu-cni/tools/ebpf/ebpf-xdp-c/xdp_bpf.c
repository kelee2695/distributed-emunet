// 包含 vmlinux.h 是最现代的做法，但为了通用性，这里使用标准库
// 如果你的环境支持 CO-RE，建议替换为 #include "vmlinux.h"
#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <bpf/bpf_helpers.h>

// 调试宏: 开启后通过 /sys/kernel/debug/tracing/trace_pipe 查看 
// 生产环境建议注释掉或减少日志量 
#define DEBUG 1 

#ifdef DEBUG 
#define bpf_debug(fmt, ...) bpf_printk(fmt, ##__VA_ARGS__) 
#else 
#define bpf_debug(fmt, ...) 
#endif

// 这是一个辅助宏，用于现代化的 Map 定义
// 位于 <bpf/bpf_helpers.h> 中，如果未定义需手动补充，但通常 libbpf 开发包里都有
#ifndef __type
#define __type(name, val) typeof(val) *name
#endif

// 定义 MAC 地址 Key 结构
struct mac_key {
    unsigned char mac[6];
};

// ============================================================
// 1. 现代化 Map 定义: 转发规则表 (Hash Map)
// ============================================================
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 65535);
    // 使用 __type 宏，生成 BTF 信息，让用户态工具知道 Key/Value 的具体结构
    __type(key, struct mac_key);
    __type(value, __u32); // value 是目标网卡的 ifindex
    __uint(pinning, LIBBPF_PIN_BY_NAME); // pin map by name
} mac_table SEC(".maps");

// ============================================================
// 2. 现代化 Map 定义: TX 端口映射表 (Devmap Hash)
// ============================================================
// 使用 DEVMAP_HASH (Linux 5.4+)
// 相比传统 DEVMAP (Array)，它允许直接使用 ifindex 作为 Key，
// 不需要担心 ifindex 很大导致数组越界，非常适合容器环境。
struct {
    __uint(type, BPF_MAP_TYPE_DEVMAP_HASH);
    __uint(max_entries, 1024);
    __type(key, __u32); // ifindex
    __type(value, struct bpf_devmap_val); // 标准结构，包含 if_index 和 prog_fd
    __uint(pinning, LIBBPF_PIN_BY_NAME); // pin map by name

} tx_ports SEC(".maps");


SEC("xdp")
int xdp_l2_fwd_prog(struct xdp_md *ctx) {
    void *data_end = (void *)(long)ctx->data_end;
    void *data = (void *)(long)ctx->data;
    struct ethhdr *eth = data;

    // 记录数据包进入XDP程序
    int ifindex = ctx->ingress_ifindex;
    bpf_debug("XDP: Packet received on ifindex %d\n", ifindex);

    // 1. 包长度检查 (Verifier 必须)
    if ((void *)(eth + 1) > data_end) {
        bpf_debug("XDP: Packet too short, dropping\n");
        return XDP_DROP;
    }

    // 3. 广播与组播处理
    // 检查 MAC 地址第一个字节的最低位 (IG bit)
    // 广播地址 FF:FF:FF:FF:FF:FF 的最低位也是 1
    if (eth->h_dest[0] & 1) {
        bpf_debug("XDP: Broadcast/Multicast packet, broadcasting via devmap\n");
        // 使用 BPF_F_BROADCAST 标志实现广播转发
        // BPF_F_EXCLUDE_INGRESS 确保数据包不会回发到入站接口
        return bpf_redirect_map(&tx_ports, 0, BPF_F_BROADCAST | BPF_F_EXCLUDE_INGRESS);
    }

    // 4. 单播转发查找
    struct mac_key key;
    // 这种直接拷贝比 memcpy 更高效且对 Verifier 友好
    __builtin_memcpy(key.mac, eth->h_dest, 6);

    // 在 Hash Map 中查找目的 MAC 对应的 ifindex
    __u32 *dest_ifindex = bpf_map_lookup_elem(&mac_table, &key);

    if (dest_ifindex) {
        // 5. 执行重定向
        // 如果找到了目标 ifindex，通过 devmap 转发
        // 注意：用户态程序必须先将该 ifindex 添加到 tx_ports map 中
        bpf_debug("XDP: Forwarding packet to ifindex %d\n", *dest_ifindex);
        return bpf_redirect_map(&tx_ports, *dest_ifindex, 0);
    }

    // 6. 未知单播 -> 交给内核栈 (Bridge/Routing)
    // 这样可以处理未命中缓存的情况，保证网络不断
    bpf_debug("XDP: Unknown unicast packet, passing to kernel\n");
    return XDP_PASS;
}

char _license[] SEC("license") = "GPL";