# BootSeed 实施进度与恢复指南

> **进度权威来源已迁移到根目录 [`AGENTS.md`](../AGENTS.md)(`CLAUDE.md` 软链接到它).**
> 本文件保留为历史快照.恢复工作请优先读 `AGENTS.md`.

本文件用于在会话中断时快速恢复项目实现状态.

最近更新:Go Agent / API / Web / 容器 / 脚本 / 文档 全部首版完成(详见 AGENTS.md §5)

---

## 1. 项目根基

| 文件 | 状态 | 说明 |
| --- | --- | --- |
| `.env.example` | ✅ 已完成 | 全部配置项 + 双架构控制台 + 设备超时 |
| `.gitignore` | ✅ 已完成 | 忽略 build 产物 / Alpine 二进制 / 镜像 |
| `LICENSE` | ✅ 已完成 | Apache-2.0 |
| `Makefile` | ✅ 已完成 | init / build / prepare / validate / test / up / down |
| `docker-compose.yml` | ✅ 已完成 | bootseed-pxe (host net) + bootseed-web |
| `README.md` | ⏳ 待完成 | 最后一步统一写 |
| `docs/IMPLEMENTATION.md` | ✅ 本文件 | 进度跟踪 |

## 2. Go Agent (`agent/`)

| 模块 | 路径 | 状态 |
| --- | --- | --- |
| `go.mod` | `agent/go.mod` | ✅ |
| 架构规范化 | `internal/system/arch.go` + `arch_test.go` | ✅ |
| BootMode | `internal/system/bootmode.go` | ✅ |
| BootContext | `internal/bootcontext/bootcontext.go` + 测试 | ✅ |
| Config | `internal/config/config.go` + 测试 | ✅ |
| Images | `internal/images/images.go` + 测试 | ✅ |
| Hardware | `internal/hardware/hardware.go` | ✅ |
| Drivers | `internal/drivers/drivers.go` | ✅ |
| Disks | `internal/disks/disks.go` + 测试 | ✅ |
| Progress | `internal/progress/progress.go` | ✅ |
| Deploy 状态机 | `internal/deploy/state.go` | ⏳ |
| Deploy pipeline | `internal/deploy/pipeline.go` | ⏳ |
| HTTP API | `internal/api/server.go` + `handlers.go` | ⏳ |
| Embed Web | `web/index.html` `app.js` `style.css` + `embed.go` | ⏳ |
| main 入口 | `cmd/bootseed-agent/main.go` | ⏳ |

## 3. PXE 服务 (`pxe/`)

| 文件 | 状态 |
| --- | --- |
| `Dockerfile` | ⏳ |
| `entrypoint.sh` | ⏳ |
| `dnsmasq.conf.template` | ⏳ |

要点:dnsmasq ProxyDHCP,根据 DHCP Option 93 区分架构 0 / 7 / 9 / 11;为已进入 iPXE 的客户端返回 HTTP boot.ipxe;不要写 dhcp-range 地址池,不要 dhcp-authoritative.

## 4. Web/Nginx (`web/`)

| 文件 | 状态 |
| --- | --- |
| `Dockerfile` | ⏳ |
| `nginx.conf` | ⏳ |
| `boot.ipxe.template` / `x86_64.ipxe` / `aarch64.ipxe` | ⏳(放到 `data/http/boot/`) |

要点:大文件,Range,`Cache-Control: no-store` for index.json.

## 5. Alpine initramfs builder (`alpine/`)

| 文件 | 状态 |
| --- | --- |
| `Dockerfile.builder` | ⏳ |
| `build-initramfs.sh` | ⏳ |
| `modules/network-modules.txt` | ⏳ |
| `modules/storage-modules.txt` | ⏳ |
| `modules/optional-modules.txt` | ⏳ |
| `init.d/bootseed-agent` (OpenRC service) | ⏳ |
| `overlay/etc/issue` 等 | ⏳ |

