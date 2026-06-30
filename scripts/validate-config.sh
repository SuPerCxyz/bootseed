#!/usr/bin/env bash
# 启动前配置校验：对 .env 与产物执行规范 §6 的 18 项检查。
#
# 用法：scripts/validate-config.sh [--strict]
#   默认：产物（iPXE/Alpine/index 等）缺失记 WARN；.env/网卡类问题记 FAIL。
#   --strict：产物缺失也记 FAIL。
#
# 退出码：仅当存在 FAIL 时返回非零。

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/_common.sh
source "${SCRIPT_DIR}/_common.sh"

load_env

STRICT=0
[[ "${1:-}" == "--strict" ]] && STRICT=1

FAIL=0
WARN=0
PASS=0

pass() { printf '  %s[通过]%s %s\n' "${C_GRN}" "${C_RST}" "$*"; PASS=$((PASS + 1)); }
warn() { printf '  %s[告警]%s %s\n' "${C_YEL}" "${C_RST}" "$*"; WARN=$((WARN + 1)); }
fail() { printf '  %s[失败]%s %s\n' "${C_RED}" "${C_RST}" "$*"; FAIL=$((FAIL + 1)); }

# 产物缺失：strict 记 FAIL，否则 WARN。
artifact_missing() {
  if [[ "${STRICT}" -eq 1 ]]; then fail "$*"; else warn "$* （尚未构建）"; fi
}

echo "=== BootSeed 配置校验（配置来源：${BOOTSEED_ENV_FILE:-?}）==="

# ---- 1. PXE_INTERFACE 存在 ----
echo "[1] 检查 PXE_INTERFACE"
if [[ -z "${PXE_INTERFACE:-}" ]]; then
  fail "PXE_INTERFACE 未设置"
elif ip link show "${PXE_INTERFACE}" >/dev/null 2>&1; then
  pass "网卡存在：${PXE_INTERFACE}"
else
  fail "网卡不存在：${PXE_INTERFACE}"
fi

# ---- 2. PXE_INTERFACE 处于 UP ----
echo "[2] 检查 PXE_INTERFACE 状态"
if [[ -n "${PXE_INTERFACE:-}" ]] && ip link show "${PXE_INTERFACE}" >/dev/null 2>&1; then
  if ip link show "${PXE_INTERFACE}" | grep -qE 'state UP|,UP[,>]|<.*\bUP\b'; then
    pass "网卡处于 UP：${PXE_INTERFACE}"
  else
    fail "网卡未 UP：${PXE_INTERFACE}（请 ip link set ${PXE_INTERFACE} up）"
  fi
else
  warn "网卡不存在，跳过状态检查"
fi

# ---- 3. PXE_SERVER_IP 配置在该网卡上 ----
echo "[3] 检查 PXE_SERVER_IP 是否配置在网卡上"
if [[ -z "${PXE_SERVER_IP:-}" ]]; then
  fail "PXE_SERVER_IP 未设置"
elif [[ -n "${PXE_INTERFACE:-}" ]] && ip link show "${PXE_INTERFACE}" >/dev/null 2>&1; then
  if ip -o addr show dev "${PXE_INTERFACE}" 2>/dev/null | grep -qw "${PXE_SERVER_IP}"; then
    pass "${PXE_SERVER_IP} 已配置在 ${PXE_INTERFACE}"
  else
    fail "${PXE_SERVER_IP} 未配置在 ${PXE_INTERFACE}"
  fi
else
  warn "网卡不可用，跳过 IP 归属检查"
fi

# ---- 4. PXE_SUBNET 为合法网络地址 ----
echo "[4] 检查 PXE_SUBNET 是否为网络地址"
if [[ -z "${PXE_SUBNET:-}" ]]; then
  fail "PXE_SUBNET 未设置"
elif python3 - "${PXE_SUBNET}" <<'PY' 2>/dev/null
import ipaddress, sys
s = sys.argv[1]
# 允许带或不带掩码；必须是网络地址（主机位为 0）。
if "/" not in s:
    s = s + "/24"
net = ipaddress.ip_network(s, strict=True)
PY
then
  pass "PXE_SUBNET 合法：${PXE_SUBNET}"
else
  fail "PXE_SUBNET 不是合法网络地址（主机位需为 0）：${PXE_SUBNET}"
fi

# ---- 5/6. HTTP_PORT / AGENT_PORT 合法 ----
valid_port() { [[ "$1" =~ ^[0-9]+$ ]] && (( $1 >= 1 && $1 <= 65535 )); }
echo "[5] 检查 HTTP_PORT"
if valid_port "${HTTP_PORT:-}"; then pass "HTTP_PORT 合法：${HTTP_PORT}"; else fail "HTTP_PORT 非法：${HTTP_PORT:-未设置}"; fi
echo "[6] 检查 AGENT_PORT"
if valid_port "${AGENT_PORT:-}"; then pass "AGENT_PORT 合法：${AGENT_PORT}"; else fail "AGENT_PORT 非法：${AGENT_PORT:-未设置}"; fi

