#!/bin/sh
# xray-edge 容器入口: xray 进程 + 配置文件热加载 watcher
# 语义: ingest 原子写 /configs/config.json (.tmp+rename) 后, 本脚本:
#   1) 尝试 `xray -s reload` 热加载(不断现有连接); 2) 失败则重启 xray 进程
set -u
CONFIG=/configs/config.json
PIDFILE=/run/xray.pid

# 卷首次为空时, 用镜像内基础配置播种(仅 bootstrap, 后续一律由 ingest 接管)
if [ ! -f "$CONFIG" ]; then
  echo "[watcher] $CONFIG missing, seeding from /base-config.json"
  cp /base-config.json "$CONFIG"
fi

start_xray() {
  xray run -c "$CONFIG" &
  echo $! > "$PIDFILE"
  echo "[watcher] xray started (pid $(cat $PIDFILE))"
}

trap 'echo "[watcher] stopping"; kill $(cat $PIDFILE) 2>/dev/null; wait' TERM INT
start_xray

inotifywait -m -e modify,move_create "$CONFIG" 2>/dev/null | while read -r _; do
  echo "[watcher] config changed, hot-reload"
  sleep 0.5   # 等待 ingest 的原子 rename 完成
  if xray -s reload -c "$CONFIG" 2>/dev/null; then
    echo "[watcher] hot reload ok"
  else
    echo "[watcher] hot reload failed, restarting xray process"
    kill "$(cat "$PIDFILE")" 2>/dev/null || true
    wait || true
    start_xray
  fi
done
