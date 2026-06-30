# BootSeed Web (bootseed-web) 镜像
# 基于官方 nginx alpine 版本，仅提供静态 HTTP 服务。
FROM nginx:1.27-alpine

# 使用自定义主配置覆盖默认配置。
COPY nginx.conf /etc/nginx/nginx.conf

# 说明：静态根目录 /usr/share/nginx/html 不打包进镜像，
# 由 docker-compose 将宿主机 data/http 以只读方式绑定挂载到该路径。
# data/http 内包含 boot/、alpine/、images/ 等运行时生成的内容。

# 对外暴露 HTTP 端口（实际宿主机端口由 compose 的 HTTP_PORT 映射）。
EXPOSE 80
