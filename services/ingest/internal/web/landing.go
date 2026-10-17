package web

// landing.go: 产品落地页(公开, 无会话门槛)。Lantern 设计语言(ADR-0007),
// 文案为本产品自有(不复刻 lantern.io 营销文案/素材), 插画为自绘同语言 SVG。
// 独立文件: 营销页与 admin 壳职责不同, 不混入 pages.go。

import (
	"os"

	"github.com/gin-gonic/gin"
	"vpnmaster/webui"
)

// portalURL 是落地页 CTA 指向的用户门户(user-center 服务); 生产经 env 覆盖。
func portalURL() string {
	if u := os.Getenv("PORTAL_URL"); u != "" {
		return u
	}
	return "http://127.0.0.1:9200"
}

const landingTpl = `
{{define "landing"}}
<!doctype html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<meta name="description" content="订阅式代理服务: 智能路由、真实探测、Claude 专用通道。">
<link rel="stylesheet" href="/assets/bootstrap.min.css">
<style>{{.CSS}}</style>
<style>
.ln-hero{background:var(--wm-surface);padding:96px 0 110px;overflow:hidden}
.ln-hero h1{font-size:clamp(2.2rem,5vw,3.5rem);line-height:1.15;margin:.4em 0 .5em}
.ln-hero .sub{font-size:1.15rem;color:var(--wm-ink-2);max-width:34em}
.ln-hero .btn-lg{padding:.9rem 2.6rem;font-size:1.05rem}
.ln-note{display:inline-flex;align-items:center;gap:.5em;margin-top:1.2rem;
  background:var(--wm-bg);border:1px solid var(--wm-hairline);border-radius:999px;
  padding:.45em 1.1em;font-size:.85rem;color:var(--wm-ink-2);
  max-width:100%;white-space:normal}
/* flexbox min-width:auto 链: 长内容会把整行撑宽过视口(390px 实测溢出根因) */
.ln-hero .row > [class*="col"]{min-width:0}
.ln-section{padding:96px 0}
.ln-alt{background:var(--wm-bg)}
.ln-section h2{font-size:clamp(1.6rem,3vw,2.1rem);margin:.35em 0 1em}
.ln-card{height:100%;padding:1.9rem 1.7rem;display:flex;flex-direction:column;gap:.7rem}
.ln-card h3{font-size:1.12rem;margin:0}
.ln-card p{color:var(--wm-ink-2);font-size:.95rem;line-height:1.75;margin:0}
.ln-card .ico{width:26px;height:26px;color:var(--wm-accent)}
.ln-cta{padding:88px 0;text-align:center;background:var(--wm-surface)}
.ln-cta .btn{padding:.95rem 3rem;font-size:1.08rem}
footer.ln-footer{border-top:1px solid var(--wm-hairline);padding:34px 0;
  color:var(--wm-ink-2);font-size:.85rem;background:var(--wm-surface)}
.ln-globe{max-width:min(430px, 90%);width:100%;height:auto}
@media (max-width: 991px){.ln-hero{padding:64px 0 72px}.ln-section{padding:64px 0}.ln-globe{margin-top:2.5rem}}
</style>
</head>
<body>
<div class="ln-banner py-2 text-center small">★ 新用户可先试用: 全部节点经真实能力探测, 不通不下发。</div>
<nav class="navbar navbar-expand sticky-top">
  <div class="container px-4" style="max-width:1140px">
    <a class="navbar-brand" href="/">vpn-master</a>
    <div class="navbar-nav ms-auto align-items-center gap-2 flex-row">
      <a class="nav-link d-none d-md-inline" href="#features">功能</a>
      <a class="nav-link d-none d-md-inline" href="#trust">为什么是我们</a>
      <a class="btn btn-primary btn-sm px-3" href="{{.PortalURL}}">获取订阅</a>
    </div>
  </div>
</nav>

<header class="ln-hero">
  <div class="container px-4" style="max-width:1140px">
    <div class="row align-items-center g-5">
      <div class="col-lg-6">
        <div class="kicker">订阅式代理 · 全节点真实探测</div>
        <h1>突破网络限制的<br>智能代理</h1>
        <p class="sub">快速、安全、稳定地访问你需要的服务——Claude 专用通道、主流流媒体解锁, 每一条路由都经过实测。</p>
        <a class="btn btn-primary btn-lg" href="{{.PortalURL}}">开始使用</a>
        <div class="ln-note">◆ 使用我们的订阅, 就是在为「可用才下发」的节点质量观投票。</div>
      </div>
      <div class="col-lg-6 text-center">
        <svg class="ln-globe" viewBox="0 0 400 400" role="img" aria-label="全球节点示意">
          <ellipse cx="200" cy="368" rx="120" ry="16" fill="#012d2d"/>
          <circle cx="200" cy="190" r="150" fill="#00bcd4"/>
          <path d="M120 90q40-30 90-18t60 48q-30 26-78 20t-72-50z" fill="#3ab853"/>
          <path d="M96 210q30-16 62 0t20 52q-40 18-70-6t-12-46z" fill="#3ab853"/>
          <path d="M236 236q36-12 62 10t6 48q-44 12-68-12t0-46z" fill="#3ab853"/>
          <g fill="none" stroke="#fff" stroke-width="3" stroke-dasharray="7 8">
            <path d="M110 130q90-60 180 10"/><path d="M90 240q110 60 220-30"/>
          </g>
          <g fill="#ffec01">
            <path d="M320 60l8 20 20 8-20 8-8 20-8-20-20-8 20-8z"/>
            <path d="M60 150l6 15 15 6-15 6-6 15-6-15-15-6 15-6z"/>
            <path d="M340 250l6 15 15 6-15 6-6 15-6-15-15-6 15-6z"/>
            <path d="M150 330l5 12 12 5-12 5-5 12-5-12-12-5 12-5z"/>
          </g>
        </svg>
      </div>
    </div>
  </div>
</header>

<section id="features" class="ln-section ln-alt">
  <div class="container px-4" style="max-width:1140px">
    <div class="kicker">为畅通访问而生</div>
    <h2>更智能的路由, 速度还特别快</h2>
    <p style="max-width:46em;color:var(--wm-ink-2)">默认只把受限服务送进代理通道, 其余流量直连保持飞快; 需要全量保护时一键切换全局模式。</p>
    <div class="row g-4 mt-1 justify-content-center">
      <div class="col-md-4"><div class="card ln-card">
        <svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 6h16M4 12h10M4 18h7"/><circle cx="19" cy="12" r="2.5"/><circle cx="14" cy="18" r="2.5"/></svg>
        <h3>服务感知路由</h3>
        <p>Claude 与 Anthropic 域走专用均衡器, 只选能力探测通过的节点; 其余流量走全池 leastPing 均衡, 互不挤占。</p>
      </div></div>
      <div class="col-md-4"><div class="card ln-card">
        <svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 3l7 4v5c0 5-3.5 8-7 9-3.5-1-7-4-7-9V7z"/><path d="M9 12l2 2 4-4"/></svg>
        <h3>真实能力探测</h3>
        <p>不止测连通: 每节点定期经隧道实访目标服务, 区域封锁/质询循环直接标死, 不通的节点不进池。</p>
      </div></div>
      <div class="col-md-4"><div class="card ln-card">
        <svg class="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c3 3.5 3 14 0 18M12 3c-3 3.5-3 14 0 18"/></svg>
        <h3>多上游冗余</h3>
        <p>多家上游自动汇入同一池, 证书指纹 pin 防借壳; 任一上游抖动, 均衡器秒级切走。</p>
      </div></div>
    </div>
  </div>
</section>

<section id="trust" class="ln-section">
  <div class="container px-4" style="max-width:1140px">
    <h2 class="text-center mb-5">人们为什么信任它</h2>
    <div class="row g-4 justify-content-center">
      <div class="col-lg-8"><div class="card ln-card">
        <h3>关键时刻, 经受住了考验</h3>
        <p>解锁矩阵全实测: YouTube / Netflix / X / Telegram / GitHub 经订阅链返回 200; 每一次路由变更都留有事件审计。</p>
      </div></div>
      <div class="col-md-4"><div class="card ln-card">
        <h3>探测驱动的非玄学运营</h3>
        <p>节点好坏不靠嘴: 存活、延迟、能力三类探测持续运行, 状态页可见。</p>
      </div></div>
      <div class="col-md-4"><div class="card ln-card">
        <h3>透明的事件流</h3>
        <p>摘除、回池、上游拉取全部记入事件流, 管理员与你都能看到同一份事实。</p>
      </div></div>
    </div>
  </div>
</section>

<section class="ln-cta">
  <div class="container">
    <a class="btn btn-primary" href="{{.PortalURL}}">免费试用订阅</a>
  </div>
</section>

<footer class="ln-footer">
  <div class="container d-flex flex-wrap gap-3 justify-content-between" style="max-width:1140px">
    <span>vpn-master · 订阅式代理服务</span>
    <span>节点状态以管理台实时数据为准</span>
  </div>
</footer>
</body>
</html>
{{end}}
`

func (h *Handler) landing(c *gin.Context) {
	// 不走 c.HTML: gin 引擎未 SetHTMLRender(全站 SSR 经 webui.WritePage 直渲), 否则 HTMLRender nil panic。
	webui.WritePage(c.Writer, h.Template, "landing", gin.H{
		"Title":     "vpn-master — 突破网络限制的智能代理",
		"CSS":       webui.ThemeCSS(),
		"PortalURL": portalURL(),
	})
}
