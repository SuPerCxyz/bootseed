package system

import "testing"

func TestNormalizeArchitecture(t *testing.T) {
	cases := []struct {
		in      string
		want    Architecture
		wantErr bool
	}{
		{"x86_64", ArchX8664, false},
		{"amd64", ArchX8664, false},
		{"AMD64", ArchX8664, false},
		{"x64", ArchX8664, false},
		{"x86-64", ArchX8664, false},
		{"aarch64", ArchAArch64, false},
		{"arm64", ArchAArch64, false},
		{"ARM64", ArchAArch64, false},
		{"i686", "", true},
		{"i386", "", true},
		{"x86", "", true},
		{"armv7l", "", true},
		{"armhf", "", true},
		{"ia64", "", true},
		{"", "", true},
		{"powerpc", "", true},
	}
	for _, c := range cases {
		got, err := NormalizeArchitecture(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("NormalizeArchitecture(%q) 期望错误, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeArchitecture(%q) 返回错误 %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeArchitecture(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestArchitectureIsValid(t *testing.T) {
	if !ArchX8664.IsValid() || !ArchAArch64.IsValid() {
		t.Fatal("x86_64/aarch64 应该是合法架构")
	}
	if Architecture("foo").IsValid() {
		t.Fatal("foo 不应是合法架构")
	}
}

func TestBootModeIsValid(t *testing.T) {
	if !BootModeBIOS.IsValid() || !BootModeUEFI.IsValid() {
		t.Fatal("bios/uefi 应该是合法启动模式")
	}
	if BootMode("xyz").IsValid() {
		t.Fatal("xyz 不应是合法启动模式")
	}
}
