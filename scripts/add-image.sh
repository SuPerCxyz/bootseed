#!/usr/bin/env bash
# 向镜像清单 data/http/images/index.json 添加一条镜像记录.
#
# 用法:
#   scripts/add-image.sh \
#     --file <路径> --id <唯一ID> --name <显示名> \
#     --os <系统> --version <版本> --architecture <arch> \
#     --firmware <bios|uefi> --raw-size <解压后字节数> \
#     [--description <描述>] [--format <raw|img|gz|xz|zst>]
#
# 行为:
#   - architecture 必填,规范化别名,拒绝未知架构.
#   - firmware 必须是 bios 或 uefi.
#   - 拒绝重复 id.
#   - 将文件复制到 data/http/images/,path 字段为 /images/<basename>.
#   - 计算压缩后大小(stat)与 sha256(sha256sum).
#   - 未指定 format 时按文件名推断.
#   - 原子更新 index.json(写临时文件 + mv),并用 flock/mkdir 锁防并发损坏.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/_common.sh
source "${SCRIPT_DIR}/_common.sh"

IMAGES_DIR="${DATA_DIR}/http/images"
INDEX="${IMAGES_DIR}/index.json"
LOCK_DIR="${IMAGES_DIR}/.index.lock"

usage() {
  cat <<'EOF'
用法:add-image.sh --file F --id ID --name NAME --os OS --version VER \
        --architecture ARCH --firmware {bios|uefi} --raw-size N \
        [--description DESC] [--format {raw|img|gz|xz|zst}]
EOF
}

FILE="" ID="" NAME="" OS="" VERSION="" ARCH_IN="" FIRMWARE=""
RAW_SIZE="" DESCRIPTION="" FORMAT=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --file) FILE="$2"; shift 2 ;;
    --id) ID="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --os) OS="$2"; shift 2 ;;
    --version) VERSION="$2"; shift 2 ;;
    --architecture) ARCH_IN="$2"; shift 2 ;;
    --firmware) FIRMWARE="$2"; shift 2 ;;
    --raw-size) RAW_SIZE="$2"; shift 2 ;;
    --description) DESCRIPTION="$2"; shift 2 ;;
    --format) FORMAT="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage; die "未知参数:$1" ;;
  esac
done

# ---- 参数校验 ----
[[ -n "${FILE}" ]] || { usage; die "缺少 --file"; }
[[ -n "${ID}" ]] || { usage; die "缺少 --id"; }
[[ -n "${NAME}" ]] || { usage; die "缺少 --name"; }
[[ -n "${OS}" ]] || { usage; die "缺少 --os"; }
[[ -n "${VERSION}" ]] || { usage; die "缺少 --version"; }
[[ -n "${ARCH_IN}" ]] || { usage; die "缺少 --architecture(必填)"; }
[[ -n "${FIRMWARE}" ]] || { usage; die "缺少 --firmware"; }

[[ -f "${FILE}" ]] || die "镜像文件不存在:${FILE}"

ARCH="$(normalize_arch "${ARCH_IN}")" || exit 1

# firmware 允许逗号分隔(如 bios,uefi),逐个校验.
_fw_check="${FIRMWARE//,/ }"
for _fw in ${_fw_check}; do
  case "${_fw}" in
    bios|uefi) ;;
    *) die "firmware 非法:'${_fw}'(仅支持 bios/uefi,可用逗号分隔)" ;;
  esac
done

[[ "${ID}" =~ ^[A-Za-z0-9._-]+$ ]] || die "id 含非法字符(仅允许字母数字 . _ -):${ID}"

# ============================================================
# 自动转换:若输入是 qcow2/vmdk/vdi/vhd 等磁盘镜像,自动用 qemu-img 转成 raw
# 再用 zstd 压成 raw.zst,便于流式写盘.raw/img 及已压缩的 raw.*/img.* 原样使用.
# 同时在转换时自动得出 raw_size,无需手工 --raw-size.
# ============================================================
CONVERT_TMP=""
cleanup_convert() { [[ -n "${CONVERT_TMP}" && -d "${CONVERT_TMP}" ]] && rm -rf "${CONVERT_TMP}"; }
trap cleanup_convert EXIT

detect_qemu_format() {
  command -v qemu-img >/dev/null 2>&1 || { echo ""; return; }
  # 解析 "file format: qcow2" 行,稳定可靠(不依赖 --output=json).
  qemu-img info "$1" 2>/dev/null | sed -n 's/^file format:[[:space:]]*//p' | head -1
}

