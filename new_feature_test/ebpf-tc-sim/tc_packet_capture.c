#include <linux/bpf.h>
#include <linux/pkt_cls.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

#define DEBUG 1

#ifdef DEBUG
#define bpf_debug(fmt, ...) bpf_printk(fmt, ##__VA_ARGS__)
#else
#define bpf_debug(fmt, ...)
#endif

// 统计数据包数量
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u64);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} packet_count SEC(".maps");

SEC("tc")
int tc_packet_capture(struct __sk_buff *skb) {
    void *data_end = (void *)(long)skb->data_end;
    void *data = (void *)(long)skb->data;
    struct ethhdr *eth = data;
    
    // 数据包计数
    __u32 key = 0;
    __u64 *count = bpf_map_lookup_elem(&packet_count, &key);
    if (count) {
        (*count)++;
    }
    
    // 检查以太网头部
    if ((void *)(eth + 1) > data_end) {
        bpf_debug("TC: Packet too short\n");
        return TC_ACT_OK;
    }
    
    // 打印基本信息（限制参数数量）
    __u16 proto = bpf_ntohs(eth->h_proto);
    bpf_debug("TC: Packet len=%d proto=0x%x\n", skb->len, proto);
    
    // 如果是 IPv4 包，打印 IP 信息
    if (proto == ETH_P_IP) {
        struct iphdr *ip = (struct iphdr *)(eth + 1);
        if ((void *)(ip + 1) > data_end) {
            return TC_ACT_OK;
        }
        
        // 分开打印，避免参数过多
        bpf_debug("TC: IPv4 src=%u.%u\n", (ip->saddr >> 24) & 0xFF, (ip->saddr >> 16) & 0xFF);
        bpf_debug("TC: IPv4 src=%u.%u\n", (ip->saddr >> 8) & 0xFF, ip->saddr & 0xFF);
        bpf_debug("TC: IPv4 dst=%u.%u\n", (ip->daddr >> 24) & 0xFF, (ip->daddr >> 16) & 0xFF);
        bpf_debug("TC: IPv4 dst=%u.%u proto=%d\n", (ip->daddr >> 8) & 0xFF, ip->daddr & 0xFF, ip->protocol);
    }
    
    // 允许数据包继续通过
    return TC_ACT_OK;
}

char _license[] SEC("license") = "GPL";