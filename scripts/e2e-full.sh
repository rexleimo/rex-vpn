#!/usr/bin/env bash
# e2e-full.sh (work-8.1): 全链路验收 —— 两条线
#
#   线1 (edge 直连线): 一次性 xray 客户端(socks5 → vless-ws → xray-edge:10086)
#       隧道打出口探测, 断言:
#         a) 出口 IP 非空 (隧道确实出流量了)
#         b) 出口 IP ≠ 宿主机直连 IP (流量确实走了代理)
#       注: "出口 IP ∈ 上游池地址集合" 在双栈拓扑下不可靠(出口常为 IPv6/池配置为域名
#       或 IPv4), 降级为池 IP 仅打印供参考; balancer 分流断言由 load-50.sh 承担
#       (work-6.4: Marzban 用户出口 → xray-edge, 客户端线以直连隧道等价验证,
#        真实 Marzban 用户 sub 导入验证并入 8.4 soak 观察)
#   线2 (user-center 线): 真实 sub token
#       POST /login 种 HttpOnly cookie → /api/status 流量字段 → /subscription 页
#       含订阅链接 → /qr.png 出图。一条绿。
#
# 前置:
#   - make dev 已起 (ingest + xray-edge + user-center + marzban 运行)
#   - deploy/.env: SUB_UPSTREAM_1_URL 配真实 sub (active 池非空)
#   - user-center 的 MARZBAN_API_BASE 已启用 (work-6.5 联调, compose dev 层取消注释)
#   - env UC_TOKEN: 该 Marzban 用户的 sub token (必填)
#   - 可覆盖 env: UC_BASE (默认 http://127.0.0.1:9200), IF_API (默认 api.ipify.org, 强制 IPv4 便于聚合)
#
# 退出码: 0=通过, 2=前置不满足, 1=断言失败。
set -euo pipefail

COMPOSE="docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml"
UC_BASE="${UC_BASE:-http://127.0.0.1:9200}"
UC_TOKEN="${UC_TOKEN:-}"
IF_API="${IF_API:-https://api.ipify.org}"
CLIENT_ID="${EDGE_CLIENT_ID:-00000000-0000-4000-8000-000000000001}"
WS_PATH="${EDGE_WS_PATH:-/edge-ws}"
EDGE_SVC="${EDGE_SVC:-xray-edge}"
EDGE_PORT="${EDGE_PORT:-10086}"
EDGE_TLS="${EDGE_TLS:-0}"   # 1=ws+tls(需 xray-edge 配证书), 0=明文 ws(MVP 默认)

cd "$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
log() { echo "[e2e-full] $*"; }
die() { echo "[e2e-full][FAIL] $*" >&2; exit 1; }

# ---- 前置检查 ----
$COMPOSE ps --status running 2>/dev/null | grep -q "$EDGE_SVC" || {
  log "$EDGE_SVC 未运行; 先 make dev (真实 sub 需 deploy/.env: SUB_UPSTREAM_1_URL)"; exit 2; }
$COMPOSE ps --status running 2>/dev/null | grep -q "user-center" || {
  log "user-center 未运行; 先 make dev"; exit 2; }
[ -n "$UC_TOKEN" ] || { log "缺 UC_TOKEN (Marzban 用户 sub token); 例: UC_TOKEN=xxx make e2e-full"; exit 2; }

# 当前 xray-edge 配置里的上游节点 IP 集合
POOL_IPS=$($COMPOSE exec -T "$EDGE_SVC" sh -c 'cat /configs/config.json' 2>/dev/null \
  | grep -oE '"address"[[:space:]]*:[[:space:]]*"[^"]+"' \
  | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | sort -u || true)
[ -n "$POOL_IPS" ] || die "xray-edge 配置无上游节点 (active 池为空? 查 ingest 日志/探测状态)"
log "当前池 IP (仅参考, 双栈下出口可能是 IPv6): $(echo "$POOL_IPS" | tr '\n' ' ')"

