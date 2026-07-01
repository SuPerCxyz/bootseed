#!/usr/bin/env bash
set -euo pipefail

AGENT_PORT=8088
BOOTSEED_DIR=/boot/bootseed
CFG_JSON="${BOOTSEED_DIR}/node-config.json"
INITRAMFS_FILE="${BOOTSEED_DIR}/initramfs-deploy"
KERNEL_FILE="${BOOTSEED_DIR}/vmlinuz"
GRUB_SCRIPT=/etc/grub.d/42_bootseed_once
GRUB_TITLE="BootSeed One Shot"
NETWORK_DEVICE_TIMEOUT=60
STORAGE_DEVICE_TIMEOUT=90

log() { printf '[bootseed-enter] %s\n' "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }

SERVER_URL=""
CLEANUP=0

usage() {
  cat <<'EOF'
用法:
  bootseed-enter.sh --server http://<server>:8088
  bootseed-enter.sh --cleanup
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --server)
      SERVER_URL="${2:-}"
      shift 2
      ;;
    --cleanup)
      CLEANUP=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      die "未知参数: $1"
      ;;
  esac
done

[[ "$(id -u)" -eq 0 ]] || die "需要 root 权限执行"

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "缺少命令: $1"
}

detect_grub_cmd() {
  for cmd in grub2-reboot grub-reboot; do
    command -v "$cmd" >/dev/null 2>&1 && { printf '%s\n' "$cmd"; return 0; }
  done
  return 1
}

detect_grub_editenv() {
  for cmd in grub2-editenv grub-editenv; do
    command -v "$cmd" >/dev/null 2>&1 && { printf '%s\n' "$cmd"; return 0; }
  done
  return 1
}

detect_grub_mkconfig() {
  for cmd in grub2-mkconfig grub-mkconfig; do
    command -v "$cmd" >/dev/null 2>&1 && { printf '%s\n' "$cmd"; return 0; }
  done
  return 1
}

detect_grub_cfg() {
  local candidates=(
    /boot/grub2/grub.cfg
    /boot/grub/grub.cfg
    /etc/grub2.cfg
    /etc/grub.cfg
  )
  local path
  for path in "${candidates[@]}"; do
    [[ -e "$path" ]] && { printf '%s\n' "$path"; return 0; }
  done
  if compgen -G '/boot/efi/EFI/*/grub.cfg' >/dev/null 2>&1; then
    ls /boot/efi/EFI/*/grub.cfg 2>/dev/null | head -n1
    return 0
  fi
  return 1
}

cleanup_bootseed() {
  local grub_cfg grub_mkconfig grub_editenv
  grub_cfg="$(detect_grub_cfg || true)"
  grub_mkconfig="$(detect_grub_mkconfig || true)"
  grub_editenv="$(detect_grub_editenv || true)"
  rm -rf "${BOOTSEED_DIR}"
  rm -f "${GRUB_SCRIPT}"
  if [[ -n "${grub_cfg}" && -n "${grub_mkconfig}" ]]; then
    "${grub_mkconfig}" -o "${grub_cfg}" >/dev/null
  fi
  if [[ -n "${grub_editenv}" ]]; then
    "${grub_editenv}" - unset next_entry >/dev/null 2>&1 || true
  fi
  log "清理完成"
}

if [[ "${CLEANUP}" -eq 1 ]]; then
  cleanup_bootseed
  exit 0
fi

[[ -n "${SERVER_URL}" ]] || die "必须提供 --server"
SERVER_URL="${SERVER_URL%/}"

for cmd in curl ip awk sed grep gzip cpio uname; do
  require_cmd "$cmd"
done

GRUB_REBOOT="$(detect_grub_cmd || true)"
GRUB_MKCONFIG="$(detect_grub_mkconfig || true)"
GRUB_CFG="$(detect_grub_cfg || true)"
[[ -n "${GRUB_REBOOT}" && -n "${GRUB_MKCONFIG}" && -n "${GRUB_CFG}" ]] || \
  die "未找到兼容的 grub-reboot / grub-mkconfig / grub.cfg"

ARCH_RAW="$(uname -m)"
case "${ARCH_RAW}" in
  x86_64|amd64) ARCH="x86_64" ;;
  aarch64|arm64) ARCH="aarch64" ;;
  *) die "不支持的架构: ${ARCH_RAW}" ;;
esac

if [[ -d /sys/firmware/efi ]]; then
  BOOT_MODE="uefi"
else
  BOOT_MODE="bios"
fi

DEFAULT_IFACE="$(ip route show default 2>/dev/null | awk '/default/ {print $5; exit}')"
[[ -n "${DEFAULT_IFACE}" ]] || die "未找到默认出口网卡"

CIDR="$(ip -o -4 addr show dev "${DEFAULT_IFACE}" scope global | awk '{print $4; exit}')"
[[ -n "${CIDR}" ]] || die "默认出口网卡 ${DEFAULT_IFACE} 没有 IPv4 地址"
ADDRESS="${CIDR%/*}"
PREFIX_LEN="${CIDR#*/}"
GATEWAY="$(ip route show default 2>/dev/null | awk '/default/ {print $3; exit}')"
MAC="$(cat "/sys/class/net/${DEFAULT_IFACE}/address" 2>/dev/null || true)"
UUID="$(cat /sys/class/dmi/id/product_uuid 2>/dev/null || cat /etc/machine-id 2>/dev/null || true)"
HOSTNAME_NOW="$(hostname)"
DNS_JSON="$(awk '/^nameserver[[:space:]]+/ {printf "%s\"%s\"", (count++ ? ", " : ""), $2} END {printf ""}' /etc/resolv.conf)"

