package images

import (
	"strings"
	"testing"

	"github.com/anomalyco/bootseed/agent/internal/system"
)

func sampleIndex() string {
	return `{
  "schema_version": 1,
  "images": [
    {"id":"rocky-9-x86_64-uefi","name":"Rocky 9 x86_64","os":"rocky","version":"9",
     "architecture":"x86_64","firmware":["uefi","bios"],
     "path":"/images/r9-x86_64.raw.zst","format":"raw.zst",
     "compressed_size":100,"raw_size":1000,
     "sha256_compressed":"","sha256_raw":"","description":""},
    {"id":"rocky-9-aarch64-uefi","name":"Rocky 9 ARM64","os":"rocky","version":"9",
     "architecture":"aarch64","firmware":["uefi"],
     "path":"/images/r9-aarch64.raw.zst","format":"raw.zst",
     "compressed_size":100,"raw_size":1000,
     "sha256_compressed":"","sha256_raw":"","description":""}
  ]
}`
}

func TestLoadFromReader(t *testing.T) {
	c := NewCatalog()
	if err := c.LoadFromReader(strings.NewReader(sampleIndex()), "test"); err != nil {
		t.Fatal(err)
	}
	if len(c.All()) != 2 {
		t.Fatalf("expected 2 images, got %d", len(c.All()))
	}
	if _, ok := c.Get("rocky-9-x86_64-uefi"); !ok {
		t.Fatal("Get x86_64 not found")
	}
}

func TestFilterCompatible(t *testing.T) {
	c := NewCatalog()
	if err := c.LoadFromReader(strings.NewReader(sampleIndex()), "t"); err != nil {
		t.Fatal(err)
	}
	got := c.FilterCompatible(system.ArchX8664, system.BootModeUEFI)
	if len(got) != 1 || got[0].ID != "rocky-9-x86_64-uefi" {
		t.Fatalf("x86_64 UEFI 应只匹配 1 个: %+v", got)
	}
	got = c.FilterCompatible(system.ArchAArch64, system.BootModeUEFI)
	if len(got) != 1 || got[0].ID != "rocky-9-aarch64-uefi" {
		t.Fatalf("aarch64 UEFI 应只匹配 1 个: %+v", got)
	}
	got = c.FilterCompatible(system.ArchX8664, system.BootModeBIOS)
	if len(got) != 1 || got[0].ID != "rocky-9-x86_64-uefi" {
		t.Fatalf("x86_64 BIOS 应匹配支持 bios 的 1 个: %+v", got)
	}
	// ARM64 + BIOS -> 任何 firmware=uefi 的 ARM64 镜像都不允许(ARM64 第一版只支持 UEFI)
	got = c.FilterCompatible(system.ArchAArch64, system.BootModeBIOS)
	if len(got) != 0 {
		t.Fatalf("aarch64 + BIOS 不应有匹配: %+v", got)
	}
}

func TestValidateImage(t *testing.T) {
	good := Image{
		ID: "a", Name: "n", Architecture: "amd64",
		Firmware: []string{"uefi"}, Path: "/images/x.raw",
		Format: "raw", RawSize: 1,
	}
	if err := ValidateImage(&good); err != nil {
		t.Errorf("good should pass: %v", err)
	}
	if good.Architecture != "x86_64" {
		t.Errorf("Architecture 应被规范化为 x86_64, got %q", good.Architecture)
	}
	bads := []Image{
		{Name: "n", Architecture: "x86_64", Firmware: []string{"uefi"}, Path: "/x", Format: "raw", RawSize: 1},
		{ID: "../bad", Name: "n", Architecture: "x86_64", Firmware: []string{"uefi"}, Path: "/x", Format: "raw", RawSize: 1},
		{ID: "a", Name: "n", Architecture: "foo", Firmware: []string{"uefi"}, Path: "/x", Format: "raw", RawSize: 1},
		{ID: "a", Name: "n", Architecture: "x86_64", Firmware: []string{"uefi"}, Path: "/../x", Format: "raw", RawSize: 1},
		{ID: "a", Name: "n", Architecture: "x86_64", Firmware: []string{"uefi"}, Path: "/x", Format: "qcow2", RawSize: 1},
		{ID: "a", Name: "n", Architecture: "x86_64", Firmware: nil, Path: "/x", Format: "raw", RawSize: 1},
		{ID: "a", Name: "n", Architecture: "x86_64", Firmware: []string{"weird"}, Path: "/x", Format: "raw", RawSize: 1},
		{ID: "a", Name: "n", Architecture: "x86_64", Firmware: []string{"uefi"}, Path: "/x", Format: "raw", RawSize: 0},
	}
	for i := range bads {
		if err := ValidateImage(&bads[i]); err == nil {
			t.Errorf("bad image #%d 应被拒绝: %+v", i, bads[i])
		}
	}
}

func TestValidateIndex_DuplicateID(t *testing.T) {
	idx := &Index{Images: []Image{
		{ID: "a", Name: "x", Architecture: "x86_64", Firmware: []string{"uefi"}, Path: "/x", Format: "raw", RawSize: 1},
		{ID: "a", Name: "y", Architecture: "x86_64", Firmware: []string{"uefi"}, Path: "/y", Format: "raw", RawSize: 1},
	}}
	if err := ValidateIndex(idx); err == nil {
		t.Fatal("重复 ID 应被拒绝")
	}
}

func TestResolveURL(t *testing.T) {
	got := ResolveURL("http://1.2.3.4:8080/", "/images/x.raw.zst")
	want := "http://1.2.3.4:8080/images/x.raw.zst"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestIsSupportedFormat(t *testing.T) {
	for _, f := range []string{"raw", "img", "raw.gz", "img.gz", "raw.xz", "img.xz", "raw.zst", "img.zst"} {
		if !IsSupportedFormat(f) {
			t.Errorf("%s 应支持", f)
		}
	}
	for _, f := range []string{"", "qcow2", "vmdk", "tar"} {
		if IsSupportedFormat(f) {
			t.Errorf("%s 不应支持", f)
		}
	}
}
