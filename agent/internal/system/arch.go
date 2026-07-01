// Package system 提供 BootSeed 内部使用的架构规范化与基础系统接口.
package system

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Architecture 是 BootSeed 内部使用的规范化架构标识.
type Architecture string

const (
	ArchX8664   Architecture = "x86_64"
	ArchAArch64 Architecture = "aarch64"
)

// IsValid 报告该架构是否为 BootSeed 第一版支持的规范化架构.
func (a Architecture) IsValid() bool {
	switch a {
	case ArchX8664, ArchAArch64:
		return true
	}
	return false
}

// String 实现 fmt.Stringer.
func (a Architecture) String() string { return string(a) }

// NormalizeArchitecture 把常见的架构别名(amd64/arm64/x64/...)转换为
// BootSeed 内部使用的规范化架构.
//
// 第一版只接受可以映射到 x86_64 或 aarch64 的输入.
// 32 位 x86,ARMv7 等架构会返回错误.
func NormalizeArchitecture(raw string) (Architecture, error) {
	s := strings.TrimSpace(strings.ToLower(raw))
	if s == "" {
		return "", fmt.Errorf("architecture is empty")
	}
	switch s {
	case "x86_64", "amd64", "x64", "x86-64":
		return ArchX8664, nil
	case "aarch64", "arm64":
		return ArchAArch64, nil
	case "i386", "i486", "i586", "i686", "x86":
		return "", fmt.Errorf("32 位 x86 在第一版未支持: %s", raw)
	case "armv7l", "armv7", "armhf", "armv6":
		return "", fmt.Errorf("32 位 ARM 在第一版未支持: %s", raw)
	case "ia64":
		return "", fmt.Errorf("IA64 在第一版未支持: %s", raw)
	}
	return "", fmt.Errorf("未知架构: %s", raw)
}

// RuntimeArchitecture 返回当前 Go runtime 报告的规范化架构.
func RuntimeArchitecture() (Architecture, error) {
	return NormalizeArchitecture(runtime.GOARCH)
}

// UnameArchitecture 调用 `uname -m` 获取内核报告的架构.
// 在没有 uname 命令时会返回错误,调用者应回退到 RuntimeArchitecture.
func UnameArchitecture() (Architecture, error) {
	out, err := exec.Command("uname", "-m").Output()
	if err != nil {
		return "", fmt.Errorf("调用 uname 失败: %w", err)
	}
	return NormalizeArchitecture(strings.TrimSpace(string(out)))
}

// BootMode 描述节点是 Legacy BIOS 还是 UEFI.
type BootMode string

const (
	BootModeBIOS BootMode = "bios"
	BootModeUEFI BootMode = "uefi"
)

// IsValid 报告该启动模式是否合法.
func (b BootMode) IsValid() bool { return b == BootModeBIOS || b == BootModeUEFI }
