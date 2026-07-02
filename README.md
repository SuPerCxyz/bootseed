# BootSeed

BootSeed 是一个容器化的 PXE 整盘镜像部署工具。目标节点进入 BootSeed 内存系统后，会自动向服务端门户注册；管理员在服务端门户选择部署镜像和安装目标磁盘，输入 `ERASE` 后执行整盘写入。

项目统一命名：

- `bootseed-pxe`: ProxyDHCP + TFTP
- `bootseed-server`: 服务端门户 + 静态文件服务 + 节点登记
- `bootseed-agent`: 运行在内存系统中的节点代理
- `bootseed-enter`: 现有 Linux 系统一次性进入 BootSeed 的入口脚本

## 文档导航

- 快速部署与使用：本文
- 完整部署说明：[docs/DEPLOY.md](docs/DEPLOY.md)
- 服务端门户设计：[docs/SERVER-PORTAL.md](docs/SERVER-PORTAL.md)
- 设计约束与实施进度：[AGENTS.md](AGENTS.md)

## 项目定位

BootSeed 解决的是“把一个 raw 整盘镜像写到目标磁盘”这件事。

- 适用于裸金属和虚拟机批量重装
- 适用于同二层 PXE 引导场景
- 也适用于节点和服务端不在同一二层时，从现有 Linux 系统执行 `bootseed-enter` 进入 BootSeed
- 服务端门户是统一操作入口，节点进入 BootSeed 后优先在服务端门户发起部署

不做的事情：

- 不替代现网 DHCP
- 不修改 RAID、iSCSI、NVMe-oF、FC zoning
- 不支持 qcow2 直接落盘到节点，节点侧只写 raw 整盘镜像
- 不支持 Secure Boot 签名链

## 支持范围

- 支持架构：`x86_64`、`aarch64`
- 架构别名：`amd64`/`x64` -> `x86_64`，`arm64` -> `aarch64`
- 固件支持：
  - `x86_64`: BIOS 和 UEFI
  - `aarch64`: 仅 UEFI
- 镜像格式：服务端最终统一保存为 `raw.zst`

## 核心特性

- ProxyDHCP 模式与现网 DHCP 共存，不分配 IP
- 节点启动到完全运行在内存中的 Alpine 系统
- 服务端门户统一管理镜像仓库、节点列表、部署历史和部署状态
- 后端强制校验镜像架构与节点架构
- 默认优先使用 `/dev/disk/by-id/...` 稳定磁盘路径
- 流式写盘：下载 -> 校验 -> 解压 -> 校验 -> 写盘 -> `fsync`
- 支持部署状态查看、实时进度、取消部署
- `bootseed-enter` 支持老 GRUB，并支持清理一次性进入配置

## 最小前提

- 一台 Linux 服务端
- 已安装 Docker Engine 和 `docker compose`
- 服务端可以访问目标镜像文件或镜像下载地址
- PXE 场景下，目标节点与 `bootseed-pxe` 所在网卡处于同一二层，或网络侧已配置 DHCP Relay / IP Helper

## 快速开始

1. 准备环境文件。

```bash
cp .env.example .env
```

2. 至少设置以下参数。

```dotenv
PXE_INTERFACE=enp1s0
PXE_SERVER_IP=192.168.100.161
PXE_SUBNET=192.168.100.0
HTTP_PORT=8088
PORTAL_TOKEN=change-me
BOOTSEED_ENTER_SECRET=change-me-enter-secret
```

3. 初始化引导文件和内存系统产物。

```bash
make init
```

4. 启动服务。

```bash
docker compose up -d
```

5. 打开服务端门户。

```text
http://<PXE_SERVER_IP>:<HTTP_PORT>/
```

## 正式使用流程

### 方式一：同二层 PXE 部署

1. 在服务端准备好镜像仓库。
2. 目标节点设置为一次性 PXE 启动。
3. 节点通过 iPXE 进入 BootSeed 内存系统。
4. 节点自动向服务端门户注册。
5. 在服务端门户的节点列表中选择该节点。
6. 点击“部署镜像”，选择部署镜像和安装目标磁盘。
7. 输入 `ERASE` 确认整盘写入。

### 方式二：跨二层或已有系统进入

在目标节点现有 Linux 系统中执行：

```bash
curl -fsSL http://<PXE_SERVER_IP>:<HTTP_PORT>/bootseed-enter.sh -o /usr/local/sbin/bootseed-enter
chmod +x /usr/local/sbin/bootseed-enter
bootseed-enter --server http://<PXE_SERVER_IP>:<HTTP_PORT> --secret <BOOTSEED_ENTER_SECRET>
reboot
```

说明：

- 需要同时指定 `--server` 和 `--secret`
- `--secret` 为必填，用于限制谁可以通过三层脚本进入 BootSeed
- 脚本会采集默认出口网络信息并写入 BootSeed 启动参数
- BootSeed 启动后会恢复该节点的静态网络并自动向服务端门户注册
- 若需清理一次性进入配置，可执行：

```bash
bootseed-enter --cleanup
```

### 部署完成判断

以服务端门户显示为准：

- “部署状态”为 `completed` 表示写盘完成
- 如勾选“部署完成后自动重启”，节点会自动重启
- 如未自动重启，可在节点侧自行重启并从磁盘启动

## 镜像仓库约定

- 服务端门户中的“编辑镜像”只允许修改元数据
- 镜像文件最终以 `raw.zst` 形式保存在服务端
- 镜像描述支持显示和编辑，过长内容在页面中省略显示，悬停可看完整内容

## 关键限制

- PXE 引导本质依赖二层广播；跨三层场景不能仅靠 BootSeed 自身解决，必须用 DHCP Relay / IP Helper，或改走 `bootseed-enter`
- 只支持整盘镜像部署，不做分区级安装
- 默认是破坏性写盘操作，必须输入 `ERASE`
- `aarch64` 第一版仅支持 UEFI
- Secure Boot 未实现

## 常用命令

```bash
make init
docker compose up -d
docker compose ps
docker compose logs -f bootseed-server
docker compose logs -f bootseed-pxe
```

需要更完整的部署、构建、验证和排障说明时，直接看 [docs/DEPLOY.md](docs/DEPLOY.md)。
