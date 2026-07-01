package system

import (
	"encoding/json"
	"os"
	"strings"
)

var (
	importedConfigPath = "/etc/bootseed/imported-node-config.json"
	networkStatusPath  = "/run/bootseed/network-status.json"
)

// ImportedNetConfig 是由 bootseed-enter 注入 initramfs 的管理网卡配置.
type ImportedNetConfig struct {
	Interface string   `json:"iface"`
	MAC       string   `json:"mac"`
	Address   string   `json:"address"`
	PrefixLen int      `json:"prefix_len"`
	Gateway   string   `json:"gateway"`
	DNS       []string `json:"dns"`
	ServerURL string   `json:"server_url"`
}

// BootseedNetStatus 描述内存系统网络应用结果.
type BootseedNetStatus struct {
	Mode    string `json:"mode"`   // dhcp/static
	Status  string `json:"status"` // ok/fallback_dhcp/failed
	Message string `json:"message,omitempty"`
}

func ReadImportedNetConfig() ImportedNetConfig {
	var cfg ImportedNetConfig
	b, err := os.ReadFile(importedConfigPath)
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(b, &cfg)
	return cfg
}

func ReadNetStatus() BootseedNetStatus {
	var st BootseedNetStatus
	b, err := os.ReadFile(networkStatusPath)
	if err != nil {
		return st
	}
	_ = json.Unmarshal(b, &st)
	return st
}

func Hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(name)
}
