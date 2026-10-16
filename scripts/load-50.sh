#!/usr/bin/env bash
# load-50.sh (work-8.3): 负载分布
#
#   50 并发 × 20 次 (共 1000 次) 出口 IP 探测, 按 IP 聚合,
#   断言 ≥2 个上游节点均分到流量 (leastPing balancer 生效)。
#
# 前置:
#   - make dev 已起 + 真实 sub, active 池 ≥2
#   - 可覆盖 env: CONC (默认 50), ROUNDS (默认 20), IF_API (默认 api.ipify.org, 强制 IPv4 便于聚合)
#
# 输出: 分布表 (IP / 次数 / 占比 / 对应节点)。退出码: 0=通过, 2=前置不满足, 1=断言失败。
set -euo pipefail

COMPOSE="docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.dev.yml"
EDGE_SVC="${EDGE_SVC:-xray-edge}"
EDGE_PORT="${EDGE_PORT:-10086}"
EDGE_TLS="${EDGE_TLS:-0}"
CONC="${CONC:-50}"
ROUNDS="${ROUNDS:-20}"
IF_API="${IF_API:-https://api.ipify.org}"
CLIENT_ID="${EDGE_CLIENT_ID:-00000000-0000-4000-8000-000000000001}"
WS_PATH="${EDGE_WS_PATH:-/edge-ws}"
TOTAL=$(( CONC * ROUNDS ))

cd "$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
log() { echo "[load-50] $*"; }
die() { echo "[load-50][FAIL] $*" >&2; exit 1; }

$COMPOSE ps --status running 2>/dev/null | grep -q "$EDGE_SVC" || { log "xray-edge 未运行; 先 make dev"; exit 2; }

# 池 IP → 节点地址映射 (供分布表标注)
NODE_MAP=$($COMPOSE exec -T "$EDGE_SVC" sh -c 'cat /configs/config.json' 2>/dev/null \
  | grep -oE '"address"[[:space:]]*:[[:space:]]*"[^"]+"' \
  | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | sort -u || true)
[ -n "$NODE_MAP" ] || die "xray-edge 配置无上游节点 (active 池为空?)"
log "池: $(echo "$NODE_MAP" | tr '\n' ' ')"

# 起一个长驻一次性客户端 (socks5 :10800 → vless-ws → xray-edge)
TLS_STREAM=""
[ "$EDGE_TLS" = "1" ] && TLS_STREAM=",\"security\":\"tls\",\"tlsSettings\":{\"serverName\":\"$EDGE_SVC\"}"
CLIENT_CFG=$(cat <<EOF
{
  "log": {"loglevel": "warning"},
  "inbounds": [{"tag":"in","listen":"127.0.0.1","port":10086,"protocol":"socks","settings":{"udp":false}}],
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

CONTAINER="load50-client-$$"
log "起长驻 xray 客户端 ($CONTAINER)…"
$COMPOSE run -d --name "$CONTAINER" --entrypoint sh "$EDGE_SVC" -c "
  cat > /tmp/c.json <<'CFG'
$CLIENT_CFG
CFG
  xray run -c /tmp/c.json
" 2>/dev/null || die "起长驻客户端失败"
CLEAN_CLIENT() { docker stop "$CONTAINER" 2>/dev/null || true; docker rm -f "$CONTAINER" 2>/dev/null || true; }
trap CLEAN_CLIENT EXIT

# 等 socks 就绪 (客户端 socks 监听 10086, 容器内直连)
for i in $(seq 1 15); do
  sleep 1
  docker exec -e IF_API="$IF_API" "$CONTAINER" sh -c 'curl -s --max-time 5 -x socks5://127.0.0.1:10086 -o /dev/null -w "%{http_code}" "$IF_API"' 2>/dev/null | grep -q "200" && break
done

log "$CONC 并发 × $ROUNDS 轮 = $TOTAL 次出口探测…"
OUT=$(mktemp)
trap 'CLEAN_CLIENT; rm -f "$OUT"' EXIT
# 在客户端容器内并发 (单次 exec; 避免 1000 次宿主机 docker exec 冷启动把成功率压低)
docker exec -i \
  -e TOTAL="$TOTAL" -e CONC="$CONC" -e IF_API="$IF_API" \
  "$CONTAINER" sh > "$OUT" 2>/dev/null <<'INSH' || true
seq 1 "$TOTAL" | xargs -P "$CONC" -I{} sh -c 'curl -s --max-time 20 -x socks5://127.0.0.1:10086 "$IF_API"; echo'
INSH

OK=$(grep -cE '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' "$OUT" || true)
[ -n "$OK" ] || OK=0
OK_PCT=$(( OK * 100 / TOTAL ))
log "成功 $OK/$TOTAL (${OK_PCT}%)"
# 两层判定:
#  ① 系统硬门: 隧道确实出流量(否则 xraygen/balancer/路由 挂了) — 下限 MIN_OK_PCT(默认10%)
#  ② 池质量软观测: 出口成功率取决于免费节点真实可用性(握手可达≠能出网), 不作为系统 FAIL;
#     ≥80% 视为健康(线上限期目标), <80% 打 WARN 供 8.4 soak 跟踪节点质量
[ "$OK" -ge $(( TOTAL * ${MIN_OK_PCT:-10} / 100 )) ] || die "成功率 ${OK_PCT}% < ${MIN_OK_PCT:-10}% — 隧道基本不通, 先跑 scripts/e2e-full.sh 定位"
[ "$OK_PCT" -ge 80 ] || log "WARN: 池质量 ${OK_PCT}% < 80% (免费节点握手可达但不能真出网的比例偏高; 属数据源质量, 对应 docs/soak.md S6 成功率长稳)"

log "---- 出口 IP 分布 (top 10) ----"
DISTRIBUTION=$(grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' "$OUT" | sort | uniq -c | sort -rn)
echo "$DISTRIBUTION" | head -10 | awk -v ok="$OK" '{printf "  %s  %d (%.1f%%)\n", $2, $1, $1*100/ok}'
N_DISTINCT=$(echo "$DISTRIBUTION" | grep -c . || true)
TOP_CNT=$(echo "$DISTRIBUTION" | head -1 | awk '{print $1}')
TOP_PCT=$(( TOP_CNT * 100 / OK ))
log "distinct 出口 IP: $N_DISTINCT / 池节点(IPv4): $(echo "$NODE_MAP" | wc -l | tr -d ' ')"

# 断言(均为系统级, 不依赖节点质量):
#  ① 分流广度: 单台上游的出口 IP 集合有界, distinct 出口 ≥ MIN_DISTINCT(默认5) 则证明流量跨了多台服务器
#  ② 无单点主导: 最大单一出口占比 ≤ 70% (leastPing 未把流量死塑一台)
[ "$N_DISTINCT" -ge "${MIN_DISTINCT:-5}" ] || die "distinct 出口仅 $N_DISTINCT 个 — leastPing 未分流 (池内其余节点不可达? balancer 配置?)"
log "最大单一出口占比: ${TOP_PCT}%"
[ "$TOP_PCT" -le 70 ] || die "单一出口占比 ${TOP_PCT}% > 70% — 流量基本打在一台上, leastPing 未生效"
log "PASS: 出口成功率 ${OK_PCT}% | $N_DISTINCT 个出口 IP 分流(最大单占 ${TOP_PCT}%), leastPing 生效"
