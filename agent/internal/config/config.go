// Package config 加载与校验 Agent 运行所需的配置。
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/anomalyco/bootseed/agent/internal/system"
)

// Config 描述 Agent 运行配置。
type Config struct {
	ListenAddr             string
	DeployServer           string
	AllowCustomImageServer bool
	AllowMultipathTarget   bool
	AllowUnstableDiskName  bool
	EnableHTTPSImages      bool
	WebRoot                string
	AgentVersion           string
	NetworkDeviceTimeout   int
	StorageDeviceTimeout   int
}

// FromEnv 从环境变量与启动参数装配 Config。
//
// 默认值：
//   - ListenAddr:               :8080
//   - DeployServer:             由 cmdline 注入
//   - AllowCustomImageServer:   false
//   - AllowMultipathTarget:     false
//   - AllowUnstableDiskName:    false
//   - EnableHTTPSImages:        true
func FromEnv() *Config {
	c := &Config{
		ListenAddr:             ":8080",
		AllowCustomImageServer: envBool("ALLOW_CUSTOM_IMAGE_SERVER", false),
		AllowMultipathTarget:   envBool("ALLOW_MULTIPATH_TARGET", false),
		AllowUnstableDiskName:  envBool("ALLOW_UNSTABLE_DISK_NAME", false),
		EnableHTTPSImages:      envBool("ENABLE_HTTPS_IMAGES", true),
		WebRoot:                os.Getenv("AGENT_WEB_ROOT"),
		AgentVersion:           envDefault("AGENT_VERSION", "0.1.0"),
		NetworkDeviceTimeout:   envInt("NETWORK_DEVICE_TIMEOUT", 60),
		StorageDeviceTimeout:   envInt("STORAGE_DEVICE_TIMEOUT", 90),
	}
	if v := os.Getenv("AGENT_LISTEN"); v != "" {
		c.ListenAddr = v
	}
	if v := os.Getenv("AGENT_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 {
			c.ListenAddr = fmt.Sprintf(":%d", p)
		}
	}
	if v := os.Getenv("DEPLOY_SERVER"); v != "" {
		c.DeployServer = v
	}
	return c
}

// ValidateImageURL 检查镜像 URL 是否符合策略。
// 它不做 DNS 解析，只做语法 + 协议校验。
func (c *Config) ValidateImageURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("镜像 URL 为空")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("镜像 URL 不合法: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("镜像 URL scheme 必须是 http/https: %s", u.Scheme)
	}
	if u.Scheme == "https" && !c.EnableHTTPSImages {
		return fmt.Errorf("当前配置禁止 HTTPS 镜像")
	}
	if u.Host == "" {
		return fmt.Errorf("镜像 URL 缺少 host")
	}
	if strings.Contains(u.Host, "..") {
		return fmt.Errorf("镜像 URL host 非法")
	}
	return nil
}

// SupportedArchitectures 从环境变量读取允许的架构集合。
func SupportedArchitectures() []system.Architecture {
	raw := envDefault("SUPPORTED_ARCHITECTURES", "x86_64,aarch64")
	parts := strings.Split(raw, ",")
	out := make([]system.Architecture, 0, len(parts))
	for _, p := range parts {
		a, err := system.NormalizeArchitecture(strings.TrimSpace(p))
		if err == nil {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		out = []system.Architecture{system.ArchX8664, system.ArchAArch64}
	}
	return out
}

func envBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "":
		return def
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	}
	return def
}

func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func envDefault(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}
