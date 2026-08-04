#ifndef __SIM_DEFS_H__
#define __SIM_DEFS_H__

#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

/* Global macro definitions */
#define SIM_MAGIC 0x514D
#define SIM_TX_IFINDEX_DEFAULT 10
#define MAX_SIM_STEPS 15

/* Configuration keys for sim_config_map */
#define SIM_CONFIG_KEY_TX_IFINDEX 0

/* Entity type enumeration */
enum entity_type {
    ENTITY_LINK = 1,
    ENTITY_SWITCH = 2,
    ENTITY_QUEUE = 3,
    ENTITY_END = 4
};

/* L2.5 simulation header - inserted between MAC and IP headers */
/* Size: 12 bytes */
struct sim_hdr {
    __be16 orig_eth_type;  /* Original Ethernet protocol type */
    __be16 magic;          /* Must be SIM_MAGIC (network byte order) */
    __u32 current_type;    /* Current entity type */
    __u32 current_id;      /* Current entity ID */
} __attribute__((packed));

/* Link state structure for link_map */
struct link_state {
    __u64 prop_delay_ns;   /* Propagation delay in nanoseconds */
    __u32 next_type;       /* Next entity type */
    __u32 next_id;         /* Next entity ID */
};

/* Route key structure for switch_route_map */
struct route_key {
    __u32 switch_id;       /* Switch ID */
    __be32 dest_ip;        /* Destination IP address */
} __attribute__((packed));

/* Route value structure for switch_route_map */
struct route_val {
    __u32 next_type;       /* Next entity type */
    __u32 next_id;         /* Next entity ID */
};

/* Queue state structure for queue_map */
struct queue_state {
    struct bpf_spin_lock lock;  /* Spin lock for thread safety */
    __u64 last_update_ns;       /* Last update timestamp */
    __u64 current_bits;         /* Current queue occupancy in bits */
    __u64 max_bits;             /* Maximum queue capacity in bits */
    __u64 bandwidth_bps;        /* Bandwidth in bits per second */
    __u32 next_type;            /* Next entity type */
    __u32 next_id;              /* Next entity ID */
};

/* eBPF Maps definitions */

/* Link map - stores link state information */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u32);
    __type(value, struct link_state);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
    __uint(max_entries, 1024);
} link_map SEC(".maps");

/* Switch route map - stores routing information */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, struct route_key);
    __type(value, struct route_val);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
    __uint(max_entries, 4096);
} switch_route_map SEC(".maps");

/* Queue map - stores queue state with spin lock */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u32);
    __type(value, struct queue_state);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
    __uint(max_entries, 1024);
} queue_map SEC(".maps");

/* Destination ifindex map - maps IP to target veth ifindex */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __be32);
    __type(value, __u32);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
    __uint(max_entries, 1024);
} dest_ifindex_map SEC(".maps");

/* Configuration map - stores runtime configurable parameters */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u32);           /* Configuration key */
    __type(value, __u32);         /* Configuration value */
    __uint(pinning, LIBBPF_PIN_BY_NAME);
    __uint(max_entries, 16);
} sim_config_map SEC(".maps");

#endif /* __SIM_DEFS_H__ */
