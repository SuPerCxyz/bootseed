#!/usr/bin/env bash
# 组合冒烟测试:非破坏性本地检查.
#
# 用法:scripts/smoke-test.sh
#
# 步骤:
#   1) cd agent && go vet ./... && go test ./...
#   2) docker compose config -q
#   3) scripts/validate-config.sh(非 strict)
#   4) shellcheck scripts/*.sh(若可用),否则 bash -n
# 任一关键步骤失败计入失败计数;末尾打印汇总并据此退出.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/_common.sh
source "${SCRIPT_DIR}/_common.sh"

FAIL=0
SKIP=0
section() { printf '\n=== %s ===\n' "$*"; }

# ---- 1. Go vet + test ----
section "1) Go vet + test"
if command -v go >/dev/null 2>&1; then
  if ( cd "${ROOT_DIR}/agent" && go vet ./... && go test ./... ); then
    log_pass "Go vet + test 通过"
  else
    log_fail "Go vet/test 失败"
    FAIL=$((FAIL + 1))
  fi
else
  log_warn "未找到 go,跳过 Go 检查"
  SKIP=$((SKIP + 1))
fi

# ---- 2. docker compose config ----
section "2) docker compose config -q"
COMPOSE=""
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  COMPOSE="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE="docker-compose"
fi
if [[ -n "${COMPOSE}" ]]; then
  if ( cd "${ROOT_DIR}" && ${COMPOSE} config -q ); then
    log_pass "docker compose 配置有效"
  else
    log_fail "docker compose 配置无效"
    FAIL=$((FAIL + 1))
  fi
else
  log_warn "未找到 docker compose,跳过"
  SKIP=$((SKIP + 1))
fi

# ---- 3. validate-config(非 strict)----
section "3) validate-config.sh(非 strict)"
if bash "${SCRIPT_DIR}/validate-config.sh"; then
  log_pass "配置校验通过(允许 WARN)"
else
  log_fail "配置校验存在 FAIL"
  FAIL=$((FAIL + 1))
fi

# ---- 4. shellcheck / bash -n ----
section "4) 脚本静态检查"
mapfile -t SCRIPTS < <(find "${SCRIPT_DIR}" -maxdepth 1 -name '*.sh' | sort)
if command -v shellcheck >/dev/null 2>&1; then
  if shellcheck -x "${SCRIPTS[@]}"; then
    log_pass "shellcheck 通过"
  else
    log_fail "shellcheck 报告问题"
    FAIL=$((FAIL + 1))
  fi
else
  log_warn "未找到 shellcheck,回退 bash -n"
  local_fail=0
  for s in "${SCRIPTS[@]}"; do
    if ! bash -n "${s}"; then
      log_fail "bash -n 失败:${s}"
      local_fail=1
    fi
  done
  if [[ "${local_fail}" -eq 0 ]]; then
    log_pass "bash -n 全部通过"
  else
    FAIL=$((FAIL + 1))
  fi
fi

# ---- 汇总 ----
section "冒烟测试汇总"
log_info "FAIL=${FAIL} SKIP=${SKIP}"
if [[ "${FAIL}" -gt 0 ]]; then
  log_fail "冒烟测试存在失败项"
  exit 1
fi
log_pass "冒烟测试通过(跳过项 ${SKIP} 个)"
exit 0
