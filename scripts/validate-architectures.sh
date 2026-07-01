#!/usr/bin/env bash
# 校验所有构建产物的目标架构,打印 PASS/FAIL 表格.
#
# 用法:scripts/validate-architectures.sh [--strict]
#   默认:产物缺失记为 WARN(尚未构建).
#   --strict:产物缺失记为 FAIL.
#
# 校验对象:
#   data/tftp/x86_64/undionly.kpxe      存在(BIOS/x86_64 PXE)
#   data/tftp/x86_64-uefi/snponly.efi   x86_64 EFI
#   data/tftp/aarch64/snponly.efi       ARM64 EFI
#   build/agent/bootseed-agent-x86_64   x86-64 ELF
#   build/agent/bootseed-agent-aarch64  ARM aarch64 ELF
#   data/http/alpine/x86_64/vmlinuz     存在
#   data/http/alpine/aarch64/vmlinuz    存在

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/_common.sh
source "${SCRIPT_DIR}/_common.sh"

STRICT=0
[[ "${1:-}" == "--strict" ]] && STRICT=1

FAIL_COUNT=0
WARN_COUNT=0
declare -a ROWS=()

# 记录一行结果:名称 状态 详情.
add_row() {
  ROWS+=("$1|$2|$3")
}

# 缺失文件处理:strict 记 FAIL,否则 WARN.
missing() {
  local name="$1"
  if [[ "${STRICT}" -eq 1 ]]; then
    add_row "${name}" "FAIL" "文件缺失"
    FAIL_COUNT=$((FAIL_COUNT + 1))
  else
    add_row "${name}" "WARN" "尚未构建(缺失)"
    WARN_COUNT=$((WARN_COUNT + 1))
  fi
}

# 校验文件 file 描述是否匹配期望模式.
# 用法:check_desc <名称> <文件> <匹配正则> <排斥正则|空>
check_desc() {
  local name="$1" f="$2" want="$3" deny="${4:-}"
  if [[ ! -f "${f}" ]]; then
    missing "${name}"
    return
  fi
  local desc
  desc="$(file -b "${f}" 2>/dev/null || true)"
  if [[ -n "${deny}" ]] && echo "${desc}" | grep -qiE "${deny}"; then
    add_row "${name}" "FAIL" "架构不符:${desc}"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    return
  fi
  if echo "${desc}" | grep -qiE "${want}"; then
    add_row "${name}" "PASS" "${desc}"
  else
    add_row "${name}" "FAIL" "未匹配期望架构:${desc}"
    FAIL_COUNT=$((FAIL_COUNT + 1))
  fi
}

# 仅校验存在性.
check_exists() {
  local name="$1" f="$2"
  if [[ -f "${f}" ]]; then
    add_row "${name}" "PASS" "存在"
  else
    missing "${name}"
  fi
}

# ---- iPXE TFTP 产物 ----
check_exists "tftp/x86_64/undionly.kpxe" "${DATA_DIR}/tftp/x86_64/undionly.kpxe"
# EFI 为 PE32+;x86_64 期望含 x86-64,排斥 aarch64.
check_desc "tftp/x86_64-uefi/snponly.efi" "${DATA_DIR}/tftp/x86_64-uefi/snponly.efi" \
  'x86-64|x86_64|PE32\+' 'aarch64|arm64'
check_desc "tftp/aarch64/snponly.efi" "${DATA_DIR}/tftp/aarch64/snponly.efi" \
  'aarch64|Aarch64|PE32\+' 'x86-64|Intel 80386'

# ---- Agent ELF 产物 ----
check_desc "agent/bootseed-agent-x86_64" "${BUILD_DIR}/agent/bootseed-agent-x86_64" \
  'ELF.*x86-64' 'aarch64|ARM'
check_desc "agent/bootseed-agent-aarch64" "${BUILD_DIR}/agent/bootseed-agent-aarch64" \
  'ELF.*(aarch64|ARM aarch64)' 'x86-64|Intel 80386'

# ---- Alpine 内核 ----
check_exists "alpine/x86_64/vmlinuz" "${DATA_DIR}/http/alpine/x86_64/vmlinuz"
check_exists "alpine/aarch64/vmlinuz" "${DATA_DIR}/http/alpine/aarch64/vmlinuz"

# ---- 打印表格 ----
printf '\n%-34s %-6s %s\n' "产物" "状态" "详情"
printf '%-34s %-6s %s\n' "----------------------------------" "------" "------------------------------"
for row in "${ROWS[@]}"; do
  IFS='|' read -r name status detail <<<"${row}"
  local_color=""
  case "${status}" in
    PASS) local_color="${C_GRN}" ;;
    WARN) local_color="${C_YEL}" ;;
    FAIL) local_color="${C_RED}" ;;
  esac
  printf '%-34s %s%-6s%s %s\n' "${name}" "${local_color}" "${status}" "${C_RST}" "${detail}"
done

echo ""
log_info "汇总:FAIL=${FAIL_COUNT} WARN=${WARN_COUNT}"
if [[ "${FAIL_COUNT}" -gt 0 ]]; then
  log_fail "架构校验存在失败项"
  exit 1
fi
log_pass "架构校验通过(无 FAIL)"
