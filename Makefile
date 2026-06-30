# BootSeed Makefile
# 所有命令都不依赖在源码中硬编码的服务端 IP / 网卡 / 端口，
# 全部通过 .env 控制。

SHELL := /bin/bash
ROOT  := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

ENV_FILE := $(ROOT)/.env
ifneq (,$(wildcard $(ENV_FILE)))
include $(ENV_FILE)
export
endif

AGENT_VERSION ?= 0.1.0
ALPINE_VERSION ?= 3.20.3
ALPINE_BRANCH  ?= v3.20

AGENT_DIR     := $(ROOT)/agent
BUILD_DIR     := $(ROOT)/build
DATA_DIR      := $(ROOT)/data
SCRIPTS_DIR   := $(ROOT)/scripts

GO ?= go

.PHONY: help
help:
	@echo "BootSeed make targets:"
	@echo "  init                 一次性初始化：iPXE、Alpine、Agent、initramfs、清单、校验"
	@echo "  build                = build-agent + build-initramfs"
	@echo "  build-agent[-ARCH]   构建 Go Agent"
	@echo "  build-initramfs[-ARCH] 构建 Alpine initramfs"
	@echo "  build-all-architectures 同时构建 x86_64 与 aarch64"
	@echo "  prepare-ipxe[-ARCH]  准备 iPXE 启动文件"
	@echo "  prepare-alpine       下载 Alpine netboot 内核 + modloop"
	@echo "  validate-architectures 校验所有产物架构"
	@echo "  generate-config      生成 dnsmasq.conf / boot.ipxe"
	@echo "  validate             校验 .env 与目录"
	@echo "  up / down / restart / logs"
	@echo "  test                 Go 单元测试 + vet"
	@echo "  smoke-test           组合冒烟"
	@echo "  clean                清理 build/ 产物"

.PHONY: init
init: validate prepare-ipxe prepare-alpine build-all-architectures generate-config validate-architectures
	@echo "[init] BootSeed 初始化完成"

.PHONY: build
build: build-agent build-initramfs

.PHONY: build-agent
build-agent: build-agent-x86_64 build-agent-aarch64

.PHONY: build-agent-x86_64
build-agent-x86_64:
	@mkdir -p $(BUILD_DIR)/agent
	@echo "[build-agent] x86_64"
	cd $(AGENT_DIR) && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		$(GO) build -trimpath -ldflags "-s -w -X main.version=$(AGENT_VERSION)" \
		-o $(BUILD_DIR)/agent/bootseed-agent-x86_64 ./cmd/bootseed-agent
	@file $(BUILD_DIR)/agent/bootseed-agent-x86_64 | tee -a $(BUILD_DIR)/reports/build.log

.PHONY: build-agent-aarch64
build-agent-aarch64:
	@mkdir -p $(BUILD_DIR)/agent
	@echo "[build-agent] aarch64"
	cd $(AGENT_DIR) && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
		$(GO) build -trimpath -ldflags "-s -w -X main.version=$(AGENT_VERSION)" \
		-o $(BUILD_DIR)/agent/bootseed-agent-aarch64 ./cmd/bootseed-agent
	@file $(BUILD_DIR)/agent/bootseed-agent-aarch64 | tee -a $(BUILD_DIR)/reports/build.log

.PHONY: build-initramfs
build-initramfs: build-initramfs-x86_64 build-initramfs-aarch64

# initramfs 构建依赖 Alpine 的 apk-tools/kmod 等工具，宿主机（如 Ubuntu）通常没有，
# 因此统一在 bootseed-builder 容器内执行 alpine/build-initramfs.sh。
# 容器内路径：项目根挂载到 /work，故 BUILD_DIR=/work/build、DATA_DIR=/work/data。
DOCKER        ?= docker
BUILDER_IMAGE ?= bootseed-builder:latest

.PHONY: builder-image
builder-image:
	@echo "[builder-image] 构建 $(BUILDER_IMAGE)"
	$(DOCKER) build -t $(BUILDER_IMAGE) -f $(ROOT)/alpine/Dockerfile.builder $(ROOT)/alpine

# $(call run_initramfs,<arch>)：在 builder 容器内组装指定架构 initramfs。
# 需要 CAP_MKNOD（apk --root 创建 /dev 节点），docker run 默认已具备。
define run_initramfs
	$(DOCKER) run --rm \
		-e BUILD_DIR=/work/build -e DATA_DIR=/work/data \
		-e ALPINE_BRANCH=$(ALPINE_BRANCH) -e ALPINE_VERSION=$(ALPINE_VERSION) \
		-e AGENT_VERSION=$(AGENT_VERSION) \
		-v "$(ROOT)":/work -w /work \
		$(BUILDER_IMAGE) \
		alpine/build-initramfs.sh $(1)
endef

.PHONY: build-initramfs-x86_64
build-initramfs-x86_64: build-agent-x86_64 builder-image
	@$(SCRIPTS_DIR)/prepare-alpine.sh x86_64
	$(call run_initramfs,x86_64)

.PHONY: build-initramfs-aarch64
build-initramfs-aarch64: build-agent-aarch64 builder-image
	@$(SCRIPTS_DIR)/prepare-alpine.sh aarch64
	$(call run_initramfs,aarch64)

.PHONY: build-all-architectures
build-all-architectures: build-agent build-initramfs

.PHONY: prepare-ipxe
prepare-ipxe: prepare-ipxe-x86 prepare-ipxe-x86_64 prepare-ipxe-aarch64

.PHONY: prepare-ipxe-x86
prepare-ipxe-x86:
	bash $(SCRIPTS_DIR)/prepare-ipxe.sh x86

.PHONY: prepare-ipxe-x86_64
prepare-ipxe-x86_64:
	bash $(SCRIPTS_DIR)/prepare-ipxe.sh x86_64

.PHONY: prepare-ipxe-aarch64
prepare-ipxe-aarch64:
	bash $(SCRIPTS_DIR)/prepare-ipxe.sh aarch64

.PHONY: prepare-alpine
prepare-alpine:
	bash $(SCRIPTS_DIR)/prepare-alpine.sh x86_64
	bash $(SCRIPTS_DIR)/prepare-alpine.sh aarch64

.PHONY: validate-architectures
validate-architectures:
	bash $(SCRIPTS_DIR)/validate-architectures.sh

.PHONY: generate-config
generate-config:
	bash $(SCRIPTS_DIR)/generate-config.sh

.PHONY: validate
validate:
	bash $(SCRIPTS_DIR)/validate-config.sh

.PHONY: up
up:
	docker compose up -d

.PHONY: down
down:
	docker compose down

.PHONY: restart
restart: down up

.PHONY: logs
logs:
	docker compose logs -f --tail=200

.PHONY: test
test:
	cd $(AGENT_DIR) && $(GO) vet ./...
	cd $(AGENT_DIR) && $(GO) test ./...

.PHONY: smoke-test
smoke-test:
	bash $(SCRIPTS_DIR)/smoke-test.sh

.PHONY: clean
clean:
	rm -rf $(BUILD_DIR)/agent $(BUILD_DIR)/initramfs $(BUILD_DIR)/ipxe $(BUILD_DIR)/tmp
	@echo "[clean] done"
