#!/usr/bin/env bash
# chaos.sh (work-8.2): 故障演练
#
#   对上游池里的一台节点, 在 **ingest 容器**内 iptables DROP 其探测流量:
#   (prober 在 ingest 进程里对每台节点做 TLS 拨号探测; DROP 打在 xray-edge 拦不到 prober,
#    不会产生 EVICT —— 那是转发面, 不是探测面)
#     1) 连续 EvictFails(3) 次探测失败 → events 出现 **目标节点指纹** 的 evict
#     2) DROP 期间经 edge 隧道的请求成功率不掉 (balancer 把流量打散到存活池, ≥80% 基线)
#     3) 恢复后 → 重试周期内连续 RecoverSucces(2) 次成功 → 同指纹的 recover
#
# 前置:
#   - make dev 已起 + deploy/.env 配真实 sub, active 池 ≥2 节点
#   - ingest 容器带 NET_ADMIN (dev 覆盖层已加 cap_add; 线上不需要)
#   - dev 覆盖层 PROBE_INTERVAL/PROBE_RETRY_PERIOD 为短间隔 (脚本自动读取校验)
#   - env 覆盖: CHAOS_IP (指定摘哪台, 默认取池内第一个), N_PROBES (默认 20)
#   - INGEST 登录口令: 从 deploy/.env 读 INGEST_ADMIN_PASSWORD (需已配置)
#
# 退出码: 0=通过, 2=前置不满足, 1=断言失败。
set -euo pipefail

COMPOSE="docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml"
INGEST_BASE="${INGEST_BASE:-http://127.0.0.1:9100}"
EDGE_SVC="${EDGE_SVC:-xray-edge}"
INGEST_SVC="${INGEST_SVC:-ingest}"
IF_API="${IF_API:-https://api.ipify.org}"
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

dur_to_s() {
  case "$1" in
    *h) echo $(( ${1%h} * 3600 )) ;;
    *m) echo $(( ${1%m} * 60 )) ;;
    *s) echo $(( ${1%s} + 0 )) ;;
    *)  echo "$1" ;;
  esac
}

# 读运行中 ingest 容器的真实 PROBE_INTERVAL / PROBE_RETRY_PERIOD (探测面在 ingest)
PROBES=$($COMPOSE exec -T "$INGEST_SVC" sh -c 'echo "$PROBE_INTERVAL"' 2>/dev/null || true)
[ -n "${PROBE_INTERVAL:-}" ] && PROBES="$PROBE_INTERVAL"
{ [ -z "$PROBES" ] || [ "$PROBES" = "null" ]; } && PROBES=$(grep -E '^PROBE_INTERVAL=' deploy/.env 2>/dev/null | cut -d= -f2)
[ -n "$PROBES" ] || PROBES="30s"
RETRY=$($COMPOSE exec -T "$INGEST_SVC" sh -c 'echo "$PROBE_RETRY_PERIOD"' 2>/dev/null || true)
{ [ -z "$RETRY" ] || [ "$RETRY" = "null" ]; } && RETRY="2m"

PROBE_S=$(dur_to_s "$PROBES"); RETRY_S=$(dur_to_s "$RETRY")
case "$PROBE_S" in ''|*[!0-9]*) die "PROBE_INTERVAL 无法解析为秒 (got: $PROBES)" ;; esac
case "$RETRY_S" in ''|*[!0-9]*) die "PROBE_RETRY_PERIOD 无法解析为秒 (got: $RETRY)" ;; esac
# 状态机: EvictFails=3, RecoverSucces=2 → evict ≈ 3×interval, recover ≈ retry周期 + 2×interval
WAIT_EVICT=$(( 3 * PROBE_S + 45 ))
WAIT_RECOVER=$(( RETRY_S + 2 * PROBE_S + 60 ))
[ "$PROBE_S" -le 60 ] || die "ingest PROBE_INTERVAL=${PROBE_S}s 太长; 用 dev 覆盖层 (15s) 重起: docker compose ... up -d --force-recreate ingest"
[ "$RETRY_S" -le 60 ] || die "ingest PROBE_RETRY_PERIOD=${RETRY_S}s 太长 (evicted 节点最久 ${RETRY_S}s 后才重探); dev 覆盖层应设 15s"

