// Package api 实现 bootseed-server 门户的 HTTP API 与鉴权。
package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anomalyco/bootseed/server/internal/imagesvc"
	"github.com/anomalyco/bootseed/server/internal/model"
	"github.com/anomalyco/bootseed/server/internal/store"
)

// Config 来自环境变量。
type Config struct {
	Token         string        // PORTAL_TOKEN，空=管理操作免鉴权
	OnlineTimeout time.Duration // NODE_ONLINE_TIMEOUT 秒
	DataRoot      string        // 挂载的数据根（含 http/ 与 tftp/）
	PXEServerIP   string
	HTTPPort      string
	PXEInterface  string
	PXESubnet     string
	Architectures []string
	AlpineVersion string
	AgentVersion  string
	IPXERef       string
}

// Server 聚合门户依赖。
type Server struct {
	cfg    Config
	store  *store.Store
	images *imagesvc.Service
	webFS  fs.FS
}

// New 构造门户 Server。
func New(cfg Config, st *store.Store, webFS fs.FS) *Server {
	imgDir := filepath.Join(cfg.DataRoot, "http", "images")
	return &Server{
		cfg:    cfg,
		store:  st,
		images: imagesvc.New(filepath.Join(imgDir, "index.json"), imgDir),
		webFS:  webFS,
	}
}

// Handler 组装路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/server-info", s.handleServerInfo)

	// 节点登记（agent 上报；默认免鉴权，内部网段使用）
	mux.HandleFunc("/api/nodes/register", s.handleNodeRegister)
	mux.HandleFunc("/api/nodes/heartbeat", s.handleNodeHeartbeat)
	mux.HandleFunc("/api/nodes/deploy", s.handleNodeDeploy)
	mux.HandleFunc("/api/nodes", s.handleNodeList) // GET 列表

	// 镜像
	mux.HandleFunc("/api/images", s.handleImages)             // GET 列表 / POST 添加(需鉴权)
	mux.HandleFunc("/api/images/", s.handleImageItem)         // DELETE /api/images/{id}(需鉴权)
	mux.HandleFunc("/api/images/jobs/", s.handleImageJob)     // GET 任务进度
	mux.HandleFunc("/api/images/upload", s.handleImageUpload) // POST 上传(需鉴权)

	// 静态下载目录（原 Nginx 职责）：/boot /alpine /images → /data/http/...
	// 不做 StripPrefix：请求 /boot/boot.ipxe 直接解析为 httpRoot + /boot/boot.ipxe。
	// http.FileServer 原生支持 Range，且 WriteTimeout=0 可避免慢客户端大镜像被截断。
	httpRoot := filepath.Join(s.cfg.DataRoot, "http")
	fs := http.FileServer(http.Dir(httpRoot))
	mux.Handle("/boot/", fs)
	mux.Handle("/alpine/", fs)
	mux.Handle("/images/", fs)

	if s.webFS != nil {
		mux.Handle("/", http.FileServer(http.FS(s.webFS)))
	}
	return mux
}

// ---- 通用 ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// authOK 校验管理口令。Token 为空则放行（免鉴权模式）。
func (s *Server) authOK(r *http.Request) bool {
	if s.cfg.Token == "" {
		return true
	}
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") && strings.TrimPrefix(h, "Bearer ") == s.cfg.Token {
		return true
	}
	if r.Header.Get("X-Portal-Token") == s.cfg.Token {
		return true
	}
	return false
}

func (s *Server) requireAuth(w http.ResponseWriter, r *http.Request) bool {
	if !s.authOK(r) {
		writeErr(w, http.StatusUnauthorized, "需要管理口令")
		return false
	}
	return true
}

// GET /api/server-info
func (s *Server) handleServerInfo(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	info := model.ServerInfo{
		PXEServerIP:   s.cfg.PXEServerIP,
		HTTPPort:      s.cfg.HTTPPort,
		PXEInterface:  s.cfg.PXEInterface,
		PXESubnet:     s.cfg.PXESubnet,
		Architectures: s.cfg.Architectures,
		AlpineVersion: s.cfg.AlpineVersion,
		AgentVersion:  s.cfg.AgentVersion,
		IPXERef:       s.cfg.IPXERef,
		IPXEFiles: map[string]bool{
			"x86/undionly.kpxe":   s.exists("tftp", "x86", "undionly.kpxe"),
			"x86_64/snponly.efi":  s.exists("tftp", "x86_64", "snponly.efi"),
			"aarch64/snponly.efi": s.exists("tftp", "aarch64", "snponly.efi"),
		},
		AlpineBuilds: map[string]model.AlpineBuild{
			"x86_64":  s.alpineBuild("x86_64"),
			"aarch64": s.alpineBuild("aarch64"),
		},
		Healthy: true,
		Time:    now.Format(time.RFC3339),
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) exists(parts ...string) bool {
	p := filepath.Join(append([]string{s.cfg.DataRoot}, parts...)...)
	_, err := os.Stat(p)
	return err == nil
}

// alpineBuild 读取 alpine/<arch>/manifest.json 给出结构化构建信息。
func (s *Server) alpineBuild(arch string) model.AlpineBuild {
	mp := filepath.Join(s.cfg.DataRoot, "http", "alpine", arch, "manifest.json")
	b, err := os.ReadFile(mp)
	if err != nil {
		return model.AlpineBuild{Note: "未构建"}
	}
	var m struct {
		KernelVersion string   `json:"kernel_version"`
		AlpineVersion string   `json:"alpine_version"`
		BuildTime     string   `json:"build_time"`
		Modules       []string `json:"included_modules"`
		Firmware      []string `json:"included_firmware_packages"`
	}
	if json.Unmarshal(b, &m) != nil {
		return model.AlpineBuild{Note: "manifest 解析失败"}
	}
	ready := s.exists("http", "alpine", arch, "vmlinuz") &&
		s.exists("http", "alpine", arch, "initramfs-deploy") &&
		s.exists("http", "alpine", arch, "modloop")
	bld := model.AlpineBuild{
		Ready: ready, KernelVersion: m.KernelVersion, AlpineVersion: m.AlpineVersion,
		Modules: len(m.Modules), Firmware: len(m.Firmware), BuildTime: m.BuildTime,
	}
	if !ready {
		bld.Note = "文件缺失"
	}
	return bld
}
