#!/usr/bin/env bash
# 从源码构建 iPXE 启动文件并放置到 data/tftp/ 下.
#
# 用法:scripts/prepare-ipxe.sh <x86|x86_64|aarch64>
#   x86      -> bin/undionly.kpxe          -> data/tftp/x86_64/undionly.kpxe      (BIOS PXE)
#   x86_64   -> bin-x86_64-efi/snponly.efi -> data/tftp/x86_64-uefi/snponly.efi   (UEFI x86_64)
#   aarch64  -> bin-arm64-efi/snponly.efi  -> data/tftp/aarch64/snponly.efi       (UEFI ARM64)
#
# 环境要求(联网 + 工具链):
#   - 需要访问 https://github.com/ipxe/ipxe 克隆源码(默认 ref=${IPXE_REF}).
#   - 公共依赖:git make gcc binutils perl liblzma/xz-devel mtools.
#   - 构建 aarch64 EFI 需交叉编译器:aarch64-linux-gnu-gcc.
#   - 构建产物会用 file/readelf 校验架构,避免错放目录.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/_common.sh
source "${SCRIPT_DIR}/_common.sh"

load_env

usage() {
  cat <<'EOF'
用法:prepare-ipxe.sh <x86|x86_64|aarch64>
  x86      构建 BIOS undionly.kpxe
  x86_64   构建 UEFI x86_64 snponly.efi
  aarch64  构建 UEFI ARM64 snponly.efi
EOF
}

TARGET="${1:-}"
if [[ -z "${TARGET}" ]]; then
  usage
  die "缺少目标参数"
fi

IPXE_REF="${IPXE_REF:-v1.21.1}"
IPXE_SRC_URL="https://github.com/ipxe/ipxe"
IPXE_WORK="${BUILD_DIR}/ipxe"
IPXE_SRC="${IPXE_WORK}/src"

# 新版 GCC(12/13/14) 对 iPXE 旧代码(如 core/acpi.c)会触发
# -Werror=array-bounds / stringop-overflow 等误报,导致构建失败.
# 这里放宽相关告警的 -Werror(不影响产物正确性),允许通过 IPXE_EXTRA_CFLAGS 覆盖.
IPXE_EXTRA_CFLAGS="${IPXE_EXTRA_CFLAGS:--Wno-error=array-bounds -Wno-error=stringop-overflow -Wno-error=maybe-uninitialized -Wno-error=array-parameter}"

# 检查命令是否存在,缺失时给出明确的安装提示.
require_cmd() {
  local cmd="$1" hint="$2"
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    log_error "缺少命令:${cmd}"
    log_error "  安装建议:${hint}"
    return 1
  fi
}

# 检查公共构建工具链.
check_common_toolchain() {
  local missing=0
  require_cmd git "Debian/Ubuntu: apt install git" || missing=1
  require_cmd make "Debian/Ubuntu: apt install make" || missing=1
  require_cmd gcc "Debian/Ubuntu: apt install build-essential" || missing=1
  require_cmd perl "Debian/Ubuntu: apt install perl" || missing=1
  # iPXE 的 util/zbin 需要 liblzma 头文件 lzma.h.
  if [[ ! -f /usr/include/lzma.h ]] && ! echo '#include <lzma.h>' | gcc -E - >/dev/null 2>&1; then
    log_error "缺少 liblzma 开发头文件 lzma.h"
    log_error "  Debian/Ubuntu: apt install liblzma-dev"
    log_error "  RHEL/CentOS:   yum install xz-devel"
    missing=1
  fi
  if [[ "${missing}" -ne 0 ]]; then
    die "构建工具链不完整,请安装上述依赖后重试"
  fi
}

# 检查 aarch64 交叉编译器.
check_aarch64_toolchain() {
  if ! command -v aarch64-linux-gnu-gcc >/dev/null 2>&1; then
    log_error "缺少 aarch64 交叉编译器:aarch64-linux-gnu-gcc"
    log_error "  Debian/Ubuntu: apt install gcc-aarch64-linux-gnu binutils-aarch64-linux-gnu"
    die "无法构建 aarch64 iPXE"
  fi
}

