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
	"sync"
	"syscall"
	"time"

	"vpnmaster/ingest/internal/db"
	"vpnmaster/ingest/internal/probe"
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
		// 探测周期: PROBE_INTERVAL(默认 30s)
		probeInterval, _ := time.ParseDuration(envOr("PROBE_INTERVAL", "30s"))
		prober := probe.NewProber(store)
		prober.Timeout = 3 * time.Second

		// 订阅拉取 + 探测两个周期 goroutine, ctx 取消时一起退出
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := poller.Run(ctx); err != nil {
				log.Printf("poller: %v", err)
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			probeLoop(ctx, prober, probeInterval)
		}()
		wg.Wait()
	} else {
		<-ctx.Done()
	}
	log.Println("ingest stopped")
}

// probeLoop 周期探测直到 ctx 取消。
func probeLoop(ctx context.Context, p *probe.Prober, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := p.ProbeRound(ctx); err != nil {
				log.Printf("probe round: %v", err)
			}
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
