package deploy

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/anomalyco/bootseed/agent/internal/images"
	"github.com/anomalyco/bootseed/agent/internal/progress"
)

// Pipeline 描述一次镜像写盘流水线所需的输入参数.
type Pipeline struct {
	Image        images.Image
	ImageURL     string
	TargetDevice string // /dev/disk/by-id/...
	VerifyRaw    bool
	HTTPClient   *http.Client
	Tracker      *progress.Tracker
	// 当为 nil 时使用 os.OpenFile;测试可以 mock.
	OpenTarget func(path string) (io.WriteCloser, error)
}

// Result 是 pipeline 执行结果.
type Result struct {
	BytesWritten       int64
	CompressedSHA256   string
	RawSHA256          string
	ExpectedCompressed string
	ExpectedRaw        string
	CompressedMismatch bool
	RawMismatch        bool
	Duration           time.Duration
}

// Run 执行:HTTP 流式下载 -> SHA256(compressed) -> decompress -> SHA256(raw)
// -> 写入目标磁盘 -> fsync.可被 ctx 取消.
func (p *Pipeline) Run(ctx context.Context) (*Result, error) {
	if p.Tracker == nil {
		p.Tracker = progress.NewTracker()
	}
	if p.HTTPClient == nil {
		p.HTTPClient = &http.Client{Timeout: 0}
	}

	startedAt := time.Now()
	res := &Result{
		ExpectedCompressed: p.Image.SHA256Compressed,
		ExpectedRaw:        p.Image.SHA256Raw,
	}

	p.Tracker.Start(p.Image.RawSize)
	p.Tracker.SetStage(string(StateDownloading), "正在请求镜像")

	req, err := http.NewRequestWithContext(ctx, "GET", p.ImageURL, nil)
	if err != nil {
		return res, fmt.Errorf("构造请求失败: %w", err)
	}
	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return res, fmt.Errorf("下载镜像失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("下载镜像 HTTP %d", resp.StatusCode)
	}

	compressedHash := sha256.New()
	// counter reader 统计下载字节
	dlReader := &counterReader{
		r: io.TeeReader(resp.Body, compressedHash),
		onRead: func(n int) {
			p.Tracker.AddDownloaded(int64(n))
		},
		ctx: ctx,
	}

	decompressed, closer, err := decompressByFormat(dlReader, p.Image.Format)
	if err != nil {
		return res, err
	}
	if closer != nil {
		defer closer.Close()
	}

	rawHash := sha256.New()
	rawReader := io.TeeReader(decompressed, rawHash)

	openFn := p.OpenTarget
	if openFn == nil {
		openFn = func(path string) (io.WriteCloser, error) {
			return os.OpenFile(path, os.O_RDWR|os.O_SYNC, 0)
		}
	}

	p.Tracker.SetStage(string(StateWriting), "正在写入目标磁盘")
	w, err := openFn(p.TargetDevice)
	if err != nil {
		return res, fmt.Errorf("打开目标磁盘 %s 失败: %w", p.TargetDevice, err)
	}
	defer w.Close()

	written, err := copyWithProgress(ctx, w, rawReader, p.Tracker, rawHash)
	if err != nil {
		return res, fmt.Errorf("写盘失败: %w", err)
	}
	res.BytesWritten = written

	// fsync
	p.Tracker.SetStage(string(StateSyncing), "正在 fsync")
	if syncer, ok := w.(interface{ Sync() error }); ok {
		_ = syncer.Sync()
	}
	if err := w.Close(); err != nil {
		return res, fmt.Errorf("关闭目标磁盘失败: %w", err)
	}

	res.CompressedSHA256 = hex.EncodeToString(compressedHash.Sum(nil))
	res.RawSHA256 = hex.EncodeToString(rawHash.Sum(nil))
	if res.ExpectedCompressed != "" && !strings.EqualFold(res.ExpectedCompressed, res.CompressedSHA256) {
		res.CompressedMismatch = true
	}
	if res.ExpectedRaw != "" && !strings.EqualFold(res.ExpectedRaw, res.RawSHA256) {
		res.RawMismatch = true
	}
	res.Duration = time.Since(startedAt)

	if res.CompressedMismatch {
		return res, fmt.Errorf("压缩文件 sha256 不一致: got=%s want=%s",
			res.CompressedSHA256, res.ExpectedCompressed)
	}
	if res.RawMismatch {
		return res, fmt.Errorf("RAW 数据 sha256 不一致: got=%s want=%s",
			res.RawSHA256, res.ExpectedRaw)
	}
	return res, nil
}

