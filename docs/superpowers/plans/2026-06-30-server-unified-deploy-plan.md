# Server Unified Deploy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 BootSeed 增加 `bootseed-enter`、静态网络继承和服务端统一部署在线 BootSeed 节点的能力。

**Architecture:** 复用 agent 作为唯一部署执行面，server 仅做统一控制面与代理。`bootseed-enter` 负责把 Linux 节点引导进 BootSeed，并把默认出口网卡的静态网络配置传给内存系统；BootSeed 启动后恢复网络并回连 server，随后由 server 直接代理 agent 完成部署。

**Tech Stack:** Go、原生 JS/CSS、shell、Alpine initramfs、bbolt、docker compose

---

### Task 1: 扩展数据模型与节点存储

**Files:**
- Modify: `server/internal/model/model.go`
- Modify: `server/internal/store/store.go`
- Test: `server/internal/api/server_test.go`

- [x] 增加节点来源、生命周期、网络状态、hostname、agent_url 等字段
- [x] 让注册接口和存储层保留新增字段并生成派生视图
- [x] 为新视图字段补测试

### Task 2: server 代理在线节点 agent

**Files:**
- Create: `server/internal/api/nodes_proxy.go`
- Modify: `server/internal/api/server.go`
- Modify: `server/internal/api/nodes.go`
- Test: `server/internal/api/server_test.go`

- [x] 增加按节点查询镜像/磁盘/上下文的代理接口
- [x] 增加按节点发起部署、查询进度、取消部署的代理接口
- [x] 对不可达节点返回清晰错误
- [x] 为代理接口补单测

### Task 3: 服务端门户节点详情与部署弹窗

**Files:**
- Modify: `server/web/index.html`
- Modify: `server/web/app.js`
- Modify: `server/web/style.css`

- [x] 扩展节点列表字段和展开详情
- [x] 增加“部署镜像”弹窗
- [x] 通过 server 代理接口加载节点镜像/磁盘/进度
- [x] 支持取消部署和跳转 agent 兜底页

### Task 4: agent 启动上下文与网络状态扩展

**Files:**
- Modify: `agent/internal/bootcontext/bootcontext.go`
- Modify: `agent/internal/api/handlers.go`
- Modify: `agent/internal/report/report.go`
- Create: `agent/internal/system/netconfig.go`

- [x] 定义本地导入网络配置结构
- [x] 扩展 context/register 上报字段
- [x] 让 agent 对外暴露 origin/network_mode/network_status/hostname/agent_url

### Task 5: initramfs 恢复静态网络

**Files:**
- Modify: `alpine/build-initramfs.sh`

- [x] 在 `/init` 中优先读取本地导入的 `node-config.json`
- [x] 按 MAC 匹配网卡并配置静态 IPv4
- [x] 失败时回退 DHCP
- [x] 把结果写入约定状态文件供 agent 读取

### Task 6: 实现 bootseed-enter

**Files:**
- Create: `scripts/bootseed-enter.sh`
- Modify: `README.md`
- Modify: `docs/DEPLOY.md`

- [x] 支持 `--server <url>`
- [x] 支持 `--cleanup`
- [x] 采集默认出口网卡静态网络
- [x] 下载 `vmlinuz` 与 `initramfs-deploy`
- [x] 兼容 `grub2-reboot` / `grub-reboot` 与常见 grub 配置路径
- [x] 清理 `/boot/bootseed/` 与临时启动项

### Task 7: 构建、部署与三层验证

**Files:**
- Verify only

- [x] 运行 server/agent 测试与前端语法检查
- [x] 重建 bootseed-server、本地内存系统
- [x] 在 `kvm1` 上新增独立测试网段并仅对 `pxe-test` 做隔离验证
- [x] 在 `pxe-test` 上执行 `bootseed-enter`
- [x] 验证节点以静态网络回连 server 并通过 server 门户发起部署

## Validation Notes

- 2026-07-01 在 `kvm1` 的 `pxe-test` 上完成真实验证。
- 已验证 `bootseed-enter` 从 Rocky 9.8 进入 BootSeed。
- 已验证服务端代理部署 `rocky98-x86_64` 到 `/dev/disk/by-id/virtio-ROCKY98TEST` 成功。
- 已验证无 DHCP 的 `bootseed-test` 三层网段中,节点通过导入静态地址 `172.16.50.120/24` 回连 server,`network_mode=static`。
- 验证结束后已恢复 `kvm1` 临时网络、iptables、rp_filter、本机静态路由以及 `pxe-test` 的单网卡拓扑。