# ---- 7/8. 端口占用（尽力而为）----
port_free() {
  local p="$1"
  if command -v ss >/dev/null 2>&1; then
    ! ss -ltn 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${p}\$"
  elif command -v nc >/dev/null 2>&1; then
    ! nc -z 127.0.0.1 "${p}" >/dev/null 2>&1
  else
    return 0
  fi
}
echo "[7] 检查 HTTP_PORT 占用"
if valid_port "${HTTP_PORT:-}"; then
  if port_free "${HTTP_PORT}"; then pass "HTTP_PORT 空闲：${HTTP_PORT}"; else warn "HTTP_PORT 已被占用：${HTTP_PORT}"; fi
else
  warn "HTTP_PORT 非法，跳过占用检查"
fi
echo "[8] 检查 AGENT_PORT 占用"
if valid_port "${AGENT_PORT:-}"; then
  if [[ "${AGENT_PORT}" == "${HTTP_PORT:-}" ]]; then
    pass "AGENT_PORT 与 HTTP_PORT 同端口（容器内分离），跳过占用检查"
  elif port_free "${AGENT_PORT}"; then pass "AGENT_PORT 空闲：${AGENT_PORT}"; else warn "AGENT_PORT 已被占用：${AGENT_PORT}"; fi
else
  warn "AGENT_PORT 非法，跳过占用检查"
fi

# ---- 9/10/11. 三个 iPXE 文件存在 ----
echo "[9] 检查 iPXE: tftp/x86/undionly.kpxe"
[[ -f "${DATA_DIR}/tftp/x86/undionly.kpxe" ]] && pass "存在 undionly.kpxe" || artifact_missing "缺少 tftp/x86/undionly.kpxe"
echo "[10] 检查 iPXE: tftp/x86_64/snponly.efi"
[[ -f "${DATA_DIR}/tftp/x86_64/snponly.efi" ]] && pass "存在 x86_64 snponly.efi" || artifact_missing "缺少 tftp/x86_64/snponly.efi"
echo "[11] 检查 iPXE: tftp/aarch64/snponly.efi"
[[ -f "${DATA_DIR}/tftp/aarch64/snponly.efi" ]] && pass "存在 aarch64 snponly.efi" || artifact_missing "缺少 tftp/aarch64/snponly.efi"

# ---- 12-17. 六个 Alpine 文件（vmlinuz / initramfs-deploy / modloop × 2 架构）----
i=12
for arch in x86_64 aarch64; do
  for f in vmlinuz initramfs-deploy modloop; do
    echo "[${i}] 检查 Alpine: alpine/${arch}/${f}"
    if [[ -f "${DATA_DIR}/http/alpine/${arch}/${f}" ]]; then
      pass "存在 alpine/${arch}/${f}"
    else
      artifact_missing "缺少 alpine/${arch}/${f}"
    fi
    i=$((i + 1))
  done
done

# ---- 18. 产物架构 + 镜像清单（委派子脚本）----
echo "[18] 委派校验：产物架构 + 镜像清单"
arch_args=()
[[ "${STRICT}" -eq 1 ]] && arch_args+=("--strict")
if bash "${SCRIPT_DIR}/validate-architectures.sh" "${arch_args[@]}" >/dev/null 2>&1; then
  pass "产物架构校验通过（validate-architectures.sh）"
else
  if [[ "${STRICT}" -eq 1 ]]; then
    fail "产物架构校验未通过（详见 validate-architectures.sh）"
  else
    warn "产物架构校验未完全通过（可能尚未构建，详见 validate-architectures.sh）"
  fi
fi
if bash "${SCRIPT_DIR}/validate-images.sh" >/dev/null 2>&1; then
  pass "镜像清单校验通过（validate-images.sh）"
else
  fail "镜像清单校验未通过（详见 validate-images.sh）"
fi

# ---- 汇总 ----
echo ""
echo "=== 校验汇总：PASS=${PASS} WARN=${WARN} FAIL=${FAIL} ==="
if [[ "${FAIL}" -gt 0 ]]; then
  log_fail "配置校验存在 FAIL，需修复后再启动"
  exit 1
fi
if [[ "${WARN}" -gt 0 ]]; then
  log_warn "配置校验通过，但存在 WARN（多为尚未构建的产物）"
else
  log_pass "配置校验全部通过"
fi
exit 0
