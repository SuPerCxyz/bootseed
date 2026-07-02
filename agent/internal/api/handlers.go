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
	resp := map[string]any{"active": ok, "running": task.State.IsRunning(), "task": task}
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
// 如有正在进行的部署,先取消(pipeline 会走 defer fsync,落盘已写数据),
// 等状态机置为 cancelled 后再执行 reboot——最大程度保护目标盘。
func (s *Server) handleReboot(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	s.ensureCancelledForShutdown()
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
	s.ensureCancelledForShutdown()
	if err := system.Poweroff(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "关机失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"poweringOff": true})
}

// ensureCancelledForShutdown 为即将到来的重启/关机做准备:
// 取消进行中的部署,等待 pipeline 走完 defer fsync 并上报 end;
// 无论 pipeline 是否已在 runDeploy 里报了 end,再显式上报一次(server 端幂等更新
// 最后一条 running 记录),避免节点 hard-reboot 后门户看到卡在 running/bytes=0。
func (s *Server) ensureCancelledForShutdown() {
	wasRunning := s.manager.IsRunning()
	if wasRunning {
		s.manager.Cancel()
		s.waitCancelled(3 * time.Second)
	}
	if wasRunning && s.report != nil {
		var written int64
		s.mu.RLock()
		if s.lastResult != nil {
			written = s.lastResult.BytesWritten
		} else if t := s.tracker; t != nil {
			written = t.Snapshot().WrittenBytes
		}
		s.mu.RUnlock()
		s.report.DeployEnd("cancelled", written, "节点重启/关机前取消")
	}
}

// waitCancelled 等待部署真正进入终态(pipeline 走完 defer fsync),避免直接重启造成缓冲丢失。
// 超时后仍继续 reboot/poweroff,兜底以用户意图为准。
func (s *Server) waitCancelled(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !s.manager.IsRunning() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
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
