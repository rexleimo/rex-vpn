// Package sub 负责上游订阅的拉取与解析（work-3）。
// poller: 周期拉取 sub URL，429/5xx 指数退避。
// parser: base64(纯链接: vless/vmess/ss/trojan) 与 base64(Clash YAML) 双格式 → 归一化 domain.Node。
package sub
