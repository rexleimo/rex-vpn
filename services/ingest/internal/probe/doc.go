// Package probe 负责节点健康探测与剔除状态机（work-4）。
// prober: TLS 握手探测(3s 超时, 并发 16), 延迟写回节点库。
// state:  ACTIVE/EVICTED/COOLDOWN 转移表, 全灭保最后池。
package probe
