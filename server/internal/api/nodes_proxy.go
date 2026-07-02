package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/anomalyco/bootseed/server/internal/model"
)

func (s *Server) handleNodeProxy(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	parts := strings.SplitN(path, "/", 3)
	if parts[0] == "" {
		writeErr(w, http.StatusNotFound, "节点接口不存在")
		return
	}
	if len(parts) == 1 {
		s.handleNodeDelete(w, r, parts[0])
		return
	}
	uuid, action := parts[0], parts[1]
	node, err := s.store.Get(uuid, time.Now(), s.cfg.OnlineTimeout)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询节点失败: "+err.Error())
		return
	}
	if node == nil {
		writeErr(w, http.StatusNotFound, "节点不存在")
		return
	}
	switch action {
	case "agent-context":
		s.proxyNodeJSON(w, r, node, "/api/context")
	case "agent-images":
		s.proxyNodeJSON(w, r, node, "/api/images")
	case "agent-disks":
		s.proxyNodeJSON(w, r, node, "/api/disks")
	case "deploy":
		s.proxyNodeJSON(w, r, node, "/api/deploy")
	case "deploy-status":
		s.proxyNodeJSON(w, r, node, "/api/deploy/status")
	case "deploy-cancel":
		s.proxyNodeJSON(w, r, node, "/api/deploy/cancel")
	default:
		writeErr(w, http.StatusNotFound, "节点接口不存在")
	}
}

func (s *Server) proxyNodeJSON(w http.ResponseWriter, r *http.Request, node *model.NodeView, endpoint string) {
	if node.AgentURL == "" {
		writeErr(w, http.StatusConflict, "节点未上报 agent 地址")
		return
	}
	if node.Status != "online" {
		writeErr(w, http.StatusConflict, "节点当前不在线")
		return
	}
	base, err := url.Parse(node.AgentURL)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "节点 agent 地址非法")
		return
	}
	target, err := base.Parse(endpoint)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "拼接节点请求失败")
		return
	}
	var body io.Reader
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(r.Method, target.String(), body)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "创建节点请求失败")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	hc := &http.Client{Timeout: 20 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("节点 %s 不可达: %v", nodeName(node), err))
		return
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(data)
}

func nodeName(n *model.NodeView) string {
	if n.Hostname != "" {
		return n.Hostname
	}
	return n.UUID
}

// compile-time guard to keep json imported for future structured proxy transforms.
var _ = json.Valid
