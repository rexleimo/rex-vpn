// Command ingest 是 vpn-master 的订阅聚合服务：
// 拉取上游订阅 → 归一化节点库 → 健康探测/剔除 → 生成 xray-edge 配置。
//
// work-3: poller 已接入; probe(work-4)/xraygen(work-5)/api(work-7) 逐步接入。
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vpnmaster/ingest/internal/db"
	"vpnmaster/ingest/internal/sub"
)

const version = "0.2.0-dev"

func main() {
	log.SetPrefix("ingest: ")
	log.SetOutput(os.Stdout)
	log.Printf("ingest %s starting", version)

	// 存储: 默认 sqlite 文件; 多实例时切 postgres(DSN 由 env 注入)
	driver := envOr("INGEST_DB_DRIVER", "sqlite")
	dsn := envOr("INGEST_DB_DSN", "ingest.db")
	store, err := db.NewStore(driver, dsn)
	if err != nil {
		log.Fatalf("open store (%s): %v", driver, err)
	}
	defer store.Close()

	// 上游来源: SUB_UPSTREAM_1_URL / SUB_UPSTREAM_2_URL (MVP 单上游)
	var sources []sub.Source
	if u := os.Getenv("SUB_UPSTREAM_1_URL"); u != "" {
		sources = append(sources, sub.Source{Name: "sub_upstream_1", URL: u})
	}
	if u := os.Getenv("SUB_UPSTREAM_2_URL"); u != "" {
		sources = append(sources, sub.Source{Name: "sub_upstream_2", URL: u})
	}
	if len(sources) == 0 {
		log.Println("no SUB_UPSTREAM_*_URL set; poller idle")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if len(sources) > 0 {
		interval, _ := time.ParseDuration(envOr("INGEST_POLL_INTERVAL", "5m"))
		poller := sub.NewPoller(store, sources, interval)
		if err := poller.Run(ctx); err != nil {
			log.Fatalf("poller: %v", err)
		}
	} else {
		<-ctx.Done()
	}
	log.Println("ingest stopped")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
