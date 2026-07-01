# BootSeed 部署指南

本文档描述如何从零部署 BootSeed--一个容器化的 PXE 镜像部署工具.BootSeed 由两个
`docker-compose` 服务组成:

| 服务 | 角色 | 网络 / 端口 |
| --- | --- | --- |
| `bootseed-pxe` | dnsmasq ProxyDHCP + TFTP,仅负责 PXE 引导路由,不分配 IP | `network_mode: host`,`cap_add: NET_RAW/NET_ADMIN`,UDP 67/69/4011 |
| `bootseed-server` | Go 服务:门户后端(总览/镜像增删/节点登记,bbolt 持久化)+ 静态文件下载(`/boot` `/alpine` `/images`,原生 Range)+ 内嵌门户 UI | 对外 `${HTTP_PORT}`(默认 8088) |

默认引导链:目标机 PXE 广播 -> 现网 DHCP 发 IP -> BootSeed ProxyDHCP 发引导 -> TFTP 取 iPXE
-> HTTP 取 Alpine `vmlinuz` / `initramfs-deploy` / `modloop` -> 内存系统启动
`bootseed-agent` -> 服务端门户或节点页面发起部署.

非同二层场景还支持另一条入口:

- 在现有 Linux 系统中执行 `http://<PXE_SERVER_IP>:<HTTP_PORT>/bootseed-enter.sh`
- 脚本采集当前默认出口网卡的 IPv4/网关/DNS,设置一次性 grub 启动项
- 重启后直接进入 BootSeed 内存系统
- 节点上线后仍由服务端门户统一选择镜像和磁盘

> 产物(iPXE / Alpine initramfs / Agent)默认不随仓库提交,需要在本机执行 `make init`
> 联网生成.下文第 4 节给出构建步骤.

---

## 1. 前提条件

### 1.1 宿主机要求

- Linux 宿主机,与目标节点处于同一二层广播域(或已配置 DHCP Relay,见第 11 节).
- 一块专用于 PXE 的网卡(`PXE_INTERFACE`),处于 UP 状态,且 `PXE_SERVER_IP` 已配置
  在该网卡上.
- 宿主机防火墙放行:UDP 67 / 69 / 4011(PXE/TFTP)+ TCP `${HTTP_PORT}`(HTTP 下载与门户).
- UDP 67 / 69 / 4011 不能被其它 DHCP/TFTP 服务占用(如 libvirt 自带 dnsmasq).

### 1.2 Docker / Compose

- Docker Engine(建议 24+).
- Docker Compose v2(`docker compose` 子命令形式,非旧版 `docker-compose`).

```bash
docker --version
docker compose version
```

### 1.3 构建依赖

构建产物(`make init`)需要以下宿主机工具链:

```bash
# Debian / Ubuntu
sudo apt update
sudo apt install -y make gcc git perl liblzma-dev qemu-utils zstd

# 可选:构建 aarch64 iPXE 需要交叉编译器
sudo apt install -y gcc-aarch64-linux-gnu
```

各依赖用途:

| 依赖 | 用途 | 用于哪一步 |
| --- | --- | --- |
| `make` | 驱动 Makefile target | 全部构建 |
| `gcc` | 编译 iPXE 源码 | `make prepare-ipxe` |
| `git` | 克隆 iPXE 源码 | `make prepare-ipxe` |
| `perl` | iPXE 构建脚本依赖 | `make prepare-ipxe` |
| `liblzma-dev`(提供 `lzma.h`) | iPXE `util/zbin` 压缩 | `make prepare-ipxe` |
| `gcc-aarch64-linux-gnu` | 交叉编译 ARM64 EFI iPXE | `make prepare-ipxe`(aarch64,可选) |
| `qemu-utils`(`qemu-img`) | qcow2/vmdk 等转 raw | 添加镜像时按需 |
| `zstd` | raw 镜像压缩为 `raw.zst` | 添加镜像时按需 |
| `go` | 编译双架构 Agent | `make build-agent` |

