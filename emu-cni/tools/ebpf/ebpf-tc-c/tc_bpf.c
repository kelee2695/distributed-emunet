#include <stdint.h>
#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/stddef.h>
#include <linux/in.h>
#include <linux/ip.h>
#include <linux/pkt_cls.h>
#include <linux/tcp.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>
#include "header/helpers.h"
#include "header/maps.h"

/* */
#define TIME_HORIZON_NS (2000 * 1000 * 1000)
#define NS_PER_SEC 1000000000
#define ECN_HORIZON_NS 500000000
#define NS_PER_MS 1000000
#define NS_PER_0_0_1_MS 10000
#define PKT_LOSS_SCOPE 10000

static inline int inject_delay_jitter(struct __sk_buff *skb, uint32_t *delay, uint32_t *jitter) { // 参数名从delay_ms改为delay
    uint64_t delay_ns;
    uint64_t jitter_ns;
    uint64_t now = bpf_ktime_get_ns();
    // 单位转换：0.01ms -> ns = *delay * 10000ns (因为1ms=1,000,000ns，所以0.01ms=10,000ns)
    delay_ns = (*delay) * NS_PER_0_0_1_MS; // 0.01ms = 10,000ns 
    // 单位转换：0.01ms -> ns = *jitter * 10000ns (因为1ms=1,000,000ns，所以0.01ms=10,000ns)
    jitter_ns = (*jitter) * NS_PER_0_0_1_MS; // 0.01ms = 10,000ns 
    uint64_t ts = skb->tstamp;
    
    // 生成不定正负的随机抖动：[-jitter_ns, jitter_ns]
    int64_t random_jitter = 0;
    if (jitter_ns > 0) {
        // 使用bpf_get_prandom_u32()生成随机数
        __u32 rand = bpf_get_prandom_u32();
        // 转换为[-jitter_ns, jitter_ns]的范围
        random_jitter = (int64_t)(rand % (2 * jitter_ns + 1)) - jitter_ns;
    }
    
    uint64_t new_ts = 0;
    if (ts == 0) {
        // 第一次收到包，使用当前时间作为基准
        new_ts = now + delay_ns + random_jitter;
    } else {
        // 否则在原有时间基础上添加延迟和抖动
        new_ts = ts + delay_ns + random_jitter;
    }

    // 设置新的时间戳
    skb->tstamp = new_ts;

    return TC_ACT_OK;
}

/*
 * For some reason section names need to start with "tc"
 * TODO: Remove duplicate header parsing code
 */
SEC("tc_delay_jitter")
int delay_jitter(struct __sk_buff *skb)
{
    // data_end is a void* to the end of the packet. Needs weird casting due to kernel weirdness.
    void *data_end = (void *)(unsigned long long)skb->data_end;
    // data is a void* to the beginning of the packet. Also needs weird casting.
    void *data = (void *)(unsigned long long)skb->data;

    // nh keeps track of the beginning of the next header to parse
    struct hdr_cursor nh;

    struct ethhdr *eth;

    // start parsing at beginning of data
    nh.pos = data;

    // parse ethernet header only to get source MAC address
    if (parse_ethhdr(&nh, data_end, &eth) == TC_ACT_SHOT) {
        return TC_ACT_SHOT;
    }
    
    // 创建复合键：网卡index + 源MAC地址
    struct flow_key key;
    key.ifindex = skb->ifindex;  // 获取当前网卡index
    bpf_probe_read_kernel(key.src_mac, ETH_ALEN, eth->h_source);
    
    uint32_t *delay;
    uint32_t *jitter; // 新增字段：抖动
    struct handle_emu *val_struct;
    // Map lookup - 使用复合键
    val_struct = bpf_map_lookup_elem(&MAC_HANDLE_EMU, &key);

    // Safety check, go on if no handle could be retrieved
    if (!val_struct) {
        return TC_ACT_OK;
    }

    delay = &val_struct->delay; // 字段名从delay_ms改为delay
    // Safety check, go on if no handle could be retrieved
    if (!delay) {
        return TC_ACT_OK;
    }

    jitter = &val_struct->jitter; // 新增字段：抖动
    // Safety check, go on if no handle could be retrieved
    if (!jitter) {
        return TC_ACT_OK;
    }

    return inject_delay_jitter(skb, delay, jitter);
}

