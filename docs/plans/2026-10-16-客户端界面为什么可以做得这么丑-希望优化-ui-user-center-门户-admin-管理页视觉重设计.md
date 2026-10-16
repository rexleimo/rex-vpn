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
- [ ] **t3-implement**: Implement changes — _Code changes match objective_
- [ ] **t4-verify**: Verify with tests/checks — _Evidence recorded (command or artifact path)_

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
