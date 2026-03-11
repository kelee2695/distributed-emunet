package cnifactory

import (
	cnifunc "github.com/emunet/emunet-operator/emu-cni/cni-plugin/cni-func"
	cniservice "github.com/emunet/emunet-operator/emu-cni/cni-plugin/cni-service"
)

// NewCNIPlugin 工厂函数，返回一个已填充的 EmuCNIPlugin 实例
func NewCNIPlugin(resultOutputPath string, bridgeName string) cniservice.CNIPlugin {
	return &cnifunc.EmuCNIPlugin{
		ResultOutputPath: resultOutputPath,
	}
}
