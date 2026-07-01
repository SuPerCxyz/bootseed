package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/anomalyco/bootseed/agent/internal/deploy"
	"github.com/anomalyco/bootseed/agent/internal/disks"
	"github.com/anomalyco/bootseed/agent/internal/images"
	"github.com/anomalyco/bootseed/agent/internal/progress"
	"github.com/anomalyco/bootseed/agent/internal/system"
)

// writePath 选择写盘使用的设备路径:优先稳定路径,否则内核名.
func writePath(d disks.Disk) string {
	if d.StablePath != "" {
		return d.StablePath
	}
	return d.Path
}

// deployRequest 是 POST /api/deploy 的请求体.
// 注意:除 image_id / target_disk / confirmation / verify_raw / auto_reboot
// 之外的字段(架构,格式,大小,校验和)一律忽略,全部以服务端清单为准.
type deployRequest struct {
	ImageID      string `json:"image_id"`
	ImageServer  string `json:"image_server"`
	TargetDisk   string `json:"target_disk"`
	Confirmation string `json:"confirmation"`
	VerifyRaw    bool   `json:"verify_raw"`
	AutoReboot   bool   `json:"auto_reboot"`
}

// POST /api/deploy
func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req deployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}

	// 0. 架构自检:启动参数架构与运行架构不一致时拒绝部署
	if s.archError != nil {
		writeError(w, http.StatusBadRequest, "架构自检失败,拒绝部署: "+s.archError.Error())
		return
	}

	// 1. 二次确认字符串
	if req.Confirmation != "ERASE" {
		writeError(w, http.StatusBadRequest, "确认字符串必须为 ERASE")
		return
	}

	// 2. 决定镜像服务器(默认使用启动参数中的 deploy_server)
	imageServer := s.boot.DeployServer
	if req.ImageServer != "" && req.ImageServer != imageServer {
		if !s.cfg.AllowCustomImageServer {
			writeError(w, http.StatusForbidden, "不允许自定义镜像服务器")
			return
		}
		if err := s.cfg.ValidateImageURL(req.ImageServer); err != nil {
			writeError(w, http.StatusBadRequest, "镜像服务器 URL 非法: "+err.Error())
			return
		}
		imageServer = req.ImageServer
	}

	// 3. 重新加载服务端清单(权威来源),不信任浏览器提交的元数据
	if err := s.catalog.LoadFromHTTP(imageServer); err != nil {
		writeError(w, http.StatusBadGateway, "加载镜像清单失败: "+err.Error())
		return
	}
	img, ok := s.catalog.Get(req.ImageID)
	if !ok {
		writeError(w, http.StatusNotFound, "镜像不存在: "+req.ImageID)
		return
	}

	// 4. 后端强制架构校验
	if img.Architecture != s.boot.NodeArchitecture.String() {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("镜像架构 %s 与当前节点架构 %s 不兼容",
				img.Architecture, s.boot.NodeArchitecture.String()))
		return
	}

	// 5. 固件模式兼容
	if !images.IsCompatible(img, s.boot.NodeArchitecture, s.boot.BootMode) {
		writeError(w, http.StatusBadRequest, "镜像与节点固件模式不兼容")
		return
	}

	// 6. 校验镜像格式
	if !images.IsSupportedFormat(img.Format) {
		writeError(w, http.StatusBadRequest, "不支持的镜像格式: "+img.Format)
		return
	}

	// 7. 解析并校验目标磁盘(TOCTOU:部署前再次枚举)
	disk, err := s.resolveTargetDisk(req.TargetDisk)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 8. 容量校验
	if img.RawSize > 0 && disk.Size < img.RawSize {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("目标磁盘容量不足: 磁盘 %d < 镜像 %d", disk.Size, img.RawSize))
		return
	}

	// 9. 占用部署锁(并发返回 409)
	task := &deploy.Task{ImageID: img.ID, Target: writePath(disk)}
	ctx, err := s.manager.Acquire(backgroundContext(), task)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	tracker := progress.NewTracker()
	s.mu.Lock()
	s.tracker = tracker
	s.lastResult = nil
	s.mu.Unlock()

	autoReboot := s.autoReboot || req.AutoReboot
	imageURL := images.ResolveURL(imageServer, img.Path)

	// 10. 后台执行部署
	go s.runDeploy(ctx, img, imageURL, writePath(disk), req.VerifyRaw, autoReboot, tracker)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":    true,
		"task":        task,
		"target_disk": writePath(disk),
		"image_url":   imageURL,
	})
}

// runDeploy 在后台运行 pipeline 并驱动状态机.
func (s *Server) runDeploy(ctx context.Context, img images.Image, url, target string,
	verifyRaw, autoReboot bool, tracker *progress.Tracker) {

	s.manager.SetState(deploy.StatePreparing, "")
	if s.report != nil {
		s.report.DeployStart(img.ID, target)
	}
	p := &deploy.Pipeline{
		Image:        img,
		ImageURL:     url,
		TargetDevice: target,
		VerifyRaw:    verifyRaw,
		Tracker:      tracker,
	}

	// 同步 manager.task.state 与 pipeline 阶段:tracker 仅在阶段变化时 broadcast,
	// 这里据此把任务状态更新为 downloading/writing/syncing,使 /api/deploy/status 准确.
	stopSync := make(chan struct{})
	go func() {
		ch := tracker.Subscribe()
		defer tracker.Unsubscribe(ch)
		for {
			select {
			case <-stopSync:
				return
			case snap, ok := <-ch:
				if !ok {
					return
				}
				if st := deploy.State(snap.Stage); st.IsRunning() {
					s.manager.SetState(st, "")
				}
			}
		}
	}()

	res, err := p.Run(ctx)
	close(stopSync)
	s.mu.Lock()
	s.lastResult = res
	s.mu.Unlock()
	var written int64
	if res != nil {
		written = res.BytesWritten
	}

	if err != nil {
		// 取消会使 ctx.Err() != nil;此时状态已由 Cancel 置为 cancelled
		if ctx.Err() != nil {
			tracker.Fail(fmt.Errorf("任务已取消"))
			s.manager.Finish(deploy.StateCancelled, "任务已取消")
			if s.report != nil {
				s.report.DeployEnd("cancelled", written, "任务已取消")
			}
			return
		}
		tracker.Fail(err)
		s.manager.Finish(deploy.StateFailed, err.Error())
		if s.report != nil {
			s.report.DeployEnd("failed", written, err.Error())
		}
		return
	}

	s.manager.SetState(deploy.StateCompleted, "")
	tracker.SetStage(string(deploy.StateCompleted), "部署完成")
	tracker.Broadcast()
	s.manager.Finish(deploy.StateCompleted, "")
	if s.report != nil {
		s.report.DeployEnd("completed", written, "")
	}

	if autoReboot {
		_ = system.Reboot(context.Background())
	}
}

// resolveTargetDisk 在当前磁盘列表中定位目标,并执行安全校验.
func (s *Server) resolveTargetDisk(target string) (disks.Disk, error) {
	if strings.TrimSpace(target) == "" {
		return disks.Disk{}, fmt.Errorf("未指定目标磁盘")
	}
	list, err := disks.Enumerate(disks.EnumerateOptions{
		AllowMultipathTarget:  s.cfg.AllowMultipathTarget,
		AllowUnstableDiskName: s.cfg.AllowUnstableDiskName,
	})
	if err != nil {
		return disks.Disk{}, fmt.Errorf("枚举磁盘失败: %w", err)
	}
	for _, d := range list {
		if d.StablePath == target || d.Path == target {
			if !d.Allowed {
				return disks.Disk{}, fmt.Errorf("目标磁盘不允许部署: %s", d.Reason)
			}
			return d, nil
		}
	}
	return disks.Disk{}, fmt.Errorf("未找到目标磁盘: %s", target)
}

// GET /api/deploy/events -- SSE 实时进度.
func (s *Server) handleDeployEvents(w http.ResponseWriter, r *http.Request) {
	tracker := s.currentTracker()
	if tracker == nil {
		writeError(w, http.StatusNotFound, "当前没有部署任务")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "服务器不支持流式响应")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")

	ch := tracker.Subscribe()
	defer tracker.Unsubscribe(ch)

	// 立即推送一帧当前状态
	sendSnapshot(w, flusher, tracker.Snapshot())

	// 写盘过程中字节进度不会触发 broadcast(只有阶段变化才推送),
	// 这里用定时器周期性推送当前快照,让网页进度条平滑更新.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			snap := tracker.Snapshot()
			sendSnapshot(w, flusher, snap)
			if isTerminalStage(snap.Stage) || snap.Error != "" {
				return
			}
		case snap, alive := <-ch:
			if !alive {
				return
			}
			sendSnapshot(w, flusher, snap)
			if isTerminalStage(snap.Stage) || snap.Error != "" {
				return
			}
		}
	}
}

func isTerminalStage(stage string) bool {
	return stage == string(deploy.StateCompleted) ||
		stage == string(deploy.StateFailed) ||
		stage == string(deploy.StateCancelled)
}

func sendSnapshot(w http.ResponseWriter, f http.Flusher, snap progress.Snapshot) {
	b, _ := json.Marshal(snap)
	fmt.Fprintf(w, "data: %s\n\n", b)
	f.Flush()
}

// firstNodeIP 返回第一个非回环 IPv4 地址.
func firstNodeIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		if v4 := ipnet.IP.To4(); v4 != nil {
			return v4.String()
		}
	}
	return ""
}
