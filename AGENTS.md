# BootSeed · Agent 指南与实施进度

> 本文件是 BootSeed 项目的**需求约束 + 进度跟踪权威来源**。会话中断后，
> 先读本文件即可恢复上下文。`CLAUDE.md` 软链接到本文件。
> 每完成一个大块就更新「实施进度」表。

---

## 1. 项目定位

BootSeed 是轻量级、容器化的 PXE 镜像部署工具：通过 PXE 启动一个完全运行在
内存中的 Alpine，由管理员经 Web 页面选择系统镜像与目标磁盘，把 raw 镜像流式
写入物理机/虚拟机系统盘。

统一命名：项目 BootSeed；CLI `bootseed`；节点 Agent `bootseed-agent`；
PXE 服务 `bootseed-pxe`；HTTP 服务 `bootseed-web`；构建器 `bootseed-builder`。

支持架构（内部规范化）：`x86_64`、`aarch64`。别名 `amd64`/`x64`→`x86_64`，
`arm64`→`aarch64`。第一版不支持 32 位 x86 / ARMv7 / ARMv6 / IA64。

## 2. 不做的事（硬边界）

- 不替代/不修改现有 DHCP，不分配 IP/网关/DNS（仅 ProxyDHCP 提供 PXE 引导）。
- 不创建/修改硬件 RAID；不自动登录 iSCSI、不自动连接 NVMe-oF、不改 FC zoning。
- 不在节点端转换 qcow2；不引入 K8s/数据库/消息队列/多租户/调度系统。
- 不信任浏览器提交的镜像元数据；架构校验由后端强制执行。

## 3. 关键设计约定（避免后续偏离）

- 镜像清单 `architecture` 必填；`POST /api/deploy` 后端强制再校验，前端仅过滤。
- ARM64 第一版只支持 UEFI。
- 写盘目标必须解析到 `/dev/disk/by-id/...`；`ALLOW_UNSTABLE_DISK_NAME=true` 才允许内核名。
- multipath 顶层默认禁止（`ALLOW_MULTIPATH_TARGET=true` 放开）；SAN 从属路径永远禁止。
- 部署全局互斥；并发返回 409。部署运行中禁止 reboot/poweroff。
- 流式管道：HTTP → 压缩 sha256 → 解压 → raw sha256 → 写盘 → fsync。
- 取消：cancel ctx、关闭 reader、fsync 已写部分、状态置 cancelled、不自动重启。
- 状态机：idle / validating / preparing / downloading / writing / syncing / verifying / completed / failed / cancelled。
- dnsmasq 仅 ProxyDHCP（`dhcp-range=<subnet>,proxy`），禁止地址池与 `dhcp-authoritative`；
  按 DHCP Option 93 路由架构 0→x86 BIOS / 7,9→x86_64 EFI / 11→aarch64 EFI。
- 缺失内核模块记录为 skipped，绝不因此让构建失败；缺失固件仅告警。

## 4. 目录与产物契约

```
agent/        Go 单一静态二进制（CGO_ENABLED=0；amd64 + arm64）
pxe/          dnsmasq ProxyDHCP 容器（host 网络）
web/          Nginx HTTP 容器 + boot.ipxe.template
alpine/       initramfs 构建器 + 模块清单 + /init
scripts/      iPXE/Alpine/镜像/配置/校验脚本
data/tftp/    x86/undionly.kpxe  x86_64/snponly.efi  aarch64/snponly.efi
data/http/boot/        boot.ipxe x86_64.ipxe aarch64.ipxe（generate-config 生成）
data/http/alpine/<arch>/  vmlinuz initramfs-deploy modloop manifest.json
data/http/images/index.json
build/agent/  bootseed-agent-<arch>
build/reports/ <arch>-modules.txt <arch>-firmware.txt missing-optional-modules.txt
```

入参契约：`alpine/build-initramfs.sh <arch>` 读 `${BUILD_DIR}/agent/bootseed-agent-<arch>`
和 `${DATA_DIR}/http/alpine/<arch>/{vmlinuz,modloop}`，输出 initramfs-deploy + manifest.json。

## 5. 实施进度

图例：✅ 已完成并验证 ｜ 🟡 已实现待真机验证 ｜ ⏳ 未开始 ｜ ⬜ 需联网/真机才能产出

### 5.1 Go Agent（已 `go build/vet/test` 全绿，双架构交叉编译产物 ELF 正确）

