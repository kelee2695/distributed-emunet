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

// ============================================================
// 1. 简单结构: 存储目标 ifindex (Array Map)
// ============================================================
// 由于只会有一个 ifindex，使用最简单的 Array Map
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u32); // 存储目标 ifindex
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} sim_config_map SEC(".maps");

// ============================================================
// 2. TX 端口映射表 (Devmap Hash)
// ============================================================
// 使用 DEVMAP_HASH (Linux 5.4+)
// 相比传统 DEVMAP (Array)，它允许直接使用 ifindex 作为 Key，
// 不需要担心 ifindex 很大导致数组越界
struct {
    __uint(type, BPF_MAP_TYPE_DEVMAP_HASH);
    __uint(max_entries, 1024);
    __type(key, __u32); // ifindex
    __type(value, struct bpf_devmap_val); // 标准结构，包含 if_index 和 prog_fd
    __uint(pinning, LIBBPF_PIN_BY_NAME);
}  xdp_src_redirect_map SEC(".maps");

SEC("xdp")
int src_redirect_to_sim(struct xdp_md *ctx) {
    void *data_end = (void *)(long)ctx->data_end;
    void *data = (void *)(long)ctx->data;
    struct ethhdr *eth = data;
    
    bpf_debug("REDIRECT: src_redirect_to_sim called on ifindex %d\n", ctx->ingress_ifindex);
    
    // 包长度检查 (Verifier 必须)
    if ((void *)(eth + 1) > data_end) {
        bpf_debug("REDIRECT: Packet too short\n");
        return XDP_PASS;
    }
    
    // 查找配置的目标 ifindex
    __u32 key = 0;
    __u32 *sim_ifindex = bpf_map_lookup_elem(&sim_config_map, &key);
    
    if (!sim_ifindex) {
        bpf_debug("REDIRECT: sim_config_map not configured\n");
        return XDP_PASS;
    }
    
    bpf_debug("REDIRECT: Redirecting to sim ifindex=%d\n", *sim_ifindex);
    
    // 通过 Devmap Hash 转发到目标接口
    // 用户态程序必须先将该 ifindex 添加到 xdp_src_redirect_map map 中
    return bpf_redirect_map(&xdp_src_redirect_map, *sim_ifindex, 0);
}

char _license[] SEC("license") = "GPL";
