#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <linux/pkt_cls.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>
#include "header/sim_defs.h"

#define INJECTOR_MSG(msg) bpf_printk("[SIM_INJECTOR] " msg)
#define INJECTOR_FMT(fmt, ...) bpf_printk("[SIM_INJECTOR] " fmt, ##__VA_ARGS__)

SEC("tc_egress/sim_injector")
int sim_injector(struct __sk_buff *skb) {
    // 1. 直接获取包长
    __u32 orig_len = skb->len;

    // 2. 防御性检查：防止重复注入 (检查尾部是不是已经是 SIM_MAGIC)
    if (orig_len >= sizeof(struct sim_hdr)) {
        struct sim_hdr old_sh;
        if (bpf_skb_load_bytes(skb, orig_len - sizeof(struct sim_hdr), &old_sh, sizeof(old_sh)) == 0) {
            if (old_sh.magic == bpf_htons(SIM_MAGIC)) {
                return TC_ACT_OK; // 已经有尾巴了，直接放行
            }
        }
    }

    // 3. 尾部扩容 12 字节
    if (bpf_skb_change_tail(skb, orig_len + sizeof(struct sim_hdr), 0) < 0) {
        return TC_ACT_SHOT;
    }

    // 4. 构建仿真数据（不再需要备份 orig_eth_type，因为我们根本没改它）
    struct sim_hdr sh = {
        .orig_eth_type = 0, 
        .magic = bpf_htons(SIM_MAGIC),
        .current_type = ENTITY_LINK,
        .current_id = 1
    };

    // 5. 将数据安全地写入尾部
    if (bpf_skb_store_bytes(skb, orig_len, &sh, sizeof(sh), 0) < 0) {
        return TC_ACT_SHOT;
    }

    INJECTOR_FMT("Inject: appended trailer, new_len=%d", skb->len);
    return TC_ACT_OK;
}

#define DISPATCHER_MSG(msg) bpf_printk("[SIM_DISPATCHER] " msg)
#define DISPATCHER_FMT(fmt, ...) bpf_printk("[SIM_DISPATCHER] " fmt, ##__VA_ARGS__)

SEC("tc_ingress/sim_dispatcher")
int sim_dispatcher(struct __sk_buff *skb) {
    __u32 pkt_len = skb->len;

    // 1. 长度不够，说明肯定不是仿真包，直接放行
    if (pkt_len < sizeof(struct sim_hdr) + sizeof(struct ethhdr)) {
        return TC_ACT_OK;
    }
    __u32 trailer_offset = pkt_len - sizeof(struct sim_hdr);

    // 2. 探查尾部
    struct sim_hdr sh;
    if (bpf_skb_load_bytes(skb, trailer_offset, &sh, sizeof(sh)) < 0) {
        return TC_ACT_OK;
    }

    // 3. 对暗号，如果不是我们的包，绝对不碰
    if (sh.magic != bpf_htons(SIM_MAGIC)) {
        return TC_ACT_OK;
    }

    // [这里是未来做 State Machine 循环的地方]

    // 4. 暗号对上了，执行剥离（切尾巴）
    if (bpf_skb_change_tail(skb, trailer_offset, 0) < 0) {
        DISPATCHER_FMT("Failed to strip tail");
        return TC_ACT_SHOT;
    }

    DISPATCHER_MSG("Decap successful, tail stripped");
    return TC_ACT_OK;
}

char _license[] SEC("license") = "GPL";