# BootSeed 服务端统一部署设计

日期: 2026-06-30

实现状态:

- 2026-07-01 已完成实现。
- 已在 `kvm1` 的 `pxe-test` 上完成真实验证与环境回滚。
- 已确认两条链路都能收敛到“server 统一控制、agent 唯一执行”的模型。

## 1. 背景

当前 BootSeed 只完整支持一条主链路:

1. 节点与服务端处于同一二层网络
2. 节点通过 PXE 进入 BootSeed 内存系统
3. 管理员打开节点 agent 页面
4. 在 agent 页面选择镜像和目标磁盘并发起部署

该模型有两个明显限制:

- 节点和服务端不在同一二层网络时,无法依赖 PXE 进入 BootSeed
- 即便节点已经进入 BootSeed,统一管理入口仍然在 agent 页面,服务端门户只能看状态,不能直接完成部署

本设计新增一套统一控制面:

- 对非二层场景,提供 `bootseed-enter` 脚本,使 Linux 节点可从已有系统重启进入 BootSeed 内存系统
- 对二层和非二层两类场景,只要节点已经进入 BootSeed 并在线,都支持在服务端门户直接选择镜像和磁盘进行部署
- 对静态网络节点,在进入 BootSeed 前采集网络配置并传递给内存系统,保证进入后仍可与服务端通信

## 2. 目标

### 2.1 必须实现

- 支持 Linux 节点在已有系统中执行 `bootseed-enter --server <url>` 后重启进入 BootSeed
- `bootseed-enter` 提供清理能力,用于撤销本地 BootSeed 引导文件和一次性启动设置
- `bootseed-enter` 兼容主流 `grub2` 及较老的 grub 变体
- 节点进入 BootSeed 后向服务端注册,服务端门户统一展示节点状态
- 服务端门户支持直接对在线 BootSeed 节点执行部署,不再要求必须打开 agent 页面
- PXE 进入的节点与 `bootseed-enter` 进入的节点共用同一套服务端部署入口
- 静态 IP 节点进入 BootSeed 后优先恢复原系统网络配置,失败时回退 DHCP

### 2.2 第一版不做

- Windows 节点支持
- 多网卡全量恢复
- VLAN / bond / bridge 恢复
- 服务端主动远程登录 OS 执行脚本
- 在 OS 在线阶段由服务端直接远程触发重启进入 BootSeed
- 非 grub bootloader 的广泛兼容

## 3. 总体架构

### 3.1 控制面与执行面

- `bootseed-server`
  - 统一控制面
  - 展示节点、镜像、历史、状态
  - 代理调用在线 BootSeed 节点的 agent API 完成部署

- `bootseed-agent`
  - 唯一执行面
  - 负责镜像校验、磁盘探测、写盘、进度和结果上报

- `bootseed-enter`
  - OS 侧引导入口
  - 负责采集当前系统信息和网络配置
  - 负责下载 BootSeed 启动文件并设置下一次启动进入 BootSeed
  - 不负责写盘

### 3.2 两条入口链路

#### A. PXE 链路

1. 节点通过 PXE 进入 BootSeed
2. agent 向 server 注册
3. server 门户直接发起部署

#### B. `bootseed-enter` 链路

1. Linux 节点执行 `bootseed-enter --server http://<server>:8088`
2. 脚本采集系统与静态网络配置
3. 脚本落地 BootSeed 启动文件并设置下一次进入 BootSeed
4. 节点重启进入 BootSeed
5. agent 向 server 注册
6. server 门户直接发起部署

进入 BootSeed 之后,两条链路完全统一。

## 4. 节点状态模型

服务端统一维护节点对象,不再区分“只能看”与“可以操作”的两套数据结构。

### 4.1 节点来源

- `pxe`
- `bootseed-enter`

### 4.2 生命周期状态

- `os_online`
- `pending_bootseed`
- `bootseed_online`
- `deploying`
- `completed`
- `failed`
- `offline`

### 4.3 网络状态

- `network_mode`
  - `dhcp`
  - `static`

- `network_status`
  - `ok`
  - `fallback_dhcp`
  - `failed`

### 4.4 新增节点字段

- `hostname`
- `origin`
- `lifecycle`
- `network_mode`
- `network_status`
- `bootseed_session_id`
- `last_boot_source`
- `current_stage`
- `last_error`
- `agent_url`
- `management_iface`
- `management_ip`
- `management_gateway`
- `management_dns`

