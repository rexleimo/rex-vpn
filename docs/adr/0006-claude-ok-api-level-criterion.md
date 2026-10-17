# ADR-0006: claude_ok 判据 = API 级区域放行,且仅对 trojan 节点生效(勿扩展到 vless)

## 背景

`claude_ok` 的自动判据(`ClassifyClaudeResponse`)基于 `api.anthropic.com` 的响应语义:`authentication_error` = 请求已到达 Anthropic 业务层(区域放行);`unavailable_in_region` / CF 质询页 = 封锁。但实测发现该判据的分辨率上限:**ccwu001 的 CF 出口节点 API 级同样返回 `authentication_error`(能过),却过不了 claude.ai 网页版的 CF managed challenge(浏览器无限"请稍候…")**;而 curl 层面两类节点对 claude.ai 的响应特征完全一致(都 403 `cf-mitigated: challenge`)。即:API 级判据无法预测网页版可用性。

## 决策

1. **判据语义**:`claude_ok = true` 表示"该节点可服务 Claude **API** 流量",不承诺 claude.ai 网页版。
2. **适用范围**:能力探测只对 trojan 节点执行(MVP 仅实现了 trojan 隧道的纯 Go 客户端)。当前 claude_lb 恰好全是浏览器实证过网页版的 WestData 节点——这是**探测范围与实证范围重合的偶然正确**,不是判据本身保证的。
3. **红线**:在建立网页级判据(无头浏览器解质询,或按出口 IP 的 AS/信誉做启发式,如 CF AS13335 直接排除)之前,**禁止把该 API 级探测扩展到 vless 节点**——否则 ccwu001 的 CF 出口节点会被批量标成 claude_ok 混入 claude_lb,claude.ai 网页版立即回退到质询循环,且该回退是静默的(探测全绿)。

## 后果

- 正面:Claude API 场景(工具接入、AIOS 调用)对全部 claude_ok 节点成立;判据便宜、稳定、可自动化。
- 负面:网页版保障目前依赖"人工浏览器实证 + 探测范围限制"这一对隐性约束,已由本 ADR 显性化;新增上游若含非 trojan 的"声称可开网页"节点,需先人工实证再决定是否入 claude_lb。
- 未来工作项:网页级判据自动化(候选:headless 探测容器、出口 IP ASN 启发式、claude.ai Turnstile siteverify 侧信道)。
