#!/usr/bin/env bash
# 列出镜像清单中的镜像，含架构列。
#
# 用法：scripts/list-images.sh [--json]
#   默认：打印表格（ID / 名称 / 系统 / 版本 / 架构 / 固件 / 格式 / 大小）。
#   --json：直接输出 index.json 原文。

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/_common.sh
source "${SCRIPT_DIR}/_common.sh"

INDEX="${DATA_DIR}/http/images/index.json"
[[ -f "${INDEX}" ]] || die "清单不存在：${INDEX}"

if [[ "${1:-}" == "--json" ]]; then
  cat "${INDEX}"
  exit 0
fi

# 表头
printf '%-16s %-20s %-10s %-10s %-9s %-7s %-6s %s\n' \
  "ID" "名称" "系统" "版本" "架构" "固件" "格式" "大小(压缩)"
printf '%s\n' "--------------------------------------------------------------------------------------------"

if command -v jq >/dev/null 2>&1; then
  jq -r '.images[] |
    [ (.id // "-"), (.name // "-"), (.os // "-"), (.version // "-"),
      (.architecture // "-"), (.firmware // "-"), (.format // "-"),
      ((.compressed_size // 0)|tostring) ] | @tsv' "${INDEX}" \
  | while IFS=$'\t' read -r id name os ver arch fw fmt size; do
      printf '%-16s %-20s %-10s %-10s %-9s %-7s %-6s %s\n' \
        "${id}" "${name}" "${os}" "${ver}" "${arch}" "${fw}" "${fmt}" "${size}"
    done
else
  python3 - "${INDEX}" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    data = json.load(f)
for i in data.get("images", []):
    print("{:<16} {:<20} {:<10} {:<10} {:<9} {:<7} {:<6} {}".format(
        i.get("id", "-"), i.get("name", "-"), i.get("os", "-"),
        i.get("version", "-"), i.get("architecture", "-"),
        i.get("firmware", "-"), i.get("format", "-"),
        i.get("compressed_size", 0)))
PY
fi
