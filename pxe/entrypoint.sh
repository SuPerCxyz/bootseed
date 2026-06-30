#!/bin/sh
# ============================================================
# BootSeed PXE 容器入口脚本
# 校验环境变量与 TFTP 引导文件，渲染 dnsmasq 配置并前台运行
# ============================================================
set -eu

TEMPLATE="/etc/bootseed/dnsmasq.conf.template"
RENDERED="/tmp/dnsmasq.conf"
TFTP_ROOT="/var/lib/tftp"

# ------------------------------------------------------------
# 1. 校验必需的环境变量
# ------------------------------------------------------------
missing=""
for var in PXE_INTERFACE PXE_SUBNET PXE_SERVER_IP HTTP_PORT; do
    # 用 eval 间接取值，兼容 POSIX sh（无 ${!var}）
    eval "value=\${$var:-}"
    if [ -z "$value" ]; then
        missing="$missing $var"
    fi
done

if [ -n "$missing" ]; then
    echo "ERROR: 缺少必需的环境变量:$missing" >&2
    exit 1
fi

# ------------------------------------------------------------
# 2. 校验 TFTP 引导文件是否存在（缺失仅告警，不致命）
# ------------------------------------------------------------
for f in x86/undionly.kpxe x86_64/snponly.efi aarch64/snponly.efi; do
    if [ ! -f "$TFTP_ROOT/$f" ]; then
        echo "WARN: 缺少 TFTP 引导文件: $TFTP_ROOT/$f" >&2
    fi
done

# ------------------------------------------------------------
# 3. 渲染 dnsmasq 配置
#    仅替换显式列出的变量，避免误伤配置中的其它 $ 符号
# ------------------------------------------------------------
export PXE_INTERFACE PXE_SUBNET PXE_SERVER_IP HTTP_PORT
envsubst '${PXE_INTERFACE} ${PXE_SUBNET} ${PXE_SERVER_IP} ${HTTP_PORT}' \
    < "$TEMPLATE" > "$RENDERED"

# ------------------------------------------------------------
# 4. 打印渲染结果便于调试
# ------------------------------------------------------------
echo "=== 渲染后的 dnsmasq 配置 ($RENDERED) ==="
cat "$RENDERED"
echo "=== 配置结束，启动 dnsmasq ==="

# ------------------------------------------------------------
# 5. 前台运行 dnsmasq，日志输出到标准输出
# ------------------------------------------------------------
exec dnsmasq -k -C "$RENDERED" --log-facility=-
