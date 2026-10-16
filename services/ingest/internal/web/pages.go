package web

// adminLayout 是 ingest 管理页的 layout(导航/壳/按 Content 分发)。与 user-center 的 user layout 相对。
// S1(ADR-0003/0004): Bootstrap 壳 + toast/confirm 宿主 + wm-ui.js 共享胶水;
// S4: app.css 旧 wm-* 过渡层已退役, 全站统一 Bootstrap + theme.css。
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
<script src="/assets/wm-ui.js?v=6"></script>
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
<form method="POST" action="/admin/nodes/evict" class="d-inline" data-wm-danger data-wm-title="确认摘除" data-wm-ok="摘除" data-wm-flash="已摘除并同步出口"
  onsubmit="return wmConfirmAct(event, this, '{{substr .Fingerprint 0 8}} 将把节点从出口池移除并立即同步 xray 配置。确定?')"><input type="hidden" name="fp" value="{{.Fingerprint}}"><button class="btn btn-outline-danger btn-sm" type="submit">摘除</button></form>
{{else if eq .Status "evicted"}}
<form method="POST" action="/admin/nodes/recover" class="d-inline" data-wm-title="确认恢复" data-wm-ok="恢复" data-wm-flash="已回池并同步出口"
  onsubmit="return wmConfirmAct(event, this, '{{substr .Fingerprint 0 8}} 将把节点放回出口池并立即同步 xray 配置。确定?')"><input type="hidden" name="fp" value="{{.Fingerprint}}"><button class="btn btn-outline-secondary btn-sm" type="submit">恢复</button></form>
{{end}}
{{end}}

{{define "pageOverview"}}
<h1 class="wm-display fs-2 mb-4">总览</h1>
<div class="row g-3 mb-4">
  <div class="col-6 col-lg-3"><div class="card p-4 text-center"><div class="wm-display fs-1">{{.UpstreamCount}}</div><div class="wm-muted small">上游来源</div></div></div>
  <div class="col-6 col-lg-3"><div class="card p-4 text-center"><div class="wm-display fs-1 text-success">{{index .NodeCounts "active"}}</div><div class="wm-muted small">active 节点</div></div></div>
  <div class="col-6 col-lg-3"><div class="card p-4 text-center"><div class="wm-display fs-1 text-danger">{{index .NodeCounts "evicted"}}</div><div class="wm-muted small">evicted 节点</div></div></div>
  <div class="col-6 col-lg-3"><div class="card p-4 text-center"><div class="wm-display fs-1 wm-muted">{{index .NodeCounts "pending"}}</div><div class="wm-muted small">pending 节点</div></div></div>
</div>
<div class="row g-3">
  <div class="col-12 col-lg-6">
    <div class="card p-4 h-100">
      <h2 class="h6 wm-muted mb-3">上游</h2>
      {{if .Upstreams}}
      <div class="table-responsive">
      <table class="table table-hover align-middle mb-0">
        <thead><tr><th>名称</th><th>节点</th><th>最近成功</th><th>最近错误</th></tr></thead>
        <tbody>
        {{range .Upstreams}}
          <tr><td class="fw-semibold">{{.Name}}</td><td>{{.NodeCount}}</td><td class="wm-muted small">{{.LastOKStr}}</td><td class="wm-muted small">{{.LastErr}}</td></tr>
        {{end}}
        </tbody>
      </table>
      </div>
      {{else}}<div class="wm-muted">暂无上游</div>{{end}}
    </div>
  </div>
  <div class="col-12 col-lg-6">
    <div class="card p-4 h-100">
      <h2 class="h6 wm-muted mb-3">最近事件</h2>
      {{if .RecentEvents}}
      <ul class="list-group list-group-flush">
        {{range .RecentEvents}}
        <li class="list-group-item d-flex flex-wrap gap-2 align-items-center px-0">
          {{template "badge" (dict "Text" .Kind "Variant" .KindVar)}}
          <span class="wm-muted small">{{.AtStr}}</span>
          <span class="wm-mono wm-muted">{{substr .NodeFP 0 8}}</span>
          {{if .From}}<span class="small">{{.From}} → {{.To}}</span>{{end}}
          <span class="wm-muted small flex-grow-1">{{.Detail}}</span>
        </li>
        {{end}}
      </ul>
      {{else}}<div class="wm-muted">暂无事件</div>{{end}}
    </div>
  </div>
</div>
{{end}}

