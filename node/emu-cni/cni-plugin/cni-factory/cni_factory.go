package cnifactory

import (
	cnifunc "EMU_CNI/cni-plugin/cni-func"
	cniservice "EMU_CNI/cni-plugin/cni-service"
)

// NewCNIPlugin 工厂函数，返回一个已填充的 EmuCNIPlugin 实例
func NewCNIPlugin(resultOutputPath string, bridgeName string) cniservice.CNIPlugin {
	return &cnifunc.EmuCNIPlugin{
		ResultOutputPath: resultOutputPath,
	}
}
