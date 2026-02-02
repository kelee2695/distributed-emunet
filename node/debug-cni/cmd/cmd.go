package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	"github.com/containernetworking/cni/pkg/skel"
	cniTypes "github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	cniVersion "github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
)

type NetConf struct {
	cniTypes.NetConf
	PrevResult *current.Result `json:"-"`
}

type Cmd struct{}
type Option func(cmd *Cmd)

func logToDebugFile(content string) {
	f, err := os.OpenFile("/tmp/cni-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		defer f.Close()
		f.WriteString(fmt.Sprintf("--- %s ---\n%s\n\n", "DEBUG INFO", content))
	}
}

func (c *Cmd) loadNetConf(bytes []byte) (*NetConf, error) {
	n := &NetConf{}
	if err := json.Unmarshal(bytes, n); err != nil {
		return nil, fmt.Errorf("failed to load netconf: %v", err)
	}
	if n.RawPrevResult != nil {
		prevBytes, _ := json.Marshal(n.RawPrevResult)
		res, _ := cniVersion.NewResult(n.CNIVersion, prevBytes)
		n.PrevResult, _ = current.NewResultFromResult(res)
	}
	return n, nil
}

func (c *Cmd) Add(args *skel.CmdArgs) error {
	n, err := c.loadNetConf(args.StdinData)
	if err != nil {
		return err
	}

	if n.PrevResult != nil {
		// 1. 获取当前（主机）命名空间，以便稍后切回来
		hostNs, err := ns.GetCurrentNS()
		if err != nil {
			return fmt.Errorf("failed to get host ns: %v", err)
		}
		defer hostNs.Close()

		// 2. 获取目标容器命名空间
		containerNs, err := ns.GetNS(args.Netns)
		if err != nil {
			return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
		}
		defer containerNs.Close()

		// 锁定 OS 线程，防止在切换 NS 时被调度到其他 CPU 导致混乱
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		// 3. 切入容器命名空间
		if err := containerNs.Set(); err != nil {
			return fmt.Errorf("failed to enter netns: %v", err)
		}

		// --- [在容器空间内执行] ---
		link, err := netlink.LinkByName(args.IfName)
		parentIndex := -1
		if err == nil {
			parentIndex = link.Attrs().ParentIndex
		}
		// --- ----------------- ---

		// 4. 切回主机命名空间
		hostNs.Set()

		// 5. 在主机空间通过 index 找网卡名
		if parentIndex != -1 {
			hostLink, err := netlink.LinkByIndex(parentIndex)
			if err == nil {
				n.PrevResult.Interfaces = append(n.PrevResult.Interfaces, &current.Interface{
					Name: hostLink.Attrs().Name,
				})
			}
		}

		resultBytes, _ := json.MarshalIndent(n.PrevResult, "", "  ")
		logToDebugFile(fmt.Sprintf("COMMAND: ADD\nResult:\n%s", string(resultBytes)))
		return cniTypes.PrintResult(n.PrevResult, n.CNIVersion)
	}
	return nil
}

func (c *Cmd) Del(args *skel.CmdArgs) error   { return nil }
func (c *Cmd) Check(args *skel.CmdArgs) error { return nil }

func PluginMain(opts ...Option) {
	cmd := &Cmd{}
	skel.PluginMainFuncs(skel.CNIFuncs{
		Add: cmd.Add, Del: cmd.Del, Check: cmd.Check,
	}, cniVersion.PluginSupports("0.1.0", "0.2.0", "0.3.0", "0.3.1", "0.4.0", "1.0.0", "1.1.0"), "Debug CNI")
}
