#!/usr/bin/env bash
# 生成 iPXE 启动脚本到 data/http/boot/。
#
# 用法：scripts/generate-config.sh
#
# 生成：
#   boot.ipxe       由 pxe/boot.ipxe.template 替换 ${PXE_SERVER_IP} 与 ${HTTP_PORT}。
#                   （${buildarch} 等 iPXE 运行时变量保持原样）
#   x86_64.ipxe     x86_64 架构启动脚本（控制台用 X86_KERNEL_CONSOLE）
#   aarch64.ipxe    aarch64 架构启动脚本（控制台用 ARM64_KERNEL_CONSOLE）
#
# 安全：alpine 路径中的架构是固定字面量（x86_64 / aarch64），不插值不可信输入。
# 幂等：可重复执行；目录不存在时自动创建。

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/_common.sh
source "${SCRIPT_DIR}/_common.sh"

load_env

BOOT_DIR="${DATA_DIR}/http/boot"
TEMPLATE="${ROOT_DIR}/pxe/boot.ipxe.template"
mkdir -p "${BOOT_DIR}"

# ---- 必要变量校验 ----
: "${PXE_SERVER_IP:?未设置 PXE_SERVER_IP}"
: "${HTTP_PORT:?未设置 HTTP_PORT}"
: "${AGENT_PORT:?未设置 AGENT_PORT}"
X86_KERNEL_CONSOLE="${X86_KERNEL_CONSOLE:-console=ttyS0,115200n8 console=tty0}"
ARM64_KERNEL_CONSOLE="${ARM64_KERNEL_CONSOLE:-console=ttyAMA0,115200n8 console=ttyS0,115200n8 console=tty0}"
NETWORK_DEVICE_TIMEOUT="${NETWORK_DEVICE_TIMEOUT:-60}"
STORAGE_DEVICE_TIMEOUT="${STORAGE_DEVICE_TIMEOUT:-90}"

# ---- 生成 boot.ipxe（仅替换两个占位符，保留 iPXE 运行时变量）----
[[ -f "${TEMPLATE}" ]] || die "缺少模板：${TEMPLATE}"
log_info "渲染 boot.ipxe（替换 PXE_SERVER_IP / HTTP_PORT）"
PXE_SERVER_IP="${PXE_SERVER_IP}" HTTP_PORT="${HTTP_PORT}" \
  envsubst '${PXE_SERVER_IP} ${HTTP_PORT}' < "${TEMPLATE}" > "${BOOT_DIR}/boot.ipxe.tmp"
mv -f "${BOOT_DIR}/boot.ipxe.tmp" "${BOOT_DIR}/boot.ipxe"
log_pass "已生成：${BOOT_DIR}/boot.ipxe"

# ---- 生成单架构启动脚本 ----
# 参数：<arch字面量> <控制台参数> <输出文件>
# 注意：${PXE_SERVER_IP}/${HTTP_PORT}/${AGENT_PORT}/控制台/超时 由 shell 展开；
#       ${net0/mac}、${uuid} 等是 iPXE 运行时变量，必须以 \${...} 保持字面量。
gen_arch_script() {
  local arch="$1" console="$2" out="$3"
  cat > "${out}.tmp" <<EOF
#!ipxe
# BootSeed ${arch} 架构启动脚本（由 generate-config.sh 生成，请勿手改）
#
# 流程：设置部署上下文变量 -> 加载 Alpine 内核 + initramfs -> boot。
# 路径中的架构为固定字面量 ${arch}，不接受外部输入插值。

set node_arch ${arch}
set deploy_server http://${PXE_SERVER_IP}:${HTTP_PORT}
set agent_port ${AGENT_PORT}
set node_mac \${net0/mac}
set node_uuid \${uuid}

echo BootSeed: 架构 \${node_arch}, MAC \${node_mac}
echo BootSeed: 部署服务 \${deploy_server}

kernel \${deploy_server}/alpine/${arch}/vmlinuz ${console} modules=loop,squashfs,sd-mod,usb-storage modloop=\${deploy_server}/alpine/${arch}/modloop deploy_server=\${deploy_server} node_arch=\${node_arch} node_mac=\${node_mac} node_uuid=\${node_uuid} agent_port=\${agent_port} network_device_timeout=${NETWORK_DEVICE_TIMEOUT} storage_device_timeout=${STORAGE_DEVICE_TIMEOUT}
initrd \${deploy_server}/alpine/${arch}/initramfs-deploy
boot
EOF
  mv -f "${out}.tmp" "${out}"
  log_pass "已生成：${out}"
}

gen_arch_script "x86_64"  "${X86_KERNEL_CONSOLE}"   "${BOOT_DIR}/x86_64.ipxe"
gen_arch_script "aarch64" "${ARM64_KERNEL_CONSOLE}" "${BOOT_DIR}/aarch64.ipxe"

log_pass "iPXE 启动脚本生成完成：${BOOT_DIR}"
