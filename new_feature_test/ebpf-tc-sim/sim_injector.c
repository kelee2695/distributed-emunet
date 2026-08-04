#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/pkt_cls.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>
#include "header/sim_defs.h"

#define INJECTOR_MSG(msg) bpf_printk("[SIM_INJECTOR] " msg)
#define INJECTOR_FMT(fmt, ...) bpf_printk("[SIM_INJECTOR] " fmt, ##__VA_ARGS__)

SEC("tc/egress")
int sim_injector(struct __sk_buff *skb) {
    void *data_end = (void *)(unsigned long long)skb->data_end;
    void *data = (void *)(unsigned long long)skb->data;

    struct ethhdr *eth = data;
    if ((void *)(eth + 1) > data_end) return TC_ACT_OK;

    __u16 orig_eth_proto = eth->h_proto;
    
    if (orig_eth_proto == bpf_htons(SIM_MAGIC)) {
        INJECTOR_MSG("Already SIM, pass");
        return TC_ACT_OK;
    }

    __u32 orig_len = skb->len;

    if (bpf_skb_change_tail(skb, orig_len + sizeof(struct sim_hdr), 0) < 0) {
        INJECTOR_MSG("Failed to add tail");
        return TC_ACT_SHOT;
    }

    struct sim_hdr sh = {
        .orig_eth_type = orig_eth_proto,
        .magic = bpf_htons(SIM_MAGIC),
        .current_type = ENTITY_LINK,
        .current_id = 1
    };

    if (bpf_skb_store_bytes(skb, orig_len, &sh, sizeof(sh), 0) < 0) {
        INJECTOR_MSG("Failed to store trailer");
        return TC_ACT_SHOT;
    }

    data = (void *)(unsigned long long)skb->data;
    data_end = (void *)(unsigned long long)skb->data_end;
    eth = data;
    if ((void *)(eth + 1) > data_end) return TC_ACT_SHOT;
    
    eth->h_proto = bpf_htons(SIM_MAGIC);

    INJECTOR_FMT("Inject: appended trailer, new_len=%d", skb->len);
    return TC_ACT_OK;
}

char _license[] SEC("license") = "GPL";