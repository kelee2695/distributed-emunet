package pkg

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/cilium/ebpf"
)

type FlowKey struct {
	Ifindex uint32
	SrcMac  [6]byte
}

type HandleEmu struct {
	ThrottleRateBps uint32
	Delay           uint32
	LossRate        uint32
	Jitter          uint32
}

func ParseMAC(macStr string) ([6]byte, error) {
	var mac [6]byte

	parts := strings.Split(macStr, ":")
	if len(parts) != 6 {
		return mac, fmt.Errorf("invalid MAC address format")
	}

	for i, part := range parts {
		b, err := hex.DecodeString(part)
		if err != nil {
			return mac, fmt.Errorf("invalid MAC address: %v", err)
		}
		if len(b) != 1 {
			return mac, fmt.Errorf("invalid MAC byte: %s", part)
		}
		mac[i] = b[0]
	}

	return mac, nil
}

func AddEBPFEntry(ebpfMap *ebpf.Map, ifindex uint32, macStr string, throttleRateBps, delay, lossRate, jitter uint32) error {
	mac, err := ParseMAC(macStr)
	if err != nil {
		return fmt.Errorf("failed to parse MAC address: %v", err)
	}

	key := FlowKey{
		Ifindex: ifindex,
		SrcMac:  mac,
	}

	value := HandleEmu{
		ThrottleRateBps: throttleRateBps,
		Delay:           delay,
		LossRate:        lossRate,
		Jitter:          jitter,
	}

	return ebpfMap.Put(key, value)
}

func DeleteEBPFEntry(ebpfMap *ebpf.Map, ifindex uint32, macStr string) error {
	mac, err := ParseMAC(macStr)
	if err != nil {
		return fmt.Errorf("failed to parse MAC address: %v", err)
	}

	key := FlowKey{
		Ifindex: ifindex,
		SrcMac:  mac,
	}

	return ebpfMap.Delete(key)
}

func LoadEBPFMap(mapPath string) (*ebpf.Map, error) {
	return ebpf.LoadPinnedMap(mapPath, &ebpf.LoadPinOptions{})
}

const DefaultEBPFMapPath = "/sys/fs/bpf/tc_emu/maps/MAC_HANDLE_EMU"
