#!/usr/bin/env bash
# BootSeed initramfs 构建脚本
#
# 用法: bash build-initramfs.sh <arch>      # arch 为 x86_64 或 aarch64
#
# 该脚本在 x86_64 宿主机上运行,通过 `apk --arch <arch> --root` 把对应架构的
# Alpine 软件包解包到独立 rootfs,再组装出一个「全内存运行」的 initramfs:
#   - 自定义 /init(busybox sh),完成挂载,驱动加载,网络配置后 exec bootseed-agent
#   - 广覆盖的网卡 / RAID / HBA / NVMe 驱动模块
#   - 相关网卡固件
#
# 重要约束:本脚本需要 root 权限与网络访问(apk 需要联网拉取软件包).
# 若 apk 不可用或非 root,将给出明确的中文错误并安全退出,绝不静默继续.
set -euo pipefail

# ---- 全局常量 ----
# 固定 Alpine 镜像源,仓库 URL 统一基于 ALPINE_BRANCH.
readonly MIRROR="https://dl-cdn.alpinelinux.org/alpine"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly MODULES_DIR="${SCRIPT_DIR}/modules"
readonly OVERLAY_DIR="${SCRIPT_DIR}/overlay"
readonly PACKAGES_FILE="${SCRIPT_DIR}/packages.yaml"

# 设备等待超时(秒),优先取构建环境变量,否则用规范默认值.
readonly NET_TIMEOUT="${NETWORK_DEVICE_TIMEOUT:-60}"
readonly STO_TIMEOUT="${STORAGE_DEVICE_TIMEOUT:-90}"

# 运行期会被填充的全局数组 / 变量.
ARCH=""
APK_ARCH=""
ROOTFS=""
STAGING=""
KVER=""
INCLUDED_MODULES=()
SKIPPED_MODULES=()
FW_PACKAGES=()
RUNTIME_PACKAGES=()

# ---- 日志辅助 ----
log_info() { printf '[build-initramfs][info] %s\n' "$*" >&2; }
log_warn() { printf '[build-initramfs][warn] %s\n' "$*" >&2; }
log_err()  { printf '[build-initramfs][error] %s\n' "$*" >&2; }
die()      { log_err "$*"; exit 1; }

usage() {
	cat >&2 <<'USAGE'
用法: bash build-initramfs.sh <arch>
  <arch>  目标架构,必须是 x86_64 或 aarch64

需要的环境变量:
  BUILD_DIR DATA_DIR ALPINE_BRANCH ALPINE_VERSION AGENT_VERSION
USAGE
}

# 校验架构参数并推导 apk 架构名(此处两者同名,保留映射以便将来扩展).
validate_arch() {
	case "$1" in
		x86_64)  ARCH="x86_64";  APK_ARCH="x86_64" ;;
		aarch64) ARCH="aarch64"; APK_ARCH="aarch64" ;;
		*) usage; die "不支持的架构: ${1:-<空>}(仅支持 x86_64 / aarch64)" ;;
	esac
}

# 校验必需环境变量是否齐全.
validate_env() {
	local missing=()
	local v
	for v in BUILD_DIR DATA_DIR ALPINE_BRANCH ALPINE_VERSION AGENT_VERSION; do
		[ -n "${!v:-}" ] || missing+=("$v")
	done
	[ "${#missing[@]}" -eq 0 ] || die "缺少环境变量: ${missing[*]}"
}

# 校验运行前置条件与输入文件是否存在.
validate_inputs() {
	command -v apk >/dev/null 2>&1 || \
		die "未找到 apk,本脚本必须在安装了 apk-tools 的环境中运行(见 alpine/Dockerfile.builder)."
	[ "$(id -u)" -eq 0 ] || \
		die "需要 root 权限:apk --root 解包 rootfs 时需要创建设备节点与属主信息."

	local agent_bin="${BUILD_DIR}/agent/bootseed-agent-${ARCH}"
	local vmlinuz="${DATA_DIR}/http/alpine/${ARCH}/vmlinuz"
	local modloop="${DATA_DIR}/http/alpine/${ARCH}/modloop"
	[ -f "$agent_bin" ] || die "缺少 Agent 二进制: $agent_bin(请先 make build-agent-${ARCH})"
	[ -f "$vmlinuz" ]   || die "缺少内核: $vmlinuz(请先 make prepare-alpine)"
	[ -f "$modloop" ]   || die "缺少 modloop: $modloop(请先 make prepare-alpine)"
}

