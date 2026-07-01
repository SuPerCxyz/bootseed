package system

import (
	"bufio"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// NodeNet 汇总节点网络细节,供 Web 页面现场核对.
type NodeNet struct {
	Interface string   `json:"iface,omitempty"`
	IP        string   `json:"ip"`
	Netmask   string   `json:"netmask"`
	Gateway   string   `json:"gateway"`
	DNS       []string `json:"dns"`
}

// Mem 汇总内存系统的内存用量(KB -> 字节).
type Mem struct {
	TotalBytes     int64 `json:"total_bytes"`
	AvailableBytes int64 `json:"available_bytes"`
}

// UptimeSeconds 读取 /proc/uptime,返回系统已运行秒数.
func UptimeSeconds() float64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[0], 64)
	return v
}

// BootTime 依据 uptime 推算系统启动时刻(UTC).
func BootTime(now time.Time) time.Time {
	up := UptimeSeconds()
	if up <= 0 {
		return now
	}
	return now.Add(-time.Duration(up * float64(time.Second)))
}

// ReadMem 解析 /proc/meminfo 的 MemTotal / MemAvailable.
func ReadMem() Mem {
	var m Mem
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		kb, _ := strconv.ParseInt(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			m.TotalBytes = kb * 1024
		case "MemAvailable:":
			m.AvailableBytes = kb * 1024
		}
	}
	return m
}

// CollectNodeNet 收集主网卡 IP / 掩码 / 默认网关 / DNS.
func CollectNodeNet() NodeNet {
	var n NodeNet
	n.Interface, n.Gateway = defaultRouteInfo()
	if n.Interface != "" {
		iface, err := net.InterfaceByName(n.Interface)
		if err == nil {
			addrs, _ := iface.Addrs()
			for _, a := range addrs {
				ipnet, ok := a.(*net.IPNet)
				if !ok || ipnet.IP.IsLoopback() {
					continue
				}
				if v4 := ipnet.IP.To4(); v4 != nil {
					n.IP = v4.String()
					n.Netmask = net.IP(ipnet.Mask).String()
					break
				}
			}
		}
	}
	if n.IP == "" {
		addrs, _ := net.InterfaceAddrs()
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() {
				continue
			}
			if v4 := ipnet.IP.To4(); v4 != nil {
				n.IP = v4.String()
				n.Netmask = net.IP(ipnet.Mask).String()
				break
			}
		}
	}
	n.DNS = dnsServers()
	return n
}

// defaultGateway 解析 /proc/net/route 找默认路由(Destination=00000000).
func defaultRouteInfo() (string, string) {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		if first { // 跳过表头
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		if fields[1] != "00000000" {
			continue
		}
		// 网关为小端 16 进制 IPv4
		gwHex := fields[2]
		if len(gwHex) != 8 {
			continue
		}
		var b [4]byte
		for i := 0; i < 4; i++ {
			v, err := strconv.ParseUint(gwHex[i*2:i*2+2], 16, 8)
			if err != nil {
				return "", ""
			}
			b[3-i] = byte(v) // 小端
		}
		return fields[0], net.IPv4(b[0], b[1], b[2], b[3]).String()
	}
	return "", ""
}

// dnsServers 解析 /etc/resolv.conf 的 nameserver.
func dnsServers() []string {
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "nameserver" {
			out = append(out, fields[1])
		}
	}
	return out
}

// AlpineRelease 读取 /etc/alpine-release(运行中的 Alpine 版本).
func AlpineRelease() string {
	b, err := os.ReadFile("/etc/alpine-release")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
