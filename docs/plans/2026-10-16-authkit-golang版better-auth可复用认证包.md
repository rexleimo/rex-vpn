# authkit 集成 — open-rails/authkit + PostgreSQL

日期: 2026-10-16 | 状态: 执行中(WI-0..2 完成✅, WI-3..6 待做) | 分类: planned
取代: 本文档取代同日前稿「authkit-golang版better-auth可复用认证包」（自研方案作废，用户拍板改用开源 open-rails/authkit 并把数据库切到 PostgreSQL）。

## 决策记录

- 用户决策（2026-10-16）：**不自研，采用 `github.com/open-rails/authkit`（MIT），数据库切换到 PostgreSQL。**
- 依据：authkit 活跃维护（当日有提交）、pkg.go.dev 7 下游、in-process 嵌入 Go 二进制（无 Node sidecar，不违背零构建）、自带 gin 适配器、表结构开机自动建/升级。

## 调研确认的集成事实（读仓库源码，非 README 转述）

| 项 | 结论 |
|---|---|
| Postgres | **必需**（`Deps.Postgres *pgxpool.Pool`），要求 v18；authkit + River(队列) 的表由 `New()` 启动时自动创建/升级 |
| Email/SMTP | **可选**（`Deps.Email` nil 即可；用到邮箱的流程返回 `email_unavailable`，或开 `Registration.AllowMissingSenders`）→ v1 不接 SMTP |
| SMS/Twilio | 可选，v1 不接 |
| Redis | 可选（单进程内存限流即可） |
| 签名密钥 | 必需：`KeysConfig.Path` 指向 keys.json（需生成一次，持久保存） |
| 租户 | 必需：`DefaultTenantID`（稳定 UUID，存配置） |
| 注册策略 | `RegistrationConfig` + `InvitationsConfig`：支持**关闭自助注册、仅邀请/管理员建号** → 直接回应"用户能自己创建账号很奇葩"的痛点 |
| 能力面 | 邮箱密码、JWT 会话、2FA、passkeys、API keys、RBAC、admin 用户目录（`auth_users.go`/invites/SCIM） |
| 本机环境 | `libpq 18.6` 客户端已装；**服务端未装**（docker 不可用，走 Homebrew：`postgresql@18` 有 bottle，keg-only） |

## 架构方案

```
user-center(:9200)  ──内嵌──>  authkit(唯一账号签发方)  ──>  PostgreSQL(rexvpn 库, authkit schema)
      │                            ▲
      │ 会话=authkit cookie/JWT     │ ingest admin 经 authkit 的 api/userinfo 契约包调用
      ▼                            │
  账号 ↔ Marzban token 映射表(同 PG) ┘
ingest(:9100) 业务库(sqlite: nodes/upstreams/events) v1 不动（WI-7 可选迁移）
```

- 账号签发方唯一（user-center 进程），ingest admin 不重复嵌 authkit，走其 admin/users API 建号（避免双 issuer 密钥分裂）。
- 用户从此面对「邮箱+密码」，Marzban token 降级为服务端持有的映射凭证，不再粘贴。

## 工作项图

```
WI-0 ✅ PG 18.6(brew services 常驻) + rexvpn 库/角色, psql 验证通过
WI-1 ✅ deploy/.env(gitignored; TENANT=c85ef835-de62-46f2-bc41-f905549a0edf) + ~/.local/state/rex-vpn/keys(authkit 自生成, 0600)
WI-2 ✅ internal/accounts + authkitgin.Mount; 冒烟: authkit schema 建表 / JWKS 响应 / River 启动; 未配 UC_PG_DSN 自动回退旧 token 模式, 存量测试零回归(authkit v1.31.0 已 pin)
WI-3 登录页改造: 邮箱密码 sign-in/sign-up 表单(复用 data-wm-act JSON 协商) + 会话校验  ← WI-2
WI-4 账号↔Marzban token 映射: 映射表 + 登录后解析订阅 + retoken 流程适配              ← WI-2,WI-3
WI-5 admin「+添加用户」: ingest 调 authkit users API 建号(一次性密码/邀请) + 关闭自助注册策略 ← WI-2
WI-6 验证与文档: 契约测试 + 浏览器 E2E(注册禁用/管理员建号/登录/会话) + deploy 文档(PG 运维)  ← WI-3..5
WI-7 (可选,另批) ingest 业务库 sqlite→PG 迁移                                            ← 独立
```

## 风险与对策

- **authkit 演进快（v1.30.1，当日提交）** → 锁版本 + `go.mod` 精确 pin，升级单独走 PR。
- **它带 River 队列表进我们的库** → 独立 schema（`DatabaseConfig.Schema`），与业务表隔离。
- **admin 跨进程调用 authkit API 的具体端点** → WI-5 开工前先读 `api/`、`auth_users.go` 契约，若 admin API 仅进程内可用，则把「用户管理」页挪到 user-center 侧（staff 角色），仍满足目标。
- **本机 PG 与生产 docker-compose 一致性** → WI-6 在 `deploy/` 增加 postgres 服务定义，本地 brew 仅开发态。

## 验证门

- 每 WI 完成即 `go vet` + `go test -count=1` 全绿；WI-3/4/5 附 httptest 契约测试；WI-6 真实浏览器 E2E 截图。
- 安全底线：自助注册默认关（邀请/管理员建号）、cookie HttpOnly+Secure 可配、密钥文件权限 0600 且不入库不进 git。