{{define "pageUpstreams"}}
<h1 class="wm-display fs-2 mb-4">上游</h1>
{{with .Err}}<div class="alert alert-danger py-2 small">{{.}}</div>{{end}}
<div class="card p-4">
  <div class="d-flex flex-wrap align-items-center gap-2 mb-3">
    <h2 class="h6 wm-muted mb-0 me-auto">来源清单</h2>
    {{if .CanFetch}}
    <form method="POST" action="/admin/upstreams/fetch" data-wm-act="/admin/upstreams/fetch" class="d-inline">
      <button class="btn btn-outline-secondary btn-sm" type="submit">立即拉取全部</button>
    </form>
    {{end}}
    <button type="button" class="btn btn-primary btn-sm" onclick="wmOpenAdd()">+ 添加上游</button>
  </div>
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
      <tr><td colspan="6" class="wm-muted text-center py-4">暂无上游, 点右上角「+ 添加上游」</td></tr>
    {{end}}
    </tbody>
  </table>
  </div>
</div>

<div class="modal" id="wm-add" tabindex="-1">
  <div class="modal-dialog modal-dialog-centered">
    <div class="modal-content">
      <form method="POST" action="/admin/upstreams" data-wm-act="/admin/upstreams">
        <div class="modal-header"><h5 class="modal-title">添加上游</h5>
          <button type="button" class="btn-close" data-bs-dismiss="modal"></button></div>
        <div class="modal-body d-grid gap-3">
          <div>
            <label class="form-label wm-muted small">名称</label>
            <input class="form-control" name="name" placeholder="如 vpn4zx" required autofocus>
          </div>
          <div>
            <label class="form-label wm-muted small">订阅 URL(含 token)</label>
            <input class="form-control wm-mono" name="url" placeholder="https://…/sub?token=…" required>
          </div>
        </div>
        <div class="modal-footer">
          <button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">取消</button>
          <button type="submit" class="btn btn-primary">添加</button>
        </div>
      </form>
    </div>
  </div>
</div>

<div class="modal" id="wm-edit" tabindex="-1">
  <div class="modal-dialog modal-dialog-centered">
    <div class="modal-content">
      <form method="POST" action="/admin/upstreams/update" data-wm-act="/admin/upstreams/update">
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
function wmOpenAdd(){
  var el=document.getElementById('wm-add');
  el.querySelector('input[name=name]').value='';
  el.querySelector('input[name=url]').value='';
  bootstrap.Modal.getOrCreateInstance(el).show();
}
function wmOpenEdit(name){
  var el=document.getElementById('wm-edit');
  el.querySelector('input[name=name]').value=name;
  el.querySelector('input[name=url]').value='';
  bootstrap.Modal.getOrCreateInstance(el).show();
}
</script>
{{end}}

{{define "pageNodes"}}
<h1 class="wm-display fs-2 mb-4">节点</h1>
<div class="card p-4">
  <form class="row g-2 align-items-center mb-3" id="node-filter" method="GET" action="/admin/nodes">
    <div class="col-12 col-md-5">
      <input class="form-control" name="q" placeholder="搜索指纹 / 地址 / tag" value="{{.Q}}">
    </div>
    <div class="col-6 col-md-3">
      <select class="form-select" name="status">
        <option value=""{{if eq .Status ""}} selected{{end}}>全部状态</option>
        <option value="active"{{if eq .Status "active"}} selected{{end}}>active</option>
        <option value="evicted"{{if eq .Status "evicted"}} selected{{end}}>evicted</option>
        <option value="pending"{{if eq .Status "pending"}} selected{{end}}>pending</option>
      </select>
    </div>
    <div class="col-3 col-md-2">
      <select class="form-select" name="size">
        <option value="25"{{if or (eq .Size 0) (eq .Size 25)}} selected{{end}}>25/页</option>
        <option value="50"{{if eq .Size 50}} selected{{end}}>50/页</option>
        <option value="100"{{if eq .Size 100}} selected{{end}}>100/页</option>
      </select>
    </div>
    <div class="col-3 col-md-2 d-grid">
      <button class="btn btn-primary" type="submit">查询</button>
    </div>
  </form>
  <div class="table-responsive">
  <table class="table table-hover align-middle mb-0" id="nodes-table">
    <thead><tr><th>指纹</th><th>地址</th><th>协议</th><th>状态</th><th>延迟</th><th>最近见</th><th class="text-end">操作</th></tr></thead>
    <tbody id="nodes-body">
      {{range .Nodes}}
      <tr>
        <td class="wm-mono wm-muted">{{substr .Fingerprint 0 8}}</td>
        <td>{{.Addr}}:{{.Port}}</td>
        <td>{{.Protocol}}</td>
        <td>{{template "badge" (dict "Text" .Status "Variant" .StatusVar)}}</td>
        <td>{{.LatencyStr}}</td>
        <td class="wm-muted small">{{.LastSeenStr}}</td>
        <td class="text-end">{{template "nodeOps" .}}</td>
      </tr>
      {{else}}
      <tr><td colspan="7" class="wm-muted text-center py-4">无匹配节点</td></tr>
      {{end}}
    </tbody>
  </table>
  </div>
  <div class="d-flex justify-content-between align-items-center mt-3">
    <span class="wm-muted small">共 <span id="nodes-total">{{.Total}}</span> 个 · 第 <span id="nodes-page">{{if gt .Page 0}}{{.Page}}{{else}}1{{end}}</span> 页 <span class="badge text-bg-info">5s 轮询</span></span>
    <div class="btn-group btn-group-sm">
      <button class="btn btn-outline-secondary" id="pg-prev">上一页</button>
      <button class="btn btn-outline-secondary" id="pg-next">下一页</button>
    </div>
  </div>
