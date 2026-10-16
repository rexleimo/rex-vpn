// wm-ui.js — 双端共享交互胶水层 (ADR-0003: vanilla JS + Bootstrap, 零构建)。
// 提供三件套: wmToast(反馈) / wmConfirm(破坏性操作确认) / wmAction(POST→JSON→toast→回调)。
// 依赖: bootstrap.bundle.min.js 的 Modal/Toast; 页面需有 <div id="wm-toasts"> 与 #wm-confirm 模态骨架
// (由 webui 模板统一注入)。JSON 协商: X-WM-Fetch: json 头, 服务端 webkit.WantsJSON 识别。
(function () {
  'use strict';

  function wmToast(msg, variant) {
    var host = document.getElementById('wm-toasts');
    if (!host) { alert(msg); return; }
    var el = document.createElement('div');
    el.className = 'toast align-items-center border-0 mb-2 wm-toast-' + (variant || 'info');
    el.setAttribute('role', 'alert');
    el.innerHTML =
      '<div class="d-flex">' +
      '<div class="toast-body"></div>' +
      '<button type="button" class="btn-close btn-close-white me-2 m-auto" data-bs-dismiss="toast"></button>' +
      '</div>';
    el.querySelector('.toast-body').textContent = msg;
    host.appendChild(el);
    var t = new bootstrap.Toast(el, { delay: 3500 });
    t.show();
    el.addEventListener('hidden.bs.toast', function () { el.remove(); });
  }

  // wmConfirm({title, body, danger, confirmText}, onConfirm) — Bootstrap modal, 确认后才回调。
  function wmConfirm(opts, onConfirm) {
    var el = document.getElementById('wm-confirm');
    if (!el) { if (confirm(opts.body)) onConfirm(); return; }
    el.querySelector('.modal-title').textContent = opts.title || '确认操作';
    el.querySelector('.modal-body').textContent = opts.body || '';
    var btn = el.querySelector('[data-wm-confirm]');
    btn.textContent = opts.confirmText || '确认';
    btn.className = 'btn ' + (opts.danger ? 'btn-danger' : 'btn-primary');
    var modal = bootstrap.Modal.getOrCreateInstance(el);
    btn.onclick = function () { modal.hide(); onConfirm(); };
    modal.show();
  }

  // wmAction(url, fields, cb) — 表单字段 POST, JSON 协商; cb({ok, error, ...data})。
  function wmAction(url, fields, cb) {
    var body = new URLSearchParams();
    Object.keys(fields || {}).forEach(function (k) { body.set(k, fields[k]); });
    fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'X-WM-Fetch': 'json' },
      body: body.toString(),
    }).then(function (r) { return r.json().then(function (d) { d.status = r.status; return d; }); })
      .then(function (d) { cb && cb(d); })
      .catch(function (e) { cb && cb({ ok: false, error: '网络错误: ' + e.message }); });
  }

  window.wmToast = wmToast;
  window.wmConfirm = wmConfirm;
  window.wmAction = wmAction;

  // 跨刷新反馈: SSR 重载会吃掉当前 toast, 成功消息先存 sessionStorage, 新页面加载后补弹。
  function wmFlash(msg) {
    try { sessionStorage.setItem('wm-flash', msg); } catch (e) {}
  }
  function wmDrainFlash() {
    try {
      var m = sessionStorage.getItem('wm-flash');
      if (m) { sessionStorage.removeItem('wm-flash'); wmToast(m, 'success'); }
    } catch (e) {}
  }
  // wmAct(url, fields, okMsg) — 标准 CRUD 动作: 成功 flash+重载, 失败就地 toast(不丢表单现场)。
  function wmAct(url, fields, okMsg) {
    wmAction(url, fields, function (d) {
      if (d.ok) { wmFlash(okMsg || '操作成功'); location.reload(); }
      else { wmToast(d.error || '操作失败', 'danger'); }
    });
  }
  window.wmFlash = wmFlash;
  window.wmAct = wmAct;

  function wmFields(form) {
    var f = {};
    new FormData(form).forEach(function (v, k) { f[k] = v; });
    return f;
  }
  // wmSubmitAct: 表单提交走 JSON 动作(无 JS 时原生 POST 兜底, 渐进增强)。
  function wmSubmitAct(ev, form, okMsg) {
    if (ev) ev.preventDefault();
    wmAct(form.action, wmFields(form), okMsg);
    return false;
  }
  // wmConfirmAct: 破坏性操作 = 确认弹窗 + JSON 动作; data-wm-danger/title/ok 控制弹窗。
  function wmConfirmAct(ev, form, body) {
    if (ev) ev.preventDefault();
    wmConfirm({
      title: form.getAttribute('data-wm-title') || '确认操作',
      body: body,
      danger: form.hasAttribute('data-wm-danger'),
      confirmText: form.getAttribute('data-wm-ok') || '确认',
    }, function () { wmAct(form.action, wmFields(form), form.getAttribute('data-wm-flash') || '操作成功'); });
    return false;
  }
  window.wmSubmitAct = wmSubmitAct;
  window.wmConfirmAct = wmConfirmAct;
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', wmDrainFlash);
  } else { wmDrainFlash(); }
})();