mkdir -p "${BOOTSEED_DIR}"

cat >"${CFG_JSON}" <<EOF
{
  "iface": "${DEFAULT_IFACE}",
  "mac": "${MAC}",
  "address": "${ADDRESS}",
  "prefix_len": ${PREFIX_LEN},
  "gateway": "${GATEWAY}",
  "dns": [${DNS_JSON}],
  "server_url": "${SERVER_URL}"
}
EOF

log "下载 BootSeed 启动文件"
curl -fsSL "${SERVER_URL}/alpine/${ARCH}/vmlinuz" -o "${KERNEL_FILE}"
curl -fsSL "${SERVER_URL}/alpine/${ARCH}/initramfs-deploy" -o "${INITRAMFS_FILE}.orig"

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT
mkdir -p "${tmpdir}/root/etc/bootseed"
gzip -dc "${INITRAMFS_FILE}.orig" | (cd "${tmpdir}/root" && cpio -idm --quiet)
cp -f "${CFG_JSON}" "${tmpdir}/root/etc/bootseed/imported-node-config.json"
(cd "${tmpdir}/root" && find . -print0 | cpio --null -o -H newc 2>/dev/null) | gzip -9 > "${INITRAMFS_FILE}"
rm -f "${INITRAMFS_FILE}.orig"

if [[ "${ARCH}" == "x86_64" ]]; then
  KERNEL_CONSOLE='console=ttyS0,115200n8 console=tty0'
else
  KERNEL_CONSOLE='console=ttyAMA0,115200n8 console=ttyS0,115200n8 console=tty0'
fi

cat >"${GRUB_SCRIPT}" <<EOF
#!/bin/sh
exec tail -n +3 \$0
menuentry '${GRUB_TITLE}' {
    linux ${KERNEL_FILE} ${KERNEL_CONSOLE} deploy_server=${SERVER_URL} node_arch=${ARCH} node_mac=${MAC} node_uuid=${UUID} agent_port=${AGENT_PORT} alpine_version= bootseed_origin=bootseed-enter network_device_timeout=${NETWORK_DEVICE_TIMEOUT} storage_device_timeout=${STORAGE_DEVICE_TIMEOUT}
    initrd ${INITRAMFS_FILE}
}
EOF
chmod 0755 "${GRUB_SCRIPT}"

log "重建 grub 配置: ${GRUB_CFG}"
"${GRUB_MKCONFIG}" -o "${GRUB_CFG}" >/dev/null

log "设置下一次启动进入 BootSeed"
"${GRUB_REBOOT}" "${GRUB_TITLE}"

log "准备完成"
log "主机名: ${HOSTNAME_NOW}"
log "网卡: ${DEFAULT_IFACE} ${ADDRESS}/${PREFIX_LEN} gw=${GATEWAY}"
log "来源: ${SERVER_URL}"
log "请执行 reboot 进入 BootSeed 内存系统"
