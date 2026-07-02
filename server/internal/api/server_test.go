package api

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anomalyco/bootseed/server/internal/model"
	"github.com/anomalyco/bootseed/server/internal/store"
)

func TestAlpineBuildAndIPXEFilesIncludeDetails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite := func(rel, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	mustWrite("http/alpine/x86_64/manifest.json", `{
		"kernel_version":"6.6.1",
		"alpine_version":"3.20.3",
		"build_time":"2026-06-30T12:00:00Z",
		"included_modules":["virtio_blk","virtio_net"],
		"included_firmware_packages":["linux-firmware-intel"],
		"included_runtime_packages":["curl","tcpdump"]
	}`)
	mustWrite("http/alpine/x86_64/vmlinuz", "ok")
	mustWrite("http/alpine/x86_64/initramfs-deploy", "ok")
	mustWrite("tftp/x86_64/undionly.kpxe", "ok")

	s := &Server{cfg: Config{DataRoot: root}, webFS: fs.FS(nil)}
	build := s.alpineBuild("x86_64")
	if build.Ready {
		t.Fatalf("expected not ready when modloop missing")
	}
	if len(build.MissingFiles) != 1 || build.MissingFiles[0] != "modloop" {
		t.Fatalf("unexpected missing files: %+v", build.MissingFiles)
	}
	if len(build.IncludedModules) != 2 || len(build.IncludedFirmware) != 1 || len(build.IncludedTools) != 2 {
		t.Fatalf("manifest lists missing: %+v", build)
	}

	files := s.ipxeFiles()
	if len(files) != 3 {
		t.Fatalf("unexpected ipxe file count: %d", len(files))
	}
	if !files[0].Exists || files[1].Exists || files[2].Exists {
		t.Fatalf("unexpected ipxe status: %+v", files)
	}
}

func TestNodeProxyDeployLifecycle(t *testing.T) {
	t.Parallel()

	agentCalls := make(chan struct {
		Method string
		Path   string
		Body   map[string]any
	}, 4)
	statusBody := `{"active":true,"running":true,"task":{"state":"writing"},"progress":{"stage":"writing","written_bytes":123}}`
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		agentCalls <- struct {
			Method string
			Path   string
			Body   map[string]any
		}{Method: r.Method, Path: r.URL.Path, Body: body}
		switch r.URL.Path {
		case "/api/context":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"origin":"bootseed-enter","network_mode":"static"}`))
		case "/api/deploy":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"accepted":true}`))
		case "/api/deploy/status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(statusBody))
		case "/api/deploy/cancel":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"cancelled":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer agent.Close()

	db := filepath.Join(t.TempDir(), "nodes.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now()
	if err := st.Register(model.Node{
		UUID:          "node-1",
		Hostname:      "bootseed",
		IP:            "172.16.50.120",
		MAC:           "52:54:00:93:70:39",
		Architecture:  "x86_64",
		BootMode:      "bios",
		AgentURL:      agent.URL,
		Origin:        "bootseed-enter",
		NetworkMode:   "static",
		NetworkStatus: "ok",
	}, now); err != nil {
		t.Fatalf("register node: %v", err)
	}

	srv := New(Config{
		Token:         "bootseed",
		OnlineTimeout: time.Minute,
		DataRoot:      t.TempDir(),
	}, st, fs.FS(nil))
	h := srv.Handler()

	t.Run("proxy context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-1/agent-context", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
		}
		if got := rec.Body.String(); got == "" || !bytes.Contains(rec.Body.Bytes(), []byte(`"network_mode":"static"`)) {
			t.Fatalf("unexpected body: %s", got)
		}
		call := <-agentCalls
		if call.Path != "/api/context" || call.Method != http.MethodGet {
			t.Fatalf("unexpected agent call: %+v", call)
		}
	})

	t.Run("proxy deploy status and cancel without auth", func(t *testing.T) {
		body := `{"image_id":"rocky98-x86_64","target_disk":"/dev/disk/by-id/virtio-ROCKY98TEST","confirmation":"ERASE","verify_raw":false}`
		req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-1/deploy", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected deploy status: %d body=%s", rec.Code, rec.Body.String())
		}
		call := <-agentCalls
		if call.Path != "/api/deploy" || call.Method != http.MethodPost {
			t.Fatalf("unexpected agent call: %+v", call)
		}
		if call.Body["confirmation"] != "ERASE" {
			t.Fatalf("request body not proxied: %+v", call.Body)
		}

		statusReq := httptest.NewRequest(http.MethodGet, "/api/nodes/node-1/deploy-status", nil)
		statusRec := httptest.NewRecorder()
		h.ServeHTTP(statusRec, statusReq)
		if statusRec.Code != http.StatusOK {
			t.Fatalf("unexpected status code: %d body=%s", statusRec.Code, statusRec.Body.String())
		}
		call = <-agentCalls
		if call.Path != "/api/deploy/status" || call.Method != http.MethodGet {
			t.Fatalf("unexpected status proxy call: %+v", call)
		}
		if !bytes.Contains(statusRec.Body.Bytes(), []byte(`"stage":"writing"`)) {
			t.Fatalf("unexpected status body: %s", statusRec.Body.String())
		}

		statusBody = `{"active":true,"running":false,"task":{"state":"completed"},"progress":{"stage":"completed","written_bytes":5368709120,"total_bytes":5368709120}}`
		completedReq := httptest.NewRequest(http.MethodGet, "/api/nodes/node-1/deploy-status", nil)
		completedRec := httptest.NewRecorder()
		h.ServeHTTP(completedRec, completedReq)
		if completedRec.Code != http.StatusOK {
			t.Fatalf("unexpected completed status code: %d body=%s", completedRec.Code, completedRec.Body.String())
		}
		call = <-agentCalls
		if call.Path != "/api/deploy/status" || call.Method != http.MethodGet {
			t.Fatalf("unexpected completed status proxy call: %+v", call)
		}
		if !bytes.Contains(completedRec.Body.Bytes(), []byte(`"active":true`)) ||
			!bytes.Contains(completedRec.Body.Bytes(), []byte(`"state":"completed"`)) ||
			!bytes.Contains(completedRec.Body.Bytes(), []byte(`"running":false`)) {
			t.Fatalf("unexpected completed status body: %s", completedRec.Body.String())
		}

		cancelReq := httptest.NewRequest(http.MethodPost, "/api/nodes/node-1/deploy-cancel", nil)
		cancelRec := httptest.NewRecorder()
		h.ServeHTTP(cancelRec, cancelReq)
		if cancelRec.Code != http.StatusOK {
			t.Fatalf("unexpected cancel status: %d body=%s", cancelRec.Code, cancelRec.Body.String())
		}
		call = <-agentCalls
		if call.Path != "/api/deploy/cancel" || call.Method != http.MethodPost {
			t.Fatalf("unexpected cancel proxy call: %+v", call)
		}
	})
}