现有字段 `uuid/mac/ip/arch/boot_mode/kernel_version/alpine_version/agent_version/deploys`
继续保留。

## 5. `bootseed-enter` 设计

### 5.1 入口

第一版只提供脚本入口:

```bash
bootseed-enter --server http://<server>:8088
```

清理入口:

```bash
bootseed-enter --cleanup
```

可选支持:

```bash
bootseed-enter --server http://<server>:8088 --cleanup
```

当同时出现 `--cleanup` 时,清理逻辑优先,不执行进入 BootSeed 的准备动作。

### 5.2 主要步骤

1. 检测 root 权限
2. 识别架构和启动模式
3. 识别默认出口网卡
4. 采集静态 IPv4 网络配置
5. 下载:
   - `data/http/alpine/<arch>/vmlinuz`
   - `data/http/alpine/<arch>/initramfs-deploy`
6. 写入本地目录:

```text
/boot/bootseed/
  vmlinuz
  initramfs-deploy
  node-config.json
```

7. 生成 BootSeed 启动项
8. 设置下一次启动进入该启动项
9. 输出提示并由管理员重启,或脚本可选直接 `reboot`

### 5.3 清理逻辑

`bootseed-enter --cleanup` 需要:

- 删除 `/boot/bootseed/` 下的 BootSeed 临时文件
- 尝试删除脚本创建的临时 grub 配置片段
- 清理一次性启动项状态
- 不改动原有默认启动项

清理必须做到幂等:

- 文件不存在时不报硬错误
- 重复执行不会破坏原系统启动配置

### 5.4 grub 兼容策略

第一版支持:

- 常见 `grub2`
- 较老的 grub 变体,前提是系统仍提供 `grub-reboot` / `grub2-reboot` 或等价的一次性启动能力

脚本按顺序探测:

1. `grub2-reboot`
2. `grub-reboot`
3. 常见 grub 配置路径:
   - `/boot/grub2/grub.cfg`
   - `/boot/grub/grub.cfg`
   - `/boot/efi/EFI/*/grub.cfg`

脚本需要适配:

- `grub2-mkconfig`
- `grub-mkconfig`

如果缺少一次性启动能力,脚本应明确失败并提示“不支持当前 bootloader”。

### 5.5 实现备注

- 脚本同时内置在仓库 `scripts/bootseed-enter.sh` 与服务端静态文件 `server/web/bootseed-enter.sh`。
- 服务端对外直接提供 `http://<server>:8088/bootseed-enter.sh` 下载入口。
- 在 Rocky 9.8 上已验证 `grub2-mkconfig + grub2-reboot` 组合可用。

## 6. 静态网络采集与传递

### 6.1 范围

第一版只处理:

- 当前默认出口网卡
- 单张管理网卡
- 单套 IPv4 配置

### 6.2 采集字段

`bootseed-enter` 生成:

```json
{
  "iface": "ens192",
  "mac": "52:54:00:12:34:56",
  "address": "192.168.10.25",
  "prefix_len": 24,
  "gateway": "192.168.10.1",
  "dns": ["223.5.5.5", "8.8.8.8"],
  "server_url": "http://192.168.10.100:8088"
}
```

保存路径:

```text
/boot/bootseed/node-config.json
```

### 6.3 内存系统恢复逻辑

BootSeed `/init` 启动时按以下优先级处理网络:

1. 若检测到本地导入的 `node-config.json`,优先应用静态网络
2. 若静态网络配置失败,记录原因并回退 DHCP
3. 若不存在 `node-config.json`,直接走 DHCP

匹配网卡时优先用 `mac`,避免网卡名变化导致失败。

### 6.4 失败处理

- 找不到匹配网卡:记录错误并回退 DHCP
- 静态 IP 配置失败:记录错误并回退 DHCP
- DHCP 也失败:停留在控制台并打印清晰错误

## 7. 服务端门户增强

### 7.1 节点列表新增展示

列表列建议至少包含:

- 状态
- 主机名
- IP
- MAC
- 架构
- 启动模式
- 来源
- 网络方式
- 最近活动
- 最后结果
- 操作

### 7.2 节点展开详情

- UUID
- Kernel / Alpine / Agent 版本
- 默认出口网卡
- 管理 IP / 网关 / DNS
- 静态网络继承结果
- 磁盘摘要
- 最近部署历史
- 最后错误

### 7.3 操作能力

当节点状态为 `bootseed_online` 时,服务端门户提供:

