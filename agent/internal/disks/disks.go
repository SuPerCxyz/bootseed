// Package disks 探测节点磁盘并执行 BootSeed 的“安全可写盘”策略。
//
// 设计原则：
//  1. 只允许 TYPE=disk 的整盘设备。
//  2. 默认禁止 loop / ram / zram / sr / fd / device-mapper 从属。
//  3. multipath 顶层默认禁止；从属路径永远禁止。
//  4. SAN 风险盘默认显示但标记为高风险，不会被自动选择。
//  5. 写盘必须解析为 /dev/disk/by-id/...，否则默认禁止。
package disks

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Disk 描述一块磁盘。
type Disk struct {
	Kname        string   `json:"kname"`       // sda / nvme0n1
	Path         string   `json:"path"`        // /dev/sda
	StablePath   string   `json:"stable_path"` // /dev/disk/by-id/wwn-...
	Type         string   `json:"type"`        // lsblk TYPE
	Size         int64    `json:"size"`        // bytes
	Model        string   `json:"model"`
	Serial       string   `json:"serial"`
	WWN          string   `json:"wwn"`
	Tran         string   `json:"tran"` // sata / nvme / fc / iscsi / sas / usb ...
	Rotational   bool     `json:"rotational"`
	Removable    bool     `json:"removable"`
	ReadOnly     bool     `json:"read_only"`
	Partitions   []string `json:"partitions"`
	FsTypes      []string `json:"fs_types"`
	HolderSlaves []string `json:"holders"`
	IsMultipath  bool     `json:"is_multipath"`
	MultipathTop bool     `json:"multipath_top"`
	RAIDInfo     string   `json:"raid_info,omitempty"`
	SANRisk      bool     `json:"san_risk"`
	Allowed      bool     `json:"allowed"`
	Reason       string   `json:"reason,omitempty"`
}

// EnumerateOptions 控制磁盘过滤策略。
type EnumerateOptions struct {
	AllowMultipathTarget  bool
	AllowUnstableDiskName bool
}

