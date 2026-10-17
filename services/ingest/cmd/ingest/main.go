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

	// 上游来源: env(SUB_UPSTREAM_*_URL, 权威并回写 DB) ∪ DB 持久行(admin 页添加的源)。
	// 旧语义: UI 添加的源只活在进程内存, 重启即丢且 DB 仅存掩码无法恢复(已确认为 bug);
	// 现在 pollOne 把完整 URL 落库, LoadSources 启动时重建清单, 无需天天重启/重填。
	var envSrc []sub.Source
	if u := os.Getenv("SUB_UPSTREAM_1_URL"); u != "" {
		envSrc = append(envSrc, sub.Source{Name: "sub_upstream_1", URL: u})
	}
	if u := os.Getenv("SUB_UPSTREAM_2_URL"); u != "" {
		envSrc = append(envSrc, sub.Source{Name: "sub_upstream_2", URL: u})
	}
	sources, err := sub.LoadSources(context.Background(), store, envSrc)
	if err != nil {
		log.Fatalf("load sources: %v", err)
	}
	log.Printf("poller 拉取清单: %d 个源(env %d + DB %d)", len(sources), len(envSrc), len(sources)-len(envSrc))

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

	// 订阅拉取: poller 始终构造并运行——admin 页运行时 AddSource 需要循环与手动拉取可用;
	// 0 来源时仅空转无害。旧版无 env 源时 poller=nil → UI 添加上游只入库不拉取(静默死记录)。
	interval, _ := time.ParseDuration(envOr("INGEST_POLL_INTERVAL", "5m"))
	poller := sub.NewPoller(store, sources, interval)

	// Web 层(work-7): SSR 管理页 + JSON API, 单二进制, 无前后分离
	handler, err := web.NewHandler(store, poller, prober, syncer, web.NewMarzbanClient(), web.NewSession(envOr("INGEST_ADMIN_PASSWORD", "")))
	if err != nil {
		log.Fatalf("web: %v", err)
	}

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
		probeSyncLoop(ctx, prober, syncer, probeInterval)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		addr := envOr("INGEST_API_ADDR", ":9100")
		srv := &http.Server{Addr: addr, Handler: handler.Router()}
		go func() {
			log.Printf("web: serving admin+api on %s", addr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("web: %v", err)
			}
		}()
		// SIGTERM 后必须主动 Shutdown: 旧版 ListenAndServe 不监视 ctx, wg.Wait 永阻塞,
		// 进程只能被 SIGKILL(重启/Dokploy 停止都要靠超时强杀)。
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			log.Printf("web shutdown: %v", err)
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
