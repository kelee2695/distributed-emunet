#include <linux/if_ether.h> // 引入ETH_ALEN定义
#include <linux/bpf.h>        // 引入BPF相关定义

// MAC地址键结构体
struct mac_key {
    unsigned char mac[ETH_ALEN];
} __attribute__((packed));

// FDB 表：MAC -> ifindex
// 用于单播数据包转发，提高转发效率
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, struct mac_key);
    __type(value, int); // ifindex
    __uint(max_entries, 1024);
    __uint(pinning, LIBBPF_PIN_BY_NAME); // pin map by name
} fdb_map SEC(".maps");
