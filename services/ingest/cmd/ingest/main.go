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
	"vpnmaster/ingest/internal/out"
	"vpnmaster/ingest/internal/probe"
	"vpnmaster/ingest/internal/sub"
)

const version = "0.2.0-dev"

func main() {
	log.SetPrefix("ingest: ")
	log.SetOutput(os.Stdout)
	log.Printf("ingest %s starting", version)
	// work-3/4/5: poller + prober + xraygen 全链路已接入。

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

	// 探测 + xraygen 触发链: 始终运行(0 节点时空转无害)。订阅拉取仅在配置了来源时运行。
	probeInterval, _ := time.ParseDuration(envOr("PROBE_INTERVAL", "30s"))
	prober := probe.NewProber(store)
	prober.Timeout = 3 * time.Second
	syncer := out.NewSyncer(store, envOr("EDGE_CONFIG_PATH", "/configs/config.json"), out.Options{})

	var wg sync.WaitGroup
	if len(sources) > 0 {
		interval, _ := time.ParseDuration(envOr("INGEST_POLL_INTERVAL", "5m"))
		poller := sub.NewPoller(store, sources, interval)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := poller.Run(ctx); err != nil {
				log.Printf("poller: %v", err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		probeSyncLoop(ctx, prober, syncer, probeInterval)
	}()
	wg.Wait()
	log.Println("ingest stopped")
}

// probeSyncLoop 周期: 探测一轮 → 池变化则 xraygen 原子写(5.2 触发链)。
func probeSyncLoop(ctx context.Context, p *probe.Prober, s *out.Syncer, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := p.ProbeRound(ctx); err != nil {
			log.Printf("probe round: %v", err)
		}
		if wrote, err := s.Sync(ctx); err != nil {
			log.Printf("xraygen sync: %v", err)
		} else if wrote {
			log.Println("xraygen: config written, edge watcher will reload")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