case "${FILE}" in
  *.raw|*.img|*.raw.gz|*.img.gz|*.raw.xz|*.img.xz|*.raw.zst|*.img.zst)
    : ;; # 已是 raw/img 或其压缩形式,无需转换
  *)
    qfmt="$(detect_qemu_format "${FILE}")"
    if [[ -n "${qfmt}" && "${qfmt}" != "raw" ]]; then
      command -v qemu-img >/dev/null 2>&1 || die "需要 qemu-img 转换 ${qfmt} 镜像,请安装 qemu-utils/qemu-img"
      command -v zstd >/dev/null 2>&1 || die "需要 zstd 压缩 raw 镜像,请安装 zstd"
      CONVERT_TMP="$(mktemp -d "${IMAGES_DIR}/.convert.XXXXXX")"
      log_info "检测到 ${qfmt} 镜像,自动转换为 raw 并压缩(可能较慢)..."
      qemu-img convert -p -f "${qfmt}" -O raw "${FILE}" "${CONVERT_TMP}/${ID}.raw" \
        || die "qemu-img 转换失败:${FILE}"
      AUTO_RAW_SIZE="$(stat -c %s "${CONVERT_TMP}/${ID}.raw")"
      log_info "raw 大小=${AUTO_RAW_SIZE} 字节,开始 zstd 压缩..."
      zstd -q -T0 -3 -f "${CONVERT_TMP}/${ID}.raw" -o "${CONVERT_TMP}/${ID}.raw.zst" \
        || die "zstd 压缩失败"
      rm -f "${CONVERT_TMP}/${ID}.raw"   # 及时释放空间
      FILE="${CONVERT_TMP}/${ID}.raw.zst"
      FORMAT="raw.zst"
      [[ -z "${RAW_SIZE}" ]] && RAW_SIZE="${AUTO_RAW_SIZE}"
      log_info "转换完成 -> ${FILE}(format=raw.zst, raw_size=${RAW_SIZE})"
    fi
    ;;
esac

# 若仍未提供 raw_size:raw/img 直接 stat;压缩 raw.* 用解压大小推断.
if [[ -z "${RAW_SIZE}" ]]; then
  case "${FILE}" in
    *.raw|*.img) RAW_SIZE="$(stat -c %s "${FILE}")" ;;
    *.raw.zst|*.img.zst) RAW_SIZE="$(zstd -l "${FILE}" 2>/dev/null | awk 'NR==2{print $5}' | grep -oE '[0-9]+' | head -1)" ;;
  esac
fi
[[ -n "${RAW_SIZE}" ]] || die "无法自动得出 raw_size,请用 --raw-size 指定"
[[ "${RAW_SIZE}" =~ ^[0-9]+$ ]] || die "--raw-size 必须为非负整数:${RAW_SIZE}"

BASENAME="$(basename "${FILE}")"

# 推断 format.必须与 Agent 的 IsSupportedFormat 对齐:
# raw / img / raw.gz / img.gz / raw.xz / img.xz / raw.zst / img.zst.
infer_format() {
  local name="$1"
  case "${name}" in
    *.raw.gz)  echo "raw.gz" ;;
    *.img.gz)  echo "img.gz" ;;
    *.raw.xz)  echo "raw.xz" ;;
    *.img.xz)  echo "img.xz" ;;
    *.raw.zst) echo "raw.zst" ;;
    *.img.zst) echo "img.zst" ;;
    *.raw)     echo "raw" ;;
    *.img)     echo "img" ;;
    # 裸压缩名缺少 raw/img 前缀时无法确定基底,默认按 raw 处理
    *.gz)      echo "raw.gz" ;;
    *.xz)      echo "raw.xz" ;;
    *.zst)     echo "raw.zst" ;;
    *) echo "" ;;
  esac
}

if [[ -z "${FORMAT}" ]]; then
  FORMAT="$(infer_format "${BASENAME}")"
  [[ -n "${FORMAT}" ]] || die "无法从文件名推断 format,请用 --format 指定:${BASENAME}"
fi

case "${FORMAT}" in
  raw|img|raw.gz|img.gz|raw.xz|img.xz|raw.zst|img.zst) ;;
  *) die "format 不受支持:'${FORMAT}'(raw/img/raw.gz/img.gz/raw.xz/img.xz/raw.zst/img.zst)" ;;
esac

mkdir -p "${IMAGES_DIR}"
[[ -f "${INDEX}" ]] || printf '{"schema_version":1,"images":[]}\n' > "${INDEX}"

# ---- 获取锁(优先 flock,回退 mkdir 锁)----
acquire_lock() {
  if command -v flock >/dev/null 2>&1; then
    exec {LOCK_FD}>"${IMAGES_DIR}/.index.flock"
    flock -w 30 "${LOCK_FD}" || die "获取文件锁超时:${IMAGES_DIR}/.index.flock"
    LOCK_MODE="flock"
  else
    local waited=0
    until mkdir "${LOCK_DIR}" 2>/dev/null; do
      sleep 1; waited=$((waited + 1))
      [[ "${waited}" -ge 30 ]] && die "获取目录锁超时:${LOCK_DIR}"
    done
    LOCK_MODE="mkdir"
  fi
}
release_lock() {
  if [[ "${LOCK_MODE:-}" == "mkdir" ]]; then
    rmdir "${LOCK_DIR}" 2>/dev/null || true
  fi
}
trap 'release_lock; cleanup_convert' EXIT
acquire_lock

