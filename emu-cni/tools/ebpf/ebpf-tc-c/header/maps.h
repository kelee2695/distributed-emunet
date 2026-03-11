#include <linux/if_ether.h> // 引入ETH_ALEN定义

// 复合键结构体：包含网卡index和源MAC地址
struct flow_key {
    unsigned int ifindex;        // 网卡接口索引
    unsigned char src_mac[ETH_ALEN];  // 源MAC地址
} __attribute__((packed)); // 确保结构体按照实际大小对齐

// 使用typedef定义类型别名，便于在map定义中使用
typedef struct handle_emu {
    __u32 throttle_rate_bps;
    __u32 delay; // 单位：0.01 ms
    __u32 loss_rate; // 单位：0.01%
    __u32 jitter; // 单位：0.01 ms
} HANDLE_EMU;

// 修改映射键类型为复合键（网卡index + MAC地址）
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, struct flow_key);    // 使用复合键
    __type(value, struct handle_emu);
    __uint(pinning, LIBBPF_PIN_BY_NAME); // pin map by name (accessible under /sys/fs/bpf/<name>)
    __uint(max_entries, 65535);
} MAC_HANDLE_EMU SEC(".maps");


/* flow_key => last_tstamp timestamp used */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, struct flow_key);    // 使用复合键
    __type(value, uint64_t);
    __uint(max_entries, 65535);
} flow_map SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_PROG_ARRAY);
	__uint(key_size, sizeof(uint32_t));
	__uint(max_entries, 2);
	__uint(pinning, LIBBPF_PIN_BY_NAME); // pin map by name (accessible under /sys/fs/bpf/<name>)
	__array(values, int ());
} progs SEC(".maps");