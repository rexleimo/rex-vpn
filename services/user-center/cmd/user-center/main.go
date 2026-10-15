// Command user-center 是 vpn-master 的终端用户自助平台（work-10）。
// 单二进制 Go SSR: 登录/概览/订阅/购买 + JSON 轮询。数据经 Marzban API 代理, 不建用户后端。
package main

import (
	"log"
	"net/http"
	"os"

	"vpnmaster/user-center/internal/marzban"
	"vpnmaster/user-center/internal/web"
)

const version = "0.1.0-dev"

func main() {
	log.SetPrefix("user-center: ")
	log.SetOutput(os.Stdout)
	log.Printf("user-center %s starting", version)

	// 数据层: Marzban API 代理; 未配置时用禁用桩(页面优雅降级, 联调 work-6 后启用)。
	client := marzban.NewHTTP()
	if client == nil {
		log.Println("MARZBAN_API_BASE 未配置, 使用禁用桩(登录/概览不可用)")
		client = marzban.Disabled()
	}

	secure := os.Getenv("USERCENTER_SECURE_COOKIES") == "1"
	srv, err := web.New(client, marzban.PlansFromEnv(), secure)
	if err != nil {
		log.Fatalf("init web: %v", err)
	}

	addr := os.Getenv("USERCENTER_ADDR")
	if addr == "" {
		addr = ":9200"
	}
	log.Printf("serving on %s", addr)
	if err := http.ListenAndServe(addr, srv.Router()); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
