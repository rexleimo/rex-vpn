# 24h 长稳观察（work-8.4）

> 前置：`e2e-full.sh` / `chaos.sh` / `load-50.sh` 已绿，栈 `make dev` 连续运行 24h（线上为 Dokploy 常驻）。
> 观察项均为**记录口径**，不自动判绿；结论写进下方表格后由人工判定。

## 观察项

| # | 观察项 | 采集方法 | 健康口径 | 24h 结论 |
|---|---|---|---|---|
| S1 | 节点封禁率 | `curl -b <jar> $INGEST/api/events \| grep -c '"Kind":"evict"'` 对比探测失败明细；对同一 IP 封禁（连续 3 败）的节点占比 | 封禁节点 ≤ 池的 1/3；无整池 evict 风暴 | ☐ |
| S2 | 延迟漂移 | `GET /api/nodes` 每 30min 记录一次各节点 latency，24h 序列 | P50 漂移 < 50%；无节点长期 >1500ms 未摘除 | ☐ |
| S3 | 上游账单对账 | 出口流量（上游 sub 面板/账单增量） vs ingest 探测+业务流量估算；Marzban 用户 used 流量增量 | 偏差可解释（探测流量 ≈ 节点数×周期 级别） | ☐ |
| S4 | 配置写入频率 | `/api/events` 中 `config_write` 计数 + `/configs/config.json` mtime 序列 | 无高频抖动（池稳定时应 0 次/小时级） | ☐ |
| S5 | xray-edge 重启次数 | `docker inspect` RestartCount / watcher 日志 restart 计数 | 无异常自重启风暴；重启仅跟随 config 签名变化 | ☐ |
| S6 | 成功率长稳 | 每 30min 跑一次 20 次探针（`scripts/e2e-full.sh` 线1 或 `probe_success`） | 单次批次成功率 ≥90%；无持续下降趋势 | ☐ |
| S7 | user-center 观测（wc-7.2） | access log 统计口径：登录失败率（`/login` 303→`?err=` 占比）、Marzban API 5xx（user-center 日志 `marzban` 关键字） | 登录失败率 < 10%（排除限流/限流为预期）；5xx = 0 | ☐ |

## 采集脚本（手动每 30min 或 cron）

```bash
# 登录 ingest 拿 jar（复用 chaos.sh 的登录方式）
JAR=$(mktemp)
curl -s -o /dev/null -c "$JAR" --data-urlencode "password=$INGEST_ADMIN_PASSWORD" \
  http://127.0.0.1:9100/login
curl -s -b "$JAR" http://127.0.0.1:9100/api/nodes > /tmp/nodes-$(date +%H%M).json
curl -s -b "$JAR" http://127.0.0.1:9100/api/events > /tmp/events-$(date +%H%M).json
rm -f "$JAR"
```

## 24h 后填写

| 日期 | 跑法（dev/Dokploy） | S1 | S2 | S3 | S4 | S5 | S6 | S7 | 总体判定 |
|---|---|---|---|---|---|---|---|---|---|
| （待填） | | | | | | | | | ☐ 通过 / ☐ 不通过(原因) |

## 备注

- 封禁率 S1 需对照上游 sub 面板人工确认封禁是否真实（探测被 TLS 拒 vs 真封禁）。
- S3 对账需上游供应商账单周期；24h 内用增量估算即可，月末做全量对账。
