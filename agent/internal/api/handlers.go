package api

import (
	"net/http"
	"time"

	"github.com/anomalyco/bootseed/agent/internal/deploy"
	"github.com/anomalyco/bootseed/agent/internal/disks"
	"github.com/anomalyco/bootseed/agent/internal/drivers"
	"github.com/anomalyco/bootseed/agent/internal/hardware"
	"github.com/anomalyco/bootseed/agent/internal/images"
	"github.com/anomalyco/bootseed/agent/internal/system"
)

// GET /api/health
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   nowRFC3339(),
	})
}

// GET /api/context
func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	b := s.boot
	task, _ := s.manager.Snapshot()
	now := time.Now()
	netinfo := system.CollectNodeNet()
	mem := system.ReadMem()
	netStatus := system.ReadNetStatus()

	// Alpine 版本:优先内核参数,回退读取 /etc/alpine-release.
	alpineVer := b.AlpineVersion
	if alpineVer == "" {
		alpineVer = system.AlpineRelease()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"node_ip":              netinfo.IP,
		"node_iface":           netinfo.Interface,
		"node_netmask":         netinfo.Netmask,
		"node_gateway":         netinfo.Gateway,
		"node_dns":             netinfo.DNS,
		"hostname":             system.Hostname(),
		"node_architecture":    b.NodeArchitecture.String(),
		"runtime_architecture": b.RuntimeArchitecture.String(),
		"uname_architecture":   b.UnameArchitecture.String(),
		"boot_mode":            string(b.BootMode),
		"origin":               firstNonEmpty(b.Origin, "pxe"),
		"network_mode":         firstNonEmpty(netStatus.Mode, "dhcp"),
		"network_status":       firstNonEmpty(netStatus.Status, "ok"),
		"network_error":        netStatus.Message,
		"node_mac":             b.NodeMAC,
		"node_uuid":            b.NodeUUID,
		"deploy_server":        b.DeployServer,
		"alpine_version":       alpineVer,
		"kernel_version":       b.KernelVersion,
		"agent_version":        b.AgentVersion,
		"task_state":           string(task.State),
		"current_time":         now.Format(time.RFC3339),
		"boot_time":            system.BootTime(now).Format(time.RFC3339),
		"uptime_seconds":       int64(system.UptimeSeconds()),
		"mem_total_bytes":      mem.TotalBytes,
		"mem_available_bytes":  mem.AvailableBytes,
	})
}

// GET /api/hardware
func (s *Server) handleHardware(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, hardware.Collect())
}

// GET /api/drivers
func (s *Server) handleDrivers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, drivers.Collect())
}

// imageView 是返回给前端的镜像视图,附带兼容性标记.
type imageView struct {
	images.Image
	Compatible bool   `json:"compatible"`
	Reason     string `json:"incompatible_reason,omitempty"`
}

// GET /api/images
func (s *Server) handleImages(w http.ResponseWriter, r *http.Request) {
	all := s.catalog.All()
	arch := s.boot.NodeArchitecture
	mode := s.boot.BootMode
	out := make([]imageView, 0, len(all))
	for _, img := range all {
		v := imageView{Image: img, Compatible: images.IsCompatible(img, arch, mode)}
		if !v.Compatible {
			if img.Architecture != arch.String() {
				v.Reason = "架构不兼容:镜像 " + img.Architecture + " != 节点 " + arch.String()
			} else {
				v.Reason = "固件模式不兼容(节点 " + string(mode) + ")"
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"node_architecture": arch.String(),
		"boot_mode":         string(mode),
		"source":            s.catalog.Source(),
		"images":            out,
	})
}

// POST /api/images/reload -- 从服务端清单重新加载.
func (s *Server) handleImagesReload(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if err := s.catalog.LoadFromHTTP(s.boot.DeployServer); err != nil {
		writeError(w, http.StatusBadGateway, "重新加载镜像清单失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reloaded": true,
		"count":    len(s.catalog.All()),
		"source":   s.catalog.Source(),
	})
}

// GET /api/disks
func (s *Server) handleDisks(w http.ResponseWriter, r *http.Request) {
	list, err := disks.Enumerate(disks.EnumerateOptions{
		AllowMultipathTarget:  s.cfg.AllowMultipathTarget,
		AllowUnstableDiskName: s.cfg.AllowUnstableDiskName,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "枚举磁盘失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"lsblk_available": disks.IsLsblkAvailable(),
		"disks":           list,
	})
}

// GET /api/deploy/status
func (s *Server) handleDeployStatus(w http.ResponseWriter, r *http.Request) {
	task, ok := s.manager.Snapshot()
	resp := map[string]any{"active": ok, "task": task}
	if t := s.currentTracker(); t != nil {
		resp["progress"] = t.Snapshot()
	}
	s.mu.RLock()
	if s.lastResult != nil {
		resp["last_result"] = s.lastResult
	}
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, resp)
}

// POST /api/deploy/cancel
func (s *Server) handleDeployCancel(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if s.manager.Cancel() {
		writeJSON(w, http.StatusOK, map[string]any{
			"cancelled": true,
			"warning":   "目标盘可能处于不完整状态,无法启动",
		})
		return
	}
	writeError(w, http.StatusConflict, "当前没有可取消的任务")
}

// POST /api/reboot
func (s *Server) handleReboot(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if s.manager.IsRunning() {
		writeError(w, http.StatusConflict, "部署运行中,禁止重启")
		return
	}
	if err := system.Reboot(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "重启失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rebooting": true})
}

// POST /api/poweroff
func (s *Server) handlePoweroff(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if s.manager.IsRunning() {
		writeError(w, http.StatusConflict, "部署运行中,禁止关机")
		return
	}
	if err := system.Poweroff(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "关机失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"poweringOff": true})
}

// 引用 deploy 包以确保类型可见(在 deploy.go 中使用).
var _ = deploy.StateIdle

func firstNonEmpty(parts ...string) string {
	for _, part := range parts {
		if part != "" {
			return part
		}
	}
	return ""
}