要点:
- 使用 `apk --root /rootfs add` 拉取对应架构 Alpine.
- `--arch x86_64` / `--arch aarch64`.
- 内核包 `linux-lts`,提取 `vmlinuz` + `modloop` + 模块.
- 用 `modprobe --show-depends` 或 `modinfo -F filename` 递归打包模块.
- 缺失模块写入 `build/reports/missing-optional-modules.txt`.
- bootseed-agent 在 init.d 启动,控制台打印 Web 地址.

## 6. scripts/

| 脚本 | 状态 |
| --- | --- |
| `prepare-ipxe.sh` | ⏳ |
| `prepare-alpine.sh` | ⏳ |
| `build-all-architectures.sh` | ⏳ |
| `validate-architectures.sh` | ⏳ |
| `add-image.sh` | ⏳ |
| `remove-image.sh` | ⏳ |
| `list-images.sh` | ⏳ |
| `validate-images.sh` | ⏳ |
| `validate-config.sh` | ⏳ |
| `generate-config.sh` | ⏳ |
| `smoke-test.sh` | ⏳ |

## 7. data/

| 目录 | 状态 |
| --- | --- |
| `data/tftp/{x86,x86_64,aarch64}/` | ✅ 目录存在,二进制由 `make prepare-ipxe` 生成 |
| `data/http/boot/` | ⏳ 启动脚本由 `generate-config` 生成 |
| `data/http/alpine/{x86_64,aarch64}/` | ⏳ 由 `build-initramfs` 填充 |
| `data/http/images/index.json` | ⏳ 由 `add-image.sh` 创建 |

## 8. 测试

| 测试 | 状态 |
| --- | --- |
| system/arch_test.go | ✅ |
| bootcontext/bootcontext_test.go | ✅ |
| config/config_test.go | ✅ |
| images/images_test.go | ✅ |
| disks/disks_test.go | ✅ |
| deploy/state_test.go | ⏳ |
| deploy/pipeline_test.go(含 raw.zst round-trip) | ⏳ |
| `go vet ./... && go test ./...` 全绿 | ⏳ |
| `docker compose config` | ⏳ |

## 9. 恢复工作的最短指令

```bash
cd /home/superc/code/bootseed
cat docs/IMPLEMENTATION.md
ls agent/internal
```

下一步应继续:
1. `agent/internal/deploy/state.go`(状态机 + 互斥)
2. `agent/internal/deploy/pipeline.go`(下载->hash->解压->hash->写盘->fsync)
3. `agent/internal/api/server.go` + handlers
4. `agent/web/*` + `embed.go`
5. `agent/cmd/bootseed-agent/main.go`
6. 跑 `cd agent && go vet ./... && go test ./...`
7. 写 PXE / Web / Alpine builder / scripts
8. 最后写 README + final verification

## 10. 关键设计约定(避免后续偏离)

- 架构规范:内部一律 `x86_64` / `aarch64`;接受 `amd64` / `arm64` / `x64` 别名.
- 镜像清单 `architecture` 字段必填;后端 `POST /api/deploy` 强制再校验,前端只是过滤.
- ARM64 第一版只支持 UEFI.
- 写盘必须解析到 `/dev/disk/by-id/...`;`ALLOW_UNSTABLE_DISK_NAME=true` 才允许内核名.
- multipath 顶层默认禁止;SAN(iscsi/fc)默认禁止;从属路径永远禁止.
- 部署任务全局互斥锁;并发返回 409.
- 镜像下载 -> 流式解压 -> 流式写盘 -> fsync -> 重新读分区表.
- 不在节点端 `qemu-img` 转换 qcow2.
- 状态:idle / validating / preparing / downloading / writing / syncing / verifying / completed / failed / cancelled.
- 取消:cancel HTTP,关闭 reader,停止写盘,fsync 已写部分,状态置 cancelled,不自动重启.
- 部署运行中禁止 reboot / poweroff.