| 模块 | 路径 | 状态 |
| --- | --- | --- |
| 架构规范化 + 自检 | `internal/system/arch.go` `power.go` | ✅ |
| BootMode 探测 | `internal/system/bootmode.go` | ✅ |
| BootContext（解析 cmdline） | `internal/bootcontext/` | ✅ |
| Config（.env） | `internal/config/` | ✅ |
| Images（清单 + 兼容校验） | `internal/images/` | ✅ |
| Hardware（PCI/平台/网卡/存储） | `internal/hardware/` | ✅ |
| Drivers（已加载模块） | `internal/drivers/` | ✅ |
| Disks（lsblk/by-id/RAID/multipath/SAN） | `internal/disks/` | ✅ |
| Progress（SSE tracker） | `internal/progress/` | ✅ |
| Deploy 状态机 + 互斥 | `internal/deploy/state.go` | ✅ |
| Deploy 流式管道 | `internal/deploy/pipeline.go` | ✅ |
| HTTP API + SSE | `internal/api/{server,handlers,deploy}.go` | ✅ |
| API 安全门禁测试 | `internal/api/deploy_test.go` | ✅ |
| 内嵌 Web 前端 | `web/{index.html,app.js,style.css,embed.go}` | ✅ |
| main 入口 + 控制台 banner | `cmd/bootseed-agent/main.go` | ✅ |

### 5.2 容器与编排

| 组件 | 路径 | 状态 |
| --- | --- | --- |
| docker-compose | `docker-compose.yml`（`docker compose config` 通过） | ✅ |
| bootseed-pxe | `pxe/{Dockerfile,entrypoint.sh,dnsmasq.conf.template}` | 🟡 |
| bootseed-web | `web/{Dockerfile,nginx.conf,boot.ipxe.template}` | 🟡 |

### 5.3 Alpine / 脚本 / 数据

