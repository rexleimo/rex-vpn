# user-center 独立计划与技术栈选定（rex-planning）

> 对象：终端用户自助 Web 平台（订阅/流量/购买/设备入口）。
> 设计文档：docs/user-center.md。本文件 = work-10 的子任务级拆解 + 决策。
> **原则（用户已确认）：不做前后分离——Go 服务端渲染多页，单二进制，无 Node 构建链。**
> 每个子任务独立 commit；回退点 = revert 对应 commit。

## 0. 技术栈选定（Decision Ticket）

| 项 | 决策 | 依据 |
|---|---|---|
| 渲染 | **Go SSR：htmlx（类型安全+组件语义）+ go:embed**，单二进制 `services/user-center`，组件与 ingest admin 页共享 `pkg/webui` | 不做前后分离；4 页 SSR，组件化管理（组件 = Go 函数返回 `htmlx.S`），无 Node/codegen |
| 交互 | vanilla JS 仅做轮询（5s 拉 JSON 端点刷新表格/流量区） | 无构建链，无框架版本债 |
| UI | 手写轻量 CSS（Mantine 风格变量直接写死）+ 移动端适配 | 不引组件库构建链 |
| 二维码 | `github.com/skip2/go-qrcode`（服务端出图） | 免前端 qrcode 依赖 |
| 数据层 | **不建用户数据后端**：服务端代理 Marzban API（用户流量/订阅/发票） | Marzban 是唯一事实源，双写=坑 |
| 鉴权 | **token 登录 → HttpOnly cookie 会话**（服务端校验 `GET /api/users?token=` 后种 cookie） | 绕开注册体系；XSS 读不到 token |
| 支付 | **MVP 不接网关**：建 invoice + "联系渠道加量"提示；网关 Phase 2 | 先通链路 |
| 部署 | 单 Go 容器（Dokploy 反代 `user.<domain>` → 该容器），无 nginx 静态层 | 比 React 方案少一个容器少一层 |
| 测试 | httptest 页面断言 + 1 条真实 token e2e（Playwright 可后置） | 页面=接口，契约可测 |

## 1. Work Items（子任务级）

> 外部前置依赖：work-6.1/6.2/6.5（Marzban 跑通 + 测试用户 + `/api/users?token=` 冒烟定 API 契约）

### wc-1 工程脚手架（dependsOn: 无，mock Marzban 可先行）
| 子任务 | 产物 | 验收 |
|---|---|---|
| wc-1.1 | `services/user-center/{cmd,web/templates,web/static,go.mod}` + Dockerfile | `go build` 绿、镜像可跑 |
| wc-1.2 | 接入 `pkg/webui` 组件库（Layout/Nav/Card，用户品牌 CSS 变体，移动端断点） | 手机视差走查无横向滚动 |
| wc-1.3 | Marzban API client 封装（base URL 走 env；mock 接口便于单测） | 单测不依赖真实 Marzban |

### wc-2 登录 + 概览（dependsOn: wc-1；联调需 work-6.2/6.5）
| 子任务 | 产物 | 验收 |
|---|---|---|
| wc-2.1 | 登录页：粘贴 token → 服务端 `GET /api/users?token=` → 成功种 HttpOnly cookie / 失败提示 | 假 token 拒绝；cookie HttpOnly 验证 |
| wc-2.2 | 概览页：流量已用/总额、到期日、在线；5s 轮询 JSON 端点刷新；会话失效跳登录 | 断网静默重试 |

### wc-3 订阅页（dependsOn: wc-2）
| 子任务 | 产物 | 验收 |
|---|---|---|
| wc-3.1 | 服务端拉 `/sub/{token}`（Clash YAML sub）渲染：链接 + 复制按钮 + 服务端二维码（go-qrcode） | 复制内容 Clash 可直接导入 |
| wc-3.2 | 客户端导入指引（Clash Verge / Stash / Phase 2 Windows 客户端占位卡片） | 指引可点 |
| wc-3.3 | "更换 token" 入口（Marzban regenerate 后重粘 → 重种 cookie） | 重粘立即生效 |