# 拼装 apk 通用参数(针对指定 root).
apk_args() {
	local root="$1"
	# Alpine 按架构分别签名 APKINDEX,跨架构组装时必须使用目标架构的签名公钥
	# (位于 /usr/share/apk/keys/<arch>/),否则会报 UNTRUSTED signature.
	local keys_dir="/usr/share/apk/keys/${APK_ARCH}"
	[ -d "$keys_dir" ] || keys_dir="/etc/apk/keys"
	printf '%s\n' \
		--arch "$APK_ARCH" \
		--root "$root" \
		--keys-dir "$keys_dir" \
		--repository "${MIRROR}/${ALPINE_BRANCH}/main" \
		--repository "${MIRROR}/${ALPINE_BRANCH}/community" \
		--no-cache \
		--no-scripts
}

# 创建源 rootfs:安装 linux-lts 以获取内核模块,KVER 与依赖解析能力.
create_rootfs() {
	ROOTFS="$(mktemp -d "${BUILD_DIR}/tmp/rootfs-${ARCH}.XXXXXX")"
	log_info "创建源 rootfs: $ROOTFS"
	local -a args
	mapfile -t args < <(apk_args "$ROOTFS")
	apk "${args[@]}" --initdb add \
		alpine-baselayout busybox musl kmod eudev util-linux \
		blkid e2fsprogs zstd xz gzip ca-certificates linux-lts \
		|| die "apk 拉取源 rootfs 失败(请检查网络与 ${ALPINE_BRANCH} 仓库可用性)"
}

