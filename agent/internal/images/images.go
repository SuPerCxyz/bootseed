// Package images 负责加载、过滤、校验镜像清单。
package images

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/anomalyco/bootseed/agent/internal/system"
)

// Image 描述清单中的一条镜像。
//
// schema_version 当前固定为 1。
type Image struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	OS               string   `json:"os"`
	Version          string   `json:"version"`
	Architecture     string   `json:"architecture"`
	Firmware         []string `json:"firmware"`
	Path             string   `json:"path"`
	Format           string   `json:"format"`
	CompressedSize   int64    `json:"compressed_size"`
	RawSize          int64    `json:"raw_size"`
	SHA256Compressed string   `json:"sha256_compressed"`
	SHA256Raw        string   `json:"sha256_raw"`
	Description      string   `json:"description"`
}

// Index 是 index.json 的整体结构。
type Index struct {
	SchemaVersion int     `json:"schema_version"`
	Images        []Image `json:"images"`
}

// Catalog 包含已加载的镜像与元信息。
type Catalog struct {
	mu     sync.RWMutex
	index  Index
	source string
	loaded time.Time
}

// NewCatalog 创建空 Catalog。
func NewCatalog() *Catalog { return &Catalog{} }

// LoadFromReader 解析 reader 中的 JSON 镜像清单。
func (c *Catalog) LoadFromReader(r io.Reader, source string) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var idx Index
	if err := dec.Decode(&idx); err != nil {
		return fmt.Errorf("解析镜像清单失败: %w", err)
	}
	if idx.SchemaVersion != 0 && idx.SchemaVersion != 1 {
		return fmt.Errorf("不支持的 schema_version: %d", idx.SchemaVersion)
	}
	if err := ValidateIndex(&idx); err != nil {
		return err
	}
	c.mu.Lock()
	c.index = idx
	c.source = source
	c.loaded = time.Now()
	c.mu.Unlock()
	return nil
}

// LoadFromHTTP 从 base URL 拉取 /images/index.json。
func (c *Catalog) LoadFromHTTP(base string) error {
	if base == "" {
		return fmt.Errorf("deploy_server 为空，无法加载镜像清单")
	}
	u := strings.TrimRight(base, "/") + "/images/index.json"
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Cache-Control", "no-cache")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载镜像清单失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("镜像清单 HTTP %d", resp.StatusCode)
	}
	return c.LoadFromReader(resp.Body, u)
}

// LoadFromFile 从本地文件加载（主要用于测试 / 离线）。
func (c *Catalog) LoadFromFile(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return c.LoadFromReader(f, p)
}

// All 返回全部镜像（按当前架构 / 模式过滤）。
func (c *Catalog) All() []Image {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Image, len(c.index.Images))
	copy(out, c.index.Images)
	return out
}

// FilterCompatible 返回与给定节点架构 + 启动模式兼容的镜像。
func (c *Catalog) FilterCompatible(arch system.Architecture, mode system.BootMode) []Image {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []Image
	for _, img := range c.index.Images {
		if !IsCompatible(img, arch, mode) {
			continue
		}
		out = append(out, img)
	}
	return out
}

// Get 按 ID 取回镜像。
func (c *Catalog) Get(id string) (Image, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, img := range c.index.Images {
		if img.ID == id {
			return img, true
		}
	}
	return Image{}, false
}

// Source 返回镜像清单来源。
func (c *Catalog) Source() string { c.mu.RLock(); defer c.mu.RUnlock(); return c.source }

// LoadedAt 返回最近一次加载时间。
func (c *Catalog) LoadedAt() time.Time { c.mu.RLock(); defer c.mu.RUnlock(); return c.loaded }

// ValidateIndex 校验整张清单。
func ValidateIndex(idx *Index) error {
	seen := make(map[string]struct{})
	for i := range idx.Images {
		if err := ValidateImage(&idx.Images[i]); err != nil {
			return fmt.Errorf("第 %d 条镜像非法: %w", i, err)
		}
		if _, dup := seen[idx.Images[i].ID]; dup {
			return fmt.Errorf("重复镜像 ID: %s", idx.Images[i].ID)
		}
		seen[idx.Images[i].ID] = struct{}{}
	}
	return nil
}

// ValidateImage 校验单条镜像条目。
func ValidateImage(img *Image) error {
	if img.ID == "" {
		return fmt.Errorf("缺少 id")
	}
	if !isSafeID(img.ID) {
		return fmt.Errorf("id 包含非法字符: %s", img.ID)
	}
	if img.Name == "" {
		return fmt.Errorf("缺少 name")
	}
	if img.Architecture == "" {
		return fmt.Errorf("缺少 architecture")
	}
	a, err := system.NormalizeArchitecture(img.Architecture)
	if err != nil {
		return fmt.Errorf("architecture 非法: %w", err)
	}
	img.Architecture = string(a)
	if img.Path == "" {
		return fmt.Errorf("缺少 path")
	}
	if !strings.HasPrefix(img.Path, "/") {
		return fmt.Errorf("path 必须是绝对路径: %s", img.Path)
	}
	if strings.Contains(img.Path, "..") {
		return fmt.Errorf("path 不能包含 ..")
	}
	if !IsSupportedFormat(img.Format) {
		return fmt.Errorf("format 不支持: %s", img.Format)
	}
	if len(img.Firmware) == 0 {
		return fmt.Errorf("缺少 firmware")
	}
	for _, f := range img.Firmware {
		f = strings.ToLower(f)
		if f != "bios" && f != "uefi" {
			return fmt.Errorf("firmware 非法: %s", f)
		}
	}
	if img.RawSize <= 0 {
		return fmt.Errorf("raw_size 必须大于 0")
	}
	return nil
}

// IsSupportedFormat 报告 BootSeed 第一版是否支持该镜像格式。
func IsSupportedFormat(f string) bool {
	switch strings.ToLower(strings.TrimSpace(f)) {
	case "raw", "img",
		"raw.gz", "img.gz",
		"raw.xz", "img.xz",
		"raw.zst", "img.zst":
		return true
	}
	return false
}

// IsCompatible 报告镜像是否与节点架构 + 启动模式兼容。
//
// 第一版规则：
//  1. 架构必须严格相等。
//  2. 节点为 UEFI -> 镜像 firmware 必须包含 "uefi"。
//  3. 节点为 BIOS -> 镜像 firmware 必须包含 "bios"。
//  4. ARM64 镜像必须包含 "uefi"。
func IsCompatible(img Image, arch system.Architecture, mode system.BootMode) bool {
	a, err := system.NormalizeArchitecture(img.Architecture)
	if err != nil {
		return false
	}
	if a != arch {
		return false
	}
	if a == system.ArchAArch64 && !containsFw(img.Firmware, "uefi") {
		return false
	}
	if mode == system.BootModeUEFI {
		return containsFw(img.Firmware, "uefi")
	}
	if mode == system.BootModeBIOS {
		return containsFw(img.Firmware, "bios")
	}
	// mode 未知时不强制 firmware
	return true
}

// ResolveURL 把清单中相对 path 与 base URL 拼接为最终下载 URL。
func ResolveURL(base, p string) string {
	base = strings.TrimRight(base, "/")
	return base + path.Clean("/"+p)
}

func containsFw(list []string, want string) bool {
	for _, f := range list {
		if strings.EqualFold(f, want) {
			return true
		}
	}
	return false
}

func isSafeID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}
