package disks

import "testing"

func TestDecideAllow(t *testing.T) {
	tests := []struct {
		name string
		d    Disk
		opts EnumerateOptions
		want bool
	}{
		{
			name: "正常磁盘有稳定路径",
			d: Disk{Type: "disk", Path: "/dev/sda",
				StablePath: "/dev/disk/by-id/wwn-0x1234"},
			want: true,
		},
		{
			name: "只读拒绝",
			d:    Disk{Type: "disk", Path: "/dev/sda", ReadOnly: true},
			want: false,
		},
		{
			name: "可移除拒绝",
			d:    Disk{Type: "disk", Path: "/dev/sdb", Removable: true},
			want: false,
		},
		{
			name: "缺稳定路径默认拒绝",
			d:    Disk{Type: "disk", Path: "/dev/sda"},
			want: false,
		},
		{
			name: "缺稳定路径但允许",
			d:    Disk{Type: "disk", Path: "/dev/sda"},
			opts: EnumerateOptions{AllowUnstableDiskName: true},
			want: true,
		},
		{
			name: "multipath 顶层默认拒绝",
			d: Disk{Type: "mpath", Path: "/dev/mapper/mpatha",
				StablePath:   "/dev/disk/by-id/dm-uuid-mpath-xx",
				MultipathTop: true},
			want: false,
		},
		{
			name: "multipath 顶层允许",
			d: Disk{Type: "mpath", Path: "/dev/mapper/mpatha",
				StablePath:   "/dev/disk/by-id/dm-uuid-mpath-xx",
				MultipathTop: true},
			opts: EnumerateOptions{AllowMultipathTarget: true},
			want: true,
		},
		{
			name: "SAN iSCSI 风险拒绝",
			d: Disk{Type: "disk", Path: "/dev/sda", Tran: "iscsi",
				StablePath: "/dev/disk/by-id/scsi-xxxxx", SANRisk: true},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, _ := decideAllow(tt.d, tt.opts)
			if allowed != tt.want {
				t.Errorf("got %v, want %v", allowed, tt.want)
			}
		})
	}
}

func TestMatchTargetDisk(t *testing.T) {
	all := []Disk{
		{Kname: "sda", Path: "/dev/sda",
			StablePath: "/dev/disk/by-id/wwn-0x1", Type: "disk",
			Allowed: true},
		{Kname: "sdb", Path: "/dev/sdb", Type: "disk",
			Allowed: false, Reason: "缺稳定路径"},
	}
	if _, err := MatchTargetDisk(all, "/dev/disk/by-id/wwn-0x1",
		EnumerateOptions{}); err != nil {
		t.Errorf("应该能匹配 wwn: %v", err)
	}
	if _, err := MatchTargetDisk(all, "/dev/sdb",
		EnumerateOptions{}); err == nil {
		t.Error("不允许的磁盘应该报错")
	}
	if _, err := MatchTargetDisk(all, "",
		EnumerateOptions{}); err == nil {
		t.Error("空目标磁盘应报错")
	}
	if _, err := MatchTargetDisk(all, "/dev/sdz",
		EnumerateOptions{}); err == nil {
		t.Error("不存在的磁盘应报错")
	}
}

func TestShouldShow(t *testing.T) {
	cases := []struct {
		d    rawDevice
		want bool
	}{
		{rawDevice{Name: "sda", Type: "disk"}, true},
		{rawDevice{Name: "mpatha", Type: "mpath"}, true},
		{rawDevice{Name: "sda1", Type: "part"}, false},
		{rawDevice{Name: "loop0", Type: "disk"}, false},
		{rawDevice{Name: "ram0", Type: "disk"}, false},
		{rawDevice{Name: "zram0", Type: "disk"}, false},
		{rawDevice{Name: "sr0", Type: "rom"}, false},
		{rawDevice{Name: "sr0", Type: "disk"}, false},
	}
	for _, c := range cases {
		if shouldShow(c.d) != c.want {
			t.Errorf("shouldShow(%s/%s)=%v, want %v", c.d.Name, c.d.Type, shouldShow(c.d), c.want)
		}
	}
}
