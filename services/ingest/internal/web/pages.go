package web

// adminLayout 是 ingest 管理页的 layout(导航/壳/按 Content 分发)。与 user-center 的 user layout 相对。
// S1(ADR-0003/0004): Bootstrap 壳 + toast/confirm 宿主 + wm-ui.js 共享胶水;
// 未迁移页面(总览/节点/事件/用户)暂靠 app.css 的 wm-* 过渡样式, 迁移切片完成后退役。
const adminLayout = `
{{define "layout"}}
<!doctype html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · vpn-master 管理</title>
<link rel="stylesheet" href="/assets/bootstrap.min.css">
<style>{{.CSS}}</style>
</head>
<body>
{{if ne .Content "login"}}
<nav class="navbar navbar-expand sticky-top wm-glass">
  <div class="container">
    <a class="navbar-brand wm-brand" href="/admin">vpn-master 管理</a>
    <button class="navbar-toggler" type="button" data-bs-toggle="collapse" data-bs-target="#admnav" aria-label="导航">
      <span class="navbar-toggler-icon"></span>
    </button>
    <div class="collapse navbar-collapse" id="admnav">
      <div class="navbar-nav ms-auto align-items-md-center gap-md-2">
        <a class="nav-link{{if eq .Active "overview"}} active fw-semibold{{end}}" href="/admin">总览</a>
        <a class="nav-link{{if eq .Active "upstreams"}} active fw-semibold{{end}}" href="/admin/upstreams">上游</a>
        <a class="nav-link{{if eq .Active "nodes"}} active fw-semibold{{end}}" href="/admin/nodes">节点</a>
        <a class="nav-link{{if eq .Active "events"}} active fw-semibold{{end}}" href="/admin/events">事件</a>
        <a class="nav-link{{if eq .Active "users"}} active fw-semibold{{end}}" href="/admin/users">用户</a>
        {{if .Degraded}}<span class="badge text-bg-danger">DEGRADED</span>{{end}}
        <a class="btn btn-outline-secondary btn-sm ms-md-2" href="/logout">退出</a>
      </div>
    </div>
  </div>
</nav>
{{end}}
<main class="container py-4 py-md-5" style="max-width:1200px">
{{if eq .Content "login"}}{{template "login" .PageData}}{{end}}
{{if eq .Content "pageOverview"}}{{template "pageOverview" .PageData}}{{end}}
{{if eq .Content "pageUpstreams"}}{{template "pageUpstreams" .PageData}}{{end}}
{{if eq .Content "pageNodes"}}{{template "pageNodes" .PageData}}{{end}}
{{if eq .Content "pageEvents"}}{{template "pageEvents" .PageData}}{{end}}
{{if eq .Content "pageUsers"}}{{template "pageUsers" .PageData}}{{end}}
</main>

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

<script src="/assets/bootstrap.bundle.min.js"></script>
<script src="/assets/wm-ui.js"></script>
</body>
</html>
{{end}}
`

// pageTemplates 是各管理页的 html/template define 块(与 pkg/webui base.html 同 set)。
// 每个页面命名块 dot = 对应 PageData 结构。表格区 id 供 7.3 轮询刷新。

