#!/usr/bin/env bash
# 下载 Alpine netboot 内核与 modloop 到 data/http/alpine/<arch>/。
#
# 用法：scripts/prepare-alpine.sh <x86_64|aarch64>
#   下载 vmlinuz-<flavor> -> data/http/alpine/<arch>/vmlinuz
#         modloop-<flavor> -> data/http/alpine/<arch>/modloop
#
# 说明：
#   - 来源：https://dl-cdn.alpinelinux.org/alpine/${ALPINE_BRANCH}/releases/<apk_arch>/netboot/
#   - flavor 由 ALPINE_X86_KERNEL_FLAVOR / ALPINE_ARM64_KERNEL_FLAVOR 决定（默认 lts）。
#   - 幂等：若目标文件已存在且 sha256 校验通过则跳过下载。
#   - 无网络时给出明确错误。

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/_common.sh
source "${SCRIPT_DIR}/_common.sh"

load_env

ARCH_IN="${1:-}"
[[ -n "${ARCH_IN}" ]] || die "用法：prepare-alpine.sh <x86_64|aarch64>"

ARCH="$(normalize_arch "${ARCH_IN}")" || exit 1
APK_ARCH="$(alpine_apk_arch "${ARCH}")" || exit 1

ALPINE_BRANCH="${ALPINE_BRANCH:-v3.20}"
ALPINE_VERSION="${ALPINE_VERSION:-}"

# 选择内核 flavor。
case "${ARCH}" in
  x86_64)  FLAVOR="${ALPINE_X86_KERNEL_FLAVOR:-lts}" ;;
  aarch64) FLAVOR="${ALPINE_ARM64_KERNEL_FLAVOR:-lts}" ;;
esac

BASE_URL="https://dl-cdn.alpinelinux.org/alpine/${ALPINE_BRANCH}/releases/${APK_ARCH}/netboot"
DEST_DIR="${DATA_DIR}/http/alpine/${ARCH}"
mkdir -p "${DEST_DIR}"

# 选择下载器。
DL=""
if command -v curl >/dev/null 2>&1; then
  DL="curl"
elif command -v wget >/dev/null 2>&1; then
  DL="wget"
else
  die "未找到 curl 或 wget，无法下载 Alpine netboot 文件"
fi

# 下载 URL 到本地文件；失败返回非零。
fetch() {
  local url="$1" out="$2"
  if [[ "${DL}" == "curl" ]]; then
    curl -fSL --connect-timeout 15 --retry 2 -o "${out}" "${url}"
  else
    wget -q -O "${out}" "${url}"
  fi
}

# 探测 URL 是否存在（HEAD）。
url_exists() {
  local url="$1"
  if [[ "${DL}" == "curl" ]]; then
    curl -fsIL --connect-timeout 15 -o /dev/null "${url}" 2>/dev/null
  else
    wget -q --spider "${url}" 2>/dev/null
  fi
}

# 给定远程文件名前缀，返回实际可用的文件名（优先带版本号）。
# Alpine netboot 目录通常包含：
#   vmlinuz-lts、modloop-lts、initramfs-lts （不带版本号）
#   netboot-<ver> 子目录有时也存在；这里优先使用不带版本号的稳定名。
remote_name() {
  local prefix="$1"  # 例如 vmlinuz 或 modloop
  local plain="${prefix}-${FLAVOR}"
  echo "${plain}"
}

# 下载并（若可能）用 .sha256 校验，幂等跳过。
download_one() {
  local remote="$1" local_name="$2"
  local url="${BASE_URL}/${remote}"
  local dest="${DEST_DIR}/${local_name}"
  local sha_url="${url}.sha256"
  local tmp_sha="${dest}.sha256.tmp"

  # 若已存在，尝试用远程 sha256 校验；校验通过则跳过。
  if [[ -s "${dest}" ]]; then
    if fetch "${sha_url}" "${tmp_sha}" 2>/dev/null; then
      local want got
      want="$(awk '{print $1}' "${tmp_sha}" 2>/dev/null | head -n1)"
      got="$(sha256sum "${dest}" | awk '{print $1}')"
      rm -f "${tmp_sha}"
      if [[ -n "${want}" && "${want}" == "${got}" ]]; then
        log_info "已存在且校验通过，跳过：${dest}"
        return 0
      fi
      log_warn "已存在但 sha256 不匹配，重新下载：${dest}"
    else
      rm -f "${tmp_sha}"
      log_info "已存在（无远程校验文件），跳过：${dest}"
      return 0
    fi
  fi

  log_info "下载：${url}"
  if ! url_exists "${url}"; then
    die "远程文件不存在或网络不可达：${url}"
  fi
  local tmp="${dest}.tmp"
  if ! fetch "${url}" "${tmp}"; then
    rm -f "${tmp}"
    die "下载失败：${url}（请检查网络连通性）"
  fi

  # 下载后若有 sha256 则校验。
  if fetch "${sha_url}" "${tmp_sha}" 2>/dev/null; then
    local want got
    want="$(awk '{print $1}' "${tmp_sha}" 2>/dev/null | head -n1)"
    got="$(sha256sum "${tmp}" | awk '{print $1}')"
    rm -f "${tmp_sha}"
    if [[ -n "${want}" && "${want}" != "${got}" ]]; then
      rm -f "${tmp}"
      die "sha256 校验失败：${url}（期望 ${want}，实际 ${got}）"
    fi
    log_pass "sha256 校验通过：${local_name}"
  fi

  mv -f "${tmp}" "${dest}"
  log_pass "已下载：${dest}"
}

log_info "准备 Alpine netboot：arch=${ARCH} branch=${ALPINE_BRANCH} flavor=${FLAVOR}"
[[ -n "${ALPINE_VERSION}" ]] && log_info "目标 Alpine 版本：${ALPINE_VERSION}"

download_one "$(remote_name vmlinuz)" "vmlinuz"
download_one "$(remote_name modloop)" "modloop"

log_pass "Alpine netboot 准备完成：${ARCH}"
