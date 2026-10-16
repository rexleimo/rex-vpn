// Command user-center 是 vpn-master 的终端用户自助平台（work-10）。
// 单二进制 Go SSR: 登录/概览/订阅/购买 + JSON 轮询。数据经 Marzban API 代理, 不建用户后端。
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"vpnmaster/user-center/internal/accounts"
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

	// 账号体系(WI-2): UC_PG_DSN 未配置时为 nil, 保持 token 粘贴登录。
	acct, err := accounts.NewFromEnv(context.Background())
	if err != nil {
		log.Fatalf("init accounts: %v", err)
	}
	if acct != nil {
		defer acct.Close(context.Background())
		if err := acct.EnsureLinksTable(context.Background()); err != nil {
			log.Fatalf("init links table: %v", err)
		}
		// 种子管理员(幂等): UC_ADMIN_EMAIL/UC_ADMIN_PASSWORD 配了才建。
		if email, pw := os.Getenv("UC_ADMIN_EMAIL"), os.Getenv("UC_ADMIN_PASSWORD"); email != "" && pw != "" {
			id, err := acct.EnsurePasswordUser(context.Background(), email, pw)
			if err != nil {
				log.Printf("seed admin %s: %v", email, err)
			} else {
				log.Printf("seed admin ready: %s (%s)", email, id)
			}
		}
		srv.AttachAccounts(acct)
		log.Println("accounts: authkit 已挂载(PostgreSQL)")
	} else {
		log.Println("accounts: 未启用(UC_PG_DSN 空), 走 token 粘贴模式")
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
