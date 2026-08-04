#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/pkt_cls.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>
#include "header/sim_defs.h"

#define DISPATCHER_MSG(msg) bpf_printk("[SIM_DISPATCHER] " msg)
#define DISPATCHER_FMT(fmt, ...) bpf_printk("[SIM_DISPATCHER] " fmt, ##__VA_ARGS__)

SEC("tc/ingress")
int sim_dispatcher(struct __sk_buff *skb) {
    void *data_end = (void *)(unsigned long long)skb->data_end;
    void *data = (void *)(unsigned long long)skb->data;

    struct ethhdr *eth = data;
    if ((void *)(eth + 1) > data_end) return TC_ACT_OK;

    if (eth->h_proto != bpf_htons(SIM_MAGIC)) return TC_ACT_OK;

    __u32 pkt_len = data_end - data;

    if (pkt_len < sizeof(struct sim_hdr) + sizeof(struct ethhdr)) {
        DISPATCHER_MSG("Packet too short");
        return TC_ACT_SHOT;
    }
    __u32 trailer_offset = pkt_len - sizeof(struct sim_hdr);

    struct sim_hdr sh;
    if (bpf_skb_load_bytes(skb, trailer_offset, &sh, sizeof(sh)) < 0) {
        DISPATCHER_MSG("Failed to load trailer");
        return TC_ACT_SHOT;
    }

    if (sh.magic != bpf_htons(SIM_MAGIC)) {
        DISPATCHER_MSG("Invalid magic");
        return TC_ACT_SHOT;
    }

    eth->h_proto = sh.orig_eth_type;

    if (bpf_skb_change_tail(skb, trailer_offset, 0) < 0) {
        DISPATCHER_MSG("Failed to strip tail");
        return TC_ACT_SHOT;
    }

    DISPATCHER_FMT("Decap successful, tail stripped");
    return TC_ACT_OK;
}

char _license[] SEC("license") = "GPL";