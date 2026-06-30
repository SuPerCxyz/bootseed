package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/anomalyco/bootseed/server/internal/model"
)

// POST /api/nodes/register
func (s *Server) handleNodeRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}
	var in model.Node
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	in.UUID = strings.TrimSpace(in.UUID)
	if in.UUID == "" {
		in.UUID = strings.TrimSpace(in.MAC) // UUID 缺失时用 MAC 兜底
	}
	if in.UUID == "" {
		writeErr(w, http.StatusBadRequest, "缺少 uuid/mac")
		return
	}
	if err := s.store.Register(in, time.Now()); err != nil {
		writeErr(w, http.StatusInternalServerError, "注册失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/nodes/heartbeat
func (s *Server) handleNodeHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}
	var in struct {
		UUID string `json:"uuid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.UUID == "" {
		writeErr(w, http.StatusBadRequest, "缺少 uuid")
		return
	}
	_ = s.store.Heartbeat(in.UUID, time.Now())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/nodes/deploy —— 部署开始/结束上报
func (s *Server) handleNodeDeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}
	var in struct {
		UUID         string `json:"uuid"`
		Event        string `json:"event"` // start|end
		ImageID      string `json:"image_id"`
		TargetDisk   string `json:"target_disk"`
		Result       string `json:"result"`
		BytesWritten int64  `json:"bytes_written"`
		Error        string `json:"error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.UUID == "" {
		writeErr(w, http.StatusBadRequest, "缺少 uuid")
		return
	}
	now := time.Now()
	switch in.Event {
	case "start":
		_ = s.store.DeployStart(in.UUID, in.ImageID, in.TargetDisk, now)
	case "end":
		_ = s.store.DeployEnd(in.UUID, in.Result, in.BytesWritten, in.Error, now)
	default:
		writeErr(w, http.StatusBadRequest, "event 必须为 start/end")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GET /api/nodes —— 节点列表
func (s *Server) handleNodeList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}
	list, err := s.store.List(time.Now(), s.cfg.OnlineTimeout)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败: "+err.Error())
		return
	}
	online := 0
	for _, n := range list {
		if n.Status == "online" {
			online++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total": len(list), "online": online, "nodes": list,
	})
}
