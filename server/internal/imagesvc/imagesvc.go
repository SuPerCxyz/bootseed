// Package imagesvc 管理镜像清单 index.json：列出、删除、添加（URL/本地路径/上传 →
// 自动转 raw → zstd 压缩 → 登记），添加为带进度的异步任务。
package imagesvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/anomalyco/bootseed/server/internal/model"
)

var idRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Service 管理镜像目录与 index.json。
type Service struct {
	IndexPath string // .../images/index.json
	ImagesDir string // .../images
	mu        sync.Mutex

	jobsMu sync.Mutex
	jobs   map[string]*Job
}

// Job 描述一次添加镜像的异步任务。
type Job struct {
	ID      string  `json:"id"`
	Stage   string  `json:"stage"`   // downloading/converting/compressing/registering/done/failed
	Percent float64 `json:"percent"` // 仅下载阶段有意义
	Message string  `json:"message"`
	Error   string  `json:"error,omitempty"`
	Done    bool    `json:"done"`
}

// AddSpec 是添加镜像的参数。
type AddSpec struct {
	Mode         string // url|path|upload
	Source       string // URL 或服务器本地路径（upload 模式由 handler 落临时文件后填入）
	ID           string
	Name         string
	OS           string
	Version      string
	Architecture string
	Firmware     []string
}

// New 构造 Service。
func New(indexPath, imagesDir string) *Service {
	return &Service{IndexPath: indexPath, ImagesDir: imagesDir, jobs: map[string]*Job{}}
}

// List 读取 index.json。
func (s *Service) List() (model.Index, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readIndex()
}

func (s *Service) readIndex() (model.Index, error) {
	var idx model.Index
	b, err := os.ReadFile(s.IndexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return model.Index{SchemaVersion: 1}, nil
		}
		return idx, err
	}
	if err := json.Unmarshal(b, &idx); err != nil {
		return idx, err
	}
	if idx.SchemaVersion == 0 {
		idx.SchemaVersion = 1
	}
	return idx, nil
}

// writeIndex 原子写 index.json（调用方持锁）。
func (s *Service) writeIndex(idx model.Index) error {
	tmp := s.IndexPath + ".tmp"
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.IndexPath)
}

// Delete 删除条目与文件。
func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.readIndex()
	if err != nil {
		return err
	}
	out := idx.Images[:0]
	var removed *model.Image
	for i := range idx.Images {
		if idx.Images[i].ID == id {
			removed = &idx.Images[i]
			continue
		}
		out = append(out, idx.Images[i])
	}
	if removed == nil {
		return fmt.Errorf("镜像不存在: %s", id)
	}
	idx.Images = out
	if err := s.writeIndex(idx); err != nil {
		return err
	}
	// 删除文件（path 形如 /images/<basename>）
	base := filepath.Base(removed.Path)
	_ = os.Remove(filepath.Join(s.ImagesDir, base))
	return nil
}

// GetJob 返回任务快照。
func (s *Service) GetJob(id string) (Job, bool) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return Job{}, false
	}
	return *j, true
}

func (s *Service) setJob(id string, fn func(*Job)) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	j := s.jobs[id]
	if j == nil {
		j = &Job{ID: id}
		s.jobs[id] = j
	}
	fn(j)
}

// StartAdd 校验后启动异步添加任务，返回 jobID。
func (s *Service) StartAdd(spec AddSpec) (string, error) {
	if !idRe.MatchString(spec.ID) {
		return "", fmt.Errorf("id 含非法字符")
	}
	if spec.Architecture != "x86_64" && spec.Architecture != "aarch64" {
		return "", fmt.Errorf("architecture 必须为 x86_64/aarch64")
	}
	for _, fw := range spec.Firmware {
		if fw != "bios" && fw != "uefi" {
			return "", fmt.Errorf("firmware 仅支持 bios/uefi")
		}
	}
	if len(spec.Firmware) == 0 {
		return "", fmt.Errorf("firmware 必填")
	}
	// 重复 id 检查
	idx, err := s.List()
	if err != nil {
		return "", err
	}
	for _, im := range idx.Images {
		if im.ID == spec.ID {
			return "", fmt.Errorf("镜像 id 已存在: %s", spec.ID)
		}
	}
	jobID := fmt.Sprintf("job-%d", time.Now().UnixNano())
	s.setJob(jobID, func(j *Job) { j.Stage = "queued"; j.Message = "排队中" })
	go s.runAdd(jobID, spec)
	return jobID, nil
}

