package system

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// Reboot 触发节点重启。
// 内存系统里 PID1 是我们自定义的 /init(shell 脚本),不响应 busybox reboot 发的信号,
// 因此**优先直接触发内核 sysrq**(不依赖 PID1),失败再退回到 busybox/systemctl。
func Reboot(ctx context.Context) error {
	// 先尝试 sysrq: echo b > /proc/sysrq-trigger(立即重启,不 sync)
	// 提前 sync 一次落盘,减少数据丢失风险。
	_ = writeFile("/proc/sys/kernel/sysrq", "1")
	_ = exec.Command("sync").Run()
	if err := writeFile("/proc/sysrq-trigger", "b"); err == nil {
		// sysrq b 会立即重启,下面不会执行到
		time.Sleep(2 * time.Second)
	}
	// 兜底命令(容器/常规 Linux)
	return runFirst(ctx, [][]string{
		{"reboot", "-f"},
		{"busybox", "reboot", "-f"},
		{"reboot"},
		{"systemctl", "reboot"},
	})
}

// Poweroff 触发节点关机。
func Poweroff(ctx context.Context) error {
	_ = writeFile("/proc/sys/kernel/sysrq", "1")
	_ = exec.Command("sync").Run()
	if err := writeFile("/proc/sysrq-trigger", "o"); err == nil {
		time.Sleep(2 * time.Second)
	}
	return runFirst(ctx, [][]string{
		{"poweroff", "-f"},
		{"busybox", "poweroff", "-f"},
		{"poweroff"},
		{"systemctl", "poweroff"},
	})
}

// writeFile 简单封装:向指定路径写入字符串(用于 /proc/sysrq-trigger 等)。
func writeFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err
}

// runFirst 依次尝试候选命令,第一个能找到并成功执行的即返回。
// 所有命令都使用结构化参数,绝不使用 sh -c 拼接用户输入。
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
