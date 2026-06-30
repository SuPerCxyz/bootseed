// Package api 实现 bootseed-agent 的 HTTP API 与 SSE 进度推送。
//
// 安全约定（见 docs/IMPLEMENTATION.md §10）：
//   - 浏览器提交的 image_server / architecture / format / raw_size / checksum
//     一律不信任，必须从服务端清单重新获取。
//   - 镜像架构由后端在 POST /api/deploy 时强制再校验。
//   - 写盘目标必须解析到稳定路径（除非显式放开 ALLOW_UNSTABLE_DISK_NAME）。
package api

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/anomalyco/bootseed/agent/internal/bootcontext"
	"github.com/anomalyco/bootseed/agent/internal/config"
	"github.com/anomalyco/bootseed/agent/internal/deploy"
	"github.com/anomalyco/bootseed/agent/internal/images"
	"github.com/anomalyco/bootseed/agent/internal/progress"
	"github.com/anomalyco/bootseed/agent/internal/report"
)

// Server 聚合 Agent 运行所需的全部依赖。
type Server struct {
	cfg        *config.Config
	boot       *bootcontext.BootContext
	catalog    *images.Catalog
	manager    *deploy.Manager
	autoReboot bool
	archError  error // 启动参数架构与运行架构不一致时非 nil，禁止部署
	report     *report.Client

	mu         sync.RWMutex
	tracker    *progress.Tracker // 当前部署任务的进度跟踪器
	lastResult *deploy.Result
	webFS      fs.FS
}

// Options 构造 Server 的参数。
type Options struct {
	Config     *config.Config
	Boot       *bootcontext.BootContext
	Catalog    *images.Catalog
	AutoReboot bool
	WebFS      fs.FS
	ArchError  error
	Report     *report.Client
}

// New 构造 Server。
func New(opt Options) *Server {
	return &Server{
		cfg:        opt.Config,
		boot:       opt.Boot,
		catalog:    opt.Catalog,
		manager:    deploy.NewManager(),
		autoReboot: opt.AutoReboot,
		archError:  opt.ArchError,
		report:     opt.Report,
		webFS:      opt.WebFS,
	}
}

// Handler 返回组装好的 http.Handler。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/context", s.handleContext)
	mux.HandleFunc("/api/hardware", s.handleHardware)
	mux.HandleFunc("/api/drivers", s.handleDrivers)
	mux.HandleFunc("/api/images", s.handleImages)
	mux.HandleFunc("/api/images/reload", s.handleImagesReload)
	mux.HandleFunc("/api/disks", s.handleDisks)
	mux.HandleFunc("/api/deploy", s.handleDeploy)
	mux.HandleFunc("/api/deploy/status", s.handleDeployStatus)
	mux.HandleFunc("/api/deploy/events", s.handleDeployEvents)
	mux.HandleFunc("/api/deploy/cancel", s.handleDeployCancel)
	mux.HandleFunc("/api/reboot", s.handleReboot)
	mux.HandleFunc("/api/poweroff", s.handlePoweroff)

	// 静态前端
	if s.webFS != nil {
		mux.Handle("/", http.FileServer(http.FS(s.webFS)))
	}
	return logMiddleware(mux)
}

// ---- 通用响应辅助 ----

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type apiError struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, apiError{Error: msg})
}

// requireMethod 校验 HTTP 方法，不匹配则写 405 并返回 false。
func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		writeError(w, http.StatusMethodNotAllowed, "方法不允许")
		return false
	}
	return true
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

// currentTracker 返回当前部署的进度跟踪器（可能为 nil）。
func (s *Server) currentTracker() *progress.Tracker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tracker
}

// freshContext 返回一个不受请求取消影响的背景 context，用于部署任务。
func backgroundContext() context.Context {
	return context.Background()
}

// nowRFC3339 提供统一时间格式。
func nowRFC3339() string { return time.Now().Format(time.RFC3339) }