> `build-initramfs` 在 `bootseed-builder` 容器内执行(容器自带 `apk-tools`/`kmod` 等),
> 宿主机不需要 Alpine 工具链;但该步骤需要 **root 权限与联网**(`apk` 联网拉包).

### 1.4 可选:KVM / libvirt

KVM / libvirt **仅用于本地测试**(起虚拟机验证 PXE 链路),生产部署非必需.若安装
libvirt,请注意其自带 dnsmasq 会占用 UDP 67,需关闭或避免与 `PXE_INTERFACE` 同网段.

---

## 2. 获取代码

```bash
git clone <bootseed-repo-url> bootseed
cd bootseed
```

仓库根目录关键文件:

```
Makefile                 # 构建/启停入口
docker-compose.yml       # 两服务编排
.env.example             # 配置模板
scripts/                 # 构建/校验/镜像管理脚本
alpine/                  # initramfs 构建脚本与模块清单
pxe/                     # dnsmasq 配置模板与容器入口
web/                     # Nginx 配置与 boot.ipxe 模板
server/                  # Go 门户后端
agent/                   # Go 节点 Agent
data/                    # 产物与数据目录(构建后生成,gitignore)
```

---

## 3. 配置 .env

复制模板并按实际环境修改:

```bash
cp .env.example .env
vi .env
```

### 3.1 关键变量说明

| 变量 | 说明 | 示例 |
| --- | --- | --- |
| `PXE_INTERFACE` | PXE 监听网卡名,必须真实存在且处于 UP | `enp1s0` |
| `PXE_SUBNET` | PXE 监听子网,**必须是网络地址(主机位为 0),不是地址池** | `192.168.100.0` |
| `PXE_SERVER_IP` | PXE/HTTP 服务端 IP,**必须真实配置在 `PXE_INTERFACE` 上** | `192.168.100.161` |
| `HTTP_PORT` | 对外 HTTP 端口(门户 + 下载) | `8088` |
| `AGENT_PORT` | 节点 Agent 监听端口(部署页面端口) | `8088` |
| `ALPINE_VERSION` | Alpine 版本,固定到已验证版本 | `3.20.3` |
| `ALPINE_BRANCH` | Alpine 仓库分支 | `v3.20` |
| `IPXE_REF` | iPXE 源码 git tag 或 commit | `v2.0.0` |
| `X86_KERNEL_CONSOLE` | x86_64 内核控制台参数 | `console=ttyS0,115200n8 console=tty0` |
| `ARM64_KERNEL_CONSOLE` | aarch64 内核控制台参数 | `console=ttyAMA0,115200n8 console=ttyS0,115200n8 console=tty0` |
| `PORTAL_TOKEN` | 服务端门户管理口令;**留空则管理操作免鉴权** | `bootseed` |
| `NODE_ONLINE_TIMEOUT` | 节点上报在线的超时阈值(秒) | `45` |
| `HEARTBEAT_INTERVAL` | 节点心跳间隔(秒) | `15` |

其它可选变量(`.env.example` 中有完整清单):`AUTO_REBOOT`,`ALLOW_CUSTOM_IMAGE_SERVER`,
`ALLOW_MULTIPATH_TARGET`,`ALLOW_UNSTABLE_DISK_NAME`,`ENABLE_HTTPS_IMAGES`,
`SUPPORTED_ARCHITECTURES`,`ALPINE_X86_KERNEL_FLAVOR` / `ALPINE_ARM64_KERNEL_FLAVOR`,
`NETWORK_DEVICE_TIMEOUT` / `STORAGE_DEVICE_TIMEOUT`,`AGENT_VERSION`.

### 3.2 ProxyDHCP 关键约束

- **`PXE_SERVER_IP` 必须真实配置在 `PXE_INTERFACE` 上**.校验脚本
  `scripts/validate-config.sh` 会用 `ip -o addr show dev` 检查归属,不匹配直接 FAIL.