# ---- 重复 id 检查 ----
if command -v jq >/dev/null 2>&1; then
  if jq -e --arg id "${ID}" '.images[] | select(.id == $id)' "${INDEX}" >/dev/null 2>&1; then
    die "镜像 id 已存在:${ID}"
  fi
else
  if python3 - "${INDEX}" "${ID}" <<'PY'
import json, sys
idx, _id = sys.argv[1], sys.argv[2]
with open(idx) as f:
    data = json.load(f)
sys.exit(0 if any(i.get("id") == _id for i in data.get("images", [])) else 1)
PY
  then
    die "镜像 id 已存在:${ID}"
  fi
fi

# ---- 复制文件 ----
DEST="${IMAGES_DIR}/${BASENAME}"
if [[ "$(readlink -f "${FILE}")" != "$(readlink -f "${DEST}" 2>/dev/null || echo /nonexistent)" ]]; then
  log_info "复制镜像到:${DEST}"
  cp -f "${FILE}" "${DEST}"
fi

# ---- 计算压缩后大小与 sha256 ----
COMPRESSED_SIZE="$(stat -c %s "${DEST}")"
log_info "计算 sha256(可能较慢)..."
SHA256="$(sha256sum "${DEST}" | awk '{print $1}')"
PATH_FIELD="/images/${BASENAME}"

log_info "镜像信息:id=${ID} arch=${ARCH} firmware=${FIRMWARE} format=${FORMAT}"
log_info "  压缩后=${COMPRESSED_SIZE} 字节, 解压后=${RAW_SIZE} 字节"
log_info "  sha256=${SHA256}"

# ---- 原子更新 index.json ----
TMP="${INDEX}.tmp.$$"

if command -v jq >/dev/null 2>&1; then
  jq \
    --arg id "${ID}" --arg name "${NAME}" --arg os "${OS}" \
    --arg version "${VERSION}" --arg arch "${ARCH}" --arg firmware "${FIRMWARE}" \
    --arg format "${FORMAT}" --arg path "${PATH_FIELD}" --arg sha256 "${SHA256}" \
    --arg desc "${DESCRIPTION}" \
    --argjson compressed "${COMPRESSED_SIZE}" --argjson raw "${RAW_SIZE}" \
    '.images += [{
        id: $id, name: $name, os: $os, version: $version,
        architecture: $arch, firmware: ($firmware | split(",")), format: $format,
        path: $path, sha256_compressed: $sha256,
        compressed_size: $compressed, raw_size: $raw,
        description: $desc
     }]' "${INDEX}" > "${TMP}"
else
  # 通过环境变量传值,避免把内容拼进 Python 源码导致注入/语法问题.
  IMG_ID="${ID}" IMG_NAME="${NAME}" IMG_OS="${OS}" IMG_VERSION="${VERSION}" \
  IMG_ARCH="${ARCH}" IMG_FIRMWARE="${FIRMWARE}" IMG_FORMAT="${FORMAT}" \
  IMG_PATH="${PATH_FIELD}" IMG_SHA256="${SHA256}" IMG_DESC="${DESCRIPTION}" \
  IMG_COMPRESSED="${COMPRESSED_SIZE}" IMG_RAW="${RAW_SIZE}" \
  python3 - "${INDEX}" "${TMP}" <<'PY'
import json, os, sys
idx, tmp = sys.argv[1], sys.argv[2]
with open(idx) as f:
    data = json.load(f)
data.setdefault("images", []).append({
    "id": os.environ["IMG_ID"],
    "name": os.environ["IMG_NAME"],
    "os": os.environ["IMG_OS"],
    "version": os.environ["IMG_VERSION"],
    "architecture": os.environ["IMG_ARCH"],
    "firmware": os.environ["IMG_FIRMWARE"].split(","),
    "format": os.environ["IMG_FORMAT"],
    "path": os.environ["IMG_PATH"],
    "sha256_compressed": os.environ["IMG_SHA256"],
    "compressed_size": int(os.environ["IMG_COMPRESSED"]),
    "raw_size": int(os.environ["IMG_RAW"]),
    "description": os.environ["IMG_DESC"],
})
with open(tmp, "w") as f:
    json.dump(data, f, ensure_ascii=False, indent=2)
    f.write("\n")
PY
fi

mv -f "${TMP}" "${INDEX}"
log_pass "已添加镜像:${ID} -> ${PATH_FIELD}"
