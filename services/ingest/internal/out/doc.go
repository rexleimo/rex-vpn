// Package out 负责 xray-edge 配置生成与热加载触发（work-5）。
// xraygen: inbound(ws :10086) + ACTIVE 节点 outbounds + balancer(leastPing), 原子写。
// reload:  edge 容器内 inotify watcher → `xray -s reload`(失败则进程重启, 不断卷内连接语义)。
package out
