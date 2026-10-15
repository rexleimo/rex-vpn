#!/usr/bin/env bash
# chaos.sh (work-8.2): 故障演练
#
#   对上游池里的一台节点在 xray-edge 容器内 iptables DROP 其出口流量:
#     1) ≤2 个探测周期内 ingest events 出现 evict (节点被摘)
#     2) DROP 期间请求成功率不掉 (balancer 把流量打散到存活池, ≥80% 基线)
#     3) 恢复后 ≤ 4 个探测周期内 events 出现 recover (连 2 次探测成功回池)
#
# 前置:
#   - make dev 已起 + deploy/.env 配真实 sub, active 池 ≥2 节点
#   - xray-edge 容器带 NET_ADMIN (dev 覆盖层已加 cap_add)
#   - env 覆盖: CHAOS_IP (指定摘哪台, 默认取池内第一个), PROBE_INTERVAL (默认读 deploy/.env, 30s)
#   - INGEST 登录口令: 从 deploy/.env 读 INGEST_ADMIN_PASSWORD (需已配置)
#
# 退出码: 0=通过, 2=前置不满足, 1=断言失败。
set -euo pipefail

COMPOSE="docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml"
INGEST_BASE="${INGEST_BASE:-http://127.0.0.1:9100}"
EDGE_SVC="${EDGE_SVC:-xray-edge}"
IF_API="${IF_API:-https://ifconfig.me/ip}"
CLIENT_ID="${EDGE_CLIENT_ID:-00000000-0000-4000-8000-000000000001}"
WS_PATH="${EDGE_WS_PATH:-/edge-ws}"
EDGE_PORT="${EDGE_PORT:-10086}"
EDGE_TLS="${EDGE_TLS:-0}"
N_PROBES="${N_PROBES:-20}"

cd "$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
log() { echo "[chaos] $*"; }
die() { echo "[chaos][FAIL] $*" >&2; exit 1; }

# ---- 前置 ----
$COMPOSE ps --status running 2>/dev/null | grep -q "$EDGE_SVC" || { log "xray-edge 未运行; 先 make dev"; exit 2; }
[ -n "${INGEST_ADMIN_PASSWORD:-}" ] || INGEST_ADMIN_PASSWORD=$(grep -E '^INGEST_ADMIN_PASSWORD=' deploy/.env 2>/dev/null | cut -d= -f2 || true)
[ -n "$INGEST_ADMIN_PASSWORD" ] || die "INGEST_ADMIN_PASSWORD 未配置 (deploy/.env)"

PROBES=$(grep -E '^PROBE_INTERVAL=' deploy/.env 2>/dev/null | cut -d= -f2)
[ -n "${PROBE_INTERVAL:-}" ] && PROBES="$PROBE_INTERVAL"
[ -n "$PROBES" ] || PROBES="30s"
PROBE_S=${PROBES%s}
case "$PROBE_S" in ''|*[!0-9]*) die "PROBE_INTERVAL 需整数秒 (got: $PROBES)" ;; esac
WAIT_EVICT=$(( 2 * PROBE_S + 30 ))
WAIT_RECOVER=$(( 4 * PROBE_S + 30 ))

# 上游池 IP (需 ≥2, 否则摘一台就全灭, 测不了成功率不掉)
POOL_IPS=$($COMPOSE exec -T "$EDGE_SVC" sh -c 'cat /configs/config.json' 2>/dev/null \
  | grep -oE '"address"[[:space:]]*:[[:space:]]*"[^"]+"' \
  | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | sort -u || true)
[ -n "$POOL_IPS" ] || die "xray-edge 配置无上游节点"
N_POOL=$(echo "$POOL_IPS" | wc -l | tr -d ' ')
[ "$N_POOL" -ge 2 ] || die "上游池只有 $N_POOL 台 — 需要 ≥2 才能演练 (全灭走的是保最后池护栏, 非本场景)"
TARGET="${CHAOS_IP:-$(echo "$POOL_IPS" | head -1)}"
echo "$POOL_IPS" | grep -qx "$TARGET" || die "CHAOS_IP=$TARGET 不在当前池内"
log "池($N_POOL 台): $(echo "$POOL_IPS" | tr '\n' ' ')"
log "演练目标: DROP $TARGET; 探测周期 $PROBES (evict 等 ≤${WAIT_EVICT}s, recover 等 ≤${WAIT_RECOVER}s)"

# ---- ingest 登录 (API 会话 cookie) ----
JAR=$(mktemp); EVENTS=$(mktemp)
trap 'rm -f "$JAR" "$EVENTS"; cleanup_ipt' EXIT

code=$(curl -s -o /dev/null -w '%{http_code}' -c "$JAR" \
  --data-urlencode "password=$INGEST_ADMIN_PASSWORD" "$INGEST_BASE/login")
[ "$code" = "303" ] || die "ingest 登录失败 (http $code; 查 INGEST_ADMIN_PASSWORD)"
log "ingest API 登录 OK"

api_events() { # 拉最新 200 条事件 JSON (含时间戳), 存 $EVENTS
  curl -s -b "$JAR" "$INGEST_BASE/api/events" > "$EVENTS" || true
}
kind_count() { # 事件列表里 kind=$1 的计数 (用于区分旧事件/新事件)
  grep -oE "\"Kind\":\"$1\"" "$EVENTS" 2>/dev/null | wc -l | tr -d ' '
}

