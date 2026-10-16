# CONTEXT — 领域词汇表

> 本文件随代码版本化。记录已确立的领域术语（含义/边界/别称），供未来会话免召回使用。
> 只有代码/文档中已确认的事实或用户拍板的决策才写入；未决项记为假设，不落此表。

## 界面 (UI Surfaces)

| 术语 | 含义 | 为什么 |
|---|---|---|
| **用户门户 (user-center)** | 终端用户自助界面：登录(token) / 概览(流量·到期) / 订阅(链接·QR·导入指引) / 购买(下单·订单历史)。Go SSR，`services/user-center`，端口 9200 | 用户口中的"客户端界面"最可能指它——面向付费终端用户的门面 |
| **运营后台 (admin)** | 运营者界面：上游池 / 节点状态 / 事件 / Marzban。Go SSR，`services/ingest` `/admin/*`，端口 9100 | 内部工具，非终端用户可见；是否纳入"美化"范围是独立决策 |
| **共享样式层 (webui)** | 设计 token(`--wm-*` CSS 变量) + 组件模板(badge/spinner) + layout，两个服务共用 `services/webui/app.css` | wc-1.2 重构确立：改一处样式两服务同步，UI 重设计的天然落点 |

## UI 约束 (Established by code)

| 术语 | 含义 | 为什么 |
|---|---|---|
| **零构建 SSR** | 页面 = Go html/template + 内联 `<style>`，无 Node/打包管线；动态数据靠原生 JS 轮询 `/api/*` JSON | Dockerfile 仅 Go；任何 UI 方案若引入 React/Tailwind-build 即破坏部署同构 |
| **Web 层 = gin + 共享中间件套件** | 手撸 net/http/session/限流已废弃，迁移到 gin 并把会话/鉴权/限流/recovery 抽成仓库共享套件（模式同 webui） | 用户拍板，见 `docs/adr/0001-gin-web-layer.md`；零构建约束不变 |
| **视觉地基 = Bootstrap 5 (vendored) + Apple 风定制** | 官方编译产物拷入仓库(禁 CDN)提供 reset/栅格/组件; Apple 风=大留白/圆角卡片/系统字体/克制配色; `--wm-*` 收敛为变量映射层后退役 | 用户拍板 A, 见 `docs/adr/0002-bootstrap5-apple-style-ui.md` |
| **门户先行** | 美化范围第一切片 = user-center 4 页; admin 同套视觉后续独立跟进 | Q1 未显式回答的默认假设, 随验收可见可推翻 |
