#!/usr/bin/env bash
# 从镜像清单 data/http/images/index.json 删除一条镜像记录.
#
# 用法:scripts/remove-image.sh --id <id> [--delete-file] [--yes]
#   --id          要删除的镜像 id(必填)
#   --delete-file 同时删除磁盘上的镜像文件
#   --yes         跳过确认
#
# 原子更新 index.json(写临时文件 + mv),并加锁防并发.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/_common.sh
source "${SCRIPT_DIR}/_common.sh"

IMAGES_DIR="${DATA_DIR}/http/images"
INDEX="${IMAGES_DIR}/index.json"
LOCK_DIR="${IMAGES_DIR}/.index.lock"

usage() { echo "用法:remove-image.sh --id <id> [--delete-file] [--yes]"; }

ID="" DELETE_FILE=0 ASSUME_YES=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --id) ID="$2"; shift 2 ;;
    --delete-file) DELETE_FILE=1; shift ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage; die "未知参数:$1" ;;
  esac
done

[[ -n "${ID}" ]] || { usage; die "缺少 --id"; }
[[ -f "${INDEX}" ]] || die "清单不存在:${INDEX}"

# ---- 获取锁 ----
acquire_lock() {
  if command -v flock >/dev/null 2>&1; then
    exec {LOCK_FD}>"${IMAGES_DIR}/.index.flock"
    flock -w 30 "${LOCK_FD}" || die "获取文件锁超时"
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
  [[ "${LOCK_MODE:-}" == "mkdir" ]] && rmdir "${LOCK_DIR}" 2>/dev/null || true
}
trap release_lock EXIT
acquire_lock

# ---- 查找目标镜像的 path ----
TARGET_PATH=""
if command -v jq >/dev/null 2>&1; then
  if ! jq -e --arg id "${ID}" '.images[] | select(.id == $id)' "${INDEX}" >/dev/null 2>&1; then
    die "未找到镜像 id:${ID}"
  fi
  TARGET_PATH="$(jq -r --arg id "${ID}" '.images[] | select(.id == $id) | .path // ""' "${INDEX}")"
else
  TARGET_PATH="$(IMG_ID="${ID}" python3 - "${INDEX}" <<'PY'
import json, os, sys
with open(sys.argv[1]) as f:
    data = json.load(f)
_id = os.environ["IMG_ID"]
for img in data.get("images", []):
    if img.get("id") == _id:
        print(img.get("path", ""))
        sys.exit(0)
sys.exit(1)
PY
)" || die "未找到镜像 id:${ID}"
fi

if [[ "${ASSUME_YES}" -ne 1 ]]; then
  read -r -p "确认从清单删除镜像 '${ID}'?[y/N] " ans
  [[ "${ans}" =~ ^[Yy]$ ]] || die "已取消"
fi

# ---- 原子删除条目 ----
TMP="${INDEX}.tmp.$$"
if command -v jq >/dev/null 2>&1; then
  jq --arg id "${ID}" '.images |= map(select(.id != $id))' "${INDEX}" > "${TMP}"
else
  IMG_ID="${ID}" python3 - "${INDEX}" "${TMP}" <<'PY'
import json, os, sys
idx, tmp = sys.argv[1], sys.argv[2]
with open(idx) as f:
    data = json.load(f)
_id = os.environ["IMG_ID"]
data["images"] = [i for i in data.get("images", []) if i.get("id") != _id]
with open(tmp, "w") as f:
    json.dump(data, f, ensure_ascii=False, indent=2)
    f.write("\n")
PY
fi
mv -f "${TMP}" "${INDEX}"
log_pass "已从清单删除镜像:${ID}"

# ---- 可选删除文件 ----
if [[ "${DELETE_FILE}" -eq 1 && -n "${TARGET_PATH}" ]]; then
  # path 形如 /images/<basename>,映射到 images 目录.
  local_base="$(basename "${TARGET_PATH}")"
  file_path="${IMAGES_DIR}/${local_base}"
  if [[ -f "${file_path}" ]]; then
    rm -f "${file_path}"
    log_pass "已删除镜像文件:${file_path}"
  else
    log_warn "镜像文件不存在,跳过删除:${file_path}"
  fi
fi
