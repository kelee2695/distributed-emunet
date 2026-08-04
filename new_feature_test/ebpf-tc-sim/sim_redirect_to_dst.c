#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

#define DEBUG 1

#ifdef DEBUG
#define bpf_debug(fmt, ...) bpf_printk(fmt, ##__VA_ARGS__)
#else
#define bpf_debug(fmt, ...)
#endif

// 定义 MAC 地址 Key 结构
struct mac_key {
    unsigned char mac[6];
};

// ============================================================
// 1. MAC 地址映射表 (Hash Map)
// ============================================================
// 存储目的 MAC 地址到目标 ifindex 的映射
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 65535);
    __type(key, struct mac_key);
    __type(value, __u32); // value 是目标网卡的 ifindex
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} mac_table SEC(".maps");

// ============================================================
// 2. TX 端口映射表 (Devmap Hash)
// ============================================================
// 使用 DEVMAP_HASH (Linux 5.4+)
struct {
    __uint(type, BPF_MAP_TYPE_DEVMAP_HASH);
    __uint(max_entries, 1024);
    __type(key, __u32); // ifindex
    __type(value, struct bpf_devmap_val);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} tx_ports SEC(".maps");

SEC("xdp")
int sim_redirect_to_dst(struct xdp_md *ctx) {
    void *data_end = (void *)(long)ctx->data_end;
    void *data = (void *)(long)ctx->data;
    struct ethhdr *eth = data;
    
    bpf_debug("SIM_REDIRECT: sim_redirect_to_dst called on ifindex %d\n", ctx->ingress_ifindex);
    
    // 包长度检查 (Verifier 必须)
    if ((void *)(eth + 1) > data_end) {
        bpf_debug("SIM_REDIRECT: Packet too short\n");
        return XDP_PASS;
    }
    
    // 广播与组播处理
    if (eth->h_dest[0] & 1) {
        bpf_debug("SIM_REDIRECT: Broadcast/Multicast packet, broadcasting via devmap\n");
        return bpf_redirect_map(&tx_ports, 0, BPF_F_BROADCAST | BPF_F_EXCLUDE_INGRESS);
    }
    
    // 单播转发: 根据目的 MAC 地址查找目标 ifindex
    struct mac_key key;
    __builtin_memcpy(key.mac, eth->h_dest, 6);
    
    __u32 *dest_ifindex = bpf_map_lookup_elem(&mac_table, &key);
    
    if (dest_ifindex) {
        bpf_debug("SIM_REDIRECT: Forwarding to ifindex %d\n", *dest_ifindex);
        return bpf_redirect_map(&tx_ports, *dest_ifindex, 0);
    }
    
    // 未知单播 -> 交给内核栈处理
    bpf_debug("SIM_REDIRECT: Unknown destination MAC, passing to kernel\n");
    return XDP_PASS;
}

char _license[] SEC("license") = "GPL";