</div>
<script>
// S2: 搜索/过滤/分页全部服务端(/api/nodes?q=&status=&page=&size=, 总数走 X-Total-Count);
// 5s 轮询当前查询条件, 只换 tbody 不闪页。摘除/回池 = 确认弹窗 + toast。
(function () {
  var BADGE = {active:'text-bg-success',pending:'text-bg-secondary',evicted:'text-bg-danger'};
  var page = {{if gt .Page 0}}{{.Page}}{{else}}1{{end}}, size = {{if gt .Size 0}}{{.Size}}{{else}}25{{end}}, total = {{.Total}};
  var q = "{{.Q}}", status = "{{.Status}}";
  var body = document.getElementById('nodes-body');
  function param(k, v) { return encodeURIComponent(k) + '=' + encodeURIComponent(v == null ? '' : v); }
  function queryUrl(p) {
    return '/api/nodes?' + param('q', q) + '&' + param('status', status) + '&' + param('page', p) + '&' + param('size', size);
  }
  function opForm(action, fp, label, danger) {
    var title = danger ? '确认摘除' : '确认恢复';
    var text = danger ? ' 将把节点从出口池移除并立即同步 xray 配置。确定?' : ' 将把节点放回出口池并立即同步 xray 配置。确定?';
    var flash = danger ? '已摘除并同步出口' : '已回池并同步出口';
    var f = document.createElement('form');
    f.method = 'POST'; f.action = action; f.className = 'd-inline';
    if (danger) f.setAttribute('data-wm-danger', '');
    f.setAttribute('data-wm-title', title);
    f.setAttribute('data-wm-ok', label);
    f.setAttribute('data-wm-flash', flash);
    f.onsubmit = function (ev) { return wmConfirmAct(ev, f, fp.slice(0, 8) + text); };
    var inp = document.createElement('input');
    inp.type = 'hidden'; inp.name = 'fp'; inp.value = fp; f.appendChild(inp);
    var btn = document.createElement('button');
    btn.type = 'submit'; btn.textContent = label;
    btn.className = 'btn btn-outline-' + (danger ? 'danger' : 'secondary') + ' btn-sm';
    f.appendChild(btn);
    return f;
  }
  function render(nodes) {
    body.innerHTML = '';
    if (!nodes.length) {
      body.innerHTML = '<tr><td colspan="7" class="wm-muted text-center py-4">无匹配节点</td></tr>';
      return;
    }
    nodes.forEach(function (n) {
      var tr = document.createElement('tr');
      tr.innerHTML = '<td class="wm-mono wm-muted"></td><td></td><td></td><td></td><td></td><td class="wm-muted small"></td><td class="text-end"></td>';
      tr.cells[0].textContent = (n.Fingerprint || '').slice(0, 8);
      tr.cells[1].textContent = n.Addr + ':' + n.Port;
      tr.cells[2].textContent = n.Protocol;
      var b = document.createElement('span');
      b.className = 'badge ' + (BADGE[n.Status] || 'text-bg-secondary');
      b.textContent = n.Status;
      tr.cells[3].appendChild(b);
      tr.cells[4].textContent = n.LatencyStr || '—';
      tr.cells[5].textContent = n.LastSeenStr || '—';
      if (n.Status === 'active') tr.cells[6].appendChild(opForm('/admin/nodes/evict', n.Fingerprint, '摘除', true));
      else if (n.Status === 'evicted') tr.cells[6].appendChild(opForm('/admin/nodes/recover', n.Fingerprint, '恢复', false));
      body.appendChild(tr);
    });
  }
  function load(p) {
    page = p;
    fetch(queryUrl(p)).then(function (r) {
      total = parseInt(r.headers.get('X-Total-Count') || '0', 10);
      return r.json();
    }).then(render).catch(function () {});
    document.getElementById('nodes-page').textContent = p;
    document.getElementById('nodes-total').textContent = total;
  }
  document.getElementById('pg-prev').onclick = function () { if (page > 1) load(page - 1); };
  document.getElementById('pg-next').onclick = function () { if (page * size < total) load(page + 1); };
  setInterval(function () { load(page); }, 5000);
})();
</script>
{{end}}