# 上游池: 从 ingest /api/nodes 取 active 且地址为 IPv4 字面的节点 (DB 权威源; config 会因探测转移随时重写, 不适合选目标)
api_nodes() {
  curl -s -b "$JAR" "$INGEST_BASE/api/nodes" || true
}

# cleanup_ipt 必须在 trap 注册前定义 (否则基线/登录阶段 set -e 退出时 EXIT trap 调未定义函数)
IPT_APPLIED=0
cleanup_ipt() {
  [ "$IPT_APPLIED" = "1" ] || return 0
  $COMPOSE exec -T "$INGEST_SVC" iptables -D OUTPUT -d "$TARGET" -j DROP 2>/dev/null || true
}

# ---- ingest 登录 (API 会话 cookie) ----
JAR=$(mktemp); EVENTS=$(mktemp)
trap 'rm -f "$JAR" "$EVENTS"; cleanup_ipt' EXIT

code=$(curl -s -o /dev/null -w '%{http_code}' -c "$JAR" \
  --data-urlencode "password=$INGEST_ADMIN_PASSWORD" "$INGEST_BASE/login")
[ "$code" = "303" ] || die "ingest 登录失败 (http $code; 查 INGEST_ADMIN_PASSWORD)"
log "ingest API 登录 OK"

# ---- 选目标: active 且 Addr 为 IPv4 字面 (才能用 iptables -d 拦 prober 拨号) ----
api_nodes() { curl -s -b "$JAR" "$INGEST_BASE/api/nodes" || true; }
POOL=$(api_nodes | sed 's/},{/}\n{/g' | grep '"Status":"active"' | grep -oE '"Addr":"[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+"' | grep -oE '[0-9.]+' | sort -u || true)
[ -n "$POOL" ] || die "active 池无 IPv4 字面地址节点 (域名地址无法 iptables -d; 设 CHAOS_IP 指定)"
N_POOL=$(echo "$POOL" | wc -l | tr -d ' ')
[ "$N_POOL" -ge 2 ] || die "上游 IPv4 池只有 $N_POOL 台 — 需要 ≥2 才能演练"
TARGET="${CHAOS_IP:-$(echo "$POOL" | head -1)}"
TARGET_ROW=$(api_nodes | sed 's/},{/}\n{/g' | grep "\"Addr\":\"$TARGET\"" | grep '"Status":"active"' | head -1)
[ -n "$TARGET_ROW" ] || die "目标 $TARGET 当前不在 active 池 (池在 churn; 重跑或 CHAOS_IP 另选)"
TARGET_FP=$(echo "$TARGET_ROW" | grep -oE '"Fingerprint":"[0-9a-f]{32}"' | grep -oE '[0-9a-f]{32}')
[ -n "$TARGET_FP" ] || die "无法解析 $TARGET 的指纹"
log "IPv4 池($N_POOL 台): $(echo "$POOL" | tr '\n' ' ')"
log "演练目标: DROP $TARGET (fp=$TARGET_FP); 探测周期 ${PROBES}, 重试周期 ${RETRY} (evict 等 ≤${WAIT_EVICT}s, recover 等 ≤${WAIT_RECOVER}s)"

api_events() { # 拉最新 200 条事件 JSON, 存 $EVENTS
  curl -s -b "$JAR" "$INGEST_BASE/api/events" > "$EVENTS" || true
}
# 目标指纹的 kind=$1 事件计数 (逐对象切行统计, 防"任意 evict"假阳性)
kind_fp_count() {
  sed 's/},{/}\n{/g' "$EVENTS" 2>/dev/null \
    | { grep "\"Kind\":\"$1\"" || true; } | { grep -c "\"NodeFP\":\"$2\"" || true; }
}

# ---- 成功探针: 一次性 xray 客户端 × N_PROBES 次, 打印成功数/总数 ----
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

