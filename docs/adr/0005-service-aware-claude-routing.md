# ADR-0005: 服务感知路由 — Claude 流量走 claude_ok 专用均衡器

## 背景

产品验收标准 = Claude 可用。实测(2026-10-17)两类上游对 Claude 的能力截然不同:ccwu001 池(CF Workers edgetunnel + Oracle VPS 出口)31 节点 claude.ai 网页版全部被 Cloudflare managed challenge 无限循环(0/31);WestData trojan 节点浏览器实证可打开 claude.ai 登录页。存活探测(TCP+TLS 握手)测不出这种**区域/IP 信誉封锁**——节点"活着"但服务不可用。单池均衡会把 Claude 流量随机打进死池。

## 决策

1. **能力探测**(`probe.TrojanClaudeOK`):经 trojan 隧道(uTLS chrome 指纹)CONNECT `api.anthropic.com:443`,POST `/v1/messages` 不带 key;响应含 `authentication_error` = 区域放行 → 节点标 `claude_ok`。节奏门 `CapInterval=10m`(能力探测消耗上游按量计费流量,存活探测仍每轮)。
2. **双均衡器**(xraygen):`claude_ok` 节点额外组成 `claude_lb`;路由规则置顶 `claude.ai / claude.com / anthropic.com / claudeusercontent.com` → `claude_lb`,catch-all → `upstream_lb`(全池)。无 claude_ok 节点时不生成 claude_lb,Claude 域名回落主池(降级为"尽力而为",不留路由黑洞)。
3. **配置重写触发**:cert_pin 与 claude_ok 纳入 poolSignature,能力/证书变化自动触发 edge 热重启。

## 被拒选项

- **单池 + 节点 tag 选择**:xray 26 `routing.balancers.selector` 只支持 outbound tag 前缀匹配,不支持 metadata tag;要按能力分池只能生成独立 balancer + 独立 outbound 集合。
- **网页级探测入 claude_lb 判据**:curl 层面 CF 质询页对"能过/不能过"的节点特征完全一致(都是 403 `cf-mitigated: challenge`),区分需要真浏览器解 JS——成本与稳定性不适合放进探测循环,留作未来工作项。

## 后果

- 正面:Claude 流量确定性走已验证节点;一般流量不受影响(YouTube/X 实测仍 200);新增任何上游,只要节点过能力探测即自动进 claude_lb。
- 负面/风险:Claude 能力当前 100% 依赖 WestData 单点(其订阅 URL 被机场风控,file:// 源是手动兜底);能力探测约 366 次/小时消耗其计费流量(~1-2GB/月量级)。
- 红线:claude_ok 判据是 API 级的,见 ADR-0006。
