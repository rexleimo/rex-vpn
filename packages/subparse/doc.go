// Package subparse 是面板/上游/客户端三方共享的订阅归一化解析包。
//
// 输入两种主流订阅形态, 输出统一的 Node 列表:
//   - 纯链接列表 (vless / vmess / shadowsocks / trojan), 可整体 base64 包裹;
//   - Clash YAML 的 proxies 段 (协议白名单同上, 白名单外节点跳过)。
//
// 该包是节点"理解方式"的唯一权威: ingest 上游聚合与 Phase2 Windows 客户端
// 对同一份订阅永远解析出同一形态 (ADR-0005), 消费方零重复实现。
package subparse
