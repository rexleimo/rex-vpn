package web

// userLayout 是 user-center 的 layout + 页 define。
// 视觉地基 = vendored Bootstrap 5 + Apple 风 theme.css(ADR-0002);
// 轮询契约(data-f / inv-body 选择器)与 wc-6.x 保持一致。
const userLayout = `
{{define "layout"}}
<!doctype html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · vpn-master 用户中心</title>
<link rel="stylesheet" href="/assets/bootstrap.min.css">
<style>{{.CSS}}</style>
</head>
<body>
{{if and (ne .Content "login") (ne .Content "bind")}}
<nav class="navbar navbar-expand sticky-top wm-glass">
  <div class="container">
    <a class="navbar-brand wm-brand" href="/dashboard">我的</a>
    <button class="navbar-toggler" type="button" data-bs-toggle="collapse" data-bs-target="#ucnav" aria-label="导航">
      <span class="navbar-toggler-icon"></span>
    </button>
    <div class="collapse navbar-collapse" id="ucnav">
      <div class="navbar-nav ms-auto align-items-md-center gap-md-2">
        <a class="nav-link{{if eq .Active "dashboard"}} active fw-semibold{{end}}" href="/dashboard">概览</a>
        <a class="nav-link{{if eq .Active "subscription"}} active fw-semibold{{end}}" href="/subscription">订阅</a>
        <a class="nav-link{{if eq .Active "purchase"}} active fw-semibold{{end}}" href="/purchase">购买</a>
        {{if .IsAdmin}}<a class="nav-link{{if eq .Active "admin"}} active fw-semibold{{end}}" href="/admin/users">用户管理</a>{{end}}
        <form method="POST" action="/logout" class="d-inline ms-md-2">
          <button type="submit" class="btn btn-outline-secondary btn-sm">退出</button>
        </form>
      </div>
    </div>
  </div>
</nav>
{{end}}
<main class="container py-4 py-md-5" style="max-width:1080px">
{{if eq .Content "login"}}{{template "login" .PageData}}{{end}}
{{if eq .Content "bind"}}{{template "bind" .PageData}}{{end}}
{{if eq .Content "adminUsers"}}{{template "adminUsers" .PageData}}{{end}}
{{if eq .Content "pageDashboard"}}{{template "pageDashboard" .PageData}}{{end}}
{{if eq .Content "pageSubscription"}}{{template "pageSubscription" .PageData}}{{end}}
{{if eq .Content "pagePurchase"}}{{template "pagePurchase" .PageData}}{{end}}
</main>
<script src="/assets/bootstrap.bundle.min.js"></script>
<script src="/assets/wm-ui.js?v=6"></script>
<div class="toast-container position-fixed top-0 end-0 p-3" id="wm-toasts" style="z-index:1090"></div>
<div class="modal" id="wm-confirm" tabindex="-1">
  <div class="modal-dialog modal-dialog-centered">
    <div class="modal-content">
      <div class="modal-header"><h5 class="modal-title">确认操作</h5>
        <button type="button" class="btn-close" data-bs-dismiss="modal"></button></div>
      <div class="modal-body"></div>
      <div class="modal-footer">
        <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">取消</button>
        <button type="button" class="btn btn-primary" data-wm-confirm>确认</button>
      </div>
    </div>
  </div>
</div>
</body>
</html>
{{end}}

{{define "login"}}
<div class="wm-auth-wrap d-flex align-items-center justify-content-center px-3">
  <div class="card wm-auth-card p-4 p-md-5 w-100" style="max-width:420px">
    <div class="text-center mb-4">
      <div class="wm-display fs-3">用户中心</div>
      {{if .Accounts}}<div class="wm-muted small mt-2">使用运营发放的账号登录</div>{{else}}<div class="wm-muted small mt-2">粘贴运营发给你的订阅 token</div>{{end}}
    </div>
    {{if .Err}}<div class="alert alert-danger py-2 small" role="alert">{{if .Accounts}}账号或密码错误，请重试{{else}}token 无效或已过期，请核对后重试{{end}}</div>{{end}}
    {{if .Accounts}}
    <form method="POST" action="/login" data-wm-act="/login" class="d-grid gap-3">
      <input class="form-control form-control-lg" type="text" name="identifier" placeholder="邮箱或用户名" autocomplete="username" autofocus required>
      <input class="form-control form-control-lg" type="password" name="password" placeholder="密码" autocomplete="current-password" required>
      <button class="btn btn-primary btn-lg" type="submit">登录</button>
    </form>
    {{else}}
    <form method="POST" action="/login" data-wm-act="/login" class="d-grid gap-3">
      <input class="form-control form-control-lg" type="text" name="token" placeholder="订阅 token" autofocus required>
      <button class="btn btn-primary btn-lg" type="submit">登录</button>
    </form>
    {{end}}
  </div>
</div>
{{end}}

{{define "bind"}}
<div class="wm-auth-wrap d-flex align-items-center justify-content-center px-3">
  <div class="card wm-auth-card p-4 p-md-5 w-100" style="max-width:460px">
    <div class="text-center mb-4">
      <div class="wm-display fs-4">绑定订阅</div>
      <div class="wm-muted small mt-2">账号 <strong>{{.Email}}</strong> 尚未关联订阅。把运营发给你的订阅 token 粘贴一次，之后登录不再需要它。</div>
    </div>
    {{if .Err}}<div class="alert alert-danger py-2 small" role="alert">token 无效或已过期，请核对后重试</div>{{end}}
    <form method="POST" action="/bind" data-wm-act="/bind" class="d-grid gap-3">
      <input class="form-control form-control-lg" type="text" name="token" placeholder="订阅 token" autofocus required>
      <button class="btn btn-primary btn-lg" type="submit">绑定并进入</button>
    </form>
  </div>
</div>
{{end}}

{{define "adminUsers"}}
<div class="d-flex justify-content-between align-items-end mb-4">
  <div>
    <h1 class="wm-display fs-2 mb-1">用户管理</h1>
    <div class="wm-muted">账号体系(authkit): 运营建号、禁用、重置密码</div>
  </div>
  <button class="btn btn-primary" data-bs-toggle="modal" data-bs-target="#wm-add-user">+ 添加用户</button>
</div>
<form method="GET" action="/admin/users" class="input-group mb-3" style="max-width:360px">
  <input class="form-control" type="search" name="q" placeholder="搜邮箱/用户名" value="{{.Search}}">
  <button class="btn btn-outline-secondary" type="submit">搜索</button>
</form>
<div class="card">
  <table class="table table-hover align-middle mb-0">
    <thead><tr><th>邮箱</th><th>用户名</th><th>状态</th><th>角色</th><th>最近登录</th><th class="text-end">操作</th></tr></thead>
    <tbody>
    {{range .Users}}
      <tr>
        <td>{{.Email}}</td>
        <td class="wm-muted">{{.Username}}</td>
        <td>{{if eq .Status "active"}}<span class="badge text-bg-success">正常</span>{{else if eq .Status "banned"}}<span class="badge text-bg-warning">已禁用</span>{{else}}<span class="badge text-bg-secondary">已删除</span>{{end}}</td>
        <td>{{if .Admin}}<span class="badge text-bg-dark">管理员</span>{{end}}</td>
        <td class="wm-muted small">{{.LastLogin}}</td>
        <td class="text-end">
          {{if ne .ID $.SelfID}}
          <form method="POST" action="/admin/users/resetpw" data-wm-act="/admin/users/resetpw" data-wm-flash="密码已重置 {{.Email}}" class="d-inline-flex gap-1">
            <input type="hidden" name="id" value="{{.ID}}">
            <input class="form-control form-control-sm" type="text" name="password" placeholder="新密码" style="width:7.5rem" required minlength="8">
            <button class="btn btn-outline-secondary btn-sm" type="submit">重置密码</button>
          </form>
          {{if not .Admin}}<form method="POST" action="/admin/users/grant" data-wm-act="/admin/users/grant" data-wm-flash="已授予管理员" class="d-inline">
            <input type="hidden" name="email" value="{{.Email}}">
            <button class="btn btn-outline-primary btn-sm" type="submit">授管理</button>
          </form>{{end}}
          {{if eq .Status "active"}}<form method="POST" action="/admin/users/ban" class="d-inline" data-wm-danger data-wm-title="确认禁用" data-wm-ok="禁用" data-wm-flash="已禁用 {{.Email}}"
            onsubmit="return wmConfirmAct(event, this, '禁用 {{.Email}} 后该账号立即无法登录与使用订阅。确定?')">
            <input type="hidden" name="id" value="{{.ID}}"><button class="btn btn-outline-warning btn-sm" type="submit">禁用</button></form>
          {{else if eq .Status "banned"}}<form method="POST" action="/admin/users/unban" data-wm-act="/admin/users/unban" data-wm-flash="已恢复 {{.Email}}" class="d-inline">
            <input type="hidden" name="id" value="{{.ID}}"><button class="btn btn-outline-success btn-sm" type="submit">恢复</button></form>{{end}}
          {{if ne .Status "deleted"}}<form method="POST" action="/admin/users/delete" class="d-inline" data-wm-danger data-wm-title="确认删除" data-wm-ok="删除" data-wm-flash="已删除 {{.Email}}"
            onsubmit="return wmConfirmAct(event, this, '删除 {{.Email}} 为软删(30 天内可恢复), 订阅绑定保留。确定?')">
            <input type="hidden" name="id" value="{{.ID}}"><button class="btn btn-outline-danger btn-sm" type="submit">删除</button></form>{{end}}
          {{else}}<span class="wm-muted small">当前账号</span>{{end}}
        </td>
      </tr>
    {{else}}
      <tr><td colspan="6" class="text-center wm-muted py-4">暂无账号 —— 点右上「+ 添加用户」建第一个</td></tr>
    {{end}}
    </tbody>
  </table>
</div>

<div class="modal fade" id="wm-add-user" tabindex="-1">
  <div class="modal-dialog modal-dialog-centered">
    <div class="modal-content">
      <form method="POST" action="/admin/users/create" data-wm-act="/admin/users/create" data-wm-flash="用户已添加">
      <div class="modal-header"><h5 class="modal-title">添加用户</h5>
        <button type="button" class="btn-close" data-bs-dismiss="modal"></button></div>
      <div class="modal-body">
        <div class="mb-3"><label class="form-label">邮箱</label>
          <input class="form-control" type="email" name="email" placeholder="user@example.com" required autofocus></div>
        <div><label class="form-label">初始密码(≥ 8 位)</label>
          <input class="form-control" type="text" name="password" minlength="8" required></div>
        <div class="wm-muted small mt-2">用户名自动从邮箱派生; 用户登录后可在绑定页关联订阅 token。</div>
      </div>
      <div class="modal-footer">
        <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">取消</button>
        <button type="submit" class="btn btn-primary">创建</button>
      </div>
      </form>
    </div>
  </div>
</div>
{{end}}

{{define "pageDashboard"}}
<div class="d-flex justify-content-between align-items-end mb-4">
  <div>
    <h1 class="wm-display fs-2 mb-1">{{.Username}}</h1>
    <div class="wm-muted">账号概览</div>
  </div>
  <span class="badge text-bg-secondary" data-f="online">{{if .Online}}在线{{else}}离线{{end}}</span>
</div>
<div class="row g-3">
  <div class="col-12 col-md-8">
    <div class="card h-100 p-4">
      <h2 class="h6 wm-muted mb-3">流量 <span class="badge text-bg-info">5s 轮询</span></h2>
      <div class="progress"><div class="progress-bar" data-f="quota" style="width:{{.QuotaPct}}%"></div></div>
      <p class="mt-3 mb-1"><strong data-f="used">{{.UsedStr}}</strong>
        <span class="wm-muted">/ <span data-f="limit">{{.LimitStr}}</span> 已用</span></p>
      <p class="wm-muted small mb-0">到期: <span data-f="expire">{{.ExpireStr}}</span> · 状态: <span data-f="status">{{.Status}}</span></p>
    </div>
  </div>
  <div class="col-12 col-md-4">
    <div class="card h-100 p-4">
      <h2 class="h6 wm-muted mb-2">快捷入口</h2>
      <div class="list-group list-group-flush">
        <a class="list-group-item list-group-item-action px-0" href="/subscription">订阅链接与二维码 →</a>
        <a class="list-group-item list-group-item-action px-0" href="/purchase">购买加量 →</a>
      </div>
    </div>
  </div>
</div>
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
<h1 class="wm-display fs-2 mb-4">订阅</h1>
{{if .Err}}<div class="alert alert-danger py-2 small">{{.Err}}</div>{{end}}
{{if .Changed}}<div class="alert alert-success py-2 small">已更换 token, 立即生效</div>{{end}}
<div class="row g-3">
  <div class="col-12 col-lg-7">
    <div class="card p-4 h-100">
      <h2 class="h6 wm-muted mb-2">订阅链接</h2>
      <div class="input-group">
        <input id="suburl" class="form-control wm-mono" readonly value="{{.SubURL}}">
        <button class="btn btn-primary" id="copybtn" data-wm-copy-text="{{.SubURL}}">复制</button>
      </div>
      <div class="text-center mt-4 mb-2">
        <img src="/qr.png" width="200" height="200" alt="订阅二维码" class="wm-qr">
        <div class="wm-muted small mt-2">扫码导入支持 Clash 系客户端</div>
      </div>
    </div>
  </div>
  <div class="col-12 col-lg-5">
    <div class="card p-4 h-100">
      <h2 class="h6 wm-muted mb-2">客户端导入指引</h2>
      <div class="list-group list-group-flush mb-3">
        {{range .Guides}}
        <div class="list-group-item px-0"><strong>{{.Title}}</strong>
          <div class="wm-muted small">{{.Desc}}</div></div>
        {{end}}
      </div>
      <h2 class="h6 wm-muted">更换 token</h2>
      <p class="wm-muted small mb-2">运营 regenerate 后粘贴新 token, 立即生效</p>
      <form method="POST" action="/subscription/token" data-wm-act="/subscription/token" class="input-group">
        <input class="form-control" type="text" name="token" placeholder="新 token" required>
        <button class="btn btn-outline-secondary" type="submit">更换</button>
      </form>
    </div>
  </div>
</div>
{{end}}

{{define "pagePurchase"}}
<h1 class="wm-display fs-2 mb-4">购买</h1>
{{if .Err}}<div class="alert alert-danger py-2 small">{{.Err}}</div>{{end}}
{{if .JustOrdered}}<div class="alert alert-success py-2 small">已下单, 等待到账…(5s 轮询)</div>{{end}}
<div class="row g-3">
  {{range .Plans}}
  <div class="col-12 col-md-6">
    <div class="card p-4 h-100 d-flex flex-column">
      <h2 class="h5 mb-1">{{.Name}}</h2>
      <div class="wm-muted small mb-3">{{.Price}} 分 · 流量 {{.GBStr}}GB · {{.Days}}天</div>
      <div class="wm-muted small mb-3">未支付需 <strong>联系渠道加量</strong>(MVP 不接支付网关)</div>
      <form method="POST" action="/purchase" data-wm-act="/purchase" class="mt-auto d-grid">
        <input type="hidden" name="plan" value="{{.Name}}">
        <button class="btn btn-primary" type="submit">下单</button>
      </form>
    </div>
  </div>
  {{end}}
</div>
<div class="card p-4 mt-4">
  <h2 class="h6 wm-muted mb-2">订单历史</h2>
  <table class="table table-striped align-middle mb-0" id="inv-table">
    <thead><tr><th>套餐</th><th>金额</th><th>状态</th><th>时间</th></tr></thead>
    <tbody id="inv-body">
      {{range .Invoices}}<tr><td>{{.PlanName}}</td><td>{{.Amount}}</td><td>{{.Status}}</td><td>{{timefmt .CreatedAt "2006-01-02 15:04"}}</td></tr>{{end}}
    </tbody>
  </table>
</div>
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