- **`PXE_SUBNET` 是网络地址,不是地址池**.dnsmasq 模板中写的是
  `dhcp-range=${PXE_SUBNET},proxy`(ProxyDHCP 模式),BootSeed 绝不下发 IP,真实地址由
  现网已有 DHCP 服务器分配.
- ProxyDHCP 必须与现网 DHCP 共享同一网段的 UDP/67.dnsmasq 使用 `bind-dynamic`
  (SO_REUSEADDR)与之共存;若误用 `bind-interfaces` 独占 67 端口,会导致现网 DHCP 收
  不到请求,客户端拿不到 IP(PXE-E16).

确认 IP 已配置在网卡上:

```bash
ip -o addr show dev enp1s0
# 应能看到 PXE_SERVER_IP
```

---

## 4. 构建产物

### 4.1 一键初始化(推荐)

```bash
make init
```

`make init` 等价于:

```
validate -> prepare-ipxe -> prepare-alpine -> build-all-architectures
        -> generate-config -> validate-architectures
```

完成后产物落到 `data/` 下:

```
data/tftp/x86_64/undionly.kpxe        # BIOS PXE
data/tftp/x86_64-uefi/snponly.efi     # UEFI x86_64
data/tftp/aarch64/snponly.efi         # UEFI ARM64
data/http/alpine/<arch>/vmlinuz
data/http/alpine/<arch>/initramfs-deploy
data/http/alpine/<arch>/modloop
data/http/alpine/<arch>/manifest.json
data/http/boot/boot.ipxe
data/http/boot/x86_64.ipxe
data/http/boot/aarch64.ipxe
build/agent/bootseed-agent-{x86_64,aarch64}
```

### 4.2 分步构建

如需单独重跑某一步:

```bash
make validate              # 校验 .env 与网卡(18 项检查)
make prepare-ipxe          # 三架构 iPXE(联网克隆 + 编译)
make prepare-alpine        # 下载 Alpine netboot 内核 + modloop
make build-agent           # 双架构 Go Agent(CGO_ENABLED=0 交叉编译)
make build-initramfs       # 容器内组装 Alpine initramfs+modloop+vmlinuz
make generate-config       # 生成 dnsmasq.conf 占位渲染 + boot.ipxe / x86_64.ipxe / aarch64.ipxe
make validate-architectures
```

各步骤的联网 / root 要求:

| 步骤 | 联网 | root | 说明 |
| --- | --- | --- | --- |
| `prepare-ipxe` | 是 | 否 | 克隆 `github.com/ipxe/ipxe` 并编译;aarch64 需交叉编译器 |
| `prepare-alpine` | 是 | 否 | 下载 Alpine netboot 内核与 modloop |
| `build-agent` | 否(仅 Go 模块缓存) | 否 | 纯 Go 交叉编译 |
| `build-initramfs` | **是** | **是** | 在 `bootseed-builder` 容器内 `apk` 联网拉包组装 rootfs,需要 CAP_MKNOD |
| `generate-config` | 否 | 否 | 纯模板渲染 |

> `build-initramfs` 通过 `docker run` 在 `bootseed-builder` 容器内执行
> `alpine/build-initramfs.sh`,项目根挂载到 `/work`.容器内 `apk --root` 创建 `/dev`
> 节点需要权限,`docker run` 默认已具备 CAP_MKNOD.
>
> 运行期工具包由 `alpine/packages.yaml` 定义;包名不存在会让构建直接失败.
> 额外二进制,固件或脚本可放入 `data/vendor-tools/`,其中根目录文件会复制到
> `/usr/local/bin/`,`common/` 与架构子目录会按 rootfs overlay 原样注入.

### 4.3 构建后校验

```bash
make validate              # 默认:产物缺失只 WARN
make validate --strict     # 产物缺失也记 FAIL
```

`scripts/validate-config.sh` 会逐项检查网卡存在/UP,IP 归属,`PXE_SUBNET` 网络地址合法
性,端口合法与占用,9 个引导/Alpine 产物文件是否存在,产物架构与镜像清单一致性.出现
FAIL 必须修复后再启动.

---

## 5. 启动服务

```bash
make up        # = docker compose up -d
```

