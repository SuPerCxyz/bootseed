#!/usr/bin/env bash
# 编排:为 x86_64 与 aarch64 同时构建 Agent 与 initramfs.
#
# 用法:scripts/build-all-architectures.sh
#
# 设计:保持轻薄.优先调用项目根 Makefile 的 build-all-architectures
# 目标,由其驱动底层 build-agent / build-initramfs 步骤.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/_common.sh
source "${SCRIPT_DIR}/_common.sh"

load_env

if ! command -v make >/dev/null 2>&1; then
  die "未找到 make,无法执行构建编排"
fi

log_info "开始构建全部架构(Agent + initramfs)"
make -C "${ROOT_DIR}" build-all-architectures

log_pass "全部架构构建完成"
