package system

import (
	"errors"
	"os"
)

// DetectBootMode 通过检查 /sys/firmware/efi 判断节点启动模式.
//
// 在 sysfs 不可访问时(例如开发机上做单元测试)会返回 ("", error),
// 上层应在这种情况下按需回退或忽略.
func DetectBootMode() (BootMode, error) {
	_, err := os.Stat("/sys/firmware/efi")
	if err == nil {
		return BootModeUEFI, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return BootModeBIOS, nil
	}
	return "", err
}
