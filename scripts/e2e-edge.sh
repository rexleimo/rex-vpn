#!/usr/bin/env bash
# e2e-edge.sh (work-5.4): 全链路 xray-edge 出口断言
#
# 前置:
#   - make dev 已起 (ingest 生成配置 + xray-edge 运行, 共享 xray-edge-config 卷)
#   - deploy/.env 配了真实上游 SUB_UPSTREAM_1_URL, active 池非空
# 断言: 一次性 xray 客户端(socks5 inbound → vless-ws outbound 指 xray-edge:10086)
#       隧道打 https://api.ipify.org, 出口 IP 非空且 ≠ 本机直连; ∈ 池为加分
#       (池常为域名/双栈, 字面匹配不可靠; 分流断言见 load-50.sh)
# 退出码: 0=通过, 2=前置不满足, 1=断言失败。
set -euo pipefail

COMPOSE="docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml"
CLIENT_ID="${EDGE_CLIENT_ID:-00000000-0000-4000-8000-000000000001}"
WS_PATH="${EDGE_WS_PATH:-/edge-ws}"
EDGE_SVC="${EDGE_SVC:-xray-edge}"    # compose 服务名(客户端容器内 DNS 可达)
EDGE_PORT="${EDGE_PORT:-10086}"
EDGE_TLS="${EDGE_TLS:-0}"            # 1=ws+tls(需 xray-edge 配证书), 0=明文 ws(MVP 默认)

cd "$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
log() { echo "[e2e-edge] $*"; }
die() { echo "[e2e-edge][FAIL] $*" >&2; exit 1; }

# ---- 前置检查 ----
$COMPOSE ps --status running 2>/dev/null | grep -q "$EDGE_SVC" || {
  log "$EDGE_SVC 未运行; 先 make dev (真实 sub 需 deploy/.env: SUB_UPSTREAM_1_URL)"; exit 2; }

# 当前 xray-edge 配置里的上游节点 IP 集合
POOL_IPS=$($COMPOSE exec -T "$EDGE_SVC" sh -c 'cat /configs/config.json' 2>/dev/null \
  | grep -oE '"address"[[:space:]]*:[[:space:]]*"[^"]+"' \
  | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | sort -u || true)
[ -n "$POOL_IPS" ] || die "xray-edge 配置无上游节点 (active 池为空? 查 ingest 日志/探测状态)"
log "上游池 IP: $(echo "$POOL_IPS" | tr '\n' ' ')"

# ---- 一次性 vless 客户端 (跑在 compose 网络内, 复用 xray-edge 镜像里的 xray) ----
TLS_STREAM=""
if [ "$EDGE_TLS" = "1" ]; then
  TLS_STREAM=",\"security\":\"tls\",\"tlsSettings\":{\"serverName\":\"$EDGE_SVC\"}"
fi
CLIENT_CFG=$(cat <<EOF
{
  "log": {"loglevel": "warning"},
  "inbounds": [{"tag":"in","listen":"127.0.0.1","port":10800,"protocol":"socks","settings":{"udp":false}}],
  "outbounds": [{
      "tag":"up","protocol":"vless",
      "settings":{"vnext":[{"address":"$EDGE_SVC","port":$EDGE_PORT,
        "users":[{"id":"$CLIENT_ID","encryption":"none","flow":""}]}]},
      "streamSettings":{"network":"ws"$TLS_STREAM,"wsSettings":{"path":"$WS_PATH"}}
  }],
  "routing": {"domainStrategy":"AsIs","rules":[{"type":"field","domain":["regexp:.*"],"outboundTag":"up"},{"type":"field","ip":["0.0.0.0/0"],"outboundTag":"up"}]}
}
EOF
)

log "经 $EDGE_SVC:$EDGE_PORT (vless-ws) 打出口探测…"
probe_exit_ip() {
  $COMPOSE run --rm --entrypoint sh "$EDGE_SVC" -c "
  cat > /tmp/c.json <<'CFG'
$CLIENT_CFG
CFG
  xray run -c /tmp/c.json >/tmp/xray-client.log 2>&1 &
  XPID=\$!
  for i in 1 2 3 4 5 6 7 8 9 10; do
    OUT=\$(curl -s --max-time 15 -x socks5://127.0.0.1:10800 https://api.ipify.org || true)
    [ -n \"\$OUT\" ] && { echo \"\$OUT\"; kill \$XPID 2>/dev/null; exit 0; }
    sleep 1
  done
  kill \$XPID 2>/dev/null
  tail -5 /tmp/xray-client.log
  exit 1
" 2>/dev/null | grep -oE '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true
}
OUT_IP=""
for i in $(seq 1 5); do
  OUT_IP=$(probe_exit_ip)
  [ -n "$OUT_IP" ] && break
  log "try$i 未出流量, 重试 (balancer 可能轮询到死节点)…"
  sleep 3
done

[ -n "$OUT_IP" ] || die "未取到出口 IP (隧道不通? 查 xray-edge 日志: make logs)"
log "测得出口 IP: $OUT_IP"

# ---- 断言 (与 e2e-full 线1 口径一致): ∈ 池为加分; 非空且 ≠ 本机直连即通过 ----
# 上游池常为域名/双栈, 出口 IP 字面 ∈ 池 IPv4 集合不可靠; 分流断言见 load-50.sh
if echo "$POOL_IPS" | grep -qx "$OUT_IP"; then
  log "PASS: 出口 IP $OUT_IP ∈ 上游池 (balancer 命中池内 IPv4 节点)"; exit 0
fi
HOST_IP=$(curl -s --max-time 15 https://api.ipify.org || true)
if [ -n "$HOST_IP" ] && [ "$OUT_IP" = "$HOST_IP" ]; then
  die "出口 IP $OUT_IP = 宿主机直连 IP — 流量没走代理 (路由/balancer 失效?)"
fi
log "PASS: 出口 IP 非空且 ≠ 本机直连 (${HOST_IP:-<探测失败>}); 池 IP 仅参考 {$(echo "$POOL_IPS" | tr '\n' ' ')}"