static inline int throttle_flow(struct __sk_buff *skb, struct flow_key *key, uint32_t *throttle_rate_bps)
{
    uint64_t *last_tstamp = bpf_map_lookup_elem(&flow_map, key);
    
    // 修正1：单位换算，字节转比特 (* 8)
    uint64_t delay_ns = ((uint64_t)skb->len) * 8 * NS_PER_SEC / *throttle_rate_bps;

    uint64_t now = bpf_ktime_get_ns();
    uint64_t tstamp = skb->tstamp;
    
    // 如果 skb->tstamp 是 0 或者旧时间，修正为当前时间
    if (tstamp < now)
        tstamp = now;

    uint64_t next_tstamp = 0;

    if (last_tstamp) {
        next_tstamp = *last_tstamp + delay_ns;
    } else {
        // 第一次收到包，基准是当前时间 + 传输耗时
        next_tstamp = tstamp + delay_ns;
    }

    // 如果计算出的发送时间在“现在”之前（即不需要排队），或者只是轻微排队
    if (next_tstamp <= tstamp) {
        // 即使立即发送，也要更新 map 为“该包发送完成的时间”，防止突发
        // 修正3：使用 next_tstamp (即 last + delay) 而不是 tstamp (now)
        // 这里的逻辑稍微调整：如果拥塞并不严重，我们重置时间窗口到 now + delay，避免长时间空闲后积累过多 credit
        uint64_t new_base = tstamp + delay_ns; 
        bpf_map_update_elem(&flow_map, key, &new_base, BPF_ANY);
        
        bpf_tail_call(skb, &progs, 0);
        return TC_ACT_OK;
    }

    // 防止队列积压过大（例如超过2秒的包直接丢弃）
    if (next_tstamp - now >= TIME_HORIZON_NS)
        return TC_ACT_SHOT;

    // 更新 Map：告诉下一个包，你最早只能在 next_tstamp 之后发
    if (bpf_map_update_elem(&flow_map, key, &next_tstamp, BPF_ANY)) // 建议用 BPF_ANY 以防万一
        return TC_ACT_SHOT;

    // 设置 skb 的发送时间，fq qdisc 会看到这个时间并挂起数据包
    skb->tstamp = next_tstamp;

    bpf_tail_call(skb, &progs, 0);
    return TC_ACT_OK;
}

SEC("tc_loss_bps")
int loss_bps(struct __sk_buff *skb)
{
    // 数据包尾指针
    void *data_end = (void *)(unsigned long long)skb->data_end;
    // 数据包头指针
    void *data = (void *)(unsigned long long)skb->data;

    // nh 指向数据包头
    struct hdr_cursor nh;

    struct ethhdr *eth;

    // start parsing at beginning of data
    nh.pos = data;

    // 解析以太网头，获取源MAC地址
    if (parse_ethhdr(&nh, data_end, &eth) == TC_ACT_SHOT) {
        return TC_ACT_SHOT;
    }
    
    // 创建复合键：网卡index + 源MAC地址
    struct flow_key key;
    key.ifindex = skb->ifindex;  // 获取当前网卡index
    bpf_probe_read_kernel(key.src_mac, ETH_ALEN, eth->h_source);

    struct handle_emu *val_struct;
    // Map lookup - 使用复合键
    val_struct = bpf_map_lookup_elem(&MAC_HANDLE_EMU, &key);

    // Safety check, go on if no handle could be retrieved
    if (!val_struct) {
        return TC_ACT_OK;
    }
    
    //========================================================================
    // 丢包逻辑
    __u32 *loss_rate = &val_struct->loss_rate;
    
    // 丢包逻辑：生成[0, PKT_LOSS_SCOPE]随机数与loss_rate比较，比loss_rate小则丢包
    if (*loss_rate > 0) {
        __u32 rand_num = bpf_get_prandom_u32() % PKT_LOSS_SCOPE;
        if (rand_num < *loss_rate) {
            return TC_ACT_SHOT;  // 丢包
        }
    }
    //========================================================================
    //========================================================================
    // 限速逻辑
    __u32 *throttle_rate_bps = &val_struct->throttle_rate_bps;
    
    // Safety check, go on if no handle could be retrieved
    if (!throttle_rate_bps)  {
        return TC_ACT_OK;
    }
    return throttle_flow(skb, &key, throttle_rate_bps);
    //========================================================================
}

char _license[] SEC("license") = "GPL";