查看状态与日志:

```bash
docker compose ps
docker compose logs -f --tail=200     # 或 make logs
```

两个容器的健康检查:

| 容器 | 健康检查 |
| --- | --- |
| `bootseed-pxe` | `pgrep dnsmasq` |
| `bootseed-server` | `wget --spider http://127.0.0.1/healthz` |

`bootseed-pxe` 启动时会渲染 `/etc/bootseed/dnsmasq.conf.template` 并前台运行 dnsmasq,
日志会打印渲染后的完整配置,可用于核对 `PXE_INTERFACE` / `PXE_SUBNET` /
`PXE_SERVER_IP` / `HTTP_PORT` 是否正确.

---

## 6. 验证服务端

在宿主机或任意同网段机器上执行:

```bash
# bootseed-server 健康检查
curl http://<PXE_SERVER_IP>:<HTTP_PORT>/healthz
# 期望:ok

# 服务端信息(bootseed-server 直接提供)
curl http://<PXE_SERVER_IP>:<HTTP_PORT>/api/server-info
# 返回 PXE_SERVER_IP / HTTP_PORT / PXE_INTERFACE / PXE_SUBNET /
#       受支持架构 / Alpine 版本 / Agent 版本 / iPXE ref /
#       三个 iPXE 文件是否就绪 / 两架构 Alpine 构建信息(含 kernel_version)

# 门户首页(bootseed-server 直接提供)
curl -I http://<PXE_SERVER_IP>:<HTTP_PORT>/
```

浏览器打开 `http://<PXE_SERVER_IP>:<HTTP_PORT>/` 即为管理门户.

### 门户鉴权

`PORTAL_TOKEN` 控制管理操作鉴权:

- 留空:管理操作(镜像增删等)**免鉴权**,仅适合隔离测试环境.
- 非空:管理操作需携带该 token.生产环境务必设置一个强口令.

```bash
# .env
PORTAL_TOKEN=<强口令>
```

---

## 7. 正式使用流程

BootSeed 现提供两套正式使用方式:

- **方式 A: 二层 PXE 部署**
- **方式 B: 三层 `bootseed-enter` 部署**

两种方式在节点进入 BootSeed 后完全统一:都通过服务端门户选择镜像和目标磁盘。

### 7.1 使用前检查

开始前建议确认:

1. `docker compose ps` 中 `bootseed-pxe` 与 `bootseed-server` 均为健康状态
2. 服务端门户 `http://<PXE_SERVER_IP>:<HTTP_PORT>/` 可正常打开
3. “镜像仓库”中已有目标镜像
4. “内存系统构建”中目标架构为“就绪”
5. 如需在门户执行写操作,已设置管理口令 `PORTAL_TOKEN`

### 7.2 方式 A: 二层 PXE 部署

适用条件:

- 目标节点与 `PXE_INTERFACE` 在同一二层广播域
- 现网 DHCP 正常工作
- 节点支持 PXE / iPXE 启动

操作步骤:

1. 打开服务端门户 `http://<PXE_SERVER_IP>:<HTTP_PORT>/`
2. 确认镜像和内存系统构建状态正常
3. 在目标节点固件中选择一次性网络启动,或把网络启动排到磁盘前
4. 重启目标节点进入 PXE
5. 等待节点进入 BootSeed,并出现在门户“节点列表”
6. 在该节点行点击“部署镜像”
7. 选择镜像与目标磁盘
8. 输入确认词 `ERASE`
9. 等待进度到 `completed`
10. 按需重启节点,让其从本地磁盘启动目标系统

建议核对:

- 节点 `arch` 与镜像 `architecture` 一致
- 节点 `boot_mode` 与镜像 `firmware` 一致
- 目标磁盘优先使用 `/dev/disk/by-id/...`

### 7.3 方式 B: 三层 `bootseed-enter` 部署

适用条件:

- 目标节点当前已有 Linux 系统可登录
- 无法依赖 PXE 广播直接进入 BootSeed
- 目标节点现有系统到 `bootseed-server` 的 HTTP 地址可达