# 从源 rootfs 读取内核版本(KVER),形如 6.6.x-lts.
detect_kver() {
	local d="${ROOTFS}/lib/modules"
	[ -d "$d" ] || die "源 rootfs 中没有 /lib/modules,linux-lts 安装可能失败"
	local entry
	for entry in "$d"/*; do
		[ -d "$entry" ] || continue
		KVER="$(basename "$entry")"
		break
	done
	[ -n "$KVER" ] || die "无法确定内核版本 KVER"
	log_info "内核版本 KVER=$KVER"
	# 因 apk 使用 --no-scripts 跳过了 linux-lts 的 depmod 触发器,
	# 这里显式生成 modules.dep,供后续 modprobe --show-depends 解析依赖.
	depmod -b "$ROOTFS" "$KVER" \
		|| die "源 rootfs depmod 失败,无法解析模块依赖"
}

# 创建 staging 目录:作为 initramfs 的根,安装运行期用户态(不含 linux-lts,
# 避免把全部内核模块塞进 initramfs),随后只选择性拷贝需要的模块.
build_staging_userspace() {
	STAGING="$(mktemp -d "${BUILD_DIR}/tmp/staging-${ARCH}.XXXXXX")"
	log_info "创建 staging 根目录: $STAGING"
	local -a args
	mapfile -t args < <(apk_args "$STAGING")
	apk "${args[@]}" --initdb add \
		alpine-baselayout busybox musl kmod eudev util-linux \
		blkid e2fsprogs zstd xz gzip ca-certificates \
		|| die "apk 安装 staging 用户态失败"
	install_runtime_tools
	ensure_busybox_links
	make_base_dirs
}

load_runtime_packages() {
	[ -f "$PACKAGES_FILE" ] || die "缺少工具配置文件: $PACKAGES_FILE"
	python3 - "$PACKAGES_FILE" "$ARCH" <<'PYEOF'
import sys
from pathlib import Path

import yaml

path = Path(sys.argv[1])
arch = sys.argv[2]
with path.open("r", encoding="utf-8") as fh:
    data = yaml.safe_load(fh) or {}

default = data.get("default", [])
architectures = data.get("architectures", {})
arch_pkgs = architectures.get(arch, [])

def validate(name, value):
    if value is None:
        return []
    if not isinstance(value, list) or not all(isinstance(i, str) and i.strip() for i in value):
        raise SystemExit(f"{name} 必须是非空字符串数组")
    return [i.strip() for i in value]

packages = []
seen = set()
for item in validate("default", default) + validate(f"architectures.{arch}", arch_pkgs):
    if item not in seen:
        seen.add(item)
        packages.append(item)

for item in packages:
    print(item)
PYEOF
}

install_runtime_tools() {
	log_info "安装内存系统默认/自定义工具包"
	local -a args pkgs
	mapfile -t args < <(apk_args "$STAGING")
	mapfile -t pkgs < <(load_runtime_packages)
	[ "${#pkgs[@]}" -gt 0 ] || { log_warn "packages.yaml 未配置任何工具包"; return 0; }
	apk "${args[@]}" add "${pkgs[@]}" \
		|| die "安装运行期工具包失败(请检查 ${PACKAGES_FILE} 中的包名是否存在)"
	RUNTIME_PACKAGES=("${pkgs[@]}")
	log_info "已安装工具包 ${#RUNTIME_PACKAGES[@]} 个"
}

# 跨架构组装时 busybox 的 applet 软链触发器可能不会执行,这里手动补齐
# /init 实际会用到的 applet 软链,确保 busybox sh 环境可用.
ensure_busybox_links() {
	local bb="${STAGING}/bin/busybox"
	[ -x "$bb" ] || die "staging 中缺少 /bin/busybox"
	local applet
	# 跨架构(aarch64)无法执行 busybox --install,故显式枚举 init / udhcpc 脚本
	# 实际用到的全部 applet,外加常用工具.遗漏会导致命令"not found"被静默吞掉
	# (例如缺 route 会使 udhcpc 设不上默认路由 -> 网络不可达).
	for applet in sh ash mount umount mkdir mknod mdev ip ifconfig route udhcpc \
		sleep cat ls ln cp mv rm sync echo printf grep sed cut tr head tail \
		basename dirname readlink find test [ wc sort uniq xargs \
		hostname dmesg lsmod uname switch_root pivot_root poweroff reboot halt \
		chmod chown dd mkfifo kill ps mountpoint blkid true false env \
		getty setsid cttyhack login clear less vi more id whoami free df du top watch; do
		ln -sf /bin/busybox "${STAGING}/bin/${applet}"
	done
}

# 创建 initramfs 必备的基础目录与设备节点.
make_base_dirs() {
	local d
	for d in proc sys dev run tmp etc root usr/local/bin lib/firmware \
		"lib/modules/${KVER}" etc/bootseed run/bootseed usr/share/udhcpc; do
		mkdir -p "${STAGING}/${d}"
	done
	# 提前准备 console / null,极早期 init 输出可用.
	[ -e "${STAGING}/dev/console" ] || mknod -m 600 "${STAGING}/dev/console" c 5 1
	[ -e "${STAGING}/dev/null" ]    || mknod -m 666 "${STAGING}/dev/null" c 1 3
}

# 解析单个模块的 .ko 路径及其全部依赖,逐个拷贝到 staging.
# 返回 0 表示模块存在(已拷贝),返回 1 表示该内核没有此模块.
copy_one_module() {
	local mod="$1"
	local out
	# --show-depends 会按依赖顺序列出每个 insmod 行,第二列是 .ko 绝对路径.
	if ! out="$(modprobe -d "$ROOTFS" --set-version "$KVER" \
		--show-depends "$mod" 2>/dev/null)"; then
		return 1
	fi
	local line path rel
	while IFS= read -r line; do
		path="$(printf '%s\n' "$line" | awk '{print $2}')"
		[ -n "$path" ] || continue
		[ -f "$path" ] || continue
		rel="${path#"$ROOTFS"}"
		mkdir -p "${STAGING}$(dirname "$rel")"
		cp -a "$path" "${STAGING}${rel}"
	done <<<"$out"
	return 0
}

# 遍历一份模块清单文件,逐个解析并拷贝;缺失的记入跳过列表(不报错).
process_module_list() {
	local list="$1"
	[ -f "$list" ] || { log_warn "模块清单不存在: $list"; return 0; }
	local mod
	while IFS= read -r mod; do
		mod="${mod%%#*}"
		mod="$(printf '%s' "$mod" | tr -d '[:space:]')"
		[ -n "$mod" ] || continue
		if copy_one_module "$mod"; then
			INCLUDED_MODULES+=("$mod")
		else
			SKIPPED_MODULES+=("$mod")
			printf '%s %s\n' "$ARCH" "$mod" \
				>>"${BUILD_DIR}/reports/missing-optional-modules.txt"
		fi
	done <"$list"
}

# 拷贝三份清单中的模块,并在 staging 内执行 depmod 生成依赖索引.
copy_modules() {
	log_info "解析并拷贝内核模块"
	process_module_list "${MODULES_DIR}/network-modules.txt"
	process_module_list "${MODULES_DIR}/storage-modules.txt"
	process_module_list "${MODULES_DIR}/optional-modules.txt"
	# 把实际尝试加载的模块名写入 initramfs,供 /init 运行期 modprobe 使用.
	printf '%s\n' "${INCLUDED_MODULES[@]}" \
		>"${STAGING}/etc/bootseed/modules.list"
	depmod -b "$STAGING" "$KVER" \
		|| die "depmod 失败,无法生成 modules.dep"
	log_info "已拷贝模块 ${#INCLUDED_MODULES[@]} 个,跳过 ${#SKIPPED_MODULES[@]} 个"
}

# 候选固件子包(按网卡 / HBA 家族挑选,避免拉取整个 linux-firmware 巨包).
# 仅列出 Alpine 实际存在的子包;不存在或安装失败的只告警,不失败.
# 说明:Intel i40e/ice/ixgbe 所需固件归于 linux-firmware-intel;
#       bnxt_en 通常从网卡 NVRAM 取固件,无独立子包.
firmware_candidates() {
	printf '%s\n' \
		linux-firmware-bnx2 \
		linux-firmware-bnx2x \
		linux-firmware-qed \
		linux-firmware-qlogic \
		linux-firmware-cxgb3 \
		linux-firmware-cxgb4 \
		linux-firmware-intel \
		linux-firmware-mellanox \
		linux-firmware-netronome
}

# 安装存在的固件子包到源 rootfs,再把 /lib/firmware 拷贝进 staging.
# 直接以 apk add 的退出码判定可用性(不再用 search 预检,避免 --no-cache 的
# fetch 进度输出污染判断).
copy_firmware() {
	log_info "处理网卡 / HBA 固件"
	local -a args
	mapfile -t args < <(apk_args "$ROOTFS")
	local pkg
	while IFS= read -r pkg; do
		if apk "${args[@]}" add "$pkg" >/dev/null 2>&1; then
			FW_PACKAGES+=("$pkg")
			log_info "已安装固件包: $pkg"
		else
			log_warn "固件包不可用(跳过): $pkg"
		fi
	done < <(firmware_candidates)

	if [ -d "${ROOTFS}/lib/firmware" ]; then
		cp -a "${ROOTFS}/lib/firmware/." "${STAGING}/lib/firmware/" 2>/dev/null || true
	fi
}

# 用源 rootfs 中 linux-lts 的内核与模块重建 vmlinuz / modloop,确保
# vmlinuz,modloop,initramfs 内模块三者内核版本严格一致(spec §12).
# 这会覆盖 prepare-alpine 下载的 netboot 版本--netboot 发行文件版本可能滞后于
# apk 仓库(实测 netboot=6.6.134 而 apk linux-lts=6.6.142),不一致会导致
# uname -r 与 /lib/modules 不匹配,模块全部加载失败,无网卡无磁盘.
override_kernel_and_modloop() {
	local outdir="${DATA_DIR}/http/alpine/${ARCH}"
	local kimg="${ROOTFS}/boot/vmlinuz-lts"
	[ -f "$kimg" ] || die "源 rootfs 缺少 /boot/vmlinuz-lts,无法对齐内核版本"
	cp -f "$kimg" "${outdir}/vmlinuz"
	log_info "已用 rootfs 内核覆盖 vmlinuz(与模块同为 ${KVER})"
	if command -v mksquashfs >/dev/null 2>&1; then
		local sqsrc
		sqsrc="$(mktemp -d "${BUILD_DIR}/tmp/modloop-${ARCH}.XXXXXX")"
		mkdir -p "${sqsrc}/modules"
		cp -a "${ROOTFS}/lib/modules/." "${sqsrc}/modules/" 2>/dev/null || true
		[ -d "${ROOTFS}/lib/firmware" ] && cp -a "${ROOTFS}/lib/firmware" "${sqsrc}/" 2>/dev/null || true
		rm -f "${outdir}/modloop"
		if mksquashfs "$sqsrc" "${outdir}/modloop" -comp xz -no-progress >/dev/null 2>&1; then
			log_info "已重建 modloop(与内核同版本 ${KVER})"
		else
			log_warn "mksquashfs 重建 modloop 失败,沿用现有 modloop(可能与内核不匹配)"
		fi
		rm -rf "$sqsrc"
	else
		log_warn "无 mksquashfs,无法重建 modloop"
	fi
}

# 写入 busybox udhcpc 事件脚本,负责把 DHCP 结果落到接口与 resolv.conf.
write_udhcpc_script() {
	local s="${STAGING}/usr/share/udhcpc/default.script"
	cat >"$s" <<'UDHCPC_EOF'
#!/bin/sh
# busybox udhcpc 事件脚本:配置 IP / 默认路由 / DNS
[ -n "$1" ] || exit 1
case "$1" in
	deconfig)
		ifconfig "$interface" 0.0.0.0 2>/dev/null
		;;
	bound|renew)
		ifconfig "$interface" "$ip" netmask "${subnet:-255.255.255.0}" 2>/dev/null
		if [ -n "$router" ]; then
			while route del default gw 0.0.0.0 dev "$interface" 2>/dev/null; do :; done
			for r in $router; do
				route add default gw "$r" dev "$interface" 2>/dev/null
			done
		fi
		: >/etc/resolv.conf
		[ -n "$domain" ] && echo "search $domain" >>/etc/resolv.conf
		for d in $dns; do echo "nameserver $d" >>/etc/resolv.conf; done
		;;
esac
exit 0
UDHCPC_EOF
	chmod +x "$s"
}

# 拷贝 Agent 二进制与 overlay 静态文件到 staging.
copy_agent_and_overlay() {
	install -m 0755 "${BUILD_DIR}/agent/bootseed-agent-${ARCH}" \
		"${STAGING}/usr/local/bin/bootseed-agent"
	if [ -d "$OVERLAY_DIR" ]; then
		cp -a "${OVERLAY_DIR}/." "${STAGING}/"
	fi
	inject_vendor_tools
	# 写入 /etc/alpine-release,供 agent 显示 Alpine 版本(boot.ipxe 未传 alpine_version 时回退用).
	mkdir -p "${STAGING}/etc"
	printf '%s\n' "${ALPINE_VERSION}" > "${STAGING}/etc/alpine-release"
}

inject_vendor_tools() {
	local root="${DATA_DIR}/vendor-tools"
	[ -d "$root" ] || return 0
	mkdir -p "${STAGING}/usr/local/bin"
	local copied=0
	local src
	for src in "$root/common" "$root/${ARCH}"; do
		[ -d "$src" ] || continue
		cp -a "${src}/." "${STAGING}/"
		copied=1
	done
	while IFS= read -r -d '' src; do
		cp -a "$src" "${STAGING}/usr/local/bin/"
		copied=1
	done < <(find "$root" -maxdepth 1 -type f -print0)
	if [ "$copied" -eq 1 ]; then
		find "${STAGING}/usr/local/bin" -type f -exec chmod 0755 {} +
		log_info "已注入 data/vendor-tools 自定义文件"
	fi
}

# 写入自定义 /init.先用 printf 注入超时常量,再追加 quoted heredoc 主体,
# 避免主体中的 $ 变量在构建期被宿主 shell 误展开.
write_init() {
	local init="${STAGING}/init"
	{
		printf '#!/bin/sh\n'
		printf '# BootSeed initramfs 入口:挂载 -> 驱动 -> 网络 -> exec agent\n'
		printf 'NETWORK_DEVICE_TIMEOUT=%s\n' "$NET_TIMEOUT"
		printf 'STORAGE_DEVICE_TIMEOUT=%s\n' "$STO_TIMEOUT"
	} >"$init"
	cat >>"$init" <<'INIT_EOF'
set -u
PATH=/sbin:/usr/sbin:/bin:/usr/bin
export PATH

msg() { printf '[bootseed-init] %s\n' "$*" >/dev/console 2>/dev/null || \
	printf '[bootseed-init] %s\n' "$*"; }

# 基础文件系统挂载(已挂载则忽略报错).
mount -t proc     none /proc 2>/dev/null
mount -t sysfs    none /sys  2>/dev/null
mount -t devtmpfs none /dev  2>/dev/null
mount -t tmpfs    none /run  2>/dev/null
mount -t tmpfs    none /tmp  2>/dev/null
mkdir -p /run/bootseed 2>/dev/null

# 启动设备管理:优先 udev,回退 busybox mdev.
start_devmgr() {
	if command -v udevd >/dev/null 2>&1; then
		udevd --daemon 2>/dev/null
		udevadm trigger --type=subsystems --action=add 2>/dev/null
		udevadm trigger --type=devices --action=add 2>/dev/null
		udevadm settle --timeout=30 2>/dev/null
	else
		echo /sbin/mdev >/proc/sys/kernel/hotplug 2>/dev/null
		mdev -s 2>/dev/null
	fi
}

# 按 /etc/bootseed/modules.list 逐个 modprobe(失败忽略).
load_modules() {
	[ -f /etc/bootseed/modules.list ] || return 0
	while read -r m; do
		[ -n "$m" ] || continue
		modprobe "$m" 2>/dev/null || true
	done </etc/bootseed/modules.list
}

# 从 /proc/cmdline 取某个 key 的值.
cmdline_val() {
	for tok in $(cat /proc/cmdline); do
		case "$tok" in
			"$1"=*) printf '%s' "${tok#*=}"; return 0 ;;
		esac
	done
	return 1
}

# 确定 PXE 网卡:优先按 node_mac 匹配,否则取第一个非 lo 接口.
find_pxe_nic() {
	want=$(printf '%s' "${1:-}" | tr 'A-Z' 'a-z')
	if [ -n "$want" ]; then
		for ifp in /sys/class/net/*; do
			[ -e "$ifp/address" ] || continue
			a=$(tr 'A-Z' 'a-z' <"$ifp/address" 2>/dev/null)
			[ "$a" = "$want" ] && { basename "$ifp"; return 0; }
		done
	fi
	for ifp in /sys/class/net/*; do
		n=$(basename "$ifp"); [ "$n" = "lo" ] && continue
		printf '%s' "$n"; return 0
	done
	return 1
}

# 等待至少一个非 lo 网卡出现.
wait_network_device() {
	i=0
	while [ "$i" -lt "$NETWORK_DEVICE_TIMEOUT" ]; do
		for ifp in /sys/class/net/*; do
			n=$(basename "$ifp"); [ "$n" = "lo" ] && continue
			return 0
		done
		sleep 1; i=$((i + 1))
	done
	return 1
}

# 等待出现至少一个本地块设备(排除 ram/loop/zram).
wait_storage_device() {
	i=0
	while [ "$i" -lt "$STORAGE_DEVICE_TIMEOUT" ]; do
		for b in /sys/block/*; do
			n=$(basename "$b")
			case "$n" in ram*|loop*|zram*) continue ;; esac
			[ -e "$b" ] && return 0
		done
		sleep 1; i=$((i + 1))
	done
	return 1
}

# 无网卡时把硬件诊断信息打到控制台.
dump_diag() {
	msg "===== 未发现可用网卡,输出诊断信息 ====="
	if command -v lspci >/dev/null 2>&1; then
		lspci -k >/dev/console 2>&1
	else
		ls /sys/bus/pci/devices >/dev/console 2>&1
	fi
	msg "----- 已加载模块 -----"; lsmod >/dev/console 2>&1
	msg "----- /sys/class/net -----"; ls /sys/class/net >/dev/console 2>&1
	msg "----- PCI 设备及驱动 -----"
	for d in /sys/bus/pci/devices/*; do
		drv=""; [ -e "$d/driver" ] && drv=$(basename "$(readlink "$d/driver")")
		printf '%s vendor=%s device=%s driver=%s\n' "$(basename "$d")" \
			"$(cat "$d/vendor" 2>/dev/null)" "$(cat "$d/device" 2>/dev/null)" "${drv:-none}" >/dev/console 2>&1
	done
	msg "----- dmesg 末尾 -----"; dmesg 2>/dev/null | tail -n 80 >/dev/console 2>&1
}

start_devmgr
load_modules
start_devmgr

msg "等待网络设备(超时 ${NETWORK_DEVICE_TIMEOUT}s)"
wait_network_device || dump_diag
msg "等待存储设备稳定(超时 ${STORAGE_DEVICE_TIMEOUT}s)"
wait_storage_device || msg "未检测到本地存储,继续(交由 Agent 处理)"

DEPLOY_SERVER=$(cmdline_val deploy_server || true)
NODE_ARCH=$(cmdline_val node_arch || true)
NODE_MAC=$(cmdline_val node_mac || true)
AGENT_PORT=$(cmdline_val agent_port || true)
NET_STATUS_FILE=/run/bootseed/network-status.json
IMPORTED_NETWORK_APPLIED=0

ip link set dev lo up 2>/dev/null
hostname bootseed 2>/dev/null

write_net_status() {
	mode="$1"; status="$2"; message="${3:-}"
	cat >"$NET_STATUS_FILE" <<EOF
{"mode":"$mode","status":"$status","message":"$message"}
EOF
}

json_string() {
	key="$1"; file="$2"
	sed -n "s/.*\"$key\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p" "$file" | head -n1
}

json_number() {
	key="$1"; file="$2"
	sed -n "s/.*\"$key\"[[:space:]]*:[[:space:]]*\\([0-9][0-9]*\\).*/\\1/p" "$file" | head -n1
}