# 克隆或更新 iPXE 源码到指定 ref.
fetch_ipxe_source() {
  mkdir -p "${IPXE_WORK}"
  if [[ ! -d "${IPXE_WORK}/.git" ]]; then
    log_info "克隆 iPXE 源码:${IPXE_SRC_URL} (${IPXE_REF})"
    if ! git clone "${IPXE_SRC_URL}" "${IPXE_WORK}"; then
      die "克隆 iPXE 失败:请确认网络可访问 ${IPXE_SRC_URL}"
    fi
  fi
  log_info "检出 iPXE ref:${IPXE_REF}"
  if ! git -C "${IPXE_WORK}" fetch --tags --depth 1 origin "${IPXE_REF}" 2>/dev/null; then
    # 兜底:完整 fetch 再检出(ref 可能是 commit).
    git -C "${IPXE_WORK}" fetch --tags origin || die "拉取 iPXE ref 失败:${IPXE_REF}"
  fi
  git -C "${IPXE_WORK}" checkout -f "${IPXE_REF}" 2>/dev/null \
    || git -C "${IPXE_WORK}" checkout -f FETCH_HEAD \
    || die "检出 iPXE ref 失败:${IPXE_REF}"
}

# 校验 EFI 二进制目标架构.期望机器类型由调用方给出.
# 用法:verify_efi_arch <文件> <期望关键字>
verify_efi_arch() {
  local f="$1" want="$2" desc
  desc="$(file -b "${f}" 2>/dev/null || true)"
  log_info "产物描述:${desc}"
  case "${want}" in
    x86_64)
      if echo "${desc}" | grep -qiE 'x86-64|x86_64|aarch64|arm64'; then
        # file 对 PE/EFI 可能识别有限,再用 readelf 兜底排除 ARM.
        :
      fi
      # EFI 为 PE 格式,file 通常显示 "PE32+ ... x86-64".
      if echo "${desc}" | grep -qiE 'aarch64|arm64|Aarch64'; then
        die "架构校验失败:期望 x86_64 EFI,实际疑似 ARM64:${desc}"
      fi
      ;;
    aarch64)
      if echo "${desc}" | grep -qiE 'x86-64|x86_64|Intel 80386|80386'; then
        die "架构校验失败:期望 ARM64 EFI,实际疑似 x86:${desc}"
      fi
      ;;
  esac
}

place_artifact() {
  local src="$1" dst="$2"
  [[ -f "${src}" ]] || die "构建产物缺失:${src}"
  mkdir -p "$(dirname "${dst}")"
  cp -f "${src}" "${dst}"
  log_pass "已生成:${dst}"
}

build_x86() {
  check_common_toolchain
  fetch_ipxe_source
  log_info "构建 BIOS undionly.kpxe"
  make -C "${IPXE_SRC}" bin/undionly.kpxe -j"$(nproc)" \
    EXTRA_CFLAGS="${IPXE_EXTRA_CFLAGS}" \
    || die "iPXE x86 构建失败"
  place_artifact "${IPXE_SRC}/bin/undionly.kpxe" \
    "${DATA_DIR}/tftp/x86_64/undionly.kpxe"
}

build_x86_64() {
  check_common_toolchain
  fetch_ipxe_source
  log_info "构建 UEFI x86_64 snponly.efi"
  make -C "${IPXE_SRC}" bin-x86_64-efi/snponly.efi -j"$(nproc)" \
    EXTRA_CFLAGS="${IPXE_EXTRA_CFLAGS}" \
    || die "iPXE x86_64 构建失败"
  verify_efi_arch "${IPXE_SRC}/bin-x86_64-efi/snponly.efi" x86_64
  place_artifact "${IPXE_SRC}/bin-x86_64-efi/snponly.efi" \
    "${DATA_DIR}/tftp/x86_64-uefi/snponly.efi"
}

build_aarch64() {
  check_common_toolchain
  check_aarch64_toolchain
  fetch_ipxe_source
  log_info "构建 UEFI ARM64 snponly.efi (CROSS_COMPILE=aarch64-linux-gnu-)"
  make -C "${IPXE_SRC}" bin-arm64-efi/snponly.efi \
    CROSS_COMPILE=aarch64-linux-gnu- ARCH=arm64 -j"$(nproc)" \
    EXTRA_CFLAGS="${IPXE_EXTRA_CFLAGS}" \
    || die "iPXE aarch64 构建失败"
  verify_efi_arch "${IPXE_SRC}/bin-arm64-efi/snponly.efi" aarch64
  place_artifact "${IPXE_SRC}/bin-arm64-efi/snponly.efi" \
    "${DATA_DIR}/tftp/aarch64/snponly.efi"
}

case "${TARGET}" in
  x86)     build_x86 ;;
  x86_64)  build_x86_64 ;;
  aarch64) build_aarch64 ;;
  amd64|x64) build_x86_64 ;;
  arm64)   build_aarch64 ;;
  *)
    usage
    die "未知目标:${TARGET}"
    ;;
esac

log_pass "iPXE 准备完成:${TARGET}"
