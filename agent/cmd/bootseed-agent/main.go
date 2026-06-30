// Command bootseed-agent 是运行在 PXE 内存 Alpine 中的节点 Agent。
//
// 它解析内核启动参数确定节点身份与部署服务端，提供 HTTP API 与 Web 页面，
// 由管理员选择镜像和目标磁盘并把镜像写入系统盘。
package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/anomalyco/bootseed/agent/internal/api"
	"github.com/anomalyco/bootseed/agent/internal/bootcontext"
	"github.com/anomalyco/bootseed/agent/internal/config"
	"github.com/anomalyco/bootseed/agent/internal/images"
	"github.com/anomalyco/bootseed/agent/internal/report"
	webassets "github.com/anomalyco/bootseed/agent/web"
)

// version 由构建时通过 -ldflags "-X main.version=..." 注入。
var version = "0.1.0"

func main() {
	log.SetFlags(log.LstdFlags)

	// 优先使用 BOOTSEED_CMDLINE 环境变量（便于测试与非 PXE 场景）；
	// 否则读取 /proc/cmdline（PXE 内存系统中的正常路径）。
	cmdline := os.Getenv("BOOTSEED_CMDLINE")
	if cmdline == "" {
		var err error
		cmdline, err = bootcontext.ReadCmdline()
		if err != nil {
			log.Printf("[warn] 读取 /proc/cmdline 失败: %v", err)
		}
	}

	boot, err := bootcontext.Build(cmdline, version)
	if err != nil {
		log.Fatalf("[fatal] 构建 BootContext 失败: %v", err)
	}

	// 架构自检：启动参数架构必须与运行架构一致，否则禁止部署。
	var archErr error
	if err := boot.VerifyArchitectures(); err != nil {
		archErr = err
		log.Printf("[error] 架构自检失败: %v —— 将禁止部署", err)
	}

	cfg := config.FromEnv()
	if cfg.DeployServer == "" {
		cfg.DeployServer = boot.DeployServer
	}
	if boot.AgentPort > 0 {
		cfg.ListenAddr = fmt.Sprintf(":%d", boot.AgentPort)
	}

	// 加载镜像清单（失败不致命，页面可再次 reload）。
	catalog := images.NewCatalog()
	if boot.DeployServer != "" {
		if err := catalog.LoadFromHTTP(boot.DeployServer); err != nil {
			log.Printf("[warn] 初始加载镜像清单失败: %v", err)
		} else {
			log.Printf("[info] 已加载镜像清单，共 %d 个镜像", len(catalog.All()))
		}
	}

	sub, err := fs.Sub(webassets.Files, ".")
	if err != nil {
		log.Fatalf("[fatal] 加载内嵌前端失败: %v", err)
	}

	// 向服务端门户上报：注册 + 周期心跳（尽力而为，失败不影响本地部署）。
	rep := report.New(boot.DeployServer, boot.NodeUUID)
	if rep != nil {
		rep.Register(report.RegisterInfo{
			UUID: boot.NodeUUID, MAC: boot.NodeMAC, IP: firstIPv4(),
			Architecture: boot.NodeArchitecture.String(), BootMode: string(boot.BootMode),
			KernelVersion: boot.KernelVersion, AlpineVersion: boot.AlpineVersion,
			AgentVersion: boot.AgentVersion,
		})
		rep.StartHeartbeat(context.Background(), time.Duration(envInt("HEARTBEAT_INTERVAL", 15))*time.Second)
	}

	srv := api.New(api.Options{
		Config:     cfg,
		Boot:       boot,
		Catalog:    catalog,
		AutoReboot: envBool("AUTO_REBOOT"),
		WebFS:      sub,
		ArchError:  archErr,
		Report:     rep,
	})

	httpServer := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: srv.Handler(),
	}

	printBanner(boot, cfg.ListenAddr, archErr)

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[fatal] HTTP 服务退出: %v", err)
		}
	}()

	// 优雅退出
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
}

// printBanner 在控制台打印 Web 管理地址，供管理员从其他电脑访问。
// 同时写到 stdout 与各物理控制台设备（/dev/tty0 = VNC/VGA，串口），
// 这样无论内核把哪个设为主 /dev/console，VNC 与串口都能看到部署地址。
func printBanner(boot *bootcontext.BootContext, listen string, archErr error) {
	port := strings.TrimPrefix(listen, ":")
	ip := firstIPv4()
	addr := "http://<node-ip>:" + port
	if ip != "" {
		addr = "http://" + ip + ":" + port
	}
	line := strings.Repeat("=", 56)
	var b strings.Builder
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, line)
	fmt.Fprintln(&b, "  BootSeed is ready")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "  Architecture: %s\n", boot.NodeArchitecture)
	if archErr != nil {
		fmt.Fprintf(&b, "  [WARNING] 架构自检失败，部署被禁用: %v\n", archErr)
	}
	fmt.Fprintln(&b, "  Deployment page:")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "    %s\n", addr)
	fmt.Fprintln(&b, line)
	fmt.Fprintln(&b)
	banner := b.String()

	fmt.Print(banner)
	// 显式写到各控制台设备（best-effort，失败忽略）
	for _, dev := range []string{"/dev/tty0", "/dev/ttyS0", "/dev/ttyAMA0", "/dev/console"} {
		if f, err := os.OpenFile(dev, os.O_WRONLY, 0); err == nil {
			_, _ = f.WriteString(banner)
			_ = f.Close()
		}
	}
}

func envBool(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return v == "1" || v == "true" || v == "yes"
}

// firstIPv4 返回第一个非回环 IPv4 地址，用于控制台横幅。
func firstIPv4() string {
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

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