func TestRegisterPXENodeDropsHostnameAndDeleteNode(t *testing.T) {
	t.Parallel()

	db := filepath.Join(t.TempDir(), "nodes.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	srv := New(Config{
		Token:         "bootseed",
		OnlineTimeout: time.Minute,
		DataRoot:      t.TempDir(),
	}, st, fs.FS(nil))
	h := srv.Handler()

	registerBody := `{
		"uuid":"node-pxe",
		"hostname":"rocky98-x86_64",
		"ip":"192.168.100.10",
		"mac":"52:54:00:11:22:33",
		"arch":"x86_64",
		"boot_mode":"bios",
		"origin":"pxe"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/register", bytes.NewBufferString(registerBody))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("register failed: %d body=%s", rec.Code, rec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list failed: %d body=%s", listRec.Code, listRec.Body.String())
	}
	var listResp struct {
		Nodes []model.NodeView `json:"nodes"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Nodes) != 1 {
		t.Fatalf("unexpected node count: %d", len(listResp.Nodes))
	}
	if listResp.Nodes[0].Hostname != "" {
		t.Fatalf("expected empty hostname for pxe node, got %q", listResp.Nodes[0].Hostname)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/nodes/node-pxe", nil)
	delReq.Header.Set("Authorization", "Bearer bootseed")
	delRec := httptest.NewRecorder()
	h.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete failed: %d body=%s", delRec.Code, delRec.Body.String())
	}

	listRec = httptest.NewRecorder()
	h.ServeHTTP(listRec, listReq)
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list after delete: %v", err)
	}
	if len(listResp.Nodes) != 0 {
		t.Fatalf("expected empty node list after delete, got %d", len(listResp.Nodes))
	}
}

func TestBootseedEnterRegisterRequiresSecret(t *testing.T) {
	t.Parallel()

	db := filepath.Join(t.TempDir(), "nodes.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	srv := New(Config{
		Token:         "bootseed",
		EnterSecret:   "enter-123",
		OnlineTimeout: time.Minute,
		DataRoot:      t.TempDir(),
	}, st, fs.FS(nil))
	h := srv.Handler()

	bodyNoSecret := `{
		"uuid":"node-enter-1",
		"ip":"192.168.100.30",
		"mac":"52:54:00:aa:bb:cc",
		"arch":"x86_64",
		"boot_mode":"bios",
		"origin":"bootseed-enter"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/register", bytes.NewBufferString(bodyNoSecret))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without secret, got %d body=%s", rec.Code, rec.Body.String())
	}

	bodyBadSecret := `{
		"uuid":"node-enter-1",
		"ip":"192.168.100.30",
		"mac":"52:54:00:aa:bb:cc",
		"arch":"x86_64",
		"boot_mode":"bios",
		"origin":"bootseed-enter",
		"enter_secret":"bad"
	}`
	req = httptest.NewRequest(http.MethodPost, "/api/nodes/register", bytes.NewBufferString(bodyBadSecret))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 with wrong secret, got %d body=%s", rec.Code, rec.Body.String())
	}

	bodyOK := `{
		"uuid":"node-enter-1",
		"hostname":"node-a",
		"ip":"192.168.100.30",
		"mac":"52:54:00:aa:bb:cc",
		"arch":"x86_64",
		"boot_mode":"bios",
		"origin":"bootseed-enter",
		"enter_secret":"enter-123"
	}`
	req = httptest.NewRequest(http.MethodPost, "/api/nodes/register", bytes.NewBufferString(bodyOK))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with correct secret, got %d body=%s", rec.Code, rec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, listReq)
	var listResp struct {
		Nodes []model.NodeView `json:"nodes"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Nodes) != 1 {
		t.Fatalf("expected 1 node after successful register, got %d", len(listResp.Nodes))
	}
	if listResp.Nodes[0].Hostname != "node-a" {
		t.Fatalf("expected hostname preserved for bootseed-enter, got %q", listResp.Nodes[0].Hostname)
	}
}