### wc-4 购买页（dependsOn: wc-2，不阻塞上线）
| 子任务 | 产物 | 验收 |
|---|---|---|
| wc-4.1 | 套餐 3 档（Marzban admin 预设，前端模板常量可 env 覆盖） | 展示正确 |
| wc-4.2 | 下单 → Marzban 建 invoice → 5s 轮询到账 → 未付态"联系渠道加量" | 到款后流量上限更新 |
| wc-4.3 | 订单历史（Marzban invoices API） | 状态正确 |

### wc-5 安全（dependsOn: wc-2，可并行）
| 子任务 | 产物 | 验收 |
|---|---|---|
| wc-5.1 | cookie：HttpOnly + SameSite=Lax + Secure；登录限流（每 IP 10 次/分钟） | curl 验证 |
| wc-5.2 | 页面安全头（CSP 默认 deny + self）；token 只在 cookie，页面不渲染 token 明文 | 走查 |

### wc-6 测试（随 wc-2..5 增量做）
| 子任务 | 产物 | 验收 |
|---|---|---|
| wc-6.1 | httptest 页面断言：4 页 200 + 关键 DOM 片段 + 未登录 302 | `go test` 绿 |
| wc-6.2 | 真实 token e2e：登录→看流量→复制订阅（进 scripts/，并入 work-8） | 脚本退出码 0 |

### wc-7 部署（dependsOn: wc-6）
| 子任务 | 产物 | 验收 |
|---|---|---|
| wc-7.1 | compose 加 `user-center`（Go 单容器）；Dokploy 第二站点 `user.<domain>` + HTTPS | 浏览器可访问 |
| wc-7.2 | 观察项：登录失败率 / Marzban API 5xx（access log 统计口径） | docs 记录口径 |

## 2. Dependency Graph

```
work-6(marzban 就绪)
   │
wc-1 ─► wc-2 ─┬─► wc-3
              ├─► wc-4
              └─► wc-5 ─► wc-6 ─► wc-7 ─► (汇入 work-8 e2e 验收)
```

- **frontier**：wc-1 可立即开工（mock Marzban，不等 work-6）；wc-3/4/5 并行
- **最短路径**：wc-1→2→3→6→7（约 2–3 人日，比 React 方案省 ~1 天）；wc-4 不占路径
- **convergenceGate**：wc-7 部署后，work-8 的 user-center e2e 线（真实 token 全链路）绿

## 3. 风险与处置

| 风险 | 处置 |
|---|---|
| Marzban API 字段/行为与预期不符 | work-6.5 冒烟先行，wc-2 前定契约 |
| `/sub/{token}` 出 Clash YAML，非 Clash 用户用不了 | 订阅页引导 Clash 系客户端；Phase 2 可加 subconverter 转纯链接 |
| 无支付网关，购买不闭环 | 页面明示"联系渠道加量"，运营在 Marzban 手动加量；Phase 2 接网关 |

## 4. Ledger（本轮）

```
ledger: {
  is_complete: false,
  in_progress: [],
  facts: [
    "wc-1..6 完成: user-center Go SSR 单二进制(登录/概览/订阅/购买 4 页 + JSON 轮询), 共享模块 vpnmaster/webui(ComponentsTemplate + CSS/组件), Marzban client 接口化(真实 HTTP + Mock + Disabled 桩), go-qrcode 服务端出图, 会话(HttpOnly+SameSite=Lax, 30s 缓存), 登录限流(10/min/IP) + CSP/安全头, compose 服务+Dockerfile(context=services/ 含 webui) 已落地; 单测 4页 200/未登录 302/限流/CSP/token不渲染 全绿",
    "模板引擎: htmlx 不可获取, 回退 stdlib html/template+go:embed(同 ingest §0 风险预案), 渲染层隔离在 vpnmaster/webui",
    "user-center 改为 Go SSR 单二进制(无前后分离/无 Node), 决策记录: 本文件 §0",
    "外部前置 work-6.2/6.5 定 Marzban API 契约; wc-6.2 真实 token e2e 与 wc-7.2 观察项待联调"
  ],
  done: [ "wc-1", "wc-2", "wc-3", "wc-4", "wc-5", "wc-6", "wc-7.1" ],
  assignment: { wc-6.2: implementer /* 并入 work-8 */, wc-7.2: implementer }
}
```
