package main

import (
	"os"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/version"

	cnifactory "EMU_CNI/cni-plugin/cni-factory"
)

var (
	// 日志文件路径，可以通过编译时ldflags注入
	// 编译命令示例: go build -ldflags "-X main.LogPath=/custom/path/result.log" -o emu-cni
	LogPath = "/home/node01/EMU_CNI/result.log"
)

func main() {
	// 支持通过环境变量覆盖日志路径
	if envPath := os.Getenv("EMU_CNI_LOG_PATH"); envPath != "" {
		LogPath = envPath
	}

	// 初始化工厂
	cniPlugin := cnifactory.NewCNIPlugin(LogPath, "emu-br0")

	skel.PluginMainFuncs(skel.CNIFuncs{
		Add:   cniPlugin.Add,
		Check: cniPlugin.Check,
		Del:   cniPlugin.Del,
		/* FIXME GC */
	}, version.All, "EMU_CNI plugin")
}