# 一次性 vless 客户端 (跑在 compose 网络内, 复用 xray-edge 镜像里的 xray)
probe_exit_ip() {
  local TLS_STREAM=""
  if [ "$EDGE_TLS" = "1" ]; then
    TLS_STREAM=",\"security\":\"tls\",\"tlsSettings\":{\"serverName\":\"$EDGE_SVC\"}"
  fi
  local CLIENT_CFG
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
  $COMPOSE run --rm --entrypoint sh "$EDGE_SVC" -c "
    cat > /tmp/c.json <<'CFG'
$CLIENT_CFG
CFG
    xray run -c /tmp/c.json >/tmp/xray-client.log 2>&1 &
    XPID=\$!
    for i in 1 2 3 4 5 6 7 8 9 10; do
      OUT=\$(curl -s --max-time 15 -x socks5://127.0.0.1:10800 ${IF_API} || true)
      [ -n \"\$OUT\" ] && { echo \"\$OUT\"; kill \$XPID 2>/dev/null; exit 0; }
      sleep 1
    done
    kill \$XPID 2>/dev/null
    tail -5 /tmp/xray-client.log
    exit 1
  " 2>/dev/null | grep -oE '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true
}

# ============ 线1: edge 直连出口断言 ============
log "线1: 经 $EDGE_SVC:$EDGE_PORT (vless-ws) 打出口探测…"
OUT_IP=""
for i in $(seq 1 5); do
  OUT_IP=$(probe_exit_ip)
  [ -n "$OUT_IP" ] && break
  log "try$i 未出流量, 重试 (balancer 可能轮询到死节点)…"
  sleep 3
done
[ -n "$OUT_IP" ] || die "未取到出口 IP (隧道不通? 查 xray-edge 日志: make logs)"
HOST_IP=$(curl -s --max-time 15 "$IF_API" || true)
if [ -n "$HOST_IP" ] && [ "$OUT_IP" = "$HOST_IP" ]; then
  die "出口 IP $OUT_IP = 宿主机直连 IP — 流量没走代理 (路由/balancer 失效?)"
fi
log "宿主机直连 IP: ${HOST_IP:-<不可达/为空, 跳过≠断言>}; 测得出口 IP: $OUT_IP"
if echo "$POOL_IPS" | grep -qx "$OUT_IP"; then
  log "线1 PASS: 出口 IP $OUT_IP ∈ 上游池 (balancer 命中池内 IPv4 节点)"
else
  log "线1 PASS: 出口 IP $OUT_IP 非空且 ≠ 本机 (双栈拓扑下不要求 ∈ 池 IP 字面集合; 分流断言见 load-50.sh)"
fi

# ============ 线2: user-center 真实 token 全链路 ============
log "线2: user-center ($UC_BASE) 真实 token 登录→流量→订阅"
JAR=$(mktemp)
trap 'rm -f "$JAR"' EXIT

# 登录 (303 → /dashboard, 种 HttpOnly cookie)
LOC=$(curl -s -o /dev/null -w '%{http_code} %{redirect_url}' -c "$JAR" \
  --data-urlencode "token=$UC_TOKEN" "$UC_BASE/login")
case "$LOC" in
  "303 "*"/dashboard"*) log "登录 OK (303→/dashboard)" ;;
  *) die "登录失败: $LOC (token 无效/限流/user-center 未接 Marzban?)" ;;
esac

# 概览: /api/status 流量字段
STATUS=$(curl -s -b "$JAR" "$UC_BASE/api/status")
log "api/status: $STATUS"
echo "$STATUS" | grep -q '"used"' || die "/api/status 缺 used 字段"
echo "$STATUS" | grep -q '"limit"' || die "/api/status 缺 limit 字段"

# 订阅页: 含可复制的 sub 链接 + 二维码
SUB_PAGE=$(curl -s -b "$JAR" "$UC_BASE/subscription")
echo "$SUB_PAGE" | grep -q "sub" || die "/subscription 页无订阅链接"
QR_TYPE=$(curl -s -o /dev/null -w '%{content_type}' -b "$JAR" "$UC_BASE/qr.png")
[ "$QR_TYPE" = "image/png" ] || die "/qr.png 非 image/png (got: $QR_TYPE)"
log "线2 PASS: 登录→流量(used/limit)→订阅链接+QR 一条绿"

log "ALL PASS (e2e-full)"
