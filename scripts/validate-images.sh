#!/usr/bin/env bash
# 校验镜像清单 data/http/images/index.json 的有效性.
#
# 用法:scripts/validate-images.sh
#
# 校验项:
#   - 合法 JSON
#   - schema_version 存在
#   - 每条镜像具备必填字段:id name os version architecture firmware format path sha256_compressed
#   - architecture in {x86_64, aarch64}
#   - firmware in {bios, uefi}
#   - format in {raw, img, raw.gz, img.gz, raw.xz, img.xz, raw.zst, img.zst}
#   - 无重复 id
#   - 引用文件存在(缺失记 WARN,不算失败)
# 退出码:存在 FAIL 项返回非零.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/_common.sh
source "${SCRIPT_DIR}/_common.sh"

IMAGES_DIR="${DATA_DIR}/http/images"
INDEX="${IMAGES_DIR}/index.json"

FAIL=0
WARN=0
fail() { log_fail "$*"; FAIL=$((FAIL + 1)); }
warn() { log_warn "$*"; WARN=$((WARN + 1)); }

[[ -f "${INDEX}" ]] || die "清单不存在:${INDEX}"

# 1) 合法 JSON
if command -v jq >/dev/null 2>&1; then
  jq empty "${INDEX}" 2>/dev/null || die "index.json 不是合法 JSON"
else
  python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "${INDEX}" 2>/dev/null \
    || die "index.json 不是合法 JSON"
fi

# 用 python3 做结构化校验(jq 也可,但 python 更易读且总是可用).
RESULT="$(IMAGES_DIR="${IMAGES_DIR}" python3 - "${INDEX}" <<'PY'
import json, os, sys

idx = sys.argv[1]
images_dir = os.environ["IMAGES_DIR"]
with open(idx) as f:
    data = json.load(f)

required = ["id", "name", "os", "version", "architecture",
            "firmware", "format", "path", "sha256_compressed"]
valid_arch = {"x86_64", "aarch64"}
valid_fw = {"bios", "uefi"}
valid_fmt = {"raw", "img", "raw.gz", "img.gz", "raw.xz", "img.xz", "raw.zst", "img.zst"}

fails, warns = [], []

if "schema_version" not in data:
    fails.append("缺少 schema_version 字段")

images = data.get("images")
if not isinstance(images, list):
    fails.append("images 字段缺失或不是数组")
    images = []

seen = {}
for n, img in enumerate(images):
    tag = img.get("id", f"#{n}")
    for key in required:
        if not img.get(key):
            fails.append(f"镜像 {tag} 缺少必填字段:{key}")
    arch = img.get("architecture")
    if arch and arch not in valid_arch:
        fails.append(f"镜像 {tag} 架构非法:{arch}")
    fw = img.get("firmware")
    fw_list = fw if isinstance(fw, list) else ([fw] if fw else [])
    if not fw_list:
        fails.append(f"镜像 {tag} 缺少 firmware")
    for one in fw_list:
        if one not in valid_fw:
            fails.append(f"镜像 {tag} 固件非法:{one}")
    fmt = img.get("format")
    if fmt and fmt not in valid_fmt:
        fails.append(f"镜像 {tag} 格式非法:{fmt}")
    _id = img.get("id")
    if _id:
        seen[_id] = seen.get(_id, 0) + 1
    path = img.get("path", "")
    if path:
        base = os.path.basename(path)
        fpath = os.path.join(images_dir, base)
        if not os.path.isfile(fpath):
            warns.append(f"镜像 {tag} 引用文件缺失:{fpath}")

for _id, cnt in seen.items():
    if cnt > 1:
        fails.append(f"重复 id:{_id}(出现 {cnt} 次)")

for w in warns:
    print("WARN\t" + w)
for fmsg in fails:
    print("FAIL\t" + fmsg)
PY
)"

if [[ -n "${RESULT}" ]]; then
  while IFS=$'\t' read -r level msg; do
    [[ -z "${level}" ]] && continue
    case "${level}" in
      WARN) warn "${msg}" ;;
      FAIL) fail "${msg}" ;;
    esac
  done <<<"${RESULT}"
fi

echo ""
log_info "镜像清单校验汇总:FAIL=${FAIL} WARN=${WARN}"
if [[ "${FAIL}" -gt 0 ]]; then
  log_fail "镜像清单校验未通过"
  exit 1
fi
log_pass "镜像清单校验通过"