json_array_items() {
	key="$1"; file="$2"
	sed -n "s/.*\"$key\"[[:space:]]*:[[:space:]]*\\[\\([^]]*\\)\\].*/\\1/p" "$file" \
		| head -n1 | tr -d '"' | tr ',' ' '
}

find_iface_by_mac() {
	want=$(printf '%s' "${1:-}" | tr 'A-Z' 'a-z')
	[ -n "$want" ] || return 1
	for ifp in /sys/class/net/*; do
		[ -e "$ifp/address" ] || continue
		addr=$(tr 'A-Z' 'a-z' <"$ifp/address" 2>/dev/null)
		[ "$addr" = "$want" ] && { basename "$ifp"; return 0; }
	done
	return 1
}

apply_imported_network() {
	cfg=/etc/bootseed/imported-node-config.json
	[ -f "$cfg" ] || return 1
	cfg_mac=$(json_string mac "$cfg")
	cfg_if=$(json_string iface "$cfg")
	cfg_addr=$(json_string address "$cfg")
	cfg_prefix=$(json_number prefix_len "$cfg")
	cfg_gateway=$(json_string gateway "$cfg")
	cfg_server=$(json_string server_url "$cfg")
	cfg_dns=$(json_array_items dns "$cfg")
	ifc=$(find_iface_by_mac "$cfg_mac" || true)
	[ -n "$ifc" ] || ifc="$cfg_if"
	if [ -z "$ifc" ] || [ -z "$cfg_addr" ] || [ -z "$cfg_prefix" ]; then
		write_net_status static failed "imported config incomplete"
		return 1
	fi
	ip link set dev "$ifc" up 2>/dev/null || {
		write_net_status static failed "failed to bring up $ifc"
		return 1
	}
	ip addr flush dev "$ifc" scope global 2>/dev/null || true
	ip addr add "$cfg_addr/$cfg_prefix" dev "$ifc" >/dev/console 2>&1 || {
		write_net_status static failed "failed to set address on $ifc"
		return 1
	}
	if [ -n "$cfg_gateway" ]; then
		ip route del default >/dev/null 2>&1 || true
		ip route add default via "$cfg_gateway" dev "$ifc" >/dev/console 2>&1 || {
			write_net_status static failed "failed to set gateway"
			return 1
		}
	fi
	: >/etc/resolv.conf
	for d in $cfg_dns; do echo "nameserver $d" >>/etc/resolv.conf; done
	[ -n "$cfg_server" ] && [ -z "$DEPLOY_SERVER" ] && DEPLOY_SERVER="$cfg_server"
	NIC="$ifc"
	IMPORTED_NETWORK_APPLIED=1
	write_net_status static ok "restored $ifc"
	msg "静态网络已恢复: $ifc $cfg_addr/$cfg_prefix gw=$cfg_gateway"
	return 0
}

# 拉起接口并等待 carrier(虚拟/物理网卡 link up 后载波可能延迟若干秒).
bring_up_and_dhcp() {
	ifc="$1"
	ip link set dev "$ifc" up 2>/dev/null
	c=0
	while [ "$c" -lt 10 ]; do
		[ "$(cat "/sys/class/net/$ifc/carrier" 2>/dev/null)" = "1" ] && break
		sleep 1; c=$((c + 1))
	done
	msg "在 $ifc 上请求 DHCP(carrier=$(cat "/sys/class/net/$ifc/carrier" 2>/dev/null))"
	# 前台 + 拿到租约即退出;输出打到控制台便于排查
	udhcpc -i "$ifc" -s /usr/share/udhcpc/default.script -f -q -t 8 -T 3 >/dev/console 2>&1
}

NIC=$(find_pxe_nic "$NODE_MAC" || true)
if apply_imported_network; then
	msg "使用导入的静态网络配置"
else
	[ -f /etc/bootseed/imported-node-config.json ] && msg "导入网络配置失败,回退 DHCP"
	[ -n "$NIC" ] && { msg "PXE 网卡 $NIC"; bring_up_and_dhcp "$NIC" || true; }
fi

# 兜底:遍历真实网卡(含 address 文件,排除 lo/bond*)尝试 DHCP.
if ! ip route 2>/dev/null | grep -q '^default'; then
	for ifp in /sys/class/net/*; do
		[ -e "$ifp/address" ] || continue
		n=$(basename "$ifp")
		case "$n" in lo|bond*|bonding_masters) continue ;; esac
		[ "$n" = "$NIC" ] && continue
		msg "兜底尝试接口 $n"
		bring_up_and_dhcp "$n" || true
		ip route 2>/dev/null | grep -q '^default' && break
	done
fi
if ip route 2>/dev/null | grep -q '^default'; then
	if [ -f /etc/bootseed/imported-node-config.json ]; then
		[ "$IMPORTED_NETWORK_APPLIED" -eq 1 ] || write_net_status static fallback_dhcp "static config unavailable, using dhcp"
	else
		write_net_status dhcp ok "dhcp ready"
	fi
	msg "网络就绪:$(ip -o -4 addr show 2>/dev/null | grep -v ' lo ' | awk '{print $2,$4}' | tr '\n' ' ')"
else
	[ -f /etc/bootseed/imported-node-config.json ] && write_net_status static failed "no route after static/dhcp fallback"
	[ ! -f /etc/bootseed/imported-node-config.json ] && write_net_status dhcp failed "dhcp failed"
	msg "未获取到 IP / 默认路由"
	dump_diag
fi

[ -f /etc/issue ] && cat /etc/issue >/dev/console 2>/dev/null
msg "deploy_server=${DEPLOY_SERVER} node_arch=${NODE_ARCH} agent_port=${AGENT_PORT}"
msg "启动 bootseed-agent ..."

# 后台启动 agent(agent 自行从 /proc/cmdline 解析身份与端口).
/usr/local/bin/bootseed-agent &
AGENT_PID=$!

# 在 VNC(tty0) 与串口(ttyS0/ttyAMA0) 提供 root 登录 shell,便于进入内存系统排查.
# 使用 getty 自动登录到 /bin/sh;退出后自动重启(respawn).
spawn_login() {
	_tty="$1"
	[ -e "/dev/$_tty" ] || return 0
	( while true; do
		setsid getty -n -l /bin/sh 115200 "$_tty" vt100 2>/dev/null \
			|| setsid sh -c "exec </dev/$_tty >/dev/$_tty 2>&1; exec /bin/sh" 2>/dev/null
		sleep 1
	done ) &
}
spawn_login tty0
spawn_login ttyS0
spawn_login ttyAMA0
msg "已在 VNC(tty0)/串口 开放 root shell,可直接进入内存系统"

# PID1 保活:等待 agent 退出;退出后保留应急 shell.
wait "$AGENT_PID" 2>/dev/null
msg "bootseed-agent 已退出,进入应急 shell"
exec /bin/sh
INIT_EOF
	chmod +x "$init"
}

# 打包 initramfs:newc 格式 cpio + gzip(gzip 兼容性最好,所有内核都支持).
pack_initramfs() {
	local out="${DATA_DIR}/http/alpine/${ARCH}/initramfs-deploy"
	log_info "打包 initramfs -> $out"
	( cd "$STAGING" && find . -print0 | cpio --null -o -H newc 2>/dev/null ) \
		| gzip -9 >"$out" || die "打包 initramfs 失败"
}

# 把字符串数组渲染为 JSON 数组(已做最小转义,模块/包名不含特殊字符).
json_array() {
	local first=1 e
	printf '['
	for e in "$@"; do
		[ "$first" -eq 1 ] && first=0 || printf ', '
		printf '"%s"' "$e"
	done
	printf ']'
}

# 生成 manifest.json.
write_manifest() {
	local out="${DATA_DIR}/http/alpine/${ARCH}/manifest.json"
	local build_time
	build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	local mods fws
	mods="$(json_array ${INCLUDED_MODULES[@]+"${INCLUDED_MODULES[@]}"})"
	fws="$(json_array ${FW_PACKAGES[@]+"${FW_PACKAGES[@]}"})"
	local tools
	tools="$(json_array ${RUNTIME_PACKAGES[@]+"${RUNTIME_PACKAGES[@]}"})"
	cat >"$out" <<JSON
{
  "architecture": "${ARCH}",
  "alpine_version": "${ALPINE_VERSION}",
  "kernel_flavor": "lts",
  "kernel_version": "${KVER}",
  "kernel_file": "vmlinuz",
  "initramfs_file": "initramfs-deploy",
  "modloop_file": "modloop",
  "agent_version": "${AGENT_VERSION}",
  "build_time": "${build_time}",
  "included_runtime_packages": ${tools},
  "included_modules": ${mods},
  "included_firmware_packages": ${fws}
}
JSON
	log_info "已生成 manifest: $out"
}

# 写模块与固件报告.
write_reports() {
	local mrep="${BUILD_DIR}/reports/${ARCH}-modules.txt"
	local frep="${BUILD_DIR}/reports/${ARCH}-firmware.txt"
	local trep="${BUILD_DIR}/reports/${ARCH}-tools.txt"
	{
		echo "# ${ARCH} 内核模块报告 (KVER=${KVER})"
		echo "# 已包含 (${#INCLUDED_MODULES[@]}):"
		printf '%s\n' ${INCLUDED_MODULES[@]+"${INCLUDED_MODULES[@]}"}
		echo "# 已跳过 (${#SKIPPED_MODULES[@]}, 该内核无此模块):"
		printf '%s\n' ${SKIPPED_MODULES[@]+"${SKIPPED_MODULES[@]}"}
	} >"$mrep"
	{
		echo "# ${ARCH} 固件包报告"
		echo "# 已安装固件包 (${#FW_PACKAGES[@]}):"
		printf '%s\n' ${FW_PACKAGES[@]+"${FW_PACKAGES[@]}"}
	} >"$frep"
	{
		echo "# ${ARCH} 运行期工具包报告"
		echo "# 已安装工具包 (${#RUNTIME_PACKAGES[@]}):"
		printf '%s\n' ${RUNTIME_PACKAGES[@]+"${RUNTIME_PACKAGES[@]}"}
	} >"$trep"
	log_info "已写报告: $mrep / $frep / $trep"
}

# 退出时清理临时 rootfs / staging.
cleanup() {
	[ -n "${ROOTFS:-}" ] && [ -d "$ROOTFS" ] && rm -rf "$ROOTFS"
	[ -n "${STAGING:-}" ] && [ -d "$STAGING" ] && rm -rf "$STAGING"
	return 0
}

main() {
	[ "$#" -eq 1 ] || { usage; die "需要恰好一个架构参数"; }
	validate_arch "$1"
	validate_env
	validate_inputs

	mkdir -p "${BUILD_DIR}/tmp" "${BUILD_DIR}/reports" \
		"${DATA_DIR}/http/alpine/${ARCH}"
	# 清理本架构在 missing 报告里的旧记录,避免重复累积.
	local miss="${BUILD_DIR}/reports/missing-optional-modules.txt"
	if [ -f "$miss" ]; then
		grep -v "^${ARCH} " "$miss" >"${miss}.tmp" 2>/dev/null || true
		mv "${miss}.tmp" "$miss"
	fi
	trap cleanup EXIT

	create_rootfs
	detect_kver
	build_staging_userspace
	copy_modules
	copy_firmware
	override_kernel_and_modloop
	copy_agent_and_overlay
	write_udhcpc_script
	write_init
	pack_initramfs
	write_manifest
	write_reports

	log_info "完成:${ARCH} initramfs 已生成于 ${DATA_DIR}/http/alpine/${ARCH}/"
}

main "$@"