- 获取镜像列表
- 获取磁盘列表
- 选择镜像
- 选择目标磁盘
- 输入确认词
- 发起部署
- 查看实时进度
- 取消部署

管理员不再需要打开 agent 页完成主流程。

## 8. 服务端直接部署的实现方式

第一版不重写部署逻辑,采用“server 代理 agent”模型。

### 8.1 代理对象

server 对在线 BootSeed 节点调用:

- `GET /api/images`
- `GET /api/disks`
- `POST /api/deploy`
- `GET /api/deploy/status`
- `GET /api/deploy/events`
- `POST /api/deploy/cancel`

### 8.2 设计原则

- server 是统一控制面
- agent 是唯一写盘执行器
- 二层 PXE 节点和 `bootseed-enter` 节点都通过同一 server UI 触发部署
- agent 页保留,作为兜底和调试入口

## 9. 数据与接口变更

### 9.1 server 侧

需要扩展:

- `server/internal/model/model.go`
- `server/internal/store/*`
- `server/internal/api/nodes.go`
- `server/web/*`

新增能力:

- 更丰富的节点注册字段
- 节点来源和网络状态记录
- server 代理 agent 的部署 API

### 9.2 agent 侧

需要扩展:

- `/init` 或相关启动脚本
- `internal/bootcontext`
- `internal/api/context/hardware/deploy`
- `internal/report`
- `web/*` 仅保留兜底页,不再是唯一操作入口

新增能力:

- 读取 `node-config.json`
- 恢复静态网络
- 上报 `origin/network_mode/network_status`

### 9.3 新增脚本

建议新增:

```text
scripts/bootseed-enter.sh
scripts/bootseed-cleanup.sh    # 可选,也可并入 --cleanup
```

第一版更推荐合并为一个脚本:

```text
scripts/bootseed-enter.sh
```

通过 `--cleanup` 分支处理清理逻辑。

## 10. 完整时序

### 10.1 非二层节点

1. 管理员执行 `bootseed-enter --server <url>`
2. 脚本采集网络并写本地配置
3. 脚本设置下一次启动进入 BootSeed
4. 节点重启进入 BootSeed
5. 内存系统恢复静态网络
6. agent 向 server 注册
7. server 节点显示 `bootseed_online`
8. 管理员在 server 页面直接发起部署
9. server 代理 agent 写盘
10. agent 上报结果

### 10.2 二层 PXE 节点

1. 节点通过 PXE 进入 BootSeed
2. agent 向 server 注册
3. server 节点显示 `bootseed_online`
4. 管理员在 server 页面直接发起部署

进入 BootSeed 后两条链路完全一致。

## 11. 失败与回退

### 11.1 `bootseed-enter` 失败

- 下载失败
- 无法写 `/boot/bootseed`
- 无法识别或配置 grub
- 无法设置一次性启动项

处理:

- 立即失败退出
- 不修改默认启动项
- 打印明确错误

### 11.2 BootSeed 内存系统网络失败

- 静态配置读不到
- 网卡匹配失败
- 静态 IP 应用失败

处理:

- 打印错误
- 回退 DHCP
- 若 DHCP 也失败,停留控制台等待人工排障

### 11.3 server 控制失败

- server 无法连 agent
- 节点未正确注册
- 代理调用失败

处理:

- 节点详情显示最后错误
- 保留直接打开 agent 页排障的能力

## 12. 第一版最小可行实现

### 12.1 范围

- Linux only
- 默认出口网卡的 IPv4 静态网络继承
- `bootseed-enter` 支持主流 grub 与老 grub 兼容路径
- 服务端门户直接部署 `bootseed_online` 节点
- agent 页作为兜底

### 12.2 不进入第一版

- Windows
- 多网卡恢复
- VLAN / bond / bridge
- 非 grub 大范围 bootloader 兼容
- server 主动远程登录 OS 执行脚本

## 13. 推荐实施顺序

1. 扩展节点模型和持久化字段
2. 扩展服务端节点列表与详情展示
3. 实现 server 代理 agent 的部署弹窗和进度
4. 实现内存系统静态网络恢复
5. 实现 `bootseed-enter` 和 `--cleanup`
6. 串联非二层全链路验证

## 14. 风险与取舍

- 老 grub 兼容会显著增加脚本分支判断,但比一开始支持多 bootloader 更可控
- 非二层场景最核心风险在于静态网络恢复失败,因此必须先保证控制台可观测性
- server 代理 agent 部署可快速复用现有执行逻辑,是第一版最稳方案
