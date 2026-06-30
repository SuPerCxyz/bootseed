# BootSeed 服务端门户镜像（多阶段）
# 构建 Go 二进制 → 运行期含 qemu-img + zstd（用于 URL/本地/上传镜像的自动转换）。

FROM golang:1.22-alpine AS build
WORKDIR /src
# 先拷依赖清单以利用缓存
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" \
    -o /out/bootseed-server ./cmd/bootseed-server

FROM alpine:3.20
# qemu-img 转 raw、zstd 压缩、ca-certificates 用于 https URL 拉取
RUN apk add --no-cache qemu-img zstd ca-certificates
COPY --from=build /out/bootseed-server /usr/local/bin/bootseed-server
# 数据根（compose 挂载 ./data:/data）；状态库默认 /data/state/bootseed.db
ENV DATA_ROOT=/data PORTAL_LISTEN=:9090
EXPOSE 9090
ENTRYPOINT ["/usr/local/bin/bootseed-server"]