// counterReader 包装 io.Reader 并在每次读取时回调字节数 + 检查 ctx.
type counterReader struct {
	r      io.Reader
	onRead func(int)
	ctx    context.Context
}

func (c *counterReader) Read(p []byte) (int, error) {
	select {
	case <-c.ctx.Done():
		return 0, c.ctx.Err()
	default:
	}
	n, err := c.r.Read(p)
	if n > 0 && c.onRead != nil {
		c.onRead(n)
	}
	return n, err
}

func decompressByFormat(src io.Reader, format string) (io.Reader, io.Closer, error) {
	f := strings.ToLower(strings.TrimSpace(format))
	switch f {
	case "raw", "img":
		return src, nil, nil
	case "raw.gz", "img.gz":
		gr, err := gzip.NewReader(src)
		if err != nil {
			return nil, nil, fmt.Errorf("gzip 解压初始化失败: %w", err)
		}
		return gr, gr, nil
	case "raw.xz", "img.xz":
		return startSubprocess(src, "xz", "-d", "--stdout")
	case "raw.zst", "img.zst":
		return startSubprocess(src, "zstd", "-d", "--stdout")
	}
	return nil, nil, fmt.Errorf("不支持的镜像格式: %s", format)
}

// startSubprocess 用结构化参数调用解压外部程序.
// 关键:不使用 sh -c,stdin 走 pipe,stderr 收集,返回 stdout reader.
func startSubprocess(src io.Reader, name string, args ...string) (io.Reader, io.Closer, error) {
	cmd := exec.Command(name, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("启动 %s 失败: %w", name, err)
	}
	// 单独 goroutine 把压缩数据喂进去
	go func() {
		_, _ = io.Copy(stdin, src)
		_ = stdin.Close()
	}()
	closer := &subprocessCloser{cmd: cmd, out: stdout, stderr: stderr, name: name}
	return stdout, closer, nil
}

type subprocessCloser struct {
	cmd    *exec.Cmd
	out    io.Closer
	stderr *strings.Builder
	name   string
}

func (s *subprocessCloser) Close() error {
	_ = s.out.Close()
	if err := s.cmd.Wait(); err != nil {
		return fmt.Errorf("%s 异常退出: %v: %s", s.name, err, s.stderr.String())
	}
	return nil
}

// copyWithProgress 复制 src->dst,并向 tracker 报告字节数;
// 写完每个 chunk 检查 ctx 是否被取消.
func copyWithProgress(ctx context.Context, dst io.Writer, src io.Reader,
	tracker *progress.Tracker, rawHash hash.Hash) (int64, error) {
	_ = rawHash // hash 已经被 io.TeeReader 喂入;这里只是为了显式提示生命周期
	buf := make([]byte, 4*1024*1024)
	var written int64
	for {
		select {
		case <-ctx.Done():
			return written, ctx.Err()
		default:
		}
		n, err := src.Read(buf)
		if n > 0 {
			w, werr := dst.Write(buf[:n])
			if werr != nil {
				return written, werr
			}
			if w != n {
				return written, io.ErrShortWrite
			}
			written += int64(n)
			tracker.AddWritten(int64(n))
		}
		if err == io.EOF {
			return written, nil
		}
		if err != nil {
			return written, err
		}
	}
}
