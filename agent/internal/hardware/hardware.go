// Package hardware 提供节点硬件 / 驱动 / 网络信息的探测.
//
// 探测以尽力而为方式实现:在缺少 lspci / ethtool / sysfs 的环境下
// (例如开发机 / 单元测试)应返回部分结果而不是直接报错.
package hardware

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// NetworkInterface 表示一块网卡的探测结果.
type NetworkInterface struct {
	Name       string   `json:"name"`
	MAC        string   `json:"mac"`
	State      string   `json:"state"`
	MTU        int      `json:"mtu"`
	Driver     string   `json:"driver"`
	Firmware   string   `json:"firmware"`
	IPs        []string `json:"ips"`
	PCIID      string   `json:"pci_id,omitempty"`
	PlatformID string   `json:"platform_id,omitempty"`
}

// StorageController 表示一块 RAID / HBA / NVMe 控制器.
type StorageController struct {
	PCIID  string `json:"pci_id,omitempty"`
	Class  string `json:"class"`
	Vendor string `json:"vendor"`
	Device string `json:"device"`
	Driver string `json:"driver"`
}

// UnboundDevice 表示一台没有驱动绑定的关键设备.
type UnboundDevice struct {
	PCIID string `json:"pci_id,omitempty"`
	Path  string `json:"path"`
	Class string `json:"class"`
}

// Report 是 GET /api/hardware 的整体返回.
type Report struct {
	KernelVersion      string              `json:"kernel_version"`
	CPUArchitecture    string              `json:"cpu_architecture"`
	Interfaces         []NetworkInterface  `json:"interfaces"`
	StorageControllers []StorageController `json:"storage_controllers"`
	UnboundDevices     []UnboundDevice     `json:"unbound_devices"`
	DmesgWarnings      []string            `json:"dmesg_warnings"`
}

// Collect 收集硬件报告.
func Collect() *Report {
	r := &Report{
		KernelVersion:   readKernel(),
		CPUArchitecture: readArch(),
	}
	r.Interfaces = collectInterfaces()
	r.StorageControllers = collectStorageControllers()
	r.UnboundDevices = collectUnboundDevices()
	r.DmesgWarnings = collectDmesgWarnings()
	return r
}

func readKernel() string {
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

func readArch() string {
	if out, err := exec.Command("uname", "-m").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

func collectInterfaces() []NetworkInterface {
	var out []NetworkInterface
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		if strings.HasPrefix(ifi.Name, "docker") ||
			strings.HasPrefix(ifi.Name, "veth") ||
			strings.HasPrefix(ifi.Name, "br-") {
			continue
		}
		ni := NetworkInterface{
			Name:  ifi.Name,
			MAC:   ifi.HardwareAddr.String(),
			MTU:   ifi.MTU,
			State: ifaceState(ifi),
		}
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			ni.IPs = append(ni.IPs, a.String())
		}
		ni.Driver = readSysLink("/sys/class/net/" + ifi.Name + "/device/driver")
		ni.PCIID = readSysFile("/sys/class/net/" + ifi.Name + "/device/uevent")
		ni.Firmware = readEthtoolFirmware(ifi.Name)
		out = append(out, ni)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func ifaceState(ifi net.Interface) string {
	if ifi.Flags&net.FlagUp != 0 {
		return "up"
	}
	return "down"
}

func readSysLink(p string) string {
	target, err := os.Readlink(p)
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

func readSysFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "PCI_ID=") {
			return strings.TrimPrefix(line, "PCI_ID=")
		}
	}
	return ""
}

func readEthtoolFirmware(name string) string {
	out, err := exec.Command("ethtool", "-i", name).Output()
	if err != nil {
		return ""
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "firmware-version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "firmware-version:"))
		}
	}
	return ""
}

// collectStorageControllers 扫描 /sys/bus/pci/devices 中的存储控制器.
// 它使用 PCI class 前缀 0x01 来识别 mass storage.
// 在 sysfs 不可访问的环境(开发机)下会返回空切片.
func collectStorageControllers() []StorageController {
	var out []StorageController
	base := "/sys/bus/pci/devices"
	entries, err := os.ReadDir(base)
	if err != nil {
		return out
	}
	for _, e := range entries {
		dir := filepath.Join(base, e.Name())
		class := readSysFile2(dir + "/class")
		if !strings.HasPrefix(class, "0x01") {
			continue
		}
		ctl := StorageController{
			PCIID:  e.Name(),
			Class:  class,
			Vendor: readSysFile2(dir + "/vendor"),
			Device: readSysFile2(dir + "/device"),
			Driver: readSysLink(dir + "/driver"),
		}
		out = append(out, ctl)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PCIID < out[j].PCIID })
	return out
}

// collectUnboundDevices 找出关键 class(network,storage)但没有 driver 链接的 PCI 设备.
func collectUnboundDevices() []UnboundDevice {
	var out []UnboundDevice
	base := "/sys/bus/pci/devices"
	entries, err := os.ReadDir(base)
	if err != nil {
		return out
	}
	for _, e := range entries {
		dir := filepath.Join(base, e.Name())
		class := readSysFile2(dir + "/class")
		if class == "" {
			continue
		}
		// 0x01 mass storage, 0x02 network
		if !(strings.HasPrefix(class, "0x01") || strings.HasPrefix(class, "0x02")) {
			continue
		}
		if _, err := os.Stat(dir + "/driver"); err == nil {
			continue
		}
		out = append(out, UnboundDevice{
			PCIID: e.Name(), Class: class, Path: dir,
		})
	}
	return out
}

func collectDmesgWarnings() []string {
	out, err := exec.Command("dmesg").Output()
	if err != nil {
		return nil
	}
	var warns []string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		ll := strings.ToLower(line)
		if strings.Contains(ll, "firmware") ||
			strings.Contains(ll, "driver") ||
			strings.Contains(ll, "timeout") {
			warns = append(warns, line)
			if len(warns) > 200 {
				break
			}
		}
	}
	return warns
}

func readSysFile2(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
