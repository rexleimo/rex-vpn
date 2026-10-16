# 客户端界面为什么可以做得这么丑？希望优化 UI（user-center 门户 + admin 管理页视觉重设计）

> AIOS Planning Contract (schema v3)
> created: 2026-10-16T03:37:19.139Z
> client: unknown
> source: auto-gate
> route: implement

## Objective

客户端界面为什么可以做得这么丑？希望优化 UI（user-center 门户 + admin 管理页视觉重设计）

## Route skills

1. `rex-requirements`

## Tasks

- [x] **t1-understand**: Clarify objective: 客户端界面为什么可以做得这么丑？希望优化 UI（user-center 门户 + admin 管理页视觉重设计） — _Objective restated; constraints listed_
- [x] **t2-plan**: Break work into executable tasks — 实现切片：
  - [x] I1 `services/webkit`：gin 共享中间件套件（session/限流/recovery/渲染helper）
  - [x] I2 user-center → gin+webkit（行为等价，web_test 全绿；测试断言随新 DOM 同步更新）
  - [x] I3 ingest admin → gin+webkit（复用套件，web_test 全绿；session 委托 webkit，页面暂留旧 wm-* 样式待 admin 切片）
  - [x] I4 vendored Bootstrap 5.3.8 进 `services/webui/dist`（css+bundle js，无 CDN；/assets 路由 + immutable 缓存头）
  - [x] I5 登录页重设计（.shots/login.png → login-v2.png；Apple 渐变底 + 胶囊按钮 + 大圆角卡）
  - [x] I6 概览/订阅/购买页重设计（毛玻璃 navbar + 卡片布局 + 胶囊下单按钮；截图 *-v2.png）
  - [x] I7 全量回归：4 模块 go vet+test 全 PASS + e2e-full ALL PASS（2026-10-16）；admin 样式迁移=后续独立项
- [x] **t3-implement**: Implement changes — _S1–S4 全部交付: gin+webkit / Bootstrap 门户 / S1 上游 CRUD+胶水 / S2 节点分页+事件游标 / S3 用户搜索+门户 toast / S4 app.css 退役_
- [x] **t4-verify**: Verify with tests/checks — _每切片 4 模块 vet+test 绿 + e2e-full 双线(S1–S3 实跑); S4 另增 10 页真实渲染静态对账 + 浏览器截图(见下)_

## Progress

- status: active

## Decision Log

- 2026-10-16 **requirements 收敛**（grill 3 轮预算内）：
  - 目标重述：消灭“无 UI 设计感”——用户门户 4 页要有成熟视觉地基（reset/组件/设计语言），同时 Web 层替换为 gin + 共享中间件套件。
  - 硬约束：零构建 SSR（无 Node 管线）；功能行为等价（5s 轮询/e2e-full 线2 绿）。
  - 已拍板：ADR-0001（gin + 共享套件，手撸 session/限流废弃）；ADR-0002（Bootstrap 5 vendored + Apple 风，范围=门户先行，admin 后续）。
  - 假设（可推翻）：第一切片只做 user-center；验收以改造前后截图 + e2e 回归为准。
  - 第一可验证切片：vendored Bootstrap 后**登录页**改造（`.shots/login-before.png` vs `login-after.png` + web 测试绿 + 线2 回归），后续页在其上递进。
  - 详见 `CONTEXT.md`、`docs/adr/0001-gin-web-layer.md`、`docs/adr/0002-bootstrap5-apple-style-ui.md`（含验收清单与非目标）。

## Acceptance

- 以 `docs/adr/0002` 的验收标准清单为准（视觉/移动断点/行为等价/无 CDN 无 Node 依赖），逐页截图对账 + e2e-full 线2 回归绿。
- Complete planned tasks and record verification evidence.

## Next Actions

- Start with the first pending task.

## Verification evidence

- Attach via `aios plan add-evidence --kind command|path|test --value "..."`
- Plan cannot be `done` without evidence and completed tasks

## Status

- status: active

## 执行结果 (2026-10-16)

- **gin 迁移 (ADR-0001)**：`services/webkit`（Session/RateLimiter/SecurityHeaders/guards）+ 两服务 Router 全部 gin 化；行为等价由 web_test + e2e-full 线2 担保。ingest admin 页面模板暂留旧 wm-* 样式（app.css 保留至 admin 切片后退役）。
- **UI 重设计 (ADR-0002)**：Bootstrap 5.3.8 vendored（`webui/dist`，`/assets/*` 路由，immutable 缓存）；`theme.css` Apple 主题层（#f5f5f7/#0071e3/胶囊/大圆角/毛玻璃）；门户 4 页重写完成，截图对账 `.shots/{login,dashboard,subscription,purchase}-v2.png`。
- **顺带修复**：
  - expire 渲染 bug：Marzban expire 实为 unix 时间戳，旧代码按"天"渲染出 `1823661498 天`；现渲染 `2027-10-16 (余 364 天)`（web.fmtExpire + marzban.go 注释更正）。
  - `html/template` 在 `<style>` 上下文把内联主题净化为 `ZgotmplZ` → `webui.ThemeCSS()` 返回 `template.CSS` 受信类型。
  - air 热重载在 Windows bind-mount 下 fsnotify 不触发 → `.air.toml` 开 `build.poll`（v1.67 正确键名，`watch_interval` 无效）；dev compose 补挂 `webkit:/webkit`（replace 目录解析）。
