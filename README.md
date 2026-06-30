# BootSeed

> 容器化的 PXE「内存 Alpine」整盘镜像部署工具。
> 将目标节点通过 PXE 引导进入完全运行在内存中的 Alpine Linux，自动拉起 `bootseed-agent`，
> 由管理员在 Web 页面选择「与节点架构兼容」的镜像与目标磁盘，输入 `ERASE` 后，
> Agent 以流式管线下载 → 校验 → 解压 → 校验 → 写盘 → `fsync`，将 raw 整盘镜像写入磁盘。

本项目的「内存运行 Alpine + 流式写盘」思路参考了
[bin456789/reinstall](https://github.com/bin456789/reinstall)（MIT License）的设计灵感；
BootSeed 为独立实现，源码以 Apache-2.0 授权（见 `LICENSE`）。

> **文档导航**：快速使用见本 README；从零到运行的**完整部署指南**见 [`docs/DEPLOY.md`](docs/DEPLOY.md)；
> 服务端门户设计见 [`docs/SERVER-PORTAL.md`](docs/SERVER-PORTAL.md)；
> 实施进度与设计约束见 [`AGENTS.md`](AGENTS.md)（`CLAUDE.md` 软链至此）。

---

## 1. 简介

BootSeed 用于在数据中心 / 实验室内对裸金属或虚拟机进行整盘镜像批量部署。它不在目标节点本地安装任何软件，
而是通过 PXE 把节点引导进入一个**完全驻留内存**的 Alpine 系统；该系统开机即自动启动 `bootseed-agent`
并在控制台打印 Web 访问地址。管理员打开该页面，挑选与本机 CPU 架构兼容的镜像和目标磁盘，输入 `ERASE`
进行二次确认，Agent 随即把原始（raw）磁盘镜像以流式管线直接写入目标盘。

整套服务由两个容器化组件 + 一个内存系统组件构成：

- **bootseed-pxe**：dnsmasq 提供 ProxyDHCP + TFTP，仅负责 PXE 引导路由，不分配 IP。
- **bootseed-server**：Go 编写，集服务端门户（总览/镜像增删/节点登记）与静态文件服务于一体，
  提供 iPXE 启动脚本、Alpine 启动文件与镜像下载（原生 Range，WriteTimeout=0 防慢写截断）；bbolt 持久化。
- **bootseed-agent**：Go 编写、嵌入 Web UI，运行在内存 Alpine 中，执行硬件探测与写盘部署。

设计目标是**安全、可追溯、架构清晰**：架构不匹配在后端硬拒绝、前端元数据永不被信任、
写盘默认要求稳定设备路径、SAN / multipath 默认禁止、部署任务全局互斥。

## 2. 功能特性

- **双架构支持**：x86_64 与 aarch64（ARM64），统一规范化别名（`amd64`/`x64` → `x86_64`，`arm64` → `aarch64`）。
- **ProxyDHCP 引导**：与现网已有 DHCP 共存，不抢占地址分配，按 DHCP Option 93 自动区分架构与固件。
- **内存系统**：Alpine 完整运行在 RAM 中，部署过程不依赖目标磁盘上的任何已有系统。
- **流式写盘管线**：下载 → sha256 → 解压 → sha256 → 写盘 → `fsync`，全程流式，不落地临时大文件。
- **架构强校验**：后端在 `POST /api/deploy` 时强制校验 `image.architecture == 节点架构`，不匹配返回 HTTP 400。
- **磁盘安全**：仅列出整盘，排除 loop/ram/zram/sr/fd；优先解析稳定路径 `/dev/disk/by-id/`；SAN / multipath 默认禁止。
- **二次确认**：必须输入 `ERASE` 才会执行破坏性写盘。
- **实时进度**：通过 SSE 推送状态机进度，可随时取消。
- **广覆盖驱动**：内置主流网卡、RAID/HBA、NVMe、virtio 等驱动与精选固件子包。
- **服务端门户**：`bootseed-server` 提供总览、镜像增删（URL/本地/上传，自动 qcow2→raw.zst 转换）、
  节点登记（在线/历史/多次部署结果，bbolt 持久化）；节点 agent 自动上报注册/心跳/部署结果。
- **嵌入式 Web UI**：Agent 与门户均为单一静态二进制（`CGO_ENABLED=0`）内嵌前端页面，无外部依赖。

## 3. 架构图

```text
                          现网已有 DHCP 服务器
                         (负责分配 IP/GW/DNS, BootSeed 不碰)
                                   │
                                   ▼
 ┌──────────────────────────────────────────────────────────────┐
 │                     BootSeed 服务主机 (Docker)                   │
 │                                                                │
 │   ┌────────────────────┐        ┌──────────────────────────┐  │
 │   │   bootseed-pxe       │        │     bootseed-server (Go)    │  │
 │   │ dnsmasq ProxyDHCP    │        │ 门户 UI + 管理 API + 节点登记 │  │
 │   │ + TFTP (host network)│        │  /boot/boot.ipxe           │  │
 │   │ UDP 67 / 69 / 4011   │        │  /alpine/<arch>/...        │  │
 │   │ Option93 架构路由     │        │  /images/index.json+raw    │  │
 │   └─────────┬───────────┘        │  (静态下载, 原生 Range)       │  │
 │             │                     │  bbolt 持久化              │  │
 │             │                     └────────────┬─────────────┘  │
 └─────────────┼──────────────────────────────────┼────────────────┘
               │ 1) PXE 请求 / TFTP 下发 iPXE       │ 2) HTTP chain boot.ipxe
               │                                     │    + 下载 vmlinuz/initramfs/镜像
               │                                     │ 3) 节点 agent 向门户上报(注册/心跳/部署)
               ▼                                     ▼
 ┌──────────────────────────────────────────────────────────────┐
 │                         目标节点 (待部署)                        │
 │                                                                │
 │   PXE 固件 ──► iPXE ──► 内存 Alpine (initramfs) ──► bootseed-agent │
 │                                                       │         │
 │                       管理员浏览器 ◄── Web UI / SSE ────┘         │
 │                                                       │         │
 │              选择镜像 + 目标盘 + 输入 ERASE             │         │
 │                                                       ▼         │
 │        下载→sha256→解压→sha256→写盘→fsync ──► 目标磁盘 (raw)       │
 └──────────────────────────────────────────────────────────────┘

 管理员浏览器 ──► 服务端门户 http://<PXE_SERVER_IP>:<HTTP_PORT>/
                  总览 / 镜像增删 / 节点列表(在线·历史·部署结果)
```

服务端为两个容器(docker-compose):`bootseed-pxe`(ProxyDHCP+TFTP)、
`bootseed-server`(门户后端 + 静态文件服务 + bbolt 持久化,替代原 Nginx)。
对外只有 `bootseed-server` 一个地址与端口:`/boot`、`/alpine`、`/images` 为静态文件
(原生 Range,大镜像可断点续传),`/api/*` 为管理 API,`/` 为内嵌门户页面。



## 4. x86_64 / ARM64 支持矩阵

| 能力                    | x86_64                          | aarch64 (ARM64)                 |
| ----------------------- | ------------------------------- | ------------------------------- |
| 架构规范名              | `x86_64`                        | `aarch64`                       |
| 接受的别名              | `amd64`, `x64`                  | `arm64`                         |
| PXE 引导固件            | Legacy BIOS + UEFI              | 仅 UEFI                         |
| iPXE 二进制             | `undionly.kpxe` / `snponly.efi` | `snponly.efi`                   |
| DHCP Option 93          | `0`(BIOS) / `7`,`9`(UEFI)       | `11`(UEFI)                      |
| Alpine 内核 flavor      | `lts`（可配）                   | `lts`（可配）                   |
| 默认串口控制台          | `ttyS0,115200n8`                | `ttyAMA0` + `ttyS0,115200n8`    |
| Agent 构建              | `GOARCH=amd64`, `CGO_ENABLED=0` | `GOARCH=arm64`, `CGO_ENABLED=0` |
| 镜像 `architecture`     | 必须为 `x86_64`                 | 必须为 `aarch64`                |

> 架构不匹配的镜像即使在前端被强行选择，后端 `POST /api/deploy` 也会以 HTTP 400 拒绝。

## 5. Legacy BIOS / UEFI 矩阵

| 架构    | Legacy BIOS              | UEFI                       | 说明                                       |
| ------- | ------------------------ | -------------------------- | ------------------------------------------ |
| x86_64  | 支持（Option 93 = 0）    | 支持（Option 93 = 7 / 9）  | 部分厂商固件在 x86_64 上报 7，统一归 UEFI  |
| aarch64 | 不支持                   | 支持（Option 93 = 11）     | ARM64 第一版仅支持 UEFI                     |

镜像清单中的 `firmware` 字段（`bios` / `uefi`）用于标注镜像本身所需的固件类型，
应与目标节点的实际引导方式一致；选择不当会导致写入的系统无法启动。

## 6. 镜像架构区分方式

- 每条镜像记录在清单 `data/http/images/index.json` 中都有**必填**的 `architecture` 字段（`x86_64` / `aarch64`）。
- 节点架构由 Agent 在内存系统中真实探测（`uname -m` 等），并经 `system` 包规范化。
- **后端强校验**：`POST /api/deploy` 时服务端重新加载清单并校验 `image.architecture == 节点架构`，
  不匹配直接返回 HTTP 400。
- **前端元数据永不被信任**：浏览器提交的镜像信息只用于过滤展示；真正决策依据是服务端重新读取的清单。
- 架构别名在录入（`add-image.sh`）和探测时统一规范化，避免 `amd64` / `arm64` 等写法造成误判。

## 7. ProxyDHCP 工作原理

BootSeed 的 `bootseed-pxe` 容器以 **ProxyDHCP** 模式运行 dnsmasq：

1. 目标节点 PXE 固件广播 DHCP DISCOVER。
2. 现网**已有的** DHCP 服务器照常分配 IP / 网关 / DNS。
3. BootSeed 的 ProxyDHCP **只补充 PXE 引导信息**（boot server / boot file），不分配任何地址。
4. dnsmasq 通过 DHCP Option 93（client-arch）识别架构与固件：
   - `0` → x86 Legacy BIOS，下发 `x86/undionly.kpxe`
   - `7` / `9` → x86_64 UEFI，下发 `x86_64/snponly.efi`
   - `11` → aarch64 UEFI，下发 `aarch64/snponly.efi`
5. 通过 `dhcp-userclass=set:ipxe,iPXE` 识别客户端是否已进入 iPXE：
   - 已是 iPXE → 直接 chain 到 HTTP 上的 `boot/boot.ipxe`
   - 尚未进入 iPXE → 按架构下发对应裸 iPXE 二进制（`tag:!ipxe` 防止重复加载自身造成循环）

关键配置（`pxe/dnsmasq.conf.template`）：`port=0`（关闭 DNS）、`dhcp-range=${PXE_SUBNET},proxy`（ProxyDHCP）、
`enable-tftp`，**没有**普通 DHCP 地址池，**没有** `dhcp-authoritative`。

## 8. 为什么不与现有 DHCP 冲突

- ProxyDHCP 不下发 IP 地址池：配置里只有 `dhcp-range=<subnet>,proxy`，没有可分配地址范围。
- 没有 `dhcp-authoritative`：dnsmasq 不会以「权威」身份抢答或拒绝其它 DHCP 应答。
- 职责分离：现网 DHCP 负责寻址，BootSeed 只负责回应 PXE 选项（boot server + boot file）。
- `port=0` 关闭了 DNS 功能，BootSeed 只承担 PXE/TFTP，不影响名称解析。
- 因此可以**与现有 DHCP 服务器并存**于同一广播域，无需改动现网 DHCP 配置。

## 9. 同 VLAN 部署

最简单的拓扑：BootSeed 主机与目标节点处于**同一 VLAN / 同一广播域**。

- PXE DISCOVER 与 ProxyDHCP 应答都是二层广播，可直接互通。
- 在 `.env` 中设置 `PXE_INTERFACE` 为该 VLAN 上的网卡，`PXE_SUBNET` 为该网段的网络地址，
  `PXE_SERVER_IP` 为该网卡上已配置的 IP。
- `bootseed-pxe` 使用 `network_mode: host` + `NET_RAW` / `NET_ADMIN`，以便监听并回应广播。
- 无需任何 DHCP Relay 或路由器配合。

## 10. 多 VLAN 部署

当目标节点分布在多个 VLAN 时，有两种常见做法：

1. **在每个目标 VLAN 各部署一套 ProxyDHCP**（或一台多臂主机，多个网卡分别接入各 VLAN，
   为每个 VLAN 各起一个 `bootseed-pxe` 实例 / 各自 `.env`）。这样每个广播域内都有本地 ProxyDHCP。
2. **使用 DHCP Relay / IP Helper**：在三层设备上把目标 VLAN 的 DHCP/PXE 广播中继到 BootSeed 主机所在网段
   （同时也需要把广播中继给现网 DHCP）。

无论哪种方式，核心约束是：**ProxyDHCP 必须能「看到」目标节点的广播**，要么本地同广播域，要么经中继到达。

## 11. 跨三层限制

PXE / DHCP 的发现阶段依赖**二层广播**。BootSeed 的应用代码（dnsmasq ProxyDHCP）**无法跨越广播域**——
这是网络协议层面的物理限制，任何应用层逻辑都无法绕过。

因此当目标节点与 BootSeed 主机不在同一广播域（跨三层 / 跨子网）时，必须满足以下条件之一：

- 在三层设备（路由器 / 交换机）上配置 **DHCP Relay / IP Helper**，将目标 VLAN 的广播中继到
  BootSeed 主机所在网段；**同时**也要把广播中继到现网真实 DHCP 服务器。
- 或者直接**在目标 VLAN 内部署一套 ProxyDHCP**（见第 10 节），让广播在本地即可被响应。

请不要期望仅靠 BootSeed 软件配置就「打通」跨三层的 PXE，那超出了广播域的边界。

## 12. Docker 要求

- 已安装 Docker Engine 且支持 `docker compose`（Compose v2 插件）。
- `bootseed-pxe` 需要 `network_mode: host`，因此**仅支持 Linux 宿主机**（host 网络模式在 macOS/Windows Docker Desktop 上不可用）。
- 需要授予容器 `NET_RAW` 与 `NET_ADMIN` 能力以处理 DHCP/PXE 广播。
- 宿主机上 UDP 67 / 69 / 4011 不能被其它服务占用（见第 29 节）。
- 交叉构建 ARM64 产物需要 Docker Buildx + QEMU binfmt（见第 13 节）。

## 13. Buildx 和 ARM64 交叉构建

- **Go Agent** 使用 `CGO_ENABLED=0` 静态构建，天然支持交叉编译：x86_64 用 `GOARCH=amd64`，
  aarch64 用 `GOARCH=arm64`（见 Makefile 的 `build-agent-x86_64` / `build-agent-aarch64`）。
- **Alpine initramfs** 需要在对应架构的 rootfs 中 `apk add`、`depmod`、打包模块。在单机上构建另一架构时，
  依赖 Docker Buildx + QEMU binfmt 模拟运行目标架构容器：

  ```bash
  # 一次性注册 QEMU binfmt（提供多架构模拟）
  docker run --privileged --rm tonistiigi/binfmt --install all
  docker buildx create --use --name bootseed-builder 2>/dev/null || true
  ```

- `make build-all-architectures` 会依次构建 x86_64 与 aarch64 的 Agent 与 initramfs。
- 所有产物在构建后都会用 `file` 校验架构（`make validate-architectures`），防止误把 amd64 产物当作 arm64 发布。

## 14. .env 配置

复制 `.env.example` 为 `.env` 后按实际环境修改。完整变量如下：

| 变量                        | 示例 / 默认                                   | 说明                                                     |
| --------------------------- | --------------------------------------------- | -------------------------------------------------------- |
| `PXE_INTERFACE`             | `ens192`                                      | PXE 网卡名称，必须真实存在并处于 UP 状态                 |
| `PXE_SUBNET`                | `192.168.10.0`                                | PXE 监听子网，必须是网络地址（不是地址池）               |
| `PXE_SERVER_IP`             | `192.168.10.20`                               | PXE 服务端 IP，必须已配置在 `PXE_INTERFACE` 上           |
| `HTTP_PORT`                 | `8080`                                        | Web / iPXE / 镜像 HTTP 端口                              |
| `AGENT_PORT`                | `8080`                                        | 内存系统中 `bootseed-agent` 的监听端口                   |
| `AUTO_REBOOT`               | `false`                                       | 部署完成后是否自动重启目标节点                           |
| `ALLOW_CUSTOM_IMAGE_SERVER` | `false`                                       | 是否允许前端指定自定义镜像服务器（开启会扩大攻击面）     |
| `ALLOW_MULTIPATH_TARGET`    | `false`                                       | 是否允许选择 multipath 顶层设备作为目标盘                |
| `ALLOW_UNSTABLE_DISK_NAME`  | `false`                                       | 是否允许使用没有 by-id 稳定路径的磁盘                    |
| `ENABLE_HTTPS_IMAGES`       | `true`                                        | 是否允许通过 HTTPS 拉取镜像                              |
| `SUPPORTED_ARCHITECTURES`   | `x86_64,aarch64`                              | 受支持架构（逗号分隔，目前仅这两种）                     |
| `ALPINE_VERSION`            | `3.20.3`                                       | Alpine 版本，固定到已验证版本                            |
| `ALPINE_BRANCH`             | `v3.20`                                        | Alpine 分支                                              |
| `ALPINE_X86_KERNEL_FLAVOR`  | `lts`                                          | x86_64 Alpine 内核 flavor                               |
| `ALPINE_ARM64_KERNEL_FLAVOR`| `lts`                                          | aarch64 Alpine 内核 flavor                              |
| `IPXE_REF`                  | `v2.0.0`                                        | iPXE 源码 git tag 或 commit（v2.0.0 适配新版 binutils） |
| `X86_KERNEL_CONSOLE`        | `console=ttyS0,115200n8 console=tty0`           | x86_64 内核控制台参数（tty0 放最后 = VNC 主控制台）      |
| `ARM64_KERNEL_CONSOLE`      | `console=ttyAMA0,115200n8 console=ttyS0,... console=tty0` | aarch64 内核控制台参数                          |
| `NETWORK_DEVICE_TIMEOUT`    | `60`                                           | initramfs 等待网卡就绪的超时（秒）                       |
| `STORAGE_DEVICE_TIMEOUT`    | `90`                                           | initramfs 等待磁盘就绪的超时（秒）                       |
| `AGENT_VERSION`             | `0.1.0`                                        | Agent 版本号，写入 manifest.json                         |
| `PORTAL_TOKEN`              | `bootseed`                                      | 服务端门户管理口令；留空则增删镜像等写操作免鉴权（不推荐）|
| `NODE_ONLINE_TIMEOUT`       | `45`                                            | 节点离线判定阈值（秒），超过该时长无心跳判离线           |
| `HEARTBEAT_INTERVAL`        | `15`                                            | 节点 agent 向门户上报心跳的间隔（秒）                    |

## 15. 初始化

```bash
# 1) 获取代码
git clone <repo-url> bootseed
cd bootseed

# 2) 准备配置
cp .env.example .env
$EDITOR .env            # 按实际网卡 / 子网 / 服务端 IP / 端口修改

# 3) 一次性初始化（需要网络 + root）
#    包含：构建 iPXE、下载 Alpine netboot、构建 Agent 与 initramfs、
#         生成 dnsmasq.conf / boot.ipxe、校验所有产物架构
make init

# 4) 校验配置（18 项启动检查）
make validate

# 5) 启动服务
make up

# 查看日志 / 停止
make logs
make down
```

> **注意**：iPXE、Alpine 内核 / initramfs、固件等二进制产物由 `make init` 在本机构建 / 下载（需要联网），
> **不随仓库提交**（见 `.gitignore`）。务必在受信网络环境下执行初始化。

## 16. 添加 x86_64 镜像

镜像必须是 **raw 整盘镜像**（可压缩）。使用 `add-image.sh` 录入清单：

```bash
scripts/add-image.sh \
  --file /path/to/ubuntu-22.04-x86_64.raw.zst \
  --id ubuntu-2204-x86_64 \
  --name "Ubuntu 22.04 (x86_64)" \
  --os ubuntu --version 22.04 \
  --architecture x86_64 \
  --firmware uefi \
  --raw-size 10737418240 \
  --format zst \
  --description "Ubuntu 22.04 整盘镜像"
```

脚本会：规范化并校验 `architecture`（拒绝未知架构）、校验 `firmware` 为 `bios`/`uefi`、拒绝重复 `id`、
把文件复制到 `data/http/images/`、计算压缩后大小与 sha256，并原子更新 `index.json`（带锁防并发损坏）。
`--raw-size` 为解压后的原始字节数，用于进度展示。

## 17. 添加 ARM64 镜像

与 x86_64 完全一致，仅 `--architecture` 改为 `aarch64`：

```bash
scripts/add-image.sh \
  --file /path/to/ubuntu-22.04-aarch64.raw.zst \
  --id ubuntu-2204-aarch64 \
  --name "Ubuntu 22.04 (ARM64)" \
  --os ubuntu --version 22.04 \
  --architecture aarch64 \
  --firmware uefi \
  --raw-size 10737418240 \
  --format zst
```

ARM64 镜像 `firmware` 必须为 `uefi`（第一版仅支持 UEFI）。
列出 / 删除 / 校验镜像可分别使用 `scripts/list-images.sh`、`scripts/remove-image.sh`、`scripts/validate-images.sh`。

## 18. qcow2 转 raw

BootSeed 只写 **raw** 镜像。若源镜像是 qcow2，需在**准备阶段**（部署主机或制作机上）转换并压缩：

```bash
# 1) qcow2 -> raw
qemu-img convert -f qcow2 -O raw source.qcow2 disk.raw

# 2) 用 zstd 压缩（流式写盘时会被流式解压）
zstd -T0 --long=27 disk.raw -o disk.raw.zst
```

> **重要**：`qemu-img convert` 只改变**磁盘镜像封装格式**，**不会改变镜像内部操作系统的 CPU 架构**。
> 一个 ARM64 系统的 qcow2 转成 raw 后仍然是 ARM64 系统；请按其真实架构用 `--architecture` 录入。
>
> **禁止在目标节点端转换 qcow2**：内存 Alpine 不内置 `qemu-img`，且节点端转换会引入磁盘 / 内存开销与不可控风险。
> 转换必须在准备阶段完成，节点端只做「下载 → 解压 → 写盘」。

## 19. 网卡驱动列表

initramfs 内置的网络驱动模块清单见 `alpine/modules/network-modules.txt`，构建时通过
`modprobe --show-depends` 递归解析依赖后打包。覆盖范围：

- **Intel 有线**：`e1000` `e1000e` `igb` `igc` `ixgbe` `ixgbevf` `i40e` `iavf` `ice`
- **Broadcom**：`tg3` `bnx2` `bnx2x` `bnxt_en`
- **Mellanox / NVIDIA**：`mlx4_core` `mlx4_en` `mlx5_core`
- **QLogic / Marvell**：`qede` `qed`
- **Realtek / Marvell 桌面级**：`sky2` `r8169`
- **Aquantia 万兆**：`atlantic`
- **虚拟化 / 云平台**：`virtio_net` `vmxnet3` `hv_netvsc` `netvsc` `xen-netfront` `ena` `gve` `nfp`
- **华为 / 国产 SoC**：`hns` `hns3` `hinic` `enetc` `stmmac` `thunderx` `thunder_bgx` `octeontx2`
- **基础支撑**：`mii` `mdio` `phylib` `ptp` `pps_core` `crc32` `8021q` `bonding` `bridge`

## 20. RAID / HBA 驱动列表

存储驱动清单见 `alpine/modules/storage-modules.txt`：

- **RAID / HBA 控制器**：`megaraid_sas` `mpt2sas` `mpt3sas` `mpi3mr` `smartpqi` `hpsa` `aacraid`
  `pm80xx` `arcmsr` `3w_sas` `3w_9xxx`
- **SCSI 核心 / 通用块**：`scsi_mod` `sd_mod` `sg` `sr_mod` `ses` `enclosure` `libsas`
  `scsi_transport_sas` `scsi_transport_fc`
- **SATA / AHCI**：`ahci` `libahci` `libata` `ata_piix`
- **虚拟化存储**：`virtio_pci` `virtio_blk` `virtio_scsi` `vmw_pvscsi` `hv_storvsc` `xen-blkfront`
- **NVMe**：`nvme` `nvme_core` `nvme_fabrics` `nvme_tcp`

可选模块（部分内核可能缺失，缺失只记录不报错，见 `alpine/modules/optional-modules.txt`）：
老式 HP Smart Array `cciss`；FC HBA `qla2xxx` `lpfc`；NVMe-over-Fabrics `nvme_rdma` `nvme_fc`。

> FC HBA 与 NVMe-oF 模块**仅用于设备识别与硬件信息采集**，BootSeed 永远不会自动登录 SAN，
> 也不会把 FC / iSCSI / 多路径设备当作默认部署目标。

## 21. 固件包说明

为避免拉取整个庞大的 `linux-firmware`，构建脚本（`alpine/build-initramfs.sh`）只按网卡 / HBA 家族
挑选必要的固件子包，存在则安装、不存在则跳过。候选子包：

```text
linux-firmware-bnx2      linux-firmware-bnx2x     linux-firmware-bnxt_en
linux-firmware-qed       linux-firmware-ice       linux-firmware-i40e
linux-firmware-nfp       linux-firmware-mellanox  linux-firmware-mlxsw_spectrum
linux-firmware-ql2xxx    linux-firmware-qlogic
```

实际打包进各架构 initramfs 的固件包会记录到 `build/reports/<arch>-firmware.txt`，便于核对。

## 22. 自定义额外驱动方式

如需增加上述清单之外的内核模块：

1. 编辑 `alpine/modules/network-modules.txt`、`storage-modules.txt` 或 `optional-modules.txt`，每行一个模块名
   （`#` 开头为注释，空行忽略）。
2. 若模块在某些内核版本可能缺失、又不希望构建失败，放入 `optional-modules.txt`（缺失只记录到
   `build/reports/missing-optional-modules.txt`，不会中断构建）。
3. 重新执行 `make build-initramfs`（或 `make build-all-architectures`）。
   构建脚本会用 `modprobe --show-depends` 递归解析依赖并一并打包，随后 `depmod`。

## 23. 自定义厂商固件方式

部分网卡 / HBA 需要 `linux-firmware` 之外的厂商固件文件。将这些固件放入 `data/vendor-tools/`
（该目录默认在 `.gitignore` 中忽略，不会被提交），并在 initramfs 构建流程中将其拷贝到 `/lib/firmware`
对应路径。请确保固件文件路径与驱动 `request_firmware()` 期望的路径一致。

## 24. 专有 RAID 工具注入

`storcli` / `perccli` / `ssacli` / `arcconf` 等专有 RAID 管理工具**因授权原因不内置**于 BootSeed。
如确需在内存系统中使用（例如部署前需要先配置 RAID 卷），可将对应可执行文件放入 `data/vendor-tools/`，
由构建 / 启动流程注入内存系统。请自行确认这些工具的授权与目标 CPU 架构匹配。

## 25. BIOS / UEFI

- **x86_64**：同时支持 Legacy BIOS 与 UEFI。ProxyDHCP 按 Option 93 自动区分（`0` = BIOS，`7`/`9` = UEFI），
  分别下发 `undionly.kpxe` 或 `snponly.efi`。
- **aarch64**：仅支持 UEFI（Option 93 = `11`），下发 `snponly.efi`。
- 目标节点固件的引导模式应与镜像 `firmware` 字段一致；BIOS 镜像写到 UEFI-only 机器（或反之）会无法启动。
- 启用 Secure Boot 的影响见第 32 节。

## 26. Web 页面使用

BootSeed 有两个 Web，职责不同：

### 26.1 服务端门户 `http://<PXE_SERVER_IP>:<HTTP_PORT>/`（bootseed-server）

部署在服务端的总览与管理控制台（管理员用）。包含：
- **服务端概览**：PXE IP / 端口 / 网卡 / 子网、支持架构、各版本、健康状态。
- **镜像仓库**：列表 + 下载 + **添加镜像**（URL 拉取 / 服务器本地路径 / 浏览器上传，自动 qcow2→raw→zst 转换，带进度）+ **删除**。
- **内存系统构建**：x86_64 / aarch64 内核版本、Alpine 版本、驱动 / 固件数、就绪状态。
- **iPXE 引导文件**：三架构就绪状态。
- **节点列表**：所有连接过平台的节点，在线 / 离线、是否部署过、**最后结果**、点击展开**多次部署历史**；按在线 / 已部署 / 未部署 / 失败筛选。
- **使用指引**。

> 写 / 管理操作（增删镜像）需管理口令 `PORTAL_TOKEN`；节点下载文件与 agent 上报不鉴权。节点进入内存系统后会自动向门户注册 + 心跳 + 上报部署结果。

### 26.2 节点 Agent 页 `http://<节点IP>:<AGENT_PORT>/`（运行在目标节点内存系统中）

目标节点 PXE 引导进入内存 Alpine 后，**控制台会打印该地址**。在管理员浏览器中打开：
1. 页面展示：节点架构 / 引导模式 / 网络信息 / 启动时间 / 内存（`/api/context`）、硬件（`/api/hardware`）、可用镜像（`/api/images`）、可用磁盘（`/api/disks`）。
2. 选择**与节点架构兼容**的镜像（不兼容的会标注 / 不可选）。
3. 选择目标磁盘（高风险盘明确标注，禁止项不可选）。
4. 在确认框中输入 `ERASE` 进行破坏性二次确认。
5. 提交后通过 SSE（`/api/deploy/events`）实时查看进度（含写入速度 / 下载速度）：
   `idle → validating → preparing → downloading → writing → syncing → verifying → completed`。
6. 可随时取消（`/api/deploy/cancel`）；部署完成后可重启 / 关机。部署运行中禁止重启。

节点 Agent REST / SSE 接口一览：

| 方法 | 路径                    | 说明                                     |
| ---- | ----------------------- | ---------------------------------------- |
| GET  | `/api/health`           | 健康检查                                 |
| GET  | `/api/context`          | 引导上下文（架构 / 引导模式 / 网络 / 时间 / 内存）|
| GET  | `/api/hardware`         | 硬件信息（PCI / 网卡 / 磁盘控制器等）    |
| GET  | `/api/drivers`          | 已加载 / 可用驱动                        |
| GET  | `/api/images`           | 可用镜像清单（从服务端加载）             |
| POST | `/api/images/reload`    | 从服务端清单重新加载镜像                 |
| GET  | `/api/disks`            | 可用磁盘（含安全标注）                   |
| POST | `/api/deploy`           | 发起部署（后端强校验架构，409 表示已有任务在跑）|
| GET  | `/api/deploy/status`    | 当前部署状态                             |
| GET  | `/api/deploy/events`    | SSE 实时进度                             |
| POST | `/api/deploy/cancel`    | 取消当前部署                             |
| POST | `/api/reboot`           | 重启目标节点（部署运行中禁止）           |
| POST | `/api/poweroff`         | 关闭目标节点（部署运行中禁止）           |

服务端门户 REST 接口（`http://<PXE_SERVER_IP>:<HTTP_PORT>/api/`）：

| 方法 | 路径                       | 说明                                       |
| ---- | -------------------------- | ------------------------------------------ |
| GET  | `/api/server-info`         | 服务端配置 / 版本 / iPXE / 构建状态        |
| GET  | `/api/images`              | 镜像清单                                   |
| POST | `/api/images`              | 添加镜像（url/path，需口令）               |
| POST | `/api/images/upload`       | 添加镜像（浏览器上传，需口令）             |
| DELETE | `/api/images/{id}`       | 删除镜像（需口令）                         |
| GET  | `/api/images/jobs/{jobId}` | 添加任务进度                               |
| POST | `/api/nodes/register`      | 节点注册（agent 上报）                     |
| POST | `/api/nodes/heartbeat`     | 节点心跳（agent 上报）                     |
| POST | `/api/nodes/deploy`        | 部署开始 / 结束上报（agent 上报）          |
| GET  | `/api/nodes`               | 节点列表（在线 / 历史 / 部署结果）         |

> 部署任务**全局互斥**：已有任务运行时再次 `POST /api/deploy` 返回 HTTP 409。
> 部署运行期间禁止 `reboot` / `poweroff`。

## 27. 磁盘安全

Agent 通过 `lsblk` 枚举磁盘并施加多重安全约束（见 `agent/internal/disks/`）：

- **只列出整盘**（type=disk），不把分区当作目标。
- **排除伪 / 只读设备**：`loop`、`ram`、`zram`、`sr`（光驱）、`fd`，以及 device-mapper 从属设备。
- **稳定路径优先**：写盘必须解析为 `/dev/disk/by-id/...`（优先级 `wwn-` > `nvme-` > `scsi-` >
  `virtio-` > `ata-` > `usb-`）。
- **缺少稳定路径默认禁止**：没有 by-id 路径的磁盘默认不可作为目标；
  只有设置 `ALLOW_UNSTABLE_DISK_NAME=true` 才允许使用内核名（如 `/dev/sda`，存在重命名风险）。

这样可避免因内核设备名（`sda`/`sdb`）在重启或枚举顺序变化时漂移而误写错误磁盘。

## 28. SAN 和 multipath 风险

针对网络存储与多路径设备的额外约束：

- **从属路径永远禁止**：multipath 的底层从属路径（单条物理路径）绝不可作为部署目标。
- **multipath 顶层默认禁止**：多路径聚合后的顶层设备默认禁止，需显式设置 `ALLOW_MULTIPATH_TARGET=true` 才可选。
- **SAN 标记为高风险**：传输类型为 `iscsi` / `fc` / `fcoe` 的磁盘会被标记 `san_risk`，
  默认**显示但禁止**，避免误把远端共享盘整盘擦写。
- BootSeed 永远不会自动登录 SAN（即使内置了 FC/iSCSI 识别驱动，也仅用于信息采集）。

在不确定磁盘归属时，请优先以 `/api/hardware` 与 `/api/disks` 的标注为准，谨慎放开上述开关。

## 29. 防火墙端口

| 协议 / 端口        | 用途                                       | 由谁监听               |
| ------------------ | ------------------------------------------ | ---------------------- |
| UDP 67             | ProxyDHCP（DHCP 服务端口）                 | bootseed-pxe (dnsmasq) |
| UDP 69             | TFTP（下发裸 iPXE 二进制）                  | bootseed-pxe (dnsmasq) |
| UDP 4011           | ProxyDHCP（PXE boot server 端口）          | bootseed-pxe (dnsmasq) |
| TCP `HTTP_PORT`    | HTTP：boot.ipxe / Alpine 启动文件 / 镜像 / 门户 | bootseed-server     |
| TCP `AGENT_PORT`   | 内存系统中 Agent 的 Web / API              | bootseed-agent（目标节点上）|

确保宿主机防火墙放行上述端口，且 UDP 67 / 69 / 4011 未被其它 DHCP/TFTP 服务占用。

## 30. 日志

- **容器日志**：`make logs` 或 `docker compose logs -f`，可查看 dnsmasq 的 DHCP 详细日志
  （`log-dhcp` 已开启，便于排查架构识别与引导链路）与 Nginx 访问日志。
- **节点控制台 banner**：内存 Alpine 启动后会在串口 / 本地控制台打印 BootSeed banner 与 Agent 的
  Web 访问地址；部署进度也会在控制台输出。
- **Agent 接口**：`/api/deploy/status` 与 `/api/deploy/events`（SSE）提供结构化的实时状态与进度。

## 31. 故障排查

- **PXE 不引导**：检查 `make logs` 中 dnsmasq 是否收到对应节点的 DISCOVER；确认 `PXE_INTERFACE` 正确、
  与目标节点同广播域（或已配置 Relay，见第 11 节）；确认 UDP 67/69/4011 未被占用。
- **进入 iPXE 后卡住 / 下载失败**：确认 `bootseed-server` 正常、`HTTP_PORT` 可达、`PXE_SERVER_IP` 正确。
- **没有网卡（NIC）**：内存系统会按 `NETWORK_DEVICE_TIMEOUT` 等待网卡；超时仍无网卡时，
  通过节点控制台 dump 与 `/api/hardware` 查看 PCI 设备与已加载驱动，按第 22 节补充网卡驱动后重建 initramfs。
- **没有磁盘**：内存系统会按 `STORAGE_DEVICE_TIMEOUT` 等待磁盘；超时仍无磁盘时，
  查看 `/api/hardware` 与 `/api/drivers` 确认是否缺少 RAID/HBA 驱动，按第 20 / 22 节补充存储驱动后重建。
- **架构相关报错（HTTP 400）**：说明镜像 `architecture` 与节点真实架构不一致，请核对清单录入。
- **部署返回 409**：已有部署任务在运行，等待其结束或先取消。

## 32. Secure Boot 限制

> **重要**：BootSeed **尚未实现**对 iPXE / 内核 / initramfs 的签名流程。

启用 UEFI **Secure Boot** 的节点默认**无法直接使用** BootSeed 提供的未签名 iPXE 二进制、Alpine 内核与
initramfs——固件会拒绝加载未经信任签名的引导组件。当前唯一可行的做法是：

- 在目标节点**固件中关闭 Secure Boot** 后再进行 PXE 部署。

请勿期望 BootSeed 在开启 Secure Boot 的情况下开箱即用；签名链支持尚在规划之外。

## 33. 已知限制

- **Secure Boot 未支持**：需在固件中关闭（见第 32 节）。
- **aarch64 仅支持 UEFI**：第一版不支持 ARM Legacy 引导。
- **仅支持 raw 整盘镜像**：qcow2 等格式由服务端门户 / `add-image.sh` 自动转换为 raw.zst（见第 18 节），节点端不转换。
- **PXE 受广播域限制**：跨三层必须借助 DHCP Relay / IP Helper 或在目标 VLAN 本地部署 ProxyDHCP（见第 11 节）。
- **host 网络仅限 Linux**：`bootseed-pxe` 依赖 `network_mode: host`，不支持 macOS / Windows 宿主机。
- **构建产物不入库**：iPXE / Alpine / 固件等二进制由 `make init` 在本机生成（需联网），不随仓库提交。
- **整盘擦写不可逆**：写盘是破坏性操作，输入 `ERASE` 前请务必核对目标磁盘。
- **专有 RAID 工具不内置**：`storcli` 等需自行放入 `data/vendor-tools/`（见第 24 节）。
- **服务端门户持久化用嵌入式 bbolt**：节点与部署历史存于 `data/state/bootseed.db`（纯 Go、单文件、非集群）；
  管理口令为简单口令保护，仅适用可信内网，非 HTTPS 环境下为弱保护。
- **浏览器直传大镜像体验差**：GB 级镜像建议用 URL 拉取或服务器本地路径方式添加。

---

## 许可证

本项目以 **Apache License 2.0** 授权，详见 [`LICENSE`](./LICENSE)。

「内存运行 Alpine + 流式写盘」的整体思路参考了
[bin456789/reinstall](https://github.com/bin456789/reinstall)（MIT License）作为灵感来源；
BootSeed 为独立实现，与上述项目无隶属关系。
