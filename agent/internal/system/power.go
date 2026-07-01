package system

import (
	"context"
	"os/exec"
	"time"
)

// Reboot 触发节点重启.优先调用结构化的外部命令,避免 shell 拼接.
// 在内存系统中通常存在 busybox reboot;找不到时退回到 systemctl/poweroff 工具链.
func Reboot(ctx context.Context) error {
	return runFirst(ctx, [][]string{
		{"reboot"},
		{"busybox", "reboot"},
		{"systemctl", "reboot"},
	})
}

// Poweroff 触发节点关机.
func Poweroff(ctx context.Context) error {
	return runFirst(ctx, [][]string{
		{"poweroff"},
		{"busybox", "poweroff"},
		{"systemctl", "poweroff"},
	})
}

// runFirst 依次尝试候选命令,第一个能找到并成功执行的即返回.
// 所有命令都使用结构化参数,绝不使用 sh -c 拼接用户输入.
func runFirst(ctx context.Context, candidates [][]string) error {
	var lastErr error
	for _, c := range candidates {
		bin, err := exec.LookPath(c[0])
		if err != nil {
			lastErr = err
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		cmd := exec.CommandContext(cctx, bin, c[1:]...)
		err = cmd.Run()
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return lastErr
}
