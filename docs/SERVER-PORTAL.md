# BootSeed 服务端门户（bootseed-server）产品设计文档

> 状态：设计已定稿（2026-06-30），待实现。
> 本文件是「服务端 Web 门户」的权威设计；实现进度在 [`AGENTS.md`](../AGENTS.md) 跟踪。

## 1. 背景与定位

此前服务端只有 `bootseed-web`（Nginx 静态文件服务），无管理界面。本设计新增
**服务端门户 `bootseed-server`**：一个面向管理员的 Web 控制台,集中查看/管理
镜像、内存系统构建、iPXE 文件,并**汇总所有连接过本平台的节点**(在线/历史/部署结果)。

区分两个 Web：
- **服务端门户**(本设计,`http://<PXE_SERVER_IP>:<HTTP_PORT>/`)：总览 + 镜像增删 + 节点列表。
- **节点 agent 页**(`http://<节点IP>:<AGENT_PORT>/`)：单节点的选镜像/选盘/部署操作台(已有)。

## 2. 架构

```
                      ┌─────────────── 192.168.x.x:8088 (对外单一入口) ───────────────┐
   管理员浏览器 ──────▶│  Nginx (bootseed-web)                                          │
                      │   ├─ /boot /alpine /images   → 静态文件(大文件/Range,原样)     │
                      │   └─ /  /api/*               → 反向代理到 bootseed-server       │
                      └───────────────────────────────┬───────────────────────────────┘
                                                       ▼
                                          bootseed-server (Go, 轻量后端)
                                          ├─ 门户 UI(内嵌静态资源)
                                          ├─ /api/images   (增删查 + URL/本地/上传)
                                          ├─ /api/nodes     (节点注册/心跳/部署上报/查询)
                                          ├─ /api/server-info (服务端配置/版本/iPXE 状态)
                                          └─ 嵌入式库 bbolt: data/state/bootseed.db
                                                       ▲
                          节点 agent 上报(注册/心跳/部署结果) │
                  PXE 节点 ───────────────────────────────────┘
```

- **Nginx 继续承担大文件下载**(boot/alpine/images,已调优 Range + send_timeout)。
- **bootseed-server**(新增 Go 服务)承担门户 UI、管理 API、节点登记。Nginx 反代 `/` 与 `/api/*`,对外仍是一个地址一个端口。
- **持久化**：嵌入式 **bbolt**(纯 Go、无 CGO、单文件 `data/state/bootseed.db`),保存节点与部署历史;镜像清单仍以 `data/http/images/index.json` 为权威(门户读写它)。

## 3. 组件与目录

```
agent/                      # 节点 agent(已存在)；新增「向服务端上报」客户端
  internal/report/          # 注册/心跳/部署上报客户端(agent 侧)
server/                     # 新增：服务端门户后端
  cmd/bootseed-server/main.go
  internal/store/           # bbolt 封装(节点/部署历史)
  internal/imagesvc/        # 镜像增删(URL 拉取/本地路径/上传 → 转 raw.zst → 改 index.json)
  internal/nodesvc/         # 节点注册/心跳/部署上报/在线判定
  internal/portalapi/       # HTTP API + 鉴权中间件
  web/                      # 门户前端 index.html/app.js/style.css(亮蓝主题,内嵌)
web/                        # Nginx：反代 / 与 /api/* 到 bootseed-server
docker-compose.yml          # 新增 bootseed-server 服务
data/state/bootseed.db      # bbolt 持久化(运行期生成,不入库)
```

## 4. 节点上报与数据模型

### 4.1 agent → server 上报(节点进入内存系统后)
- **注册** `POST /api/nodes/register`：`{uuid, mac, ip, arch, boot_mode, kernel_version, alpine_version, agent_version}`(开机一次)。
- **心跳** `POST /api/nodes/heartbeat`：`{uuid}`，每 ~15s 一次 → 刷新 `last_seen`。
- **部署上报** `POST /api/nodes/deploy`：
  - 开始：`{uuid, event:"start", image_id, target_disk}`
  - 结束：`{uuid, event:"end", image_id, target_disk, result, bytes_written, error}`
  - result ∈ completed/failed/cancelled。

> agent 已知 `deploy_server`,上报地址即 `${deploy_server}/api/nodes/...`。上报失败不影响本地部署(尽力而为)。

### 4.2 节点记录(bbolt，主键 = UUID)
```jsonc
{
  "uuid": "ad17...", "mac": "52:54:..", "ip": "192.168.100.243",
  "arch": "x86_64", "boot_mode": "bios",
  "kernel_version": "6.6.142-0-lts", "alpine_version": "3.20.3",
  "first_seen": "...", "last_seen": "...",
  "status": "online|offline",          // last_seen 在阈值内 = online
  "deploys": [                          // 多次部署历史(追加)
    {"image_id":"rocky98-x86_64","target_disk":"/dev/disk/by-id/..",
     "started_at":"..","ended_at":"..","result":"completed",
     "bytes_written":5368709120,"error":""}
  ],
  "last_result": "completed",           // 取 deploys 末条
  "deployed_ever": true                 // deploys 非空
}
```
- **在线判定**：`now - last_seen < ONLINE_TIMEOUT`(默认 45s,心跳 15s)。重启进系统后心跳停 → 离线。
- **只要成功 PXE 进过内存系统就注册显示**(即便从未点部署，`deployed_ever=false`)。
- **多次部署**：`deploys` 数组追加,页面可展开看每次结果。

