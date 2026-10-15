package web

// adminLayout 是 ingest 管理页的 layout(导航/壳/按 Content 分发)。与 user-center 的 user layout 相对。
const adminLayout = `
{{define "layout"}}
<!doctype html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · vpn-master</title>
<style>{{.CSS}}</style>
</head>
<body>
<nav class="wm-nav">
  <a class="wm-brand" href="/admin">vpn-master</a>
  {{if eq .Active "overview"}}<a class="wm-nav-item wm-active" href="/admin">总览</a>{{else}}<a class="wm-nav-item" href="/admin">总览</a>{{end}}
  {{if eq .Active "upstreams"}}<a class="wm-nav-item wm-active" href="/admin/upstreams">上游</a>{{else}}<a class="wm-nav-item" href="/admin/upstreams">上游</a>{{end}}
  {{if eq .Active "nodes"}}<a class="wm-nav-item wm-active" href="/admin/nodes">节点</a>{{else}}<a class="wm-nav-item" href="/admin/nodes">节点</a>{{end}}
  {{if eq .Active "events"}}<a class="wm-nav-item wm-active" href="/admin/events">事件</a>{{else}}<a class="wm-nav-item" href="/admin/events">事件</a>{{end}}
  {{if eq .Active "users"}}<a class="wm-nav-item wm-active" href="/admin/users">用户</a>{{else}}<a class="wm-nav-item" href="/admin/users">用户</a>{{end}}
  <span class="wm-nav-right">
    {{if .Degraded}}<span class="wm-badge wm-badge-danger">DEGRADED</span>{{end}}
    <a class="wm-link" href="/logout">退出</a>
  </span>
</nav>
<main class="wm-main">
{{if eq .Content "login"}}{{template "login" .PageData}}{{end}}
{{if eq .Content "pageOverview"}}{{template "pageOverview" .PageData}}{{end}}
{{if eq .Content "pageUpstreams"}}{{template "pageUpstreams" .PageData}}{{end}}
{{if eq .Content "pageNodes"}}{{template "pageNodes" .PageData}}{{end}}
{{if eq .Content "pageEvents"}}{{template "pageEvents" .PageData}}{{end}}
{{if eq .Content "pageUsers"}}{{template "pageUsers" .PageData}}{{end}}
</main>
</body>
</html>
{{end}}
`

// pageTemplates 是各管理页的 html/template define 块(与 pkg/webui base.html 同 set)。
// 每个页面命名块 dot = 对应 PageData 结构。表格区 id 供 7.3 轮询刷新。

const pageTemplates = `
{{define "login"}}
<div class="wm-card" style="max-width:360px;margin:40px auto">
  <h1 class="wm-h1">管理员登录</h1>
  {{if .Err}}<div class="wm-alert wm-alert-err">口令错误</div>{{end}}
  <form method="POST" action="/login" class="wm-form">
    <input class="wm-input" type="password" name="password" placeholder="口令" autofocus>
    <button class="wm-btn wm-btn-primary" type="submit">登录</button>
  </form>
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
<h1 class="wm-h1">上游</h1>
{{with .Err}}<div class="wm-alert wm-alert-err">{{.}}</div>{{end}}
<section class="wm-card">
  <h2 class="wm-card-title">来源清单</h2>
  {{if .Upstreams}}
  <table class="wm-table">
    <tr><th>名称</th><th>来源(掩码)</th><th>节点数</th><th>最近成功</th><th>最近错误</th><th>操作</th></tr>
    {{range .Upstreams}}
    <tr>
      <td>{{.Name}}</td><td class="wm-mono wm-dim">{{.URLMasked}}</td><td>{{.NodeCount}}</td><td>{{.LastOKStr}}</td><td class="wm-dim">{{.LastErr}}</td>
      <td>
        <form method="POST" action="/admin/upstreams/delete" style="display:inline">
          <input type="hidden" name="name" value="{{.Name}}"><button class="wm-btn wm-btn-danger wm-btn-sm" type="submit">删除</button>
        </form>
      </td>
    </tr>
    {{end}}
  </table>
  {{else}}<div class="wm-dim">暂无上游, 用下方表单添加</div>{{end}}
</section>
<section class="wm-card">
  <h2 class="wm-card-title">添加来源</h2>
  <form method="POST" action="/admin/upstreams" class="wm-form">
    <input class="wm-input" name="name" placeholder="名称(如 vpn4zx)" required>
    <input class="wm-input" name="url" placeholder="订阅 URL(含 token)" required>
    <button class="wm-btn wm-btn-primary" type="submit">添加</button>
  </form>
  {{if .CanFetch}}
  <form method="POST" action="/admin/upstreams/fetch" class="wm-form wm-mt">
    <button class="wm-btn" type="submit">立即拉取全部</button>
  </form>
  {{end}}
</section>
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
