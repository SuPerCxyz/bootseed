package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/anomalyco/bootseed/agent/internal/bootcontext"
	"github.com/anomalyco/bootseed/agent/internal/config"
	"github.com/anomalyco/bootseed/agent/internal/images"
	"github.com/anomalyco/bootseed/agent/internal/system"
)

// testWebFS 返回一个最小的内嵌前端文件系统用于路由测试.
func testWebFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html": {Data: []byte("<title>BootSeed</title>")},
	}
}

// fakeIndex 返回一个只含 aarch64 镜像的清单.
const fakeIndex = `{"schema_version":1,"images":[{"id":"rocky-9-aarch64-uefi",` +
	`"name":"Rocky 9 ARM64","os":"rocky","version":"9","architecture":"aarch64",` +
	`"firmware":["uefi"],"path":"/images/rocky-9-aarch64-uefi.raw.zst","format":"raw.zst",` +
	`"compressed_size":100,"raw_size":1000}]}`

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	// 模拟镜像服务端,提供 /images/index.json
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/images/index.json") {
			_, _ = w.Write([]byte(fakeIndex))
			return
		}
		http.NotFound(w, r)
	}))

	boot := &bootcontext.BootContext{
		NodeArchitecture:    system.ArchX8664, // 节点是 x86_64
		RuntimeArchitecture: system.ArchX8664,
		UnameArchitecture:   system.ArchX8664,
		BootMode:            system.BootModeUEFI,
		DeployServer:        upstream.URL,
		AgentVersion:        "test",
	}
	cat := images.NewCatalog()
	srv := New(Options{
		Config:  config.FromEnv(),
		Boot:    boot,
		Catalog: cat,
	})
	return srv, upstream
}

// TestDeployRejectsNonErase 验证缺少 ERASE 确认时返回 400.
func TestDeployRejectsNonErase(t *testing.T) {
	srv, up := newTestServer(t)
	defer up.Close()
	body := `{"image_id":"rocky-9-aarch64-uefi","target_disk":"/dev/disk/by-id/x","confirmation":"yes"}`
	r := httptest.NewRequest(http.MethodPost, "/api/deploy", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleDeploy(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400,实际 %d (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ERASE") {
		t.Errorf("错误信息应提及 ERASE,实际: %s", w.Body.String())
	}
}

// TestDeployRejectsArchMismatch 验证 x86_64 节点提交 aarch64 镜像返回 400.
func TestDeployRejectsArchMismatch(t *testing.T) {
	srv, up := newTestServer(t)
	defer up.Close()
	body := `{"image_id":"rocky-9-aarch64-uefi","target_disk":"/dev/disk/by-id/x","confirmation":"ERASE"}`
	r := httptest.NewRequest(http.MethodPost, "/api/deploy", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleDeploy(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400,实际 %d (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "不兼容") {
		t.Errorf("错误信息应提及架构不兼容,实际: %s", w.Body.String())
	}
}

// TestDeployUnknownImage 验证未知镜像 ID 返回 404.
func TestDeployUnknownImage(t *testing.T) {
	srv, up := newTestServer(t)
	defer up.Close()
	body := `{"image_id":"does-not-exist","target_disk":"/dev/x","confirmation":"ERASE"}`
	r := httptest.NewRequest(http.MethodPost, "/api/deploy", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleDeploy(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("期望 404,实际 %d (%s)", w.Code, w.Body.String())
	}
}

// TestHealth 验证健康检查端点.
func TestHealth(t *testing.T) {
	srv, up := newTestServer(t)
	defer up.Close()
	r := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	srv.handleHealth(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200,实际 %d", w.Code)
	}
}

// TestHandlerRouting 通过完整的 Handler() 路由验证 mux 与内嵌前端装配正确.
func TestHandlerRouting(t *testing.T) {
	srv, up := newTestServer(t)
	defer up.Close()
	// 注入内嵌前端文件系统
	srv.webFS = testWebFS()
	h := srv.Handler()

	// /api/context 应返回 JSON 且含节点架构
	r := httptest.NewRequest(http.MethodGet, "/api/context", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/context 期望 200,实际 %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type 应为 JSON,实际 %s", ct)
	}
	if !strings.Contains(w.Body.String(), "x86_64") {
		t.Errorf("/api/context 应包含节点架构,实际: %s", w.Body.String())
	}

	// 静态前端:/ 应返回内嵌 index.html
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("/ 期望 200,实际 %d", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "BootSeed") {
		t.Errorf("/ 应返回内嵌前端,实际: %s", w2.Body.String())
	}
}
