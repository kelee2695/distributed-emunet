module EMU_CNI

go 1.24.2

toolchain go1.24.10

require (
	github.com/cilium/ebpf v0.20.0
	github.com/containernetworking/cni v1.3.0
	github.com/containernetworking/plugins v1.9.0
	github.com/vishvananda/netlink v1.3.1
	golang.org/x/sys v0.38.0
)

require github.com/vishvananda/netns v0.0.5 // indirect
