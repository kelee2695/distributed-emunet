#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <bpf/bpf_helpers.h>

#define DEBUG 1

#ifdef DEBUG
#define bpf_debug(fmt, ...) bpf_printk(fmt, ##__VA_ARGS__)
#else
#define bpf_debug(fmt, ...)
#endif

// ============================================================
// Dummy XDP 程序
// 作用: 挂载在 veth 接口的对端，用于激活 veth 驱动的 XDP 接收队列。
//       将对端 bpf_redirect_map 过来的原始 xdp_frame 转换为 skb，
//       并放行给内核协议栈，从而触发后续的 TC Ingress Hook。
// ============================================================

SEC("xdp")
int xdp_dummy(struct xdp_md *ctx) {
    // 打印日志：如果你在 trace_pipe 看到这条日志，
    // 说明数据包已经成功跨越 veth pair，进入了当前网卡！
    bpf_debug("DUMMY_XDP: Packet entered ifindex %d, passing to TC...\n", ctx->ingress_ifindex);
    
    // 核心逻辑：啥也不干，直接放行
    return XDP_PASS;
}

char _license[] SEC("license") = "GPL";