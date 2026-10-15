package web

// userLayout 是 user-center 的 layout(用户导航/壳/按 Content 分发), 移动端适配。
const userLayout = `
{{define "layout"}}
<!doctype html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · vpn-master 用户中心</title>
<style>{{.CSS}}</style>
</head>
<body>
{{if ne .Content "login"}}
<nav class="wm-nav uc-nav">
  <a class="wm-brand" href="/dashboard">我的</a>
  {{if eq .Active "dashboard"}}<a class="wm-nav-item wm-active" href="/dashboard">概览</a>{{else}}<a class="wm-nav-item" href="/dashboard">概览</a>{{end}}
  {{if eq .Active "subscription"}}<a class="wm-nav-item wm-active" href="/subscription">订阅</a>{{else}}<a class="wm-nav-item" href="/subscription">订阅</a>{{end}}
  {{if eq .Active "purchase"}}<a class="wm-nav-item wm-active" href="/purchase">购买</a>{{else}}<a class="wm-nav-item" href="/purchase">购买</a>{{end}}
  <span class="wm-nav-right"><form method="POST" action="/logout" style="display:inline"><button type="submit" class="wm-link" style="background:none;border:none;padding:0;cursor:pointer">退出</button></form></span>
</nav>
{{end}}
<main class="wm-main uc-main">
{{if eq .Content "login"}}{{template "login" .PageData}}{{end}}
{{if eq .Content "pageDashboard"}}{{template "pageDashboard" .PageData}}{{end}}
{{if eq .Content "pageSubscription"}}{{template "pageSubscription" .PageData}}{{end}}
{{if eq .Content "pagePurchase"}}{{template "pagePurchase" .PageData}}{{end}}
</main>
</body>
</html>
{{end}}

{{define "login"}}
<div class="wm-card" style="max-width:380px;margin:40px auto">
  <h1 class="wm-h1">登录用户中心</h1>
  <p class="wm-dim">粘贴运营发给你的订阅 token</p>
  {{if .Err}}<div class="wm-alert wm-alert-err">{{.Err}}</div>{{end}}
  <form method="POST" action="/login" class="wm-form">
    <input class="wm-input" type="text" name="token" placeholder="粘贴 token" autofocus>
    <button class="wm-btn wm-btn-primary" type="submit">登录</button>
  </form>
</div>
{{end}}

{{define "pageDashboard"}}
<h1 class="wm-h1">概览</h1>
<div class="wm-stat">
  <div class="wm-stat-item"><div class="wm-stat-num">{{.Username}}</div><div class="wm-stat-label">账号</div></div>
  <div class="wm-stat-item"><div class="wm-stat-num" data-f="online">{{if .Online}}在线{{else}}离线{{end}}</div><div class="wm-stat-label">当前状态</div></div>
</div>
<section class="wm-card wm-mt">
  <h2 class="wm-card-title">流量 <span class="wm-badge wm-badge-info">5s 轮询</span></h2>
  <div class="wm-progress"><div class="wm-progress-bar" data-f="quota" style="width:{{.QuotaPct}}%"></div></div>
  <p><span data-f="used">{{.UsedStr}}</span> / <span data-f="limit">{{.LimitStr}}</span> 已用</p>
  <p class="wm-dim">到期: <span data-f="expire">{{.ExpireStr}}</span> · 账号状态: <span data-f="status">{{.Status}}</span></p>
</section>
<script>
(function(){
  async function poll(){
    try{
      const r = await fetch('/api/status');
      if(!r.ok) return location.href='/login';
      const d = await r.json();
      const q = document.querySelector('[data-f="used"]');
      if(q){
        q.textContent=d.used;
        document.querySelector('[data-f="limit"]').textContent=d.limit;
        document.querySelector('[data-f="expire"]').textContent=d.expire;
        document.querySelector('[data-f="status"]').textContent=d.status;
        document.querySelector('[data-f="online"]').textContent=d.online?'在线':'离线';
      }
    }catch(e){/* 断网静默重试 */}
  }
  poll(); setInterval(poll,5000);
})();
</script>
{{end}}

{{define "pageSubscription"}}
<h1 class="wm-h1">订阅</h1>
{{if .Err}}<div class="wm-alert wm-alert-err">{{.Err}}</div>{{end}}
{{if .Changed}}<div class="wm-alert">已更换 token, 立即生效</div>{{end}}
<section class="wm-card">
  <h2 class="wm-card-title">订阅链接</h2>
  <div class="wm-subrow">
    <input class="wm-input" id="suburl" readonly value="{{.SubURL}}">
    <button class="wm-btn wm-btn-primary" id="copybtn" onclick="navigator.clipboard.writeText(document.getElementById('suburl').value)">复制</button>
  </div>
  <img src="/qr.png" width="180" height="180" alt="订阅二维码" class="wm-mt">
</section>
<section class="wm-card">
  <h2 class="wm-card-title">客户端导入指引</h2>
  {{range .Guides}}
  <div class="wm-guide"><strong>{{.Title}}</strong><span class="wm-dim"> {{.Desc}}</span></div>
  {{end}}
</section>
<section class="wm-card">
  <h2 class="wm-card-title">更换 token</h2>
  <p class="wm-dim">运营 regenerate 后粘贴新 token, 立即生效</p>
  <form method="POST" action="/subscription/token" class="wm-form">
    <input class="wm-input" type="text" name="token" placeholder="新 token" required>
    <button class="wm-btn" type="submit">更换</button>
  </form>
</section>
{{end}}

{{define "pagePurchase"}}
<h1 class="wm-h1">购买</h1>
{{if .Err}}<div class="wm-alert wm-alert-err">{{.Err}}</div>{{end}}
{{if .JustOrdered}}<div class="wm-alert">已下单, 等待到账…(5s 轮询)</div>{{end}}
{{range .Plans}}
<section class="wm-card">
  <div class="wm-plan">
    <div><h2 class="wm-card-title" style="margin:0">{{.Name}}</h2>
      <p class="wm-dim">{{.Price}} 分 · 流量 {{.GBStr}}GB · {{.Days}}天</p>
      <p class="wm-dim">未支付需 <strong>联系渠道加量</strong>(MVP 不接支付网关)</p></div>
    <form method="POST" action="/purchase"><input type="hidden" name="plan" value="{{.Name}}">
      <button class="wm-btn wm-btn-primary" type="submit">下单</button></form>
  </div>
</section>
{{end}}
<section class="wm-card">
  <h2 class="wm-card-title">订单历史</h2>
  <table class="wm-table" id="inv-table"><thead><tr><th>套餐</th><th>金额</th><th>状态</th><th>时间</th></tr></thead>
  <tbody id="inv-body">
    {{range .Invoices}}<tr><td>{{.PlanName}}</td><td>{{.Amount}}</td><td>{{.Status}}</td><td>{{timefmt .CreatedAt "2006-01-02 15:04"}}</td></tr>{{end}}
  </tbody></table>
</section>
<script>
(function(){
  async function poll(){
    try{
      const r = await fetch('/api/invoices'); if(!r.ok) return;
      const invs = await r.json();
      const b = document.getElementById('inv-body'); if(!b) return;
      b.innerHTML='';
      for(const i of invs){
        const tr=document.createElement('tr');
        tr.innerHTML='<td></td><td></td><td></td><td></td>';
        tr.cells[0].textContent=i.PlanName; tr.cells[1].textContent=i.Amount;
        tr.cells[2].textContent=i.Status; tr.cells[3].textContent=i.CreatedAt;
        b.appendChild(tr);
      }
    }catch(e){}
  }
  poll(); setInterval(poll,5000);
})();
</script>
{{end}}
`