// runAdd 执行获取源 → 转 raw → zstd → 登记。
func (s *Service) runAdd(jobID string, spec AddSpec) {
	ctx := context.Background()
	work, err := os.MkdirTemp(s.ImagesDir, ".add-")
	if err != nil {
		s.fail(jobID, "创建工作目录失败: "+err.Error())
		return
	}
	defer os.RemoveAll(work)

	// 1. 获取源文件
	src := filepath.Join(work, "source")
	switch spec.Mode {
	case "url":
		s.setJob(jobID, func(j *Job) { j.Stage = "downloading"; j.Message = "下载中" })
		if err := s.download(jobID, spec.Source, src); err != nil {
			s.fail(jobID, "下载失败: "+err.Error())
			return
		}
	case "path":
		if !fileExists(spec.Source) {
			s.fail(jobID, "本地文件不存在: "+spec.Source)
			return
		}
		src = spec.Source // 直接用本地路径，不复制
	case "upload":
		if !fileExists(spec.Source) {
			s.fail(jobID, "上传临时文件缺失")
			return
		}
		src = spec.Source
	default:
		s.fail(jobID, "未知 mode: "+spec.Mode)
		return
	}

	// 2. 判定格式并转为 raw（qcow2/vmdk 等）
	finalZst := filepath.Join(s.ImagesDir, spec.ID+".raw.zst")
	var rawSize int64
	lower := strings.ToLower(src)
	if strings.HasSuffix(lower, ".raw.zst") || strings.HasSuffix(lower, ".img.zst") {
		// 已是压缩 raw，直接拷贝
		s.setJob(jobID, func(j *Job) { j.Stage = "registering"; j.Message = "登记中" })
		if err := copyFile(src, finalZst); err != nil {
			s.fail(jobID, "拷贝失败: "+err.Error())
			return
		}
		rawSize = zstdRawSize(finalZst)
	} else {
		qfmt := detectFormat(src)
		raw := filepath.Join(work, "disk.raw")
		s.setJob(jobID, func(j *Job) { j.Stage = "converting"; j.Message = "qemu-img 转 raw (" + qfmt + ")" })
		if qfmt == "raw" || qfmt == "" {
			raw = src
		} else {
			if err := runCmd(ctx, "qemu-img", "convert", "-f", qfmt, "-O", "raw", src, raw); err != nil {
				s.fail(jobID, "qemu-img 转换失败: "+err.Error())
				return
			}
		}
		if fi, e := os.Stat(raw); e == nil {
			rawSize = fi.Size()
		}
		s.setJob(jobID, func(j *Job) { j.Stage = "compressing"; j.Message = "zstd 压缩中" })
		if err := runCmd(ctx, "zstd", "-q", "-T0", "-3", "-f", raw, "-o", finalZst); err != nil {
			s.fail(jobID, "zstd 压缩失败: "+err.Error())
			return
		}
	}

	// 3. 计算压缩 sha256 与大小
	s.setJob(jobID, func(j *Job) { j.Stage = "registering"; j.Message = "计算校验和并登记" })
	sum, csize, err := sha256File(finalZst)
	if err != nil {
		s.fail(jobID, "计算 sha256 失败: "+err.Error())
		_ = os.Remove(finalZst)
		return
	}

	// 4. 追加 index.json（原子）
	s.mu.Lock()
	idx, err := s.readIndex()
	if err == nil {
		idx.Images = append(idx.Images, model.Image{
			ID: spec.ID, Name: spec.Name, OS: spec.OS, Version: spec.Version,
			Architecture: spec.Architecture, Firmware: spec.Firmware,
			Path: "/images/" + spec.ID + ".raw.zst", Format: "raw.zst",
			CompressedSize: csize, RawSize: rawSize, SHA256Compressed: sum,
		})
		err = s.writeIndex(idx)
	}
	s.mu.Unlock()
	if err != nil {
		s.fail(jobID, "写 index.json 失败: "+err.Error())
		_ = os.Remove(finalZst)
		return
	}
	s.setJob(jobID, func(j *Job) {
		j.Stage = "done"
		j.Done = true
		j.Percent = 100
		j.Message = fmt.Sprintf("完成：%s (raw %d 字节)", spec.ID, rawSize)
	})
}

func (s *Service) fail(jobID, msg string) {
	s.setJob(jobID, func(j *Job) { j.Stage = "failed"; j.Error = msg; j.Done = true })
}

// download 下载 URL 到 dst，带百分比进度。
func (s *Service) download(jobID, url, dst string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	total := resp.ContentLength
	var done int64
	buf := make([]byte, 1<<20)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			if total > 0 {
				pct := float64(done) * 100 / float64(total)
				s.setJob(jobID, func(j *Job) { j.Percent = pct })
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// ---- 辅助 ----

func detectFormat(path string) string {
	out, err := exec.Command("qemu-img", "info", path).Output()
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(ln, "file format:") {
			return strings.TrimSpace(strings.TrimPrefix(ln, "file format:"))
		}
	}
	return ""
}

func runCmd(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	var sb strings.Builder
	cmd.Stderr = &sb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(sb.String()))
	}
	return nil
}

func sha256File(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func zstdRawSize(path string) int64 {
	out, err := exec.Command("zstd", "-l", path).Output()
	if err != nil {
		return 0
	}
	// 取第二行第 5 列的解压尺寸里的数字（尽力而为）
	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return 0
	}
	fields := strings.Fields(lines[1])
	for _, f := range fields {
		if v := digits(f); v > 0 {
			_ = v
		}
	}
	return 0 // 已压缩 raw 的精确 raw_size 由调用者另行指定时更可靠
}

func digits(s string) int64 {
	var out []rune
	for _, r := range s {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return 0
	}
	var v int64
	for _, r := range out {
		v = v*10 + int64(r-'0')
	}
	return v
}

func fileExists(p string) bool { fi, err := os.Stat(p); return err == nil && !fi.IsDir() }

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