const pageTemplates = `
{{define "login"}}
<div class="wm-auth-wrap d-flex align-items-center justify-content-center px-3">
  <div class="card wm-auth-card p-4 p-md-5 w-100" style="max-width:420px">
    <div class="text-center mb-4">
      <div class="wm-display fs-3">vpn-master 管理</div>
      <div class="wm-muted small mt-2">运营后台 · 管理员口令登录</div>
    </div>
    {{if .Err}}<div class="alert alert-danger py-2 small" role="alert">口令错误</div>{{end}}
    <form method="POST" action="/login" class="d-grid gap-3">
      <input class="form-control form-control-lg" type="password" name="password" placeholder="口令" autofocus required>
      <button class="btn btn-primary btn-lg" type="submit">登录</button>
    </form>
  </div>
</div>
{{end}}

{{define "nodeOps"}}
{{if eq .Status "active"}}
<form method="POST" action="/admin/nodes/evict"><input type="hidden" name="fp" value="{{.Fingerprint}}"><button class="wm-btn wm-btn-danger wm-btn-sm" type="submit">摘除</button></form>
{{else if eq .Status "evicted"}}
<form method="POST" action="/admin/nodes/recover"><input type="hidden" name="fp" value="{{.Fingerprint}}"><button class="wm-btn wm-btn-sm" type="submit">恢复</button></form>
{{end}}
{{end}}

{{define "pageOverview"}}
<h1 class="wm-h1">总览</h1>
<div class="wm-stat">
  <div class="wm-stat-item"><div class="wm-stat-num">{{.UpstreamCount}}</div><div class="wm-stat-label">上游来源</div></div>
  <div class="wm-stat-item"><div class="wm-stat-num">{{index .NodeCounts "active"}}</div><div class="wm-stat-label">active 节点</div></div>
  <div class="wm-stat-item"><div class="wm-stat-num">{{index .NodeCounts "evicted"}}</div><div class="wm-stat-label">evicted 节点</div></div>
  <div class="wm-stat-item"><div class="wm-stat-num">{{index .NodeCounts "pending"}}</div><div class="wm-stat-label">pending 节点</div></div>
</div>
<section class="wm-card wm-mt">
  <h2 class="wm-card-title">上游</h2>
  {{if .Upstreams}}
  <table class="wm-table">
    <tr><th>名称</th><th>来源(掩码)</th><th>节点数</th><th>最近成功</th><th>最近错误</th></tr>
    {{range .Upstreams}}
    <tr><td>{{.Name}}</td><td class="wm-mono wm-dim">{{.URLMasked}}</td><td>{{.NodeCount}}</td><td>{{.LastOKStr}}</td><td class="wm-dim">{{.LastErr}}</td></tr>
    {{end}}
  </table>
  {{else}}<div class="wm-dim">暂无上游</div>{{end}}
</section>
<section class="wm-card">
  <h2 class="wm-card-title">最近事件</h2>
  {{if .RecentEvents}}
  <ul class="wm-timeline">
    {{range .RecentEvents}}
    <li><span class="wm-kind">{{.Kind}}</span><span class="wm-dim">{{.AtStr}}</span><span>{{.From}} → {{.To}}</span><span class="wm-mono wm-dim">{{.NodeFP}}</span><span class="wm-dim">{{.Detail}}</span></li>
    {{end}}
  </ul>
  {{else}}<div class="wm-dim">暂无事件</div>{{end}}
</section>
{{end}}

{{define "pageUpstreams"}}
<h1 class="wm-display fs-2 mb-4">上游</h1>
{{with .Err}}<div class="alert alert-danger py-2 small">{{.}}</div>{{end}}
<div class="row g-3">
  <div class="col-12 col-lg-7">
    <div class="card p-4">
      <h2 class="h6 wm-muted mb-3">来源清单</h2>
      <div class="table-responsive">
      <table class="table table-hover align-middle mb-0" id="ups-table">
        <thead><tr><th>名称</th><th>来源(掩码)</th><th>节点</th><th>最近成功</th><th>最近错误</th><th class="text-end">操作</th></tr></thead>
        <tbody>
        {{range .Upstreams}}
          <tr>
            <td class="fw-semibold">{{.Name}}</td>
            <td class="wm-mono wm-muted">{{.URLMasked}}</td>
            <td>{{.NodeCount}}</td>
            <td class="wm-muted small">{{.LastOKStr}}</td>
            <td class="wm-muted small">{{.LastErr}}</td>
            <td class="text-end text-nowrap">
              <button type="button" class="btn btn-outline-secondary btn-sm"
                onclick="wmOpenEdit('{{.Name}}')">编辑</button>
              <form method="POST" action="/admin/upstreams/delete" class="d-inline" data-wm-danger data-wm-title="删除上游" data-wm-ok="删除" data-wm-flash="已删除 {{.Name}}"
                onsubmit="return wmConfirmAct(event, this, '删除 {{.Name}} 将同时删除其 {{.NodeCount}} 个节点, 不可恢复。确定?')">
                <input type="hidden" name="name" value="{{.Name}}">
                <button class="btn btn-outline-danger btn-sm" type="submit">删除</button>
              </form>
            </td>
          </tr>
        {{else}}
          <tr><td colspan="6" class="wm-muted text-center py-4">暂无上游, 用右侧表单添加</td></tr>
        {{end}}
        </tbody>
      </table>
      </div>
    </div>
  </div>
  <div class="col-12 col-lg-5">
    <div class="card p-4">
      <h2 class="h6 wm-muted mb-3">添加来源</h2>
      <form method="POST" action="/admin/upstreams" class="d-grid gap-2"
        onsubmit="return wmSubmitAct(event, this, '已添加上游')">
        <input class="form-control" name="name" placeholder="名称(如 vpn4zx)" required>
        <input class="form-control" name="url" placeholder="订阅 URL(含 token)" required>
        <button class="btn btn-primary" type="submit">添加</button>
      </form>
      {{if .CanFetch}}
      <hr class="my-4">
      <form method="POST" action="/admin/upstreams/fetch"
        onsubmit="return wmSubmitAct(event, this, '拉取已触发')">
        <button class="btn btn-outline-secondary w-100" type="submit">立即拉取全部</button>
      </form>
      {{end}}
    </div>
  </div>
</div>

<div class="modal" id="wm-edit" tabindex="-1">
  <div class="modal-dialog modal-dialog-centered">
    <div class="modal-content">
      <form method="POST" action="/admin/upstreams/update" onsubmit="return wmEditSave(event, this)">
        <div class="modal-header"><h5 class="modal-title">编辑上游</h5>
          <button type="button" class="btn-close" data-bs-dismiss="modal"></button></div>
        <div class="modal-body">
          <label class="form-label wm-muted small">名称(不可改, 节点按来源关联)</label>
          <input class="form-control mb-3" name="name" readonly>
          <label class="form-label wm-muted small">订阅 URL(含 token)</label>
          <input class="form-control wm-mono" name="url" placeholder="新订阅 URL" required>
        </div>
        <div class="modal-footer">
          <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">取消</button>
          <button type="submit" class="btn btn-primary">保存</button>
        </div>
      </form>
    </div>
  </div>
</div>
<script>
function wmOpenEdit(name){
  var el=document.getElementById('wm-edit');
  el.querySelector('input[name=name]').value=name;
  el.querySelector('input[name=url]').value='';
  bootstrap.Modal.getOrCreateInstance(el).show();
}
function wmEditSave(ev, form){
  if(ev) ev.preventDefault();
  var fields={name:form.elements.name.value, url:form.elements.url.value};
  wmAction(form.action, fields, function(d){
    if(d.ok){ bootstrap.Modal.getInstance(document.getElementById('wm-edit')).hide(); wmFlash('已更新 '+fields.name); location.reload(); }
    else wmToast(d.error||'更新失败','danger');
  });
  return false;
}
</script>
{{end}}

{{define "pageNodes"}}
<h1 class="wm-h1">节点</h1>
<section class="wm-card">
  <h2 class="wm-card-title">节点池 <span class="wm-badge wm-badge-info">5s 轮询</span></h2>
  <table class="wm-table" id="nodes-table">
    <thead><tr><th>指纹</th><th>地址</th><th>协议</th><th>状态</th><th>延迟</th><th>最近见</th><th>操作</th></tr></thead>
    <tbody id="nodes-body">
      {{range .Nodes}}
      <tr>
        <td class="wm-mono wm-dim">{{substr .Fingerprint 0 8}}</td>
        <td>{{.Addr}}:{{.Port}}</td>
        <td>{{.Protocol}}</td>
        <td>{{template "badge" (dict "Text" .Status "Variant" .StatusVar)}}</td>
        <td>{{.LatencyStr}}</td>
        <td class="wm-dim">{{.LastSeenStr}}</td>
        <td class="wm-ops">{{template "nodeOps" .}}</td>
      </tr>
      {{else}}
      <tr><td colspan="7" class="wm-dim">暂无节点</td></tr>
      {{end}}
    </tbody>
  </table>
</section>
<script>
// 7.3: 5s 轮询 JSON 端点刷新表格区, 不整页刷新。
(function () {
  const body = document.getElementById('nodes-body');
  const BADGE = {active:'wm-badge-active',pending:'wm-badge-pending',evicted:'wm-badge-evicted'};
  async function refresh(){
    try {
      const r = await fetch('/api/nodes');
      if (!r.ok) return;
      const nodes = await r.json();
      body.innerHTML = '';
      if (!nodes || !nodes.length) {
        body.innerHTML = '<tr><td colspan="7" class="wm-dim">暂无节点</td></tr>';
        return;
      }
      for (const n of nodes) {
        const tr = document.createElement('tr');
        tr.innerHTML =
          '<td class="wm-mono wm-dim"></td><td></td><td></td><td></td><td></td><td class="wm-dim"></td><td class="wm-ops"></td>';
        tr.cells[0].textContent = (n.Fingerprint||'').slice(0,8);
        tr.cells[1].textContent = n.Addr + ':' + n.Port;
        tr.cells[2].textContent = n.Protocol;
        const b = document.createElement('span');
        b.className = 'wm-badge ' + (BADGE[n.Status] || 'wm-badge-pending');
        b.textContent = n.Status;
        tr.cells[3].appendChild(b);
        tr.cells[4].textContent = n.LatencyStr || '—';
        tr.cells[5].textContent = n.LastSeenStr || '—';
        const ops = tr.cells[6];
        if (n.Status === 'active') {
          ops.appendChild(makeForm('/admin/nodes/evict', n.Fingerprint, '摘除', true));
        } else if (n.Status === 'evicted') {
          ops.appendChild(makeForm('/admin/nodes/recover', n.Fingerprint, '恢复', false));
        }
        body.appendChild(tr);
      }
    } catch (e) { /* 轮询失败静默, 下轮再试 */ }
  }
  function makeForm(action, fp, label, danger){
    const f = document.createElement('form');
    f.method='POST'; f.action=action;
    const inp = document.createElement('input');
    inp.type='hidden'; inp.name='fp'; inp.value=fp; f.appendChild(inp);
    const btn = document.createElement('button');
    btn.type='submit'; btn.textContent=label;
    btn.className = 'wm-btn wm-btn-sm' + (danger ? ' wm-btn-danger' : '');
    f.appendChild(btn); return f;
  }
  refresh();
  setInterval(refresh, 5000);
})();
</script>
{{end}}

{{define "pageEvents"}}
<h1 class="wm-h1">事件时间线</h1>
<section class="wm-card">
  {{if .Events}}
  <ul class="wm-timeline">
    {{range .Events}}
    <li>
      {{template "badge" (dict "Text" .Kind "Variant" .KindVar)}}
      <span class="wm-dim">{{.AtStr}}</span>
      <span class="wm-mono wm-dim">{{.NodeFP}}</span>
      <span>{{.From}} → {{.To}}</span>
      <span class="wm-dim">{{.Detail}}</span>
    </li>
    {{end}}
  </ul>
  {{else}}<div class="wm-dim">暂无事件</div>{{end}}
</section>
{{end}}

{{define "pageUsers"}}
<h1 class="wm-h1">用户(只读)</h1>
{{if .ErrText}}<div class="wm-alert wm-alert-warn">{{.ErrText}}</div>{{end}}
{{if and .Configured .Users}}
<section class="wm-card">
  <table class="wm-table">
    <tr><th>ID</th><th>用户名</th><th>状态</th><th>到期</th></tr>
    {{range .Users}}
    <tr><td class="wm-mono wm-dim">{{.ID}}</td><td>{{.Username}}</td><td>{{.Status}}</td><td>{{.ExpireStr}}</td></tr>
    {{end}}
  </table>
</section>
{{end}}
{{end}}
`
