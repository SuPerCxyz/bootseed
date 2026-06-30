# PXE 验证实验环境（aicode / KVM）

本目录提供在**单台装有 KVM + libvirt + Docker** 的主机上复现 BootSeed PXE
全链路验证的最小脚手架。已在 aicode 实测：UEFI x86_64 虚拟机 PXE 启动 →
Alpine 内存系统 → bootseed-agent → 镜像写盘成功（见 `AGENTS.md` §6.1）。

## 背景：为什么用单实例 lab dnsmasq

单宿主机有两条硬约束：

1. 同一网桥接口上两个 DHCP 守护进程无法都收到 DHCP 广播（OS 限制）。
2. 自动化沙箱会杀掉非 docker/libvirt 托管的后台进程。

因此实验用**单实例** dnsmasq（Docker 托管）同时承担 DHCP + TFTP + iPXE 路由，
即“我也管理 DHCP”的部署模式。它仍然完整驱动 BootSeed 的引导链
（TFTP iPXE → HTTP → Alpine → agent → 写盘）。

产品真正的 ProxyDHCP「与现网 DHCP 共存」需要两台独立主机/独立 L2 端点，
那是产品设计的真实场景；其共存机制在包级别已验证。

## 复现步骤

```bash
cd /home/superc/code/bootseed

# 1) .env 指向 libvirt default 网络（virbr0=192.168.122.1）
sed -i 's/^PXE_INTERFACE=.*/PXE_INTERFACE=virbr0/; \
        s/^PXE_SUBNET=.*/PXE_SUBNET=192.168.122.0/; \
        s/^PXE_SERVER_IP=.*/PXE_SERVER_IP=192.168.122.1/; \
        s/^HTTP_PORT=.*/HTTP_PORT=8088/; s/^AGENT_PORT=.*/AGENT_PORT=8088/' .env

# 2) 构建产物（需联网 + root + docker）
make prepare-ipxe                 # 三架构 iPXE（已用 v2.0.0）
make build-initramfs-x86_64       # 容器内构建 x86_64 initramfs（vmlinuz/modloop 同核版本）
bash scripts/generate-config.sh   # 生成 data/http/boot/*.ipxe

# 3) 起 web 容器
docker compose up -d --build bootseed-web

# 4) 关闭 libvirt 默认 DHCP，起单实例 lab dnsmasq（DHCP+TFTP+iPXE 路由）
sudo virsh net-update default delete ip-dhcp-range \
  --xml "<range start='192.168.122.2' end='192.168.122.254'/>" --live --config
docker rm -f bs-lab-dhcp 2>/dev/null
docker run -d --name bs-lab-dhcp --network host --cap-add NET_RAW --cap-add NET_ADMIN \
  --entrypoint dnsmasq \
  -v "$PWD/test/pxe-lab/lab-dnsmasq.conf:/etc/lab.conf:ro" \
  -v "$PWD/data/tftp:/var/lib/tftp:ro" \
  bootseed/pxe:latest -k -C /etc/lab.conf --log-facility=-

# 5) 造一个小测试镜像并登记
truncate -s 64M /tmp/t.raw && sgdisk -n 1:0:0 -t 1:8300 /tmp/t.raw
zstd -q -f /tmp/t.raw -o /tmp/test-x86_64.raw.zst
bash scripts/add-image.sh --file /tmp/test-x86_64.raw.zst --id test-x86_64 \
  --name "BootSeed Test x86_64" --os test --version 1 \
  --architecture x86_64 --firmware uefi --raw-size 67108864

# 6) 建 UEFI 测试虚拟机并 PXE 启动
sudo virt-install --name bs-x86 --memory 2560 --vcpus 2 --cpu host \
  --boot uefi --pxe \
  --network network=default,model=virtio \
  --disk path=/var/lib/libvirt/images/bs-x86.qcow2,size=2,bus=virtio,serial=BSDISK01,format=qcow2 \
  --osinfo detect=on,require=off --graphics none \
  --serial file,path=/tmp/bs-x86-serial.log --noautoconsole
# 每次重启前重置 NVRAM 以恢复网络优先启动：
#   sudo virsh destroy bs-x86; sudo rm -f /var/lib/libvirt/qemu/nvram/bs-x86_VARS.fd; sudo virsh start bs-x86

# 7) 串口看到 "BootSeed is ready / http://192.168.122.150:8088" 后，从宿主验证
curl -s http://192.168.122.150:8088/api/context | python3 -m json.tool
curl -s http://192.168.122.150:8088/api/disks   | python3 -m json.tool
curl -s -X POST http://192.168.122.150:8088/api/deploy \
  -H 'Content-Type: application/json' \
  -d '{"image_id":"test-x86_64","target_disk":"/dev/disk/by-id/virtio-BSDISK01","confirmation":"ERASE","verify_raw":true}'
curl -s http://192.168.122.150:8088/api/deploy/status   # state=completed, 100%
```

## 清理

```bash
sudo virsh destroy bs-x86; sudo virsh undefine bs-x86 --nvram
docker rm -f bs-lab-dhcp
# 如需恢复 libvirt 默认 DHCP：
sudo virsh net-update default add ip-dhcp-range \
  --xml "<range start='192.168.122.2' end='192.168.122.254'/>" --live --config
```
