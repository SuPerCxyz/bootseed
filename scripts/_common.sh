#!/usr/bin/env bash
# BootSeed 脚本公共库
#
# 功能：
#   - 定位项目根目录
#   - 加载 .env（缺失时回退到 .env.example 并告警）
#   - 架构规范化（x86_64 / aarch64，接受 amd64 / x64 / arm64 别名）
#   - 统一日志输出（info / warn / error / pass / fail）
#
# 用法：在其他脚本顶部 `source "$(dirname "$0")/_common.sh"`。
# 本文件本身不应被直接执行。

set -euo pipefail

# ------------------------------------------------------------------
# 路径定位
# ------------------------------------------------------------------
# 公共库所在目录即 scripts/，其父目录为项目根。
COMMON_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${COMMON_DIR}/.." && pwd)"
DATA_DIR="${ROOT_DIR}/data"
BUILD_DIR="${ROOT_DIR}/build"
export ROOT_DIR DATA_DIR BUILD_DIR

# ------------------------------------------------------------------
# 颜色（仅在终端时启用）
# ------------------------------------------------------------------
if [[ -t 1 ]]; then
  C_RED=$'\033[31m'; C_GRN=$'\033[32m'; C_YEL=$'\033[33m'
  C_BLU=$'\033[34m'; C_RST=$'\033[0m'
else
  C_RED=''; C_GRN=''; C_YEL=''; C_BLU=''; C_RST=''
fi

log_info() { printf '%s[信息]%s %s\n' "${C_BLU}" "${C_RST}" "$*"; }
log_warn() { printf '%s[告警]%s %s\n' "${C_YEL}" "${C_RST}" "$*" >&2; }
log_error() { printf '%s[错误]%s %s\n' "${C_RED}" "${C_RST}" "$*" >&2; }
log_pass() { printf '%s[通过]%s %s\n' "${C_GRN}" "${C_RST}" "$*"; }
log_fail() { printf '%s[失败]%s %s\n' "${C_RED}" "${C_RST}" "$*"; }

# 致命错误：打印后退出。
die() {
  log_error "$*"
  exit 1
}

# ------------------------------------------------------------------
# 环境变量加载
# ------------------------------------------------------------------
# 优先加载项目根的 .env；若不存在则回退到 .env.example 并告警。
# 仅在尚未加载过时执行一次。
load_env() {
  if [[ "${_BOOTSEED_ENV_LOADED:-}" == "1" ]]; then
    return 0
  fi
  local env_file
  if [[ -f "${ROOT_DIR}/.env" ]]; then
    env_file="${ROOT_DIR}/.env"
  elif [[ -f "${ROOT_DIR}/.env.example" ]]; then
    env_file="${ROOT_DIR}/.env.example"
    log_warn "未找到 .env，回退使用 .env.example（请尽快复制为 .env）"
  else
    die "未找到 .env 或 .env.example，无法加载配置"
  fi

  # 逐行解析，避免直接 source 带来的命令执行风险。
  # 仅接受 KEY=VALUE 形式；忽略空行与注释。
  local line key val
  while IFS= read -r line || [[ -n "${line}" ]]; do
    line="${line%$'\r'}"
    [[ -z "${line}" ]] && continue
    [[ "${line}" =~ ^[[:space:]]*# ]] && continue
    [[ "${line}" != *=* ]] && continue
    key="${line%%=*}"
    val="${line#*=}"
    # 去除 key 两端空白
    key="${key#"${key%%[![:space:]]*}"}"
    key="${key%"${key##*[![:space:]]}"}"
    [[ "${key}" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue
    # 去除 value 两端成对引号
    if [[ "${val}" == \"*\" ]]; then
      val="${val:1:${#val}-2}"
    elif [[ "${val}" == \'*\' ]]; then
      val="${val:1:${#val}-2}"
    fi
    export "${key}=${val}"
  done < "${env_file}"

  export _BOOTSEED_ENV_LOADED=1
  export BOOTSEED_ENV_FILE="${env_file}"
}

# ------------------------------------------------------------------
# 架构规范化
# ------------------------------------------------------------------
# 输入任意别名，输出规范架构 x86_64 / aarch64；非法输入返回非零并报错。
normalize_arch() {
  local in="${1:-}"
  case "${in}" in
    x86_64|amd64|x64) echo "x86_64" ;;
    aarch64|arm64) echo "aarch64" ;;
    *)
      log_error "不支持的架构：'${in}'（仅支持 x86_64/aarch64，别名 amd64/x64/arm64）"
      return 1
      ;;
  esac
}

# 将规范架构映射为 Alpine apk 架构名（当前一一对应）。
alpine_apk_arch() {
  case "${1}" in
    x86_64) echo "x86_64" ;;
    aarch64) echo "aarch64" ;;
    *) log_error "无法映射 Alpine 架构：'${1}'"; return 1 ;;
  esac
}