{{define "pageEvents"}}
<h1 class="wm-display fs-2 mb-4">事件时间线</h1>
<div class="card p-4">
  <ul class="list-group list-group-flush" id="ev-list">
    {{range .Events}}
    <li class="list-group-item d-flex flex-wrap gap-2 align-items-center px-0 py-3" data-ev-id="{{.ID}}">
      {{template "badge" (dict "Text" .Kind "Variant" .KindVar)}}
      <span class="wm-mono wm-muted">{{substr .NodeFP 0 8}}</span>
      {{if .From}}<span class="small">{{.From}} <span class="wm-muted">→</span> {{.To}}</span>{{end}}
      <span class="small flex-grow-1">{{.Detail}}</span>
      <span class="wm-muted small text-nowrap ms-auto">{{.AtStr}}</span>
    </li>
    {{else}}
    <li class="list-group-item px-0 wm-muted text-center py-4">暂无事件</li>
    {{end}}
  </ul>
  <div class="text-center mt-3">
    <button class="btn btn-outline-secondary btn-sm" id="ev-more"{{if not .HasMore}} style="display:none"{{end}}>加载更多</button>
  </div>
</div>
<script>
// S2: 时间游标分页(/api/events?before=&limit=), before=当前末行 id。
(function () {
  var btn = document.getElementById('ev-more');
  if (!btn) return;
  btn.addEventListener('click', function () {
    var items = document.querySelectorAll('#ev-list [data-ev-id]');
    var before = items.length ? items[items.length - 1].getAttribute('data-ev-id') : '';
    fetch('/api/events?before=' + before + '&limit=50').then(function (r) { return r.json(); }).then(function (evs) {
      if (!evs.length) { btn.style.display = 'none'; return; }
      var BADGE = {danger: 'text-bg-danger', success: 'text-bg-success', info: 'text-bg-info'};
      evs.forEach(function (e) {
        var li = document.createElement('li');
        li.className = 'list-group-item d-flex flex-wrap gap-2 align-items-center px-0 py-3';
        li.setAttribute('data-ev-id', e.ID);
        li.innerHTML = '<span class="badge"></span><span class="wm-mono wm-muted"></span>' +
          '<span class="small"></span><span class="small flex-grow-1"></span><span class="wm-muted small text-nowrap ms-auto"></span>';
        li.children[0].className = 'badge ' + (BADGE[e.KindVar] || 'text-bg-info');
        li.children[0].textContent = e.Kind;
        li.children[1].textContent = (e.NodeFP || '').slice(0, 8);
        li.children[2].textContent = e.From ? (e.From + ' → ' + e.To) : '';
        li.children[3].textContent = e.Detail || '';
        li.children[4].textContent = e.AtStr;
        document.getElementById('ev-list').appendChild(li);
      });
    });
  });
})();
</script>
{{end}}

{{define "pageUsers"}}
<h1 class="wm-display fs-2 mb-4">用户(只读)</h1>
{{if .ErrText}}<div class="alert alert-warning py-2 small">{{.ErrText}}</div>{{end}}
<div class="card p-4">
  <form class="row g-2 mb-3" method="GET" action="/admin/users">
    <div class="col-12 col-md-6"><input class="form-control" name="q" placeholder="搜索用户名 / ID" value="{{.Q}}"></div>
    <div class="col-6 col-md-2 d-grid"><button class="btn btn-primary" type="submit">查询</button></div>
  </form>
  {{if .Users}}
  <div class="table-responsive">
  <table class="table table-hover align-middle mb-0">
    <thead><tr><th>ID</th><th>用户名</th><th>状态</th><th>到期</th><th>流量</th></tr></thead>
    <tbody>
    {{range .Users}}
      <tr><td class="wm-mono wm-muted">{{.ID}}</td><td class="fw-semibold">{{.Username}}</td>
          <td>{{template "badge" (dict "Text" .Status "Variant" .StatusVar)}}</td>
          <td class="wm-muted small">{{.ExpireStr}}</td><td class="wm-muted small">{{.TrafficStr}}</td></tr>
    {{end}}
    </tbody>
  </table>
  </div>
  <div class="wm-muted small mt-2">共 {{len .Users}} 个用户{{if .Q}}(过滤: {{.Q}}){{end}}</div>
  {{else}}
  <div class="wm-muted text-center py-4">{{if .Configured}}无匹配用户{{else}}不可用{{end}}</div>
  {{end}}
</div>
{{end}}
`