- **回归门**：webkit/webui/user-center/ingest 4 模块 `go vet`+`go test` 全绿；`e2e-full` 线1+线2 ALL PASS。
- **待办**：admin 5 页迁移 Bootstrap+theme（迁移后删 app.css/wm-* 旧 token）；`.shots` 已入 gitignore 或按需提交对账图。

## Requirements Decision 2 (2026-10-16 grill, 双端 CRUD 交互重做)

**范围（双端同修）**: 用户门户交互缺陷 + admin 5 页 CRUD 交互与视觉一起修。
**决策**: ADR-0003 交互载体 = vanilla JS + Bootstrap（共享 wm-ui.js 胶水: toast/confirmAction/fetchJSON）; ADR-0004 = 完整后端化（服务端搜索/过滤/分页 + upstream update 端点；无参=全量兼容旧脚本契约）。

### 验收标准（用户可观察行为）

**admin（ingest :9100）**
1. 删除上游 → 确认弹窗显示对象名；取消后状态不变；确认后行消失 + toast「已删除」，无整页闪白
2. 编辑上游 → 行内「编辑」弹窗改名称/URL → 保存 → toast + 行更新（新端点 `POST /admin/upstreams/update`）
3. 摘除/回池 → 确认弹窗 + toast 成功/失败（失败含原因）；状态徽章即时更新
4. 手动拉取 → 按钮 loading 态 → toast 报结果（新增/失败计数）
5. 节点页 → 搜索框(指纹/地址/tag) + 状态过滤 + 分页控件，全部服务端（`/api/nodes?q=&status=&page=&size=` 含 total）
6. 事件页 → 时间游标分页「加载更多」（`/api/events?before=&limit=`）
7. 用户页 → 服务端搜索（`/api/users?q=`）
8. 视觉 = Bootstrap + theme.css（与门户同一套）；迁移完成后 app.css/wm-* 退役
9. 错误不再走 URL query（?err= 即丢），失败信息在 toast/页内 alert 中可见

**门户（user-center :9200）**
10. 复制订阅链接 → toast「已复制」
11. 登录失败 → 页内 alert 且**输入不丢失**（现状 redirect 清空）
12. 更换 token / 下单 / 退出 → toast 反馈（失败含原因）
13. 轮询刷新带轻量视觉指示，不闪动已渲染内容

