package bootcontext

import (
	"testing"

	"github.com/anomalyco/bootseed/agent/internal/system"
)

func TestParseCmdline(t *testing.T) {
	cmd := "BOOT_IMAGE=/vmlinuz initrd=initramfs-deploy ip=dhcp " +
		"deploy_server=http://1.2.3.4:8080 node_arch=x86_64 " +
		"node_mac=52:54:00:12:34:56 node_uuid=abc-uuid agent_port=8080 quiet"
	m := ParseCmdline(cmd)
	if m["node_arch"] != "x86_64" {
		t.Errorf("node_arch = %q", m["node_arch"])
	}
	if m["deploy_server"] != "http://1.2.3.4:8080" {
		t.Errorf("deploy_server = %q", m["deploy_server"])
	}
	if m["agent_port"] != "8080" {
		t.Errorf("agent_port = %q", m["agent_port"])
	}
	if _, ok := m["quiet"]; !ok {
		t.Errorf("quiet 应被收录")
	}
}

func TestBuild_ParsesAll(t *testing.T) {
	cmd := "node_arch=amd64 deploy_server=http://10.0.0.1:8080 " +
		"node_mac=AA:BB:CC:DD:EE:FF node_uuid=u-1 agent_port=8080 " +
		"alpine_version=3.20.3"
	ctx, err := Build(cmd, "0.1.0")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ctx.NodeArchitecture != system.ArchX8664 {
		t.Errorf("NodeArchitecture = %q, want x86_64", ctx.NodeArchitecture)
	}
	if ctx.DeployServer != "http://10.0.0.1:8080" {
		t.Errorf("DeployServer = %q", ctx.DeployServer)
	}
	if ctx.AgentPort != 8080 {
		t.Errorf("AgentPort = %d", ctx.AgentPort)
	}
	if ctx.NodeMAC != "AA:BB:CC:DD:EE:FF" {
		t.Errorf("NodeMAC = %q", ctx.NodeMAC)
	}
	if ctx.AgentVersion != "0.1.0" {
		t.Errorf("AgentVersion = %q", ctx.AgentVersion)
	}
}

func TestBuild_InvalidAgentPort(t *testing.T) {
	if _, err := Build("node_arch=x86_64 agent_port=abc", "0"); err == nil {
		t.Fatal("期望 agent_port=abc 报错")
	}
	if _, err := Build("node_arch=x86_64 agent_port=70000", "0"); err == nil {
		t.Fatal("期望 agent_port=70000 报错")
	}
}

func TestBuild_InvalidNodeArch(t *testing.T) {
	if _, err := Build("node_arch=foo", "0"); err == nil {
		t.Fatal("期望 node_arch=foo 报错")
	}
}

func TestVerifyArchitectures(t *testing.T) {
	c := &BootContext{
		NodeArchitecture:    system.ArchX8664,
		RuntimeArchitecture: system.ArchX8664,
		UnameArchitecture:   system.ArchX8664,
	}
	if err := c.VerifyArchitectures(); err != nil {
		t.Errorf("一致时不应报错: %v", err)
	}

	c = &BootContext{
		NodeArchitecture:    system.ArchX8664,
		RuntimeArchitecture: system.ArchAArch64,
	}
	if err := c.VerifyArchitectures(); err == nil {
		t.Error("Runtime 不一致应报错")
	}

	c = &BootContext{
		NodeArchitecture:  system.ArchAArch64,
		UnameArchitecture: system.ArchX8664,
	}
	if err := c.VerifyArchitectures(); err == nil {
		t.Error("uname 不一致应报错")
	}

	c = &BootContext{}
	if err := c.VerifyArchitectures(); err == nil {
		t.Error("缺少 node_arch 应报错")
	}
}