## 5. 镜像管理 API

| 方法 | 路径 | 说明 | 鉴权 |
| --- | --- | --- | --- |
| GET | `/api/images` | 列出镜像(读 index.json) | 否 |
| POST | `/api/images` | 添加：`{mode:"url|path|upload", source, id, name, os, version, architecture, firmware}` → 后台拉取/读取/接收 → qemu-img 转 raw → zstd → 改 index.json,带任务进度 | **是** |
| DELETE | `/api/images/{id}` | 删除条目 + 文件 | **是** |
| GET | `/api/images/jobs/{jobId}` | 添加任务进度(下载/转换/压缩) | 是 |

- **添加三方式**：① URL(服务端下载)② 服务器本地路径 ③ 浏览器上传(大文件不推荐,UI 给出提示)。
- 复用已实现的转换逻辑(qemu-img → raw、zstd、自动 raw_size、sha256、firmware 数组、原子写 index.json)。
- 添加是**异步任务 + 进度**(GB 级耗时),前端轮询/ SSE 显示。

## 6. 鉴权

- **简单口令**：管理/写操作(POST/DELETE `/api/images`、`/api/nodes` 管理类、敏感查询)需令牌。
  - 配置：`.env` 的 `PORTAL_TOKEN`;请求头 `Authorization: Bearer <token>` 或登录后置 Cookie。
  - 前端：首次访问管理操作时弹出口令输入,存 sessionStorage。
- **不鉴权**：节点下载(/boot /alpine /images)、节点 agent 的上报接口(同网段内部上报,用单独的 `NODE_REPORT_TOKEN` 或免鉴权,默认免)、只读总览。
- 非 HTTPS 环境口令为弱保护,README 注明仅适用可信内网。

## 7. 门户页面模块(中文，亮蓝主题，与 agent 页一致)

1. **顶栏**：标题 + 服务健康徽标 + 当前时间。
2. **服务端概览**：PXE_SERVER_IP、HTTP 端口、PXE 网卡/子网、支持架构、Agent/Alpine/iPXE 版本、ProxyDHCP 状态。
3. **镜像仓库**：表格(ID/名称/OS/版本/架构/固件/格式/压缩大小/解压大小/SHA256/下载);
   **[添加镜像]** 按钮(URL/本地/上传 + 进度);每行 **[删除]**;按架构筛选。
4. **内存系统构建**：x86_64 / aarch64 各:内核版本、Alpine 版本、vmlinuz/initramfs/modloop 就绪、
   驱动数、固件包数、构建时间(读 manifest.json)。
5. **iPXE 引导文件**：三架构文件就绪 + 架构(读 server-info)。
6. **节点列表**(核心新增)：
   - 列:UUID(短)、IP、MAC、架构、BIOS/UEFI、**在线/离线**、是否部署过、**最后结果**、最近活动时间。
   - 行展开:多次部署历史(镜像/目标盘/起止/结果/字节/错误)。
   - 筛选:在线 / 已部署 / 未部署 / 失败。
7. **使用指引**：如何一次性 PXE 启动节点;节点启动后去 `http://<节点IP>:8088` 操作;端口/防火墙;ProxyDHCP 共存。

## 8. 配置(.env 新增)

```
PORTAL_TOKEN=<管理口令>           # 留空=管理操作免鉴权(不推荐)
NODE_ONLINE_TIMEOUT=45            # 秒,超时判离线
HEARTBEAT_INTERVAL=15             # agent 心跳秒数
PORTAL_DB=data/state/bootseed.db  # bbolt 路径
```

## 9. docker-compose

新增 `bootseed-server`(host 或 bridge 网络,挂载 data/);Nginx 增加 `location / { proxy_pass bootseed-server }`
与 `location /api/ { proxy_pass bootseed-server }`,保留 /boot /alpine /images 为静态。

## 10. 安全与边界

- 删除镜像/写 index.json 用原子写 + 文件锁(复用 add-image 锁思路)。
- 上传/URL 拉取限制:目标目录固定 data/http/images,文件名按 id 规范化,防路径穿越。
- 节点上报只接受结构化字段,UUID 规范化;不信任上报里的可执行内容。
- bbolt 单文件,定期/变更即写;损坏可重建(节点会重新注册,历史尽力保留)。
- 仍**不做**:多租户、调度、RAID 管理、SAN 自动连接(维持轻量定位)。

## 11. 实现阶段(建议)

1. **P-A 后端骨架**：bootseed-server + bbolt store + server-info + 只读门户(概览/镜像/构建/iPXE)。
2. **P-B 节点登记**：agent 上报客户端(注册/心跳/部署上报)+ server 节点 API + 节点列表 UI。
3. **P-C 镜像管理**：增(URL/本地/上传 + 进度)/删 + 鉴权。
4. **P-D 编排与联调**：compose 加服务 + Nginx 反代 + 真机(pxe-test)验证全链路。

## 12. 已知取舍

- 引入嵌入式库 bbolt(纯 Go,非集群),为满足「节点历史持久化」需求,属对原「不引入数据库」约束的有意放宽。
- 浏览器直传 GB 级镜像体验差,UI 明确提示优先用 URL/本地路径。
- 节点身份以 DMI UUID 为准;UUID 缺失/重复的极端情况按 MAC 兜底。