操作步骤:

1. 在目标节点现有 Linux 系统执行:

```bash
curl -fsSL http://<PXE_SERVER_IP>:<HTTP_PORT>/bootseed-enter.sh -o /root/bootseed-enter.sh
chmod +x /root/bootseed-enter.sh
/root/bootseed-enter.sh --server http://<PXE_SERVER_IP>:<HTTP_PORT>
reboot
```

2. 脚本会自动:
   - 识别当前默认出口网卡
   - 采集 IPv4 / prefix / gateway / DNS
   - 下载 `vmlinuz` 与 `initramfs-deploy`
   - 写入一次性 grub 启动项
3. 节点重启进入 BootSeed 后,会优先恢复导入的静态网络
4. 回到服务端门户,确认该节点:
   - 状态为在线
   - `origin = bootseed-enter`
   - `network_mode = static` 或 `dhcp`
5. 点击“部署镜像”,后续步骤与 PXE 流程一致

清理命令:

```bash
/root/bootseed-enter.sh --cleanup
```

该命令用于删除 `/boot/bootseed/` 和一次性 grub 入口,适合在放弃进入 BootSeed或验证结束后执行。

### 7.4 服务端门户中的标准操作

节点进入 BootSeed 后,推荐统一在服务端门户中操作:

1. 在“节点列表”定位目标节点
2. 检查节点 IP / 主机名 / 架构 / 固件模式 / `origin` / `network_mode`
3. 点击“部署镜像”
4. 选择兼容镜像和允许写入的目标磁盘
5. 输入 `ERASE`
6. 观察部署阶段:
   - `validating`
   - `preparing`
   - `downloading`
   - `writing`
   - `syncing`
   - `verifying`
   - `completed`

如需兜底排查,仍可打开该节点的 `Agent 页`。

### 7.5 完成与失败判断

判定一次部署完成的标准:

- 服务端门户或节点页显示 `task.state = completed`
- `written_bytes` 与镜像 `raw_size` 一致
- 节点重启后不再回到 BootSeed,而是从本地磁盘启动目标系统

常见失败优先排查:

- 节点未出现在门户:先查 PXE / DHCP / HTTP / 三层路由
- 节点在线但无法部署:检查镜像架构、固件模式、磁盘允许状态
- 进度中断:查看节点历史、`last_error`、节点控制台和 `/api/deploy/status`

---

## 8. 添加镜像

镜像清单存于 `data/http/images/index.json`,镜像文件存于 `data/http/images/`.可通过
门户页或脚本添加.

### 8.1 门户页操作

在门户「镜像管理」中上传镜像文件并填写元数据(id / 名称 / OS / 版本 / 架构 / 固件),
提交即写入 `index.json`.上传直连 bootseed-server `/api/images/upload`
并放宽超时(`proxy_read_timeout 3600s`),支持大镜像.

### 8.2 脚本添加

```bash
scripts/add-image.sh \
  --file /path/to/rocky-9.qcow2 \
  --id rocky-9-x86_64-uefi \
  --name "Rocky Linux 9" \
  --os rocky --version 9 \
  --architecture x86_64 \
  --firmware uefi
```

`add-image.sh` 行为:

- **自动转换**:检测到 qcow2/vmdk/vdi/vhd 等磁盘镜像时,用 `qemu-img convert` 转 raw,
  再用 `zstd` 压成 `raw.zst`,便于流式写盘;同时自动得出 `raw_size`,无需手填
  `--raw-size`.需要 `qemu-img` 与 `zstd`.
- 已是 raw / img 或其压缩形式(`raw.gz` / `raw.xz` / `raw.zst` 等)则原样使用.
- 将文件复制到 `data/http/images/`,计算压缩后大小与 sha256,原子更新 `index.json`
  (临时文件 + `mv`,带 `flock` 防并发损坏).
- 拒绝重复 id 与未知架构 / 非法 firmware.

支持的 `--format`:`raw` / `img` / `raw.gz` / `img.gz` / `raw.xz` / `img.xz` /
`raw.zst` / `img.zst`.

