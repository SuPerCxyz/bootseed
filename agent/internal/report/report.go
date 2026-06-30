// Package report 是节点 agent 向服务端门户(bootseed-server)上报的客户端：
// 开机注册、定期心跳、部署开始/结束上报。所有上报均为尽力而为，失败不影响本地部署。
package report

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

// Client 向 ${deploy_server}/api/nodes/* 上报。
type Client struct {
	base string // 形如 http://192.168.100.161:8088
	uuid string
	hc   *http.Client
}

// New 构造上报客户端；deployServer 为空则返回 nil（不上报）。
func New(deployServer, uuid string) *Client {
	deployServer = strings.TrimRight(strings.TrimSpace(deployServer), "/")
	if deployServer == "" {
		return nil
	}
	return &Client{base: deployServer, uuid: uuid, hc: &http.Client{Timeout: 8 * time.Second}}
}

func (c *Client) post(path string, body any) {
	if c == nil {
		return
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", c.base+path, bytes.NewReader(b))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		log.Printf("[report] %s 失败: %v", path, err)
		return
	}
	_ = resp.Body.Close()
}

// RegisterInfo 是注册字段（json 键与服务端 model.Node 对齐）。
type RegisterInfo struct {
	UUID          string `json:"uuid"`
	MAC           string `json:"mac"`
	IP            string `json:"ip"`
	Architecture  string `json:"arch"`
	BootMode      string `json:"boot_mode"`
	KernelVersion string `json:"kernel_version"`
	AlpineVersion string `json:"alpine_version"`
	AgentVersion  string `json:"agent_version"`
}

// Register 开机注册。
func (c *Client) Register(info RegisterInfo) { c.post("/api/nodes/register", info) }

// Heartbeat 单次心跳。
func (c *Client) Heartbeat() {
	c.post("/api/nodes/heartbeat", map[string]string{"uuid": c.uuid})
}

// DeployStart 上报部署开始。
func (c *Client) DeployStart(imageID, target string) {
	c.post("/api/nodes/deploy", map[string]any{
		"uuid": c.uuid, "event": "start", "image_id": imageID, "target_disk": target,
	})
}

// DeployEnd 上报部署结束。
func (c *Client) DeployEnd(result string, bytes int64, errMsg string) {
	c.post("/api/nodes/deploy", map[string]any{
		"uuid": c.uuid, "event": "end", "result": result,
		"bytes_written": bytes, "error": errMsg,
	})
}

// StartHeartbeat 后台周期心跳，直到 ctx 取消。
func (c *Client) StartHeartbeat(ctx context.Context, interval time.Duration) {
	if c == nil {
		return
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c.Heartbeat()
			}
		}
	}()
}
