# BootSeed initramfs 构建镜像
#
# 该镜像用于在容器内运行 alpine/build-initramfs.sh,把项目目录挂载进来即可.
# 之所以基于 alpine 是因为构建过程大量依赖宿主机 apk 工具来拉取
# 对应架构的 Alpine 软件包并组装 rootfs.
#
# aarch64 模块组装说明:
#   构建宿主机本身是 x86_64,但通过 `apk --arch aarch64 --root <rootfs>`
#   只是把 aarch64 的软件包文件解包到指定目录,并不需要执行 aarch64 二进制,
#   因此无需 QEMU 即可完成 aarch64 initramfs 的文件级组装.
#
# 典型用法(在项目根目录执行):
#   docker build -t bootseed-initramfs-builder -f alpine/Dockerfile.builder alpine
#   docker run --rm \
#     -e BUILD_DIR=/work/build -e DATA_DIR=/work/data \
#     -e ALPINE_BRANCH=v3.20 -e ALPINE_VERSION=3.20.3 -e AGENT_VERSION=0.1.0 \
#     -v "$PWD":/work -w /work \
#     bootseed-initramfs-builder \
#     bash alpine/build-initramfs.sh x86_64

FROM alpine:3.20

# 安装构建 initramfs 所需的全部工具:
#   - bash            构建脚本使用 bash 特性
#   - apk-tools       apk --arch / --root 拉取并组装目标架构 rootfs
#   - kmod            modprobe / modinfo / depmod 解析模块依赖
#   - mkinitfs        提供 initramfs 相关工具与参考
#   - cpio gzip zstd xz  打包 / 压缩 initramfs
#   - file            校验产物架构
#   - squashfs-tools  解包 modloop(squashfs)以提取内核模块
#   - util-linux      blkid 等块设备工具
RUN apk add --no-cache \
        bash \
        apk-tools \
        kmod \
        mkinitfs \
        cpio \
        gzip \
        zstd \
        xz \
        file \
        python3 \
        py3-yaml \
        squashfs-tools \
        util-linux

WORKDIR /work

# 默认进入 bash,实际构建命令由 docker run 时传入.
ENTRYPOINT ["/bin/bash"]