---

## 9. 节点 PXE 启动

1. 在目标机固件中**关闭 Secure Boot**(见第 12 节),选择一次性网络启动(PXE).
2. 确保目标机与 BootSeed 宿主机**在同一二层**(或已配置 DHCP Relay,见第 11 节).
3. 启动后流程:
   - 目标机发 DHCP DISCOVER 广播.
   - **现网 DHCP 服务器**分配 IP / 网关 / DNS(BootSeed 不参与).
   - **BootSeed ProxyDHCP** 补充 PXE 引导信息:按 DHCP Option 93 区分架构
     (`0`=BIOS,`7`/`9`=UEFI x86_64,`11`=UEFI ARM64).
   - 非 iPXE 固件:TFTP 下发对应 iPXE 二进制(`undionly.kpxe` / `snponly.efi`).
   - iPXE 加载后:HTTP 取 `boot.ipxe` -> 按 `buildarch` 转入 `x86_64.ipxe` 或
     `aarch64.ipxe` -> 加载 Alpine `vmlinuz` + `initramfs-deploy` + `modloop` 启动.
4. 内存系统启动后 `bootseed-agent` 自动运行,向 `bootseed-server` 上报节点信息,并在
   节点本地监听 `${AGENT_PORT}`(默认 8088).

> 节点上报地址来自 iPXE 启动脚本中的 `deploy_server=http://${PXE_SERVER_IP}:${HTTP_PORT}`
> 与 `agent_port=${AGENT_PORT}`,由 `generate-config.sh` 渲染.

---

## 10. 部署镜像

1. 在浏览器打开节点部署页面:`http://<节点IP>:<AGENT_PORT>`(默认 8088).
2. 选择镜像,选择目标磁盘.
3. 在确认框输入 `ERASE`(强制大写)以确认擦盘写镜像--这是不可逆操作.
4. 提交后 Agent 执行流式管线:下载 -> sha256 校验 -> 解压 -> sha256 校验 -> 写盘 ->
   `fsync`,全程不落地临时大文件.
5. 在服务端门户 `http://<PXE_SERVER_IP>:<HTTP_PORT>/` 可查看部署进度与节点状态
   (节点心跳间隔 `HEARTBEAT_INTERVAL`,在线超时 `NODE_ONLINE_TIMEOUT`).

> Agent 拒绝缺少 `ERASE` 确认的请求,返回 400 并提示「确认字符串必须为 ERASE」.

---

## 11. 网络拓扑说明

### 11.1 同 VLAN(最简单)

BootSeed 宿主机与目标节点在同一二层广播域.PXE DISCOVER 与 ProxyDHCP 应答都是二层广播,
可直接互通,无需任何 DHCP Relay 或路由器配合.

### 11.2 多 VLAN

ProxyDHCP 不能跨三层广播.多 VLAN 场景二选一:

1. **每个目标 VLAN 各部署一套 ProxyDHCP**(或一台多臂主机,多网卡分别接入各 VLAN,为
   每个 VLAN 各起一个 `bootseed-pxe` 实例 / 各自 `.env`),让每个广播域内都有本地
   ProxyDHCP.
2. **使用 DHCP Relay / IP Helper**:在三层设备上把目标 VLAN 的 DHCP/PXE 广播中继到
   BootSeed 主机所在网段.

核心约束:**ProxyDHCP 必须能「看到」目标节点的广播**,要么本地同广播域,要么经中继
到达.请不要期望仅靠 BootSeed 软件配置打通跨三层 PXE.

### 11.3 防火墙端口

| 协议 / 端口 | 用途 | 服务 |
| --- | --- | --- |
| UDP 67 | ProxyDHCP(DHCP 服务端口) | `bootseed-pxe` |
| UDP 69 | TFTP | `bootseed-pxe` |
| UDP 4011 | ProxyDHCP(PXE boot server 端口) | `bootseed-pxe` |
| TCP `${HTTP_PORT}` | HTTP 下载 + 门户 + 管理 API | `bootseed-server` |

