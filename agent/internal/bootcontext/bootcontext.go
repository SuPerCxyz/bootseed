// Package bootcontext 解析内核命令行参数，构建 Agent 运行所需的上下文。
package bootcontext

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/anomalyco/bootseed/agent/internal/system"
)

// BootContext 描述节点本次启动的环境信息。
//
// 大多数字段来自 iPXE 在内核 cmdline 中注入的参数，
// 但 RuntimeArchitecture 与 UnameArchitecture 来自实际运行环境，
// 与 cmdline 中的 node_arch 必须一致。
type BootContext struct {
	NodeArchitecture    system.Architecture // node_arch=
	RuntimeArchitecture system.Architecture // runtime.GOARCH
	UnameArchitecture   system.Architecture // uname -m
	BootMode            system.BootMode     // 通过 /sys/firmware/efi 推断
	DeployServer        string              // deploy_server=
	AgentPort           int                 // agent_port=
	NodeMAC             string              // node_mac=
	NodeUUID            string              // node_uuid=
	AlpineVersion       string              // alpine_version=
	KernelVersion       string              // 来自 uname -r
	AgentVersion        string
}

// ParseCmdline 把 /proc/cmdline 风格的字符串解析成 key=value 映射。
// 没有等号的 token 被记录为空字符串值。
func ParseCmdline(s string) map[string]string {
	m := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(s))
	scanner.Split(bufio.ScanWords)
	for scanner.Scan() {
		tok := scanner.Text()
		if idx := strings.IndexByte(tok, '='); idx >= 0 {
			k := tok[:idx]
			v := tok[idx+1:]
			m[k] = v
		} else {
			m[tok] = ""
		}
	}
	return m
}

// ReadCmdline 从 /proc/cmdline 读取原始字符串。
// 当文件不存在时返回空字符串以便单元测试和开发机调试。
func ReadCmdline() (string, error) {
	b, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}

// Build 根据 cmdline 与运行时信息构造 BootContext。
//
// 必须显式校验 node_arch 与 runtime / uname 架构一致，避免把 x86_64
// initramfs 错误地拿来跑在 aarch64 节点（或反过来）的情况下盲目部署。
func Build(cmdline string, agentVersion string) (*BootContext, error) {
	kv := ParseCmdline(cmdline)
	ctx := &BootContext{
		AgentVersion: agentVersion,
	}

	if v := kv["node_arch"]; v != "" {
		a, err := system.NormalizeArchitecture(v)
		if err != nil {
			return nil, fmt.Errorf("node_arch 无效: %w", err)
		}
		ctx.NodeArchitecture = a
	}

	if v := kv["deploy_server"]; v != "" {
		ctx.DeployServer = v
	}
	if v := kv["node_mac"]; v != "" {
		ctx.NodeMAC = v
	}
	if v := kv["node_uuid"]; v != "" {
		ctx.NodeUUID = v
	}
	if v := kv["alpine_version"]; v != "" {
		ctx.AlpineVersion = v
	}
	if v := kv["agent_port"]; v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p <= 0 || p > 65535 {
			return nil, fmt.Errorf("agent_port 无效: %q", v)
		}
		ctx.AgentPort = p
	}

	if ra, err := system.RuntimeArchitecture(); err == nil {
		ctx.RuntimeArchitecture = ra
	}
	if ua, err := system.UnameArchitecture(); err == nil {
		ctx.UnameArchitecture = ua
	}
	if mode, err := system.DetectBootMode(); err == nil {
		ctx.BootMode = mode
	}
	if kver, err := readKernelVersion(); err == nil {
		ctx.KernelVersion = kver
	}

	return ctx, nil
}

// VerifyArchitectures 检查 cmdline 声明的架构与运行时是否一致。
// 在生产部署中任意不一致都必须拒绝继续，避免误写盘。
func (c *BootContext) VerifyArchitectures() error {
	if c.NodeArchitecture == "" {
		return fmt.Errorf("缺少 node_arch 内核参数")
	}
	if !c.NodeArchitecture.IsValid() {
		return fmt.Errorf("node_arch 不在支持列表: %s", c.NodeArchitecture)
	}
	if c.RuntimeArchitecture != "" && c.RuntimeArchitecture != c.NodeArchitecture {
		return fmt.Errorf("node_arch=%s 与 Go runtime 架构 %s 不一致",
			c.NodeArchitecture, c.RuntimeArchitecture)
	}
	if c.UnameArchitecture != "" && c.UnameArchitecture != c.NodeArchitecture {
		return fmt.Errorf("node_arch=%s 与 uname 架构 %s 不一致",
			c.NodeArchitecture, c.UnameArchitecture)
	}
	return nil
}

func readKernelVersion() (string, error) {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
