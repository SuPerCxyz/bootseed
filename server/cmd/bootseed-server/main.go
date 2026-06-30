// Command bootseed-server 是 BootSeed 服务端门户后端：
// 提供总览、镜像增删、节点登记与列表；持久化用嵌入式 bbolt。
package main

import (
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anomalyco/bootseed/server/internal/api"
	"github.com/anomalyco/bootseed/server/internal/store"
	webassets "github.com/anomalyco/bootseed/server/web"
)

func main() {
	log.SetFlags(log.LstdFlags)

	dataRoot := envDefault("DATA_ROOT", "/data")
	dbPath := envDefault("PORTAL_DB", filepath.Join(dataRoot, "state", "bootseed.db"))
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		log.Fatalf("[fatal] 创建状态目录失败: %v", err)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("[fatal] 打开 bbolt 失败: %v", err)
	}
	defer st.Close()

	cfg := api.Config{
		Token:         os.Getenv("PORTAL_TOKEN"),
		OnlineTimeout: time.Duration(envInt("NODE_ONLINE_TIMEOUT", 45)) * time.Second,
		DataRoot:      dataRoot,
		PXEServerIP:   os.Getenv("PXE_SERVER_IP"),
		HTTPPort:      envDefault("HTTP_PORT", "8088"),
		PXEInterface:  os.Getenv("PXE_INTERFACE"),
		PXESubnet:     os.Getenv("PXE_SUBNET"),
		Architectures: splitCSV(envDefault("SUPPORTED_ARCHITECTURES", "x86_64,aarch64")),
		AlpineVersion: os.Getenv("ALPINE_VERSION"),
		AgentVersion:  envDefault("AGENT_VERSION", "0.1.0"),
		IPXERef:       os.Getenv("IPXE_REF"),
	}

	sub, err := fs.Sub(webassets.Files, ".")
	if err != nil {
		log.Fatalf("[fatal] 加载内嵌门户前端失败: %v", err)
	}

	srv := api.New(cfg, st, sub)
	addr := envDefault("PORTAL_LISTEN", ":9090")
	log.Printf("[info] bootseed-server 启动于 %s，数据根=%s，DB=%s，鉴权=%v",
		addr, dataRoot, dbPath, cfg.Token != "")
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("[fatal] HTTP 退出: %v", err)
	}
}

func envDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
