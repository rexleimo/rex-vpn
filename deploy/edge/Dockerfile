# xray-edge 镜像: pin xray 官方 + inotify watcher 热加载
# 用途: 配置 100% 由 ingest 生成(/configs 共享卷), 本容器只负责跑 xray + 监听配置变化
ARG XRAY_TAG=latest
FROM teddysun/xray:${XRAY_TAG}
RUN apk add --no-cache inotify-tools curl
COPY config-base.json /base-config.json
COPY watcher.sh /usr/local/bin/watcher.sh
RUN chmod +x /usr/local/bin/watcher.sh
CMD ["/usr/local/bin/watcher.sh"]