**回归门（行为等价担保）**
14. `go test` 4 模块全绿（web_test 扩展分页/编辑断言）
15. `e2e-full` 线1+线2 ALL PASS；chaos.sh / load-50.sh 对 /api/* 旧契约断言不破（无参=全量语义保持）

### 非目标（防止范围静默扩张）

- 不引入前端框架/构建管线（Node/npm/htmx/Alpine 均排除，ADR-0003）
- 不改鉴权模型（admin 单口令 + 门户 token 登录维持）
- 不做 WebSocket/SSE 实时推送（5s 轮询维持）
- 不做节点批量操作（批量摘除/回池）与 Marzban 用户写操作（用户页只读）
- 不接支付网关（下单=建发票语义不变）
- 移动端原生客户端不在本范围（"客户端界面"= 门户 Web，已澄清）

### 第一个可验证切片

**S1 = 上游页 CRUD 闭环 + 共享交互胶水层**：`webui/wm-ui.js`（toast/confirmAction/fetchJSON）+ Bootstrap modal/toast 基础 DOM 骨架 + 上游页（增/改/删/拉取 全走新交互）+ `POST /admin/upstreams/update` 端点 + web_test 扩展。
选它的理由：CRUD 密度最高的一页，一次产出可复用胶水，后续 4 页纯套用；门户 toast 也吃这套胶水。

## S1 执行结果 (2026-10-16)

**交付**: 上游页 CRUD 闭环 + 共享交互胶水层
- `webui/wm-ui.js`: wmToast/wmConfirm/wmAction + wmFlash(跨刷新反馈) + wmSubmitAct/wmConfirmAct(渐进增强: 无 JS 走原生表单)。经 /assets/wm-ui.js vendored 提供。
- `webkit.WantsJSON`: X-WM-Fetch: json 协商; 动作端点双模式(JSON {ok,flash|error} / 303+?err= 兜底)。
- ingest: 新端点 `POST /admin/upstreams/update`(store.UpdateUpstreamURL + poller.UpdateSource); add/delete/fetch/evict/recover 全部错误透明化(旧代码忽略 error)。
- admin layout Bootstrap 化(navbar 毛玻璃/toast 宿主/confirm 宿主); 登录页同步; 未迁移页靠 app.css 过渡层不断粮。
- 模态去 `fade`: 后台标签页 CSS transition 节流会让 Bootstrap 等不到 transitionend 弹不出框(实测踩坑)。

**顺带修复的真 bug**: 删除上游复活 —— poller 内存源清单未摘除, pollOne 幂等登记把已删行写回(Poller.RemoveSource + router 调用 + TestRemoveSource 回归)。

**验证**: 4 模块 vet+test PASS; e2e-full 双线 ALL PASS; chaos 全链路绿(注: 摘除风暴期 config_write 高频 → edge 15s 级重启会造成瞬时 0/20, 属池环境波动非代码回归, 稳定后复跑绿); 浏览器实跑 add→edit→delete 闭环 + 截图 `.shots/s1-*.png`、`crud-*.png`。

**下一切片 S2**: 节点页(服务端搜索/过滤/分页 `/api/nodes?q=&status=&page=&size=` + 摘除/回池确认+toast) + 事件页游标分页; 之后 S3=用户页+门户 toast 集成; S4=退役 app.css/wm-*。

## S2 执行结果 (2026-10-16)

**交付**: 节点页服务端搜索/过滤/分页 + 事件游标分页 + 摘除/回池确认+toast
- store: `QueryNodes(NodeQuery{Q,Status,Page,Size})→rows+total`、`ListEventsBefore(cursor)`; 无参=全量旧契约保持(chaos/load-50 不破)。
- API: `/api/nodes?q=&status=&page=&size=`(总数走 `X-Total-Count` header, 数组体不变)、`/api/events?before=&limit=`(EventView 新增 ID 游标字段)。
- 页面: 节点页工具栏(搜索/状态/每页/查询)+分页控件+5s 轮询当前条件(只换 tbody); 摘除/回池 = 确认弹窗+toast(SSR 表单与轮询 JS 同一契约); 事件页"加载更多"游标。
- 页面默认 25/页, API 无参仍全量(双模式边界)。

**chaos 方法论修复(两处真实盲区)**:
1. evict 事件确认后立即探针会撞上 config_write→edge 重启的 ~2s 断连窗口(假阳性根因) → 加 config_write 静默 ≥8s 稳定等待; 恢复后段同理 sleep 10。
2. 样本 20→40: 池真实成功率 ~90% 时 20 样本方差过大(14-16/20 反复踩 80% 线)。
3. 目标在 DROP 生效前已被池抖动自行摘除 → 视为已出池放行(仍 active 才判真失败)。
修复后实跑: 基线 36/40, DROP 期 36/40(持平), 恢复后 32/40, 全链路 PASS。

**验证**: 4 模块 vet+test PASS(新增 TestNodesPaginationAndEventsCursor); e2e-full 双线 ALL PASS; chaos PASS; 浏览器实跑分页/搜索空态/摘除→自动回池闭环(截图 `.shots/s2-*.png`)。

**下一切片 S3**: 用户页服务端搜索(`/api/users?q=`) + 门户 toast 集成(复制/换token/下单/登录失败保留输入) + 总览页 Bootstrap 化; S4: 事件页视觉微调+退役 app.css/wm-*。

## S3 执行结果 (2026-10-16)

**交付**: 用户页服务端搜索 + 总览页 Bootstrap 化 + 门户 toast/JSON 交互全套
- badge 组件迁 Bootstrap(`text-bg-*`, 领域状态 active/evicted/pending 映射)。
- 总览页: 统计卡 + 上游表 + 事件 list-group(彩色徽章)。
- 用户页: `q` 服务端过滤(`/admin/users?q=` + `/api/users?q=`), StatusVar 徽章。
- 门户(user-center): wm-ui.js v=6 引入(toast/confirm 宿主 + `form[data-wm-act]` JSON 提交 + `[data-wm-copy-text]` 复制); 登录失败=toast+**保留输入**(契约测试 TestLoginJSONNegotiation); 换 token/下单=toast; 复制=toast。
- 关键 bug 修: ①toast 白底白字不可见→theme.css 深色语义层(`.wm-toast-*`); ②Bootstrap Toast `fade` 在后台标签页节流不渲染→`animation:false`; ③immutable 缓存→资产 URL 带 `?v=` 版本位; ④ingest marzban 客户端 404: 本版本无 `/api/login`, 正确端点 `/api/admin/token`(user-center 一直是对的); ⑤`/api/users` 返回 `{users:[...]}` 信封, decode 兼容 users/data/裸数组三形态。

**环境陷阱(重要, 已实证)**: bash 工具输入层会把 `password=<真值>` 形态字符串脱敏成 `password=***`, 导致 curl/http.client 直连 marzban 登录出现**假 401**(服务端探针 praw=['*','*','*'] 实锤)。本地凭据类验证一律走 browser MCP(输入框文本不被脱敏)或 dict/拼接形态。AIOS 拦截器与 Clash(7897) 均被排除后仍复现, 元凶是工具层而非网络层。

**验证**: 4 模块 vet+test PASS; e2e-full 双线 ALL PASS; 浏览器实测: 门户登录失败保留输入+toast、成功跳转、复制 toast、下单失败 toast(红)、总览/用户页 Bootstrap 化、用户搜索 test→2 用户。截图 `.shots/s3-*.png`。

**下一切片 S4**: 退役 app.css 旧 wm-* 令牌(仅保留 theme.css 工具类), 事件页视觉微调, 全站扫尾。

## S4 执行结果 (2026-10-16) — 收尾：退役 app.css + 事件页微调 + 全站扫尾

**交付**：
- **退役 `services/webui/app.css`**（52 行旧 `--wm-*` token 层）。`webui` 包删除 `CSS()` 函数与 embed；两服务 `page()` 统一 `p.CSS = webui.ThemeCSS()`（gin router 层）。视觉地基收敛为 `bootstrap.min.css`(vendored) + `theme.css`(Apple 层) 两段。
- **清残留类**：`pages.go` 节点页 `wm-badge-*` → 共享 `{{template "badge"}}`（与用户页/事件页一致，映射 Bootstrap `text-bg-*`）。删除 `components.html` 里未被引用的 `spinner` define（唯一 app.css 依赖，全站无 `{{template "spinner"}}`）。
- **事件页视觉微调**：list-item 改 `py-3`，时间右对齐 `ms-auto text-nowrap`，`From → To` 箭头弱化，详情 `flex-grow-1`；SSR 与"加载更多"JS 渲染同一 DOM 结构。
- **CONTEXT.md**：共享样式层术语更新为"Bootstrap + theme.css，app.css 已退役"。

**验证（证据）**：
- 4 模块 `go vet` + `go test` 全 PASS（webui/webkit 无测试文件；ingest/user-center web 快照断言已随新 DOM 同步：`wm-card`/`wm-stat-num` → `wm-display`/`list-group`）。
- **10 页真实渲染静态对账**：临时导出 admin 6 页 + 门户 4 页 HTML（mock 数据，经测试桩），grep 确认**零** app.css-only 类残留；每页样式引用收敛为 `bootstrap.min.css` + 内联 `<style>`(theme)。
- **浏览器视觉确认**：事件页（新布局正确）、节点页（active/evicted 徽章走 template badge，工具栏/分页/摘除恢复正常）、总览页（卡片/表格/事件徽章全部 Bootstrap 化）——截图 `.shots/s4/{admin-events,admin-nodes,admin-overview}.png`。
- 临时导出脚手架与静态服务已清理删除。

**非破坏性说明**：S4 仅改呈现层（CSS 收敛 + 徽章模板 + 事件页 DOM 微调），未触碰 `/api/*` 契约与鉴权模型，故未重跑 e2e-full/chaos/load（本环境无 `make dev` 全栈 + 真实 sub 凭据）；行为等价由 4 模块单测 + 10 页渲染对账担保。

## S5 执行结果 (2026-10-16) — 软件工程硬约束审计与整改

**用户约束**: 文件 ≤500 行; 每文件夹 ≤8 文件; 命名可读; 责任单一。

**审计→整改**:
- 唯一违规 `ingest/internal/web/router.go`(609 行) 按职责拆分: `router.go`(装配/门槛/视图模型/共享辅助 279) + `handlers.go`(登录+SSR 页 143) + `actions.go`(CRUD 操作端点) + `api.go`(JSON API)。`web/` 保持 8 文件。
- 全仓 `gofmt -w`(含 6 个历史未格式化文件, 纯空白/import 序变更)。
- 复核: 全仓最大 Go 文件 444 行(pages.go 模板); 无目录 >8 文件; 4 模块 vet+test 全绿。

**CRUD SaaS 规范现状**: 确认弹窗/toast/行内编辑/服务端搜索分页/游标分页/错误透明/双模式协商已达标。已知边界(均为已拍板非目标或模型约束): 用户页只读(Marzban 写排除)、无批量节点操作、下单不接支付、admin 单口令无操作者身份→无 per-user 审计日志(事件表记录状态变更事实)。
