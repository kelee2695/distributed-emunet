package result

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
)

// CniInterface 定义CNI接口信息
type CniInterface struct {
	Name    string `json:"name"`              // 接口名称
	Mac     string `json:"mac,omitempty"`     // MAC地址，格式为 "00:11:22:33:44:55"
	Sandbox string `json:"sandbox,omitempty"` // Pod网络命名空间路径
}

// IPConfig 定义IP配置信息
type IPConfig struct {
	Version   string    `json:"version"`             // IP版本
	Address   net.IPNet `json:"address"`             // IP地址和子网掩码
	Gateway   net.IP    `json:"gateway,omitempty"`   // 网关地址
	Interface *int      `json:"interface,omitempty"` // 接口索引
}

// AddResult 定义CNI ADD命令的输出结果
type AddResult struct {
	CNIVersion string          `json:"cniVersion"`           // CNI版本
	Interfaces []*CniInterface `json:"interfaces,omitempty"` // 接口列表
	IPs        []*IPConfig     `json:"ips,omitempty"`        // IP分配列表
}

// MarshalJSON 自定义IPConfig的JSON序列化方法
func (ipc *IPConfig) MarshalJSON() ([]byte, error) {
	type Alias IPConfig
	return json.Marshal(&struct {
		Address string `json:"address"`
		*Alias
	}{
		Address: ipc.Address.String(), // 使用CIDR格式字符串
		Alias:   (*Alias)(ipc),
	})
}

// CheckResult 定义CNI CHECK命令的输出结果
type CheckResult struct {
	CNIVersion        string   `json:"cniVersion"`                  // CNI版本
	SupportedVersions []string `json:"supportedVersions,omitempty"` // 支持的版本列表
}

// OutputResult 将任意可JSON序列化的结果结构体转为JSON并输出到stdout
func OutputResult[T any](result T) error {
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal result to json failed: %v", err)
	}
	if _, err := os.Stdout.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write result to stdout failed: %v", err)
	}
	return nil
}

// NewAddResult 构造并返回一个 AddResult 实例
func NewAddResult(cniVersion string, interfaces []*CniInterface, ips []*IPConfig) *AddResult {
	return &AddResult{
		CNIVersion: cniVersion,
		Interfaces: interfaces,
		IPs:        ips,
	}
}

// NewCheckResult 构造并返回一个 CheckResult 实例
func NewCheckResult(cniVersion string, supportedVersions []string) *CheckResult {
	return &CheckResult{
		CNIVersion:        cniVersion,
		SupportedVersions: supportedVersions,
	}
}

// NewCniInterfaces 构造并返回一个 CniInterface 切片
func NewCniInterfaces(interfaces ...*CniInterface) []*CniInterface {
	return interfaces
}

// NewIPConfigs 构造并返回一个 IPConfig 切片
func NewIPConfigs(ips ...*IPConfig) []*IPConfig {
	return ips
}

// NewCniInterface 构造并返回一个 CniInterface 实例
func NewCniInterface(name string, mac string, sandbox string) *CniInterface {
	return &CniInterface{
		Name:    name,
		Mac:     mac,
		Sandbox: sandbox,
	}
}

// NewIPConfig 构造并返回一个 IPConfig 实例
func NewIPConfig(version string, address net.IPNet, gateway net.IP, iface *int) *IPConfig {
	return &IPConfig{
		Version:   version,
		Address:   address,
		Gateway:   gateway,
		Interface: iface,
	}
}
