// Command ingest 是 vpn-master 的订阅聚合服务：
// 拉取上游订阅 → 归一化节点库 → 健康探测/剔除 → 生成 xray-edge 配置。
//
// work-3: poller 已接入; probe(work-4)/xraygen(work-5)/api(work-7) 逐步接入。
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"vpnmaster/ingest/internal/db"
	"vpnmaster/ingest/internal/out"
	"vpnmaster/ingest/internal/probe"
	"vpnmaster/ingest/internal/sub"
	"vpnmaster/ingest/internal/web"
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

	// 探测 + xraygen 触发链: 始终运行(0 节点时空转无害)。
	probeInterval, _ := time.ParseDuration(envOr("PROBE_INTERVAL", "30s"))
	prober := probe.NewProber(store)
	prober.Timeout = 3 * time.Second
	// evicted 重试周期可配 (dev/chaos 需快回池; 线上保持默认 2m)
	if rp, err := time.ParseDuration(envOr("PROBE_RETRY_PERIOD", "")); err == nil && rp > 0 {
		prober.RetryPeriod = rp
	}
	syncer := out.NewSyncer(store, envOr("EDGE_CONFIG_PATH", "/configs/config.json"), out.Options{
		AccessLogPath: envOr("EDGE_ACCESS_LOG", ""), // 设路径后 edge 按连接记 access log; load-50 用它按 outbound tag 聚合分流
	})

	// 订阅拉取: 无来源时 poller 为 nil(管理页禁用拉取/手动按钮)
	var poller *sub.Poller
	if len(sources) > 0 {
		interval, _ := time.ParseDuration(envOr("INGEST_POLL_INTERVAL", "5m"))
		poller = sub.NewPoller(store, sources, interval)
	}

	// Web 层(work-7): SSR 管理页 + JSON API, 单二进制, 无前后分离
	handler, err := web.NewHandler(store, poller, prober, syncer, web.NewMarzbanClient(), web.NewSession(envOr("INGEST_ADMIN_PASSWORD", "")))
	if err != nil {
		log.Fatalf("web: %v", err)
	}

	var wg sync.WaitGroup
	if poller != nil {
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
	wg.Add(1)
	go func() {
		defer wg.Done()
		addr := envOr("INGEST_API_ADDR", ":9100")
		srv := &http.Server{Addr: addr, Handler: handler.Router()}
		log.Printf("web: serving admin+api on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("web: %v", err)
		}
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