确保宿主机防火墙放行上述端口,且 UDP 67 / 69 / 4011 未被其它 DHCP/TFTP 服务占用.

---

## 12. 常见问题

### 12.1 节点拿不到 IP / PXE-E16

- 检查 BootSeed ProxyDHCP 与现网 DHCP 是否在同一二层.跨三层必须配 DHCP Relay / IP
  Helper 或在目标 VLAN 本地部署 ProxyDHCP.
- 检查 UDP 67 是否被其它服务(如 libvirt 自带 dnsmasq)独占.dnsmasq 模板已用
  `bind-dynamic`(SO_REUSEADDR)与现网 DHCP 共存;若端口被独占,现网 DHCP 收不到请求.
- 检查 `PXE_INTERFACE` 是否正确,`PXE_SERVER_IP` 是否真的配置在该网卡上
  (`make validate` 会检查).
- 查看 `bootseed-pxe` 日志(`docker compose logs bootseed-pxe`),dnsmasq 开启了
  `log-dhcp`,可看到架构识别与引导路由是否命中.

### 12.2 内核模块不加载 / 启动卡住

- 检查 `vmlinuz` 与 `initramfs-deploy` 的内核版本是否一致.两者应在同一次
  `make build-initramfs` 中生成(initramfs 内的模块目录按内核版本组织);若混用不同版本
  产物,模块将无法加载.
- 通过 `curl http://<PXE_SERVER_IP>:<HTTP_PORT>/api/server-info` 查看两架构 Alpine 构建的
  `kernel_version`,与目标机实际加载的内核比对.
- 必要时重新执行 `make build-initramfs` 重建.

### 12.3 大镜像下载被截断(sha 不一致)

- Nginx 默认 `send_timeout=60s`,慢客户端(嵌套虚拟化 / 慢盘)接收缓冲满时会断连.
  BootSeed 的 `web/nginx.conf` 已将 `send_timeout` / `client_body_timeout` 放宽到
  `3600s` 并启用 lingering.若仍被截断,检查是否有前置反向代理 / 负载均衡覆盖了更短
  的超时.
- 确认 Nginx 未关闭 HTTP Range 支持(大镜像断点续传依赖 Range,默认开启).

### 12.4 Secure Boot 节点无法引导

- BootSeed 提供的 iPXE 二进制,Alpine 内核与 initramfs 均未签名,UEFI Secure Boot 开启
  时默认无法加载.
- **解决:在目标节点固件中关闭 Secure Boot 后再进行 PXE 部署.**

### 12.5 端口被占用

- `make validate` 会检查 `HTTP_PORT` / `AGENT_PORT` 占用.UDP 67/69/4011 占用需手动排查:

```bash
ss -lunp | grep -E ':(67|69|4011)\b'
```

---

## 13. 停止与清理

### 13.1 停止服务

```bash
make down      # = docker compose down
```

### 13.2 清理构建产物

`data/` 下会累积大文件(镜像,initramfs,modloop),清理时按需删除:

```bash
# 清理 build/ 下的中间产物(agent / initramfs / ipxe / tmp)
make clean

# 清理 data/ 下的大文件(谨慎,会删除已添加的镜像与生成的引导文件)
# rm -rf data/http/images/*.raw.zst data/http/alpine data/http/boot data/tftp
```

> `data/` 同时是 `bootseed-server` 的持久化目录(bbolt 数据库与镜像清单),删除前确认
> 是否需要保留节点登记记录与镜像元数据.

---

## 附:常用命令速查

```bash
cp .env.example .env && vi .env      # 配置
make init                            # 一键构建产物
make validate                        # 校验配置与产物
make up                              # 启动服务
docker compose ps                    # 查看状态
docker compose logs -f bootseed-pxe  # 看 PXE 引导日志
curl http://<IP>:<HTTP_PORT>/healthz # 健康检查
curl http://<IP>:<HTTP_PORT>/api/server-info
make down                            # 停止
make clean                           # 清理 build/
```
