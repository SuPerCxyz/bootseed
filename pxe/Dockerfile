# ============================================================
# BootSeed PXE 服务镜像
# dnsmasq ProxyDHCP + TFTP
# 以 host 网络运行，TFTP 文件与 boot.ipxe 通过只读挂载提供
# ============================================================
FROM alpine:3.20

# dnsmasq 提供 ProxyDHCP/TFTP；gettext 提供 envsubst 用于渲染配置
RUN apk add --no-cache dnsmasq gettext

# 配置模板与入口脚本
COPY dnsmasq.conf.template /etc/bootseed/dnsmasq.conf.template
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

# TFTP 根目录（运行时由宿主机以只读方式挂载）
# /var/lib/http-boot 由 web 服务挂载，此处仅作约定说明
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
