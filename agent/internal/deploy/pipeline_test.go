package deploy

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anomalyco/bootseed/agent/internal/images"
	"github.com/anomalyco/bootseed/agent/internal/progress"
)

// fakeWriter 实现 io.WriteCloser + Sync 接口。
type fakeWriter struct {
	buf *bytes.Buffer
}

func (f *fakeWriter) Write(p []byte) (int, error) { return f.buf.Write(p) }
func (f *fakeWriter) Close() error                { return nil }
func (f *fakeWriter) Sync() error                 { return nil }

func TestPipeline_GzipRoundTrip(t *testing.T) {
	// 构造一段 raw 数据并 gzip
	raw := bytes.Repeat([]byte("ABCDEF\n"), 4096)
	rawHash := sha256.Sum256(raw)
	var compressed bytes.Buffer
	gw := gzip.NewWriter(&compressed)
	if _, err := gw.Write(raw); err != nil {
		t.Fatal(err)
	}
	gw.Close()
	compressedHash := sha256.Sum256(compressed.Bytes())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "")
		_, _ = io.Copy(w, bytes.NewReader(compressed.Bytes()))
	}))
	defer srv.Close()

	target := &fakeWriter{buf: &bytes.Buffer{}}
	p := &Pipeline{
		Image: images.Image{
			ID: "test", Architecture: "x86_64",
			Format:           "raw.gz",
			RawSize:          int64(len(raw)),
			SHA256Compressed: hex.EncodeToString(compressedHash[:]),
			SHA256Raw:        hex.EncodeToString(rawHash[:]),
		},
		ImageURL:     srv.URL + "/img.raw.gz",
		TargetDevice: "/dev/null",
		Tracker:      progress.NewTracker(),
		OpenTarget:   func(string) (io.WriteCloser, error) { return target, nil },
	}
	res, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("pipeline.Run: %v", err)
	}
	if res.BytesWritten != int64(len(raw)) {
		t.Errorf("BytesWritten = %d, want %d", res.BytesWritten, len(raw))
	}
	if !bytes.Equal(target.buf.Bytes(), raw) {
		t.Errorf("写入数据与原始 raw 不一致")
	}
	if res.CompressedMismatch || res.RawMismatch {
		t.Errorf("sha256 应该匹配: %+v", res)
	}
}

func TestPipeline_SHA256Mismatch(t *testing.T) {
	raw := []byte("hello world")
	var compressed bytes.Buffer
	gw := gzip.NewWriter(&compressed)
	gw.Write(raw)
	gw.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(w, bytes.NewReader(compressed.Bytes()))
	}))
	defer srv.Close()

	target := &fakeWriter{buf: &bytes.Buffer{}}
	p := &Pipeline{
		Image: images.Image{
			Format:    "raw.gz",
			RawSize:   int64(len(raw)),
			SHA256Raw: "deadbeef",
		},
		ImageURL:     srv.URL,
		TargetDevice: "/dev/null",
		Tracker:      progress.NewTracker(),
		OpenTarget:   func(string) (io.WriteCloser, error) { return target, nil },
	}
	_, err := p.Run(context.Background())
	if err == nil {
		t.Fatal("期望 SHA256 不一致报错")
	}
}

func TestPipeline_ContextCancel(t *testing.T) {
	// 一个永不结束的 reader 模拟超大下载
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		buf := make([]byte, 64*1024)
		for {
			if _, err := w.Write(buf); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer srv.Close()

	target := &fakeWriter{buf: &bytes.Buffer{}}
	p := &Pipeline{
		Image:        images.Image{Format: "raw", RawSize: 1 << 30},
		ImageURL:     srv.URL,
		TargetDevice: "/dev/null",
		Tracker:      progress.NewTracker(),
		OpenTarget:   func(string) (io.WriteCloser, error) { return target, nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// 给它一点时间开始下载再 cancel
		buf := make([]byte, 0, 4)
		_ = buf
		cancel()
	}()
	_, err := p.Run(ctx)
	if err == nil {
		t.Fatal("cancel 后应返回错误")
	}
}