# ---- 2) 在 ingest(prober 所在容器) DROP 目标节点 ----
log "在 $INGEST_SVC 内 DROP $TARGET (prober 拨号被拦; apk 装 iptables 首次约 5s)…"
$COMPOSE exec -T "$INGEST_SVC" sh -c '
  command -v iptables >/dev/null || apk add --no-cache iptables >/dev/null
  iptables -A OUTPUT -d '"$TARGET"' -j DROP' || die "iptables DROP 失败 (ingest 容器有 NET_ADMIN?)"
IPT_APPLIED=1
log "DROP 生效, 等 ≤${WAIT_EVICT}s 出 **$TARGET** 的 evict 事件…"

# ---- 3) 等目标指纹的 evict 事件 (基线计数 +1, 排除旧事件/他节点事件误判) ----
api_events
EVICT_BASE=$(kind_fp_count evict "$TARGET_FP")
deadline=$(( $(date +%s) + WAIT_EVICT ))
EVICTED=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  api_events
  NOW=$(kind_fp_count evict "$TARGET_FP")
  if [ "$NOW" -gt "$EVICT_BASE" ]; then EVICTED=1; break; fi
  sleep 5
done
[ -n "$EVICTED" ] || { cleanup_ipt; die "≤${WAIT_EVICT}s 内目标 $TARGET(fp=$TARGET_FP) 未出现 evict 事件 — DROP 未拦到 prober? 探测周期?"; }
log "evict 事件确认 (目标指纹): {\"Kind\":\"evict\",\"NodeFP\":\"$TARGET_FP\"}"

# ---- 4) DROP 期间成功率不掉 ----
log "DROP 期间探针 ($N_PROBES 次)…"
DROP_OK=$(probe_success)
[ -n "$DROP_OK" ] || { cleanup_ipt; die "DROP 期间探针未返回"; }
D_OK=${DROP_OK%%/*}; D_N=${DROP_OK#*/}
FLOOR=$(( D_N * 80 / 100 ))
log "DROP 期间: $DROP_OK 成功 (阈值 ≥$FLOOR = 80%)"
[ "$D_OK" -ge "$FLOOR" ] || { cleanup_ipt; die "DROP 期间成功率 $DROP_OK 掉到 80% 以下 — balancer 未把流量打散到存活池?"; }

# ---- 5) 恢复 → 等目标指纹的 recover 事件 ----
cleanup_ipt; IPT_APPLIED=0; log "iptables 已恢复, 等 ≤${WAIT_RECOVER}s 出 **$TARGET** 的 recover 事件…"
api_events
RECOVER_BASE=$(kind_fp_count recover "$TARGET_FP")
deadline=$(( $(date +%s) + WAIT_RECOVER ))
RECOVERED=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  api_events
  NOW=$(kind_fp_count recover "$TARGET_FP")
  if [ "$NOW" -gt "$RECOVER_BASE" ]; then RECOVERED=1; break; fi
  sleep 5
done
[ -n "$RECOVERED" ] || die "≤${WAIT_RECOVER}s 内目标 $TARGET 未出现 recover 事件 (需重试周期后连续 2 次探测成功)"
log "recover 事件确认 (目标指纹): {\"Kind\":\"recover\",\"NodeFP\":\"$TARGET_FP\"}"

# ---- 6) 恢复后成功率回到基线 ----
log "恢复后探针 ($N_PROBES 次)…"
AFTER_OK=$(probe_success)
[ -n "$AFTER_OK" ] || die "恢复后探针未返回"
A_OK=${AFTER_OK%%/*}; A_N=${AFTER_OK#*/}
[ "$A_OK" -ge "$FLOOR" ] || die "恢复后成功率 $AFTER_OK 仍低于 80% — 池未完全回正"
log "恢复后: $AFTER_OK 成功"

log "PASS: DROP(ingest/prober 面)→EVICT(目标指纹)→成功率不掉→恢复→RECOVER(目标指纹) 全链路绿"