# ---- 成功探针: 一次性 xray 客户端 × N_PROBES 次, 打印成功次数 ----
probe_success() {
  local TLS_STREAM=""
  [ "$EDGE_TLS" = "1" ] && TLS_STREAM=",\"security\":\"tls\",\"tlsSettings\":{\"serverName\":\"$EDGE_SVC\"}"
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
  "routing": {"domainStrategy":"AsIs","rules":[{"type":"field","outboundTag":"up"}]}
}
EOF
)
  $COMPOSE run --rm --entrypoint sh "$EDGE_SVC" -c "
    cat > /tmp/c.json <<'CFG'
$CLIENT_CFG
CFG
    xray run -c /tmp/c.json >/tmp/xray-client.log 2>&1 &
    XPID=\$!
    sleep 2
    OK=0
    for i in \$(seq 1 $N_PROBES); do
      OUT=\$(curl -s --max-time 15 -x socks5://127.0.0.1:10800 ${IF_API} || true)
      [ -n \"\$OUT\" ] && OK=\$((OK+1))
    done
    kill \$XPID 2>/dev/null
    echo \"\$OK/$N_PROBES\"
  " 2>/dev/null | grep -oE '^[0-9]+/[0-9]+$' | head -1
}

# ---- 1) 基线成功率 ----
log "基线探针 ($N_PROBES 次)…"
BASE_OK=$(probe_success)
[ -n "$BASE_OK" ] || die "基线探针未返回 (隧道不通? 先跑 scripts/e2e-full.sh 定位)"
log "基线: $BASE_OK 成功"

# ---- 2) iptables DROP 目标节点 ----
cleanup_ipt() {
  $COMPOSE exec -T "$EDGE_SVC" iptables -D OUTPUT -d "$TARGET" -j DROP 2>/dev/null || true
}
log "在 $EDGE_SVC 内 DROP $TARGET (apk 装 iptables, 首次约 5s)…"
$COMPOSE exec -T "$EDGE_SVC" sh -c '
  command -v iptables >/dev/null || apk add --no-cache iptables >/dev/null
  iptables -A OUTPUT -d '"$TARGET"' -j DROP' || die "iptables DROP 失败 (容器有 NET_ADMIN?)"
log "DROP 生效, 等 ≤${WAIT_EVICT}s 出新 evict 事件…"

# ---- 3) 等 evict 事件 (只看 DROP 后新增, 防旧事件误判) ----
api_events
EVICT_BEFORE=$(kind_count evict)
sleep 5
deadline=$(( $(date +%s) + WAIT_EVICT ))
EVICTED=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  api_events
  EVICT_NOW=$(kind_count evict)
  if [ "$EVICT_NOW" -gt "$EVICT_BEFORE" ]; then
    EVICTED=$(grep '"Kind":"evict"' "$EVENTS" | tail -1)
    break
  fi
  sleep 5
done
[ -n "$EVICTED" ] || { cleanup_ipt; die "≤${WAIT_EVICT}s 内未出现新 evict 事件 (探测周期? 目标被摘了?)"; }
log "evict 事件: $(echo "$EVICTED" | head -c 300)"

# ---- 4) DROP 期间成功率不掉 ----
log "DROP 期间探针 ($N_PROBES 次)…"
DROP_OK=$(probe_success)
[ -n "$DROP_OK" ] || { cleanup_ipt; die "DROP 期间探针未返回"; }
D_OK=${DROP_OK%%/*}; D_N=${DROP_OK#*/}
FLOOR=$(( D_N * 80 / 100 ))
log "DROP 期间: $DROP_OK 成功 (阈值 ≥$FLOOR = 80%)"
[ "$D_OK" -ge "$FLOOR" ] || { cleanup_ipt; die "DROP 期间成功率 $DROP_OK 掉到 80% 以下 — balancer 未把流量打散到存活池?"; }

# ---- 5) 恢复 → 等 recover 事件 (只看恢复后新增) ----
cleanup_ipt; log "iptables 已恢复, 等 ≤${WAIT_RECOVER}s 出新 recover 事件…"
api_events
RECOVER_BEFORE=$(kind_count recover)
sleep 5
deadline=$(( $(date +%s) + WAIT_RECOVER ))
RECOVERED=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  api_events
  RECOVER_NOW=$(kind_count recover)
  if [ "$RECOVER_NOW" -gt "$RECOVER_BEFORE" ]; then
    RECOVERED=$(grep '"Kind":"recover"' "$EVENTS" | tail -1)
    break
  fi
  sleep 5
done
[ -n "$RECOVERED" ] || die "≤${WAIT_RECOVER}s 内未出现新 recover 事件 (需连续 2 次探测成功)"
log "recover 事件: $(echo "$RECOVERED" | head -c 300)"

# ---- 6) 恢复后成功率回到基线 ----
log "恢复后探针 ($N_PROBES 次)…"
AFTER_OK=$(probe_success)
[ -n "$AFTER_OK" ] || die "恢复后探针未返回"
A_OK=${AFTER_OK%%/*}; A_N=${AFTER_OK#*/}
[ "$A_OK" -ge "$FLOOR" ] || die "恢复后成功率 $AFTER_OK 仍低于 80% — 池未完全回正"
log "恢复后: $AFTER_OK 成功"

log "PASS: DROP→EVICT→(成功率不掉)→恢复→RECOVER 全链路绿"
