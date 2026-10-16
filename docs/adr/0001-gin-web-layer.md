# ADR-0001: Web 层由手撸 net/http 迁移到 gin + 共享中间件套件

日期: 2026-10-16
状态: 已确认（用户拍板）

## 背景

ingest(:9100) 与 user-center(:9200) 的 HTTP 层为 Go 标准库 ServeMux + 各自手写的
会话/鉴权/限流中间件（`services/ingest/internal/web/session.go`、
`services/user-center/internal/web/server.go` 的 `auth()`/`rate`）。
同一类逻辑两个服务各写一遍，无可复用中间件层；后续服务/网关类需求继续手撸
会复制膨胀，维护责任无框架生态兜底。用户明确要求替换 Web 层而非增量修补。

## 决策

1. 两个服务的 HTTP 层迁移到 **gin**（Go 主流 web 框架，路由组/中间件链/错误处理标准化）。
2. 会话、鉴权、限流、recovery、JSON/SSR 渲染helper 抽成**共享 Go 套件**
   （新模块，仓库内 replace 引用，模式同 `services/webui`），各服务只声明自己的路由与业务中间件。
3. 渲染方式不变：gin 挂 html/template，**零构建约束保留**（不引入 Node 管线），
   5s JSON 轮询端点平移为 gin group。
4. 迁移是 UI 优化的前置项：先换骨架（gin+套件，行为等价），再在稳定骨架上做视觉层（见 ADR-0002，待确认）。

## 后果

- +gin 依赖（两服务 go.mod）；换来中间件生态、统一 panic/recovery、后续网关类需求直接复用套件。
- 现有手写 session/限流行为需在迁移中保持等价：e2e-full 线2（登录/状态/订阅/QR）作为回归验收门槛。
- web_test.go 全部路由测试需改写为 gin 测试写法；迁移期间两服务不可并行双跑（路由语义单源）。
