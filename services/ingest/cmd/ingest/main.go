// Command ingest 是 vpn-master 的订阅聚合服务：
// 拉取上游订阅 → 归一化节点库 → 健康探测/剔除 → 生成 xray-edge 配置。
package main

import (
	"fmt"
	"log"
	"os"
)

const version = "0.1.0-dev"

func main() {
	// work-2 骨架: 只打印版本, 各子系统在 work-3/4/5/7 逐步接入
	fmt.Printf("ingest %s starting (skeleton)\n", version)
	log.SetPrefix("ingest: ")
	log.SetOutput(os.Stdout)
	log.Println("sub poller: TODO (work-3)")
	log.Println("prober:     TODO (work-4)")
	log.Println("xraygen:    TODO (work-5)")
	log.Println("api:        TODO (work-7)")
}
