#!/bin/sh
# xray-edge 容器入口: xray 进程 + 配置监听 + 热更新执行器
#
# 语义 (基于 Xray 26.10.10 实测):
#   Xray 没有全量配置热加载命令(`-s` 不存在, 实测)。因此:
#   A) 新增节点/全量配置变化 → 优雅重启: SIGTERM 等在途连接排空 → 拉起
#   B) 节点摘除(剔除) → `xray api rmo <tag>` 运行时移除 outbound, balancer 自动跳过,
#      不重启、在途连接不断
#   C) 节点回池 → `xray api ado <file.json>` 运行时加回(该 tag 需在 balancer 列表内)
#   D) 调试/紧急切单节点 → `xray api bo`(强制 balancer 指向单一 outbound)
#   用户层 xray-user 完全不受影响。
#
# IPC: ingest 原子写 /configs/.op(每次追加一行指令, watcher 逐行消费):
#   restart                  优雅重启(新增节点/全量配置生效)
#   rmo <tag> [<tag>...]     摘除 outbound(剔除场景, 热更不重启)
#   ado <file.json>          热添加 outbound(节点回池)
#   bo <balancer> <tag>      强制 balancer 指定目标(紧急切单节点/调试)
#   bo <balancer> -r         取消 override, 恢复正常选路
# /configs/config.json 变化(原子 rename) 也等价触发一次 restart。
# xray API 地址: 127.0.0.1:8080 (config 里 api.listen 必须显式配置, 实测默认不监听)
set -u
CONFIG=/configs/config.json
OPFILE=/configs/.op
PIDFILE=/run/xray.pid

[ -f "$CONFIG" ] || { echo "[watcher] seeding config from /base-config.json"; cp /base-config.json "$CONFIG"; }
[ -f "$OPFILE" ] || : > "$OPFILE"

start_xray() {
  xray run -c "$CONFIG" &
  echo $! > "$PIDFILE"
  echo "[watcher] xray started (pid $(cat $PIDFILE))"
}

stop_xray() {
  [ -f "$PIDFILE" ] || return 0
  kill -TERM "$(cat "$PIDFILE")" 2>/dev/null || true
  i=0
  while kill -0 "$(cat "$PIDFILE")" 2>/dev/null && [ $i -lt 10 ]; do
    sleep 0.5
    i=$((i + 1))
  done
  kill -KILL "$(cat "$PIDFILE")" 2>/dev/null || true
  echo "[watcher] xray stopped"
}

apply_op() {
  line="$1"
  case "$line" in
    restart|"")
      echo "[watcher] op=restart (graceful)"
      stop_xray
      start_xray
      ;;
    rmo*|"rmo")
      # rmo <tag> [<tag>...]
      set -- $line
      shift
      for t in "$@"; do
        if xray api rmo "$t" 2>&1; then
          echo "[watcher] rmo $t ok"
        else
          echo "[watcher] rmo $t FAILED (check tag / xray running)"
        fi
      done
      ;;
    bo*|"bo")
      # bo <balancer> <target>   或   bo <balancer> -r
      set -- $line
      shift
      if [ "${2:-}" = "-r" ]; then
        xray api bo -b "$1" -r && echo "[watcher] bo override cleared" || echo "[watcher] bo -r FAILED"
      else
        xray api bo -b "$1" "$2" && echo "[watcher] bo $1→$2 ok" || echo "[watcher] bo FAILED"
      fi
      ;;
    ado*|"ado")
      # ado <file.json>
      set -- $line
      shift
      xray api ado "$1" && echo "[watcher] ado $1 ok" || echo "[watcher] ado FAILED"
      ;;
    *)
      echo "[watcher] unknown op ignored: $line"
      ;;
  esac
}

trap 'echo "[watcher] stopping"; stop_xray; wait' TERM INT
start_xray

# 监听 config.json(全量变化→restart) 与 .op(热更新指令队列)
inotifywait -m -e modify,move_create "$CONFIG" "$OPFILE" 2>/dev/null | while read -r _ file; do
  case "$file" in
    "$OPFILE"|.op)
      # 逐行消费指令(读完整行再清空, 防半行)
      while IFS= read -r line; do
        [ -n "$line" ] && apply_op "$line"
      done < "$OPFILE"
      : > "$OPFILE"
      ;;
    *)
      # config.json 原子 rename 完成
      sleep 0.3
      apply_op "restart"
      ;;
  esac
done
