package types

import (
	"encoding/json"
	"fmt"

	cniTypes "github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/cni/pkg/version"
)

type NetConf struct {
	cniTypes.NetConf
	// 显式定义 PrevResult 以便 LoadNetConf 处理
	PrevResult *current.Result `json:"-"`
}

func LoadNetConf(bytes []byte) (*NetConf, error) {
	n := &NetConf{}
	if err := json.Unmarshal(bytes, n); err != nil {
		return nil, fmt.Errorf("failed to load netconf: %w", err)
	}

	// 解析 RawPrevResult 并转换到 PrevResult (current 1.0.0 版本格式)
	if n.RawPrevResult != nil {
		resultBytes, err := json.Marshal(n.RawPrevResult)
		if err != nil {
			return nil, fmt.Errorf("could not serialize prevResult: %w", err)
		}
		res, err := version.NewResult(n.CNIVersion, resultBytes)
		if err != nil {
			return nil, fmt.Errorf("could not parse prevResult: %w", err)
		}
		n.PrevResult, err = current.NewResultFromResult(res)
		if err != nil {
			return nil, fmt.Errorf("could not convert result to current version: %w", err)
		}
	}

	return n, nil
}