// Enumerate 调用 lsblk -J -O 输出 JSON 并构建结果。
//
// 当 lsblk 不可用时（开发机）返回空切片但不报错，
// 调用方应通过 IsLsblkAvailable 自行判断。
func Enumerate(opts EnumerateOptions) ([]Disk, error) {
	out, err := exec.Command("lsblk", "-J", "-b", "-O").Output()
	if err != nil {
		return nil, fmt.Errorf("lsblk: %w", err)
	}
	var raw struct {
		BlockDevices []rawDevice `json:"blockdevices"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	var disks []Disk
	for _, d := range raw.BlockDevices {
		if !shouldShow(d) {
			continue
		}
		disk := buildDisk(d, opts)
		disks = append(disks, disk)
	}
	sort.Slice(disks, func(i, j int) bool { return disks[i].Kname < disks[j].Kname })
	return disks, nil
}

// IsLsblkAvailable 是否能调用 lsblk。
func IsLsblkAvailable() bool {
	_, err := exec.LookPath("lsblk")
	return err == nil
}

type rawDevice struct {
	Name     string      `json:"name"`
	Path     string      `json:"path,omitempty"`
	Kname    string      `json:"kname,omitempty"`
	Type     string      `json:"type"`
	Size     json.Number `json:"size,omitempty"`
	Model    string      `json:"model,omitempty"`
	Serial   string      `json:"serial,omitempty"`
	WWN      string      `json:"wwn,omitempty"`
	Tran     string      `json:"tran,omitempty"`
	Rota     flexBool    `json:"rota,omitempty"`
	Rm       flexBool    `json:"rm,omitempty"`
	Ro       flexBool    `json:"ro,omitempty"`
	Fstype   string      `json:"fstype,omitempty"`
	Hctl     string      `json:"hctl,omitempty"`
	Vendor   string      `json:"vendor,omitempty"`
	Children []rawDevice `json:"children,omitempty"`
}

func shouldShow(d rawDevice) bool {
	if d.Type != "disk" && d.Type != "mpath" {
		return false
	}
	name := d.Name
	if name == "" {
		name = d.Kname
	}
	if name == "" {
		return false
	}
	// 排除 loop / ram / zram / sr / fd
	for _, p := range []string{"loop", "ram", "zram", "sr", "fd"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

func buildDisk(d rawDevice, opts EnumerateOptions) Disk {
	name := d.Name
	if name == "" {
		name = d.Kname
	}
	disk := Disk{
		Kname:      name,
		Path:       d.Path,
		Type:       d.Type,
		Model:      strings.TrimSpace(d.Model),
		Serial:     strings.TrimSpace(d.Serial),
		WWN:        strings.TrimSpace(d.WWN),
		Tran:       strings.TrimSpace(d.Tran),
		Rotational: bool(d.Rota),
		Removable:  bool(d.Rm),
		ReadOnly:   bool(d.Ro),
	}
	if disk.Path == "" {
		disk.Path = "/dev/" + name
	}
	if v, err := d.Size.Int64(); err == nil {
		disk.Size = v
	}
	for _, c := range d.Children {
		if c.Name == "" {
			continue
		}
		disk.Partitions = append(disk.Partitions, c.Name)
		if c.Fstype != "" {
			disk.FsTypes = append(disk.FsTypes, c.Fstype)
		}
	}
	disk.MultipathTop = d.Type == "mpath"
	disk.IsMultipath = disk.MultipathTop
	// SAN 风险判定
	switch strings.ToLower(disk.Tran) {
	case "iscsi", "fc", "fcoe":
		disk.SANRisk = true
	}
	disk.StablePath = resolveStablePath(disk)
	disk.Allowed, disk.Reason = decideAllow(disk, opts)
	return disk
}

// flexBool 兼容不同 lsblk 版本：rm/ro/rota 可能输出为布尔(true/false)、
// 数字(0/1) 或字符串("0"/"1"/"true")。统一解析为 bool。
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	switch s {
	case "true", "1", "\"1\"", "\"true\"":
		*b = true
	default:
		*b = false
	}
	return nil
}

// resolveStablePath 调用 udevadm info 查询设备 by-id 链接，找一个稳定路径。
//
// 优先级：wwn-* > scsi-* > nvme-* > virtio-* > ata-* > 其他。
func resolveStablePath(d Disk) string {
	out, err := exec.Command("udevadm", "info", "--query=symlink", "--name="+d.Path).Output()
	if err != nil {
		return guessByIDFromWWN(d)
	}
	links := strings.Fields(string(out))
	prefer := []string{"disk/by-id/wwn-", "disk/by-id/nvme-", "disk/by-id/scsi-",
		"disk/by-id/virtio-", "disk/by-id/ata-", "disk/by-id/usb-"}
	for _, pref := range prefer {
		for _, l := range links {
			if strings.Contains(l, pref) {
				return "/dev/" + l
			}
		}
	}
	return guessByIDFromWWN(d)
}

func guessByIDFromWWN(d Disk) string {
	if d.WWN == "" {
		return ""
	}
	return "/dev/disk/by-id/wwn-" + strings.TrimPrefix(d.WWN, "0x")
}

// decideAllow 综合判断该磁盘是否可作为部署目标。
func decideAllow(d Disk, opts EnumerateOptions) (bool, string) {
	if d.ReadOnly {
		return false, "磁盘为只读"
	}
	if d.Removable {
		return false, "磁盘可移除（USB / 光驱 / 软驱）"
	}
	if len(d.Partitions) > 0 {
		// 仅作风险提示，不直接拒绝；但 Reason 字段会保留信息
	}
	if d.MultipathTop && !opts.AllowMultipathTarget {
		return false, "multipath 顶层设备默认禁止，可通过 ALLOW_MULTIPATH_TARGET=true 启用"
	}
	if d.SANRisk {
		// SAN 风险盘允许显示但默认禁止，避免误写远端盘
		return false, "SAN 风险（iSCSI/FC），默认禁止"
	}
	if d.Type != "disk" && d.Type != "mpath" {
		return false, "非整盘设备: " + d.Type
	}
	if filepath.Base(d.Path) == "" {
		return false, "设备路径无效"
	}
	if d.StablePath == "" && !opts.AllowUnstableDiskName {
		return false, "缺少 by-id 稳定路径，默认禁止；可通过 ALLOW_UNSTABLE_DISK_NAME=true 启用"
	}
	return true, ""
}

// MatchTargetDisk 在已枚举的磁盘里找到允许写盘的目标。
//
// 参数 want 可以是 /dev/disk/by-id/... 也可以是 /dev/sda 一类的内核名（仅在 AllowUnstable 时）。
func MatchTargetDisk(all []Disk, want string, opts EnumerateOptions) (Disk, error) {
	if want == "" {
		return Disk{}, fmt.Errorf("缺少目标磁盘")
	}
	want = strings.TrimSpace(want)
	for _, d := range all {
		if d.StablePath == want || d.Path == want {
			if !d.Allowed {
				return Disk{}, fmt.Errorf("目标磁盘不允许部署: %s", d.Reason)
			}
			return d, nil
		}
	}
	return Disk{}, fmt.Errorf("未找到目标磁盘: %s", want)
}
