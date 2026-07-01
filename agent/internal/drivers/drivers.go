// Package drivers 列出已加载的内核模块以及它们的简要信息.
//
// 主要用途是给前端展示"当前内存系统都加载了哪些网卡 / 存储 / 控制器驱动",
// 并可标记关键模块是否在 initramfs 中存在但尚未绑定到任何设备.
package drivers

import (
	"bufio"
	"os"
	"sort"
	"strings"
)

// Module 描述 /proc/modules 中的一条记录.
type Module struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Used int    `json:"used"`
	By   string `json:"by,omitempty"`
}

// Loaded 返回当前已加载的内核模块列表.
//
// 在没有 /proc/modules(例如开发机上跑测试)时返回空切片,不返回错误.
func Loaded() []Module {
	f, err := os.Open("/proc/modules")
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Module
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 3 {
			continue
		}
		m := Module{Name: fields[0]}
		if len(fields) >= 4 {
			m.By = fields[3]
			if m.By == "-" {
				m.By = ""
			}
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Report 是 GET /api/drivers 的响应.
type Report struct {
	Loaded []Module `json:"loaded"`
}

// Collect 收集驱动报告.
func Collect() *Report { return &Report{Loaded: Loaded()} }
