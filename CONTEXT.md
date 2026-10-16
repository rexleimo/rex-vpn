# CONTEXT — 领域词汇表

> 本文件随代码版本化。记录已确立的领域术语（含义/边界/别称），供未来会话免召回使用。
> 只有代码/文档中已确认的事实或用户拍板的决策才写入；未决项记为假设，不落此表。

## 界面 (UI Surfaces)

| 术语 | 含义 | 为什么 |
|---|---|---|
| **用户门户 (user-center)** | 终端用户自助界面：登录(token) / 概览(流量·到期) / 订阅(链接·QR·导入指引) / 购买(下单·订单历史)。Go SSR，`services/user-center`，端口 9200 | 用户口中的"客户端界面"最可能指它——面向付费终端用户的门面 |
| **运营后台 (admin)** | 运营者界面：上游池 / 节点状态 / 事件 / Marzban。Go SSR，`services/ingest` `/admin/*`，端口 9100 | 已确认纳入重设计范围（双端同修, 2026-10-16）|
| **共享样式层 (webui)** | Bootstrap 5(vendored) + `theme.css` Apple 风主题层(`--wm-*` 变量) + 组件模板(badge) + 胶水 `wm-ui.js`，两个服务共用 `services/webui` | wc-1.2 重构确立；S4 已退役旧 `app.css` token 层，全站统一 Bootstrap+theme |

## UI 约束 (Established by code)

| 术语 | 含义 | 为什么 |
|---|---|---|
| **零构建 SSR** | 页面 = Go html/template + 内联 `<style>`，无 Node/打包管线；动态数据靠原生 JS 轮询 `/api/*` JSON | Dockerfile 仅 Go；任何 UI 方案若引入 React/Tailwind-build 即破坏部署同构 |
| **Web 层 = gin + 共享中间件套件** | 手撸 net/http/session/限流已废弃，迁移到 gin 并把会话/鉴权/限流/recovery 抽成仓库共享套件（模式同 webui） | 用户拍板，见 `docs/adr/0001-gin-web-layer.md`；零构建约束不变 |
| **视觉地基 = Bootstrap 5 (vendored) + Apple 风定制** | 官方编译产物拷入仓库(禁 CDN)提供 reset/栅格/组件; Apple 风=大留白/圆角卡片/系统字体/克制配色; `--wm-*` 收敛为变量映射层后退役 | 用户拍板 A, 见 `docs/adr/0002-bootstrap5-apple-style-ui.md` |
| **门户先行(已完成)** | 第一切片 = user-center 4 页视觉重设计, 已交付(commit c90beb0) | 假设已被用户确认并推进到下一阶段 |
| **双端同修** | 本轮范围 = 用户门户交互缺陷 + admin 5 页 CRUD 交互与视觉, 一起修复 | 用户 2026-10-16 显式拍板("客户端界面包括 admin 管理端的一起修复吧"), 推翻"admin 是否纳入"的悬置 |
| **交互载体 = vanilla JS + Bootstrap** | 确认弹窗用 Bootstrap modal、反馈用 Bootstrap toast、局部刷新手写 fetch 胶水; 不引入 htmx/Alpine 等声明式库 | 用户 Q2 拍板 A(2026-10-16), 见 docs/adr/0003 |
| **CRUD 交互套件** | 后台操作的标准交互契约: 破坏性操作确认弹窗 / 操作结果 toast(含失败原因) / 行内编辑(不靠删后重加) / 表格搜索+过滤+分页 / 表单在操作动线上 | 用户判定现状"CRUD 的页面不是这样的"; 门户与 admin 共用同一套反馈语言(toast/modal) |
| **完整后端化** | 搜索/过滤/分页在服务端做: `/api/nodes?q=&status=&page=&size=`, `/api/events?before=&limit=`, `POST /admin/upstreams/update`; 新参数全可选, 无参=全量语义不变 | 用户 Q3 拍板 B(为数千节点规模做准备), 见 `docs/adr/0004-admin-server-side-crud-api.md` |
