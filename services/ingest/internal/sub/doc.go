// Package sub 负责上游订阅的拉取(work-3)。
// poller: 周期拉取 sub URL，429/5xx 指数退避。
// 解析归一化已抽到仓库级共享包 vpnmaster/subparse(ADR-0005), 客户端/ingest 同一权威。
package sub
