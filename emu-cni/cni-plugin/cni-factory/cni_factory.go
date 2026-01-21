package cnifactory

import (
	"EMU_CNI/cni-plugin/cni-func"
	"EMU_CNI/cni-plugin/cni-service"
)

// NewCNIPlugin 工厂函数，返回一个已填充的 EmuCNIPlugin 实例	
func NewCNIPlugin(resultOutputPath string, bridgeName string) cniservice.CNIPlugin {	
	return &cnifunc.EmuCNIPlugin{		
		ResultOutputPath: resultOutputPath,
		BridgName: bridgeName,	
	}
}