| 组件 | 路径 | 状态 |
| --- | --- | --- |
| initramfs 构建器 | `alpine/build-initramfs.sh` + `modules/*.txt` + `init.d/` + `overlay/` | ✅（已在容器内实际构建双架构 initramfs，含 Agent/驱动/固件） |
| iPXE 准备 | `scripts/prepare-ipxe.sh` | ✅（已实际构建 x86/x86_64/aarch64 三个产物并通过架构校验） |
| Alpine netboot 下载 | `scripts/prepare-alpine.sh` | 🟡（需联网） |
| 架构校验 | `scripts/validate-architectures.sh` | ✅ |
| 镜像管理 | `scripts/{add,remove,list,validate}-images.sh` | ✅（已本地往返验证） |
| 配置校验 | `scripts/validate-config.sh` | ✅ |
| 启动脚本生成 | `scripts/generate-config.sh` | ✅（已生成 data/http/boot/*.ipxe） |
| 冒烟测试 | `scripts/smoke-test.sh` | ✅ |
| Makefile | `Makefile` | ✅ |
| iPXE/Alpine 二进制产物 | `data/tftp/*` `data/http/alpine/*` | ⬜（由 `make init` 联网产出；iPXE 三件套已在本机产出） |

### 5.4 文档

| 文件 | 状态 |
| --- | --- |
| `AGENTS.md`（本文件，`CLAUDE.md` 软链） | ✅ |
| `README.md`（§30 全部小节） | 🟡 生成中/待校 |
| `docs/IMPLEMENTATION.md` | ✅ |

## 6. 验收场景对照（spec §29）

逻辑层面已实现并可单元验证的：架构区分（场景 2/3/4）、架构不匹配 400（场景 5）、
容量不足拒绝（场景 10）、压缩损坏失败（场景 11，sha256 不一致返回错误）、
取消 cancelled 不重启（场景 12）、SAN/multipath 禁止（场景 13）、缺驱动诊断（场景 14）。

### 6.1 真机 PXE 验证（已在 aicode 用 KVM 虚拟机跑通，2026-06-30）

环境：KVM + QEMU 8.2 + OVMF（UEFI），libvirt default 网络（virbr0 192.168.122.0/24）。
拓扑：bootseed-web 容器（HTTP:8088）+ 单实例 lab dnsmasq 容器（DHCP+TFTP+iPXE 路由，
见下方说明）+ UEFI x86_64 虚拟机（virtio NIC/disk，空盘 2G，serial=BSDISK01）。

**全链路通过**（场景二 x86_64 UEFI）：
PXE(Arch 7) → DHCP 下发 IP+bootfile → TFTP `snponly.efi` → iPXE →
HTTP `boot.ipxe`→`x86_64.ipxe` → `vmlinuz`+`initramfs`(同核版本) → Alpine 内存系统 →
加载 virtio_net/blk/scsi + af_packet → udhcpc 获取 192.168.122.150 →
`bootseed-agent` 启动并打印 `http://192.168.122.150:8088`。

宿主机访问 agent 验证：
- `/api/context`：node/runtime/uname 架构均 x86_64，boot_mode uefi，IP/MAC/UUID/内核版本正确。
- `/api/images`：清单加载，test 镜像 `compatible:true`。
- `/api/disks`：识别 `/dev/disk/by-id/virtio-BSDISK01`（2G，allowed）。
- `POST /api/deploy`（ERASE，verify_raw）：下载→解压→写盘→fsync，state=completed 100%，
  写入 67108864 字节；qcow2 落盘校验出 `EFI PART` GPT 与分区表 —— **镜像写盘真实成功**。

**lab dnsmasq 说明**：单宿主机存在两条硬约束：①同一网桥接口上两个 DHCP 守护进程无法都收到
广播；②agent 沙箱会杀掉非 docker/libvirt 托管的后台进程。因此验证用 *单实例* dnsmasq
（docker 托管，存活）同时承担 DHCP+TFTP+iPXE 路由（即“我也管理 DHCP”的部署模式），
配置见 `/tmp/lab-dnsmasq.conf`。产品的 ProxyDHCP 共存机制在包级别已验证（bootseed-pxe
能收到 PXEClient:Arch:00007 并分类，libvirt DHCP 同时下发 IP），真正双服务器共存需两台
独立主机（产品设计的真实场景）。

待真机/真网验证：ProxyDHCP 与现网 DHCP 真正共存（场景一，需独立两机）、aarch64 全链路
（场景四，需 ARM64 测试机或 QEMU TCG）、真实网卡/RAID（场景 6/7/8）。

### 6.2 真机验证中发现并修复的真实缺陷

1. iPXE v1.21.1 无法在 binutils 2.42 下构建 → 固定 v2.0.0 + EXTRA_CFLAGS。
2. 缺 liblzma-dev / aarch64 交叉编译器 → prepare-ipxe.sh 增加 lzma.h 检测；文档列出依赖。
3. initramfs 构建需在容器内（宿主无 apk）→ Makefile 改 docker build+run。
4. `alpine-baselib` 包不存在 → `alpine-baselayout`。
5. mkinitfs 触发器失败污染 apk 退出码 → apk `--no-scripts` + 显式 depmod。
6. aarch64 跨架构组装 UNTRUSTED → 按架构签名公钥 `/usr/share/apk/keys/<arch>`。
7. 固件子包名错误 → 用 Alpine 实际存在的子包。
8. **vmlinuz(netboot 6.6.134) 与模块(apk 6.6.142) 版本不一致 → 模块全部加载失败**
   → build-initramfs 从同一 linux-lts 重建 vmlinuz+modloop（override_kernel_and_modloop）。
9. busybox applet 链不全（缺 route/tail 等）→ udhcpc 设不上默认路由 → 补全 applet 列表。
10. **缺 af_packet 模块 → udhcpc 无法创建 AF_PACKET 套接字** → 加入 network-modules。
11. NIC 拉起缺 carrier 等待、误遍历 bonding_masters → 增加 carrier 等待 + 过滤真实网卡。
12. add-image 写 format=`zst`/firmware=字符串/`sha256` → 应为 `raw.zst`/数组/`sha256_compressed`
    （对齐 Agent 结构体）；validate-images 同步修正。
13. **disks.go：新版 lsblk 的 rm/ro/rota 输出布尔，旧代码按 json.Number 解析失败**
    → 新增 flexBool 兼容 bool/数字/字符串。

### 6.3 kvm1 真机 PXE 验证（pxe-test，2026-06-30）

环境：物理机 kvm1（RHEL9）+ 网桥 br_100（192.168.100.0/24，有现网 DHCP）。
BootSeed 部署在 aicode（kvm1 的一台 VM，enp1s0=192.168.100.161 接 br_100）上，
PXE_INTERFACE=enp1s0 跑 ProxyDHCP。pxe-test 是 **Legacy BIOS** VM（virtio NIC ROM 本身即 iPXE）。

**全链路通过**：BIOS PXE(Arch 0, user-class iPXE) → 现网 DHCP 发 IP 192.168.100.243 +
BootSeed ProxyDHCP 经 4011 端口 pxe-service 应答 → iPXE HTTP 拉取
`boot.ipxe`→`x86_64.ipxe`→`vmlinuz`→`initramfs`(157MB) → Alpine 内存系统 → agent 启动。
宿主 `curl http://192.168.100.243:8088/api/context`：boot_mode=bios、三处架构均 x86_64、IP/MAC/UUID 正确。
安全策略亦正确生效：无稳定路径的磁盘 `allowed=false`；UEFI-only 镜像对 BIOS 节点 `compatible=false`。

**发现并修复的关键缺陷**：
14. **dnsmasq ProxyDHCP 模式下 `dhcp-boot` 不生效，必须用 `pxe-service`**。原模板只配
    dhcp-boot，导致代理收到 PXE 请求却不发任何应答（抓包确认 .161 零出向），客户端拿不到
    引导信息、PXE 快速失败。修复：`pxe/dnsmasq.conf.template` 改用 `pxe-service`，按
    架构(x86PC/BC_EFI/X86-64_EFI/ARM64_EFI) + iPXE 标签分别下发 HTTP boot.ipxe（已是 iPXE）
    或 TFTP 裸 iPXE 二进制（非 iPXE）。这是 ProxyDHCP 真实可用的前提。
15. （部署提示）aicode 的 .env 已切到 PXE_INTERFACE=enp1s0 / PXE_SERVER_IP=192.168.100.161 /
    PXE_SUBNET=192.168.100.0 以服务 br_100。

### 6.4 真实镜像部署 + VNC 修复（pxe-test，2026-06-30）

将 rocky98-compress.qcow2(1.6G)下载入仓库 → qemu-img 转 5G raw（MBR/BIOS）→ zstd 压成
1.5G `data/http/images/rocky98-x86_64.raw.zst`（firmware=bios），经 BootSeed 部署到 pxe-test
（磁盘加序列号 ROCKY98TEST 获得稳定路径），**写入 5368709120 字节 100% 完成、压缩 sha 校验通过**；
盘内 MBR 0x55AA + GRUB 确认可引导；改磁盘优先后重启，agent 消失即从磁盘启动 rocky 成功。

**发现并修复的缺陷**：
16. **nginx 默认 send_timeout=60s 会截断慢速客户端的大文件下载**。pxe-test 嵌套虚拟化写盘
    仅 ~8-16MB/s，5G 镜像传输历时数分钟，nginx 单次发送阻塞超 60s 即关闭连接，导致下载被
    截断（多帧 zst 解压到帧边界“干净结束”、压缩 sha 不一致——校验正确拦截了截断写盘）。
    修复：`web/nginx.conf` 增大 `send_timeout/client_body_timeout/lingering_*` 到 3600s。
17. **VNC 黑屏**：内核 cmdline `quiet` 抑制启动信息、且 `console=` 末项是 ttyS0（/dev/console
    指向串口），用户态输出（init/agent 横幅）只进串口、VNC(tty0) 收不到。修复：generate-config
    去掉 `quiet`、`console=` 调整为 ttyS0 在前 tty0 在后（tty0 成主控制台）；并让 agent 把横幅
    显式写到 /dev/tty0 + 串口设备，VNC 与串口都显示部署地址。
18. （pxe-test 改动记录）为部署需要在 pxe-test 上：磁盘加 `<serial>ROCKY98TEST</serial>`、
    逐设备 boot order 改为磁盘优先；其余 kvm1 内容未改动。

### 6.5 第二轮改进（pxe-test 复测通过，2026-06-30）

19. **镜像自动转换**：`add-image.sh` 现可直接喂 qcow2/vmdk/vdi 等——用 `qemu-img info`
    解析 `file format:` 判定格式，自动 `qemu-img convert -O raw` + `zstd` 压成 raw.zst，
    并自动推断 `raw_size`（`--raw-size` 变为可选）；转换产物按 `<id>.raw.zst` 命名避免冲突，
    临时文件用 trap 清理。注意早期用 `--output=json` 正则误判为 "file" 导致转换出错，已改为
    解析 `file format:` 行修正。
20. **SSE 进度不更新**：`progress.AddWritten/AddDownloaded` 不触发 broadcast，SSE 只在阶段
    变化时推送 → 网页进度条卡住。修复：`/api/deploy/events` 处理器加 1s `time.Ticker`，
    周期性推送当前快照。复测：写盘过程中每秒一帧、written_bytes 持续增长、百分比平滑上升。
21. **内存系统支持登录**：`/init` 改为后台启动 agent，并在 tty0(VNC)/ttyS0/ttyAMA0 用 getty
    自动登录 root shell（带 respawn 与无 getty 时的 sh 兜底）；PID1 `wait` agent 保活。
    busybox applet 列表补充 getty/setsid/cttyhack/login/clear/less/vi/top 等。
22. **VNC 黑屏 + 控制台输出**：见 §6.4 第 17 条（去 quiet、tty0 设为主控制台、agent 横幅
    写所有控制台设备）。
23. **节点信息换行**：`agent/web` 的网格 CSS 加 `word-break/overflow-wrap`，UUID/部署服务端/
    MAC/内核版本等长字段整行显示（`.grid div.wide { grid-column: 1/-1 }`），不再错位换行。

## 7. 恢复工作的最短指令

```bash
cd /home/superc/code/bootseed
cat AGENTS.md                       # 本文件：需求 + 进度
cd agent && go vet ./... && go test ./...
docker compose config -q
```

## 8. 当前未完成 / 已知限制（诚实清单）

- **构建依赖**（`make prepare-ipxe` 需要）：`git make gcc binutils perl liblzma-dev mtools`，
  aarch64 还需 `gcc-aarch64-linux-gnu binutils-aarch64-linux-gnu`。`prepare-ipxe.sh` 已检测 `lzma.h`。
- **initramfs 在 `bootseed-builder` 容器内构建**（宿主无需 apk）：`make build-initramfs[-arch]`
  会先 `docker build` 构建器镜像，再 `docker run` 执行 `alpine/build-initramfs.sh`。
  关键修复：① 基础包用 `alpine-baselayout`（非 `alpine-baselib`）；② apk 加 `--no-scripts`
  规避 `mkinitfs` 触发器失败污染退出码，并在 `detect_kver` 显式 `depmod` 生成 modules.dep；
  ③ 跨架构组装用按架构签名公钥 `/usr/share/apk/keys/<arch>/`（否则 UNTRUSTED signature）；
  ④ 固件子包用 Alpine 实际存在的名字（bnx2/bnx2x/qed/qlogic/cxgb3/cxgb4/intel/mellanox/netronome）。
  产物：双架构 initramfs 各 ~150MB（linux-lts 依赖拉入完整 linux-firmware，覆盖广但偏大）。
- **iPXE 版本固定为 `v2.0.0`**：旧 tag `v1.21.1` 无法在 binutils 2.42（Ubuntu 24.04）下构建
  （`core/acpi.c` 触发 `-Werror=array-bounds`，且 `.arch i386`+`.code16` 报 “64bit mode not supported on i386”）。
  v2.0.0 含上游 `[build] Fix building with newer binutils`，已在本机成功构建三个架构。
  脚本另加 `IPXE_EXTRA_CFLAGS` 放宽相关 `-Werror` 作为跨工具链兜底。
- iPXE / Alpine 内核 / initramfs / 固件二进制需要 `make init`（联网+root）实际产出，
  仓库内不提交这些大文件；CI 仅校验 Go 代码与配置解析。
- Secure Boot：未实现签名流程，需在固件中关闭 Secure Boot 才能引导未签名组件。
- 容器（pxe/web）与 initramfs `/init` 全链路尚未在真机 PXE 环境跑通（标 🟡）。
- 专有 RAID 工具（storcli/perccli/ssacli/arcconf）不内置，可放 `data/vendor-tools/` 自行注入。
- 真实网卡/RAID 卡兼容性以目标 Alpine 内核实际模块为准，未在所有硬件上验证。

## 9. 改进计划（2026-06-30：P1-P3 + S1-S5 已全部实现并真机验证；S6 待后续）

### 9.1 用户已要求（✅ 已完成）
- **P1 写入速度**：进度信息把 `speed_bps` 明确标注为「写入速度」、`average_bps` 为「平均写入」；
  并补充「下载速度」（按 `downloaded_bytes` 增量计算）。speed 本就按写入字节算，主因是之前进度
  不刷新看不到（已修）。
- **P2 系统启动时间 + 当前时间**：agent 用 `/proc/uptime` 推算系统启动时间，`/api/context` 增加
  `boot_time` / `current_time` / `uptime_seconds`；网页显示启动时间，当前时间由前端本地时钟每秒跳动。
- **P3 Alpine 版本为空**：`AlpineVersion` 仅来自内核参数 `alpine_version=`（boot.ipxe 未传）→ 空。
  修复：build-initramfs 写入 `/etc/alpine-release`(=ALPINE_VERSION)，agent 优先读该文件，回退 cmdline；
  另可在 boot.ipxe 传 `alpine_version=`。

### 9.2 建议补充（✅ S1-S5 本轮已完成；S6 待后续）
- **S1** `/api/deploy/status` 的 `task.state` 始终停在 `preparing`（pipeline 只更新 tracker.stage 未同步
  manager 状态）→ 同步为 downloading/writing/syncing，状态更准确。
- **S2** 网络详情：页面除节点 IP 外，补充网关 / 子网掩码 / DNS（便于现场核对）。
- **S3** 内存系统 RAM 用量：显示总/可用内存（内存系统跑在 RAM 中，便于判断容量与健康）。
- **S4** 部署完成后页面给出醒目「立即重启」入口（现有 auto_reboot 复选，但完成后无显式按钮）。
- **S5** 节点状态/磁盘/镜像定时自动刷新（当前只在加载时拉取一次）。
- **S6** aarch64 全链路：构建并在 ARM64（真机或 QEMU）跑通 PXE→部署（目前仅 x86_64 实测）。


## 11. 服务端门户（bootseed-server）—— ✅ 已实现并真机验证（2026-06-30）

完整设计见 [`docs/SERVER-PORTAL.md`](docs/SERVER-PORTAL.md)。要点：
- 新增 Go 后端 `bootseed-server`（门户 UI + 镜像增删 API + 节点登记），Nginx 反代 `/`、`/api/*`，
  保留 /boot /alpine /images 静态下载；对外仍单一地址 `<PXE_SERVER_IP>:<HTTP_PORT>`。
- **节点列表**：agent 上报（注册/心跳/部署结果）→ server 用嵌入式库 **bbolt** 持久化
  （在线/离线、是否部署过、最后结果、多次部署历史）。
- **镜像增删**上页面：添加支持 URL 拉取 / 服务器本地路径 / 浏览器直传（带转换进度），删除即删条目+文件。
- **鉴权**：简单口令 `PORTAL_TOKEN`（写/管理操作需令牌，下载不鉴权）。
- 实现阶段：P-A 后端骨架+只读门户 → P-B 节点登记 → P-C 镜像管理 → P-D 编排联调。
- 取舍：为节点历史持久化引入嵌入式库 bbolt（纯 Go、非集群），系对原「不引入数据库」的有意放宽（用户明确要求）。

### 11.1 实现与验证结果（pxe-test @ kvm1）
- **P-A** ✅ `server/`(Go) 后端 + bbolt(v1.3.11,纯 Go) + 只读门户;Nginx 反代 `/`、`/api/*` 到
  bootseed-server:9090,保留 /boot /alpine /images 静态;对外单一地址 8088。
- **P-B** ✅ agent `internal/report` 上报(注册/心跳/部署开始/结束);门户节点列表显示
  在线/离线、是否部署过、最后结果、可展开多次部署历史。实测:pxe-test 注册→在线→
  部署 running→completed(5368709120 字节)全程在门户可见。
- **P-C** ✅ 镜像增删:`POST /api/images`(url/path)+ `/api/images/upload`(浏览器直传)→
  qemu-img 转 raw + zstd + 登记(异步任务+进度);`DELETE /api/images/{id}`;
  `PORTAL_TOKEN` 鉴权(无口令写操作 401,实测)。
- **P-D** ✅ docker-compose 新增 bootseed-server;web/nginx.conf 反代;三容器 healthy。
- 新增 .env：`PORTAL_TOKEN`/`NODE_ONLINE_TIMEOUT`/`HEARTBEAT_INTERVAL`。
- 关键文件：`server/**`、`agent/internal/report/report.go`、`agent/cmd/bootseed-agent/main.go`
  与 `internal/api/{server,deploy}.go`(上报接线)、`web/nginx.conf`(反代)、`docker-compose.yml`。
