# 用户中心（客户端订阅管理平台）设计与实现

> 定位：**终端用户**的自助 Web 平台（我们的"客户端"，Web 版）。
> 与 admin-web 的边界：admin-web = 运营/上游侧管理（多页，cookie 会话）；
> user-center = 终端用户自助（订阅/流量/续费），按 token 隔离，无全局登录体系。

## 1. 技术栈

> **已确认：不做前后分离**——Go 服务端渲染多页，单二进制，无 Node 构建链（见 plan-user-center.md §0）。

| 层 | 选型 | 说明 |
|---|---|---|
| 渲染 | **Go + html/template + go:embed**（`services/user-center` 单二进制） | 4 个独立路由页（login/dashboard/subscription/purchase） |
| 数据/能力 | Marzban 用户 API（服务端代理，不建用户数据后端） | 用户/流量/配额/订阅/发票全在 Marzban |
| 交互 | vanilla JS 仅 5s 轮询 JSON 端点刷新 | 无构建链 |
| 二维码 | go-qrcode 服务端出图 | 免前端依赖 |
| 部署 | 单 Go 容器，Dokploy 反代 `user.<domain>`（无 nginx 静态层） | 比前后分离方案少一个容器少一层 |
| 鉴权 | **token 登录 → HttpOnly cookie 会话**（服务端校验后种 cookie） | 无邮箱/密码/找回体系；token 只在 cookie，XSS 读不到 |
| 支付 | Marzban invoice 体系 + 支付回调（Stripe/支付宝/USDT，按目标市场选） | 购买页只建 invoice，到账由 Marzban 处理 |

> 为什么不做邮箱注册：注册/找回密码/会话管理是"auth 系统"重灾区，MVP 用
> "运营建用户 + 邮件发 token + 粘贴登录" 完全绕开；Phase 2 视市场再上注册（Marzban
> 自带 registration API，届时前端加两个页面即可）。

## 2. 页面与接口映射（MVP 四页）

| 页面 | 功能 | 数据来源 |
|---|---|---|
| `/login` | 粘贴 token → 验证 | Marzban `GET /api/users?token=` |
| `/dashboard` | 流量已用/总额、到期日、当前在线 | 同上（30s 轮询） |
| `/subscription` | 订阅链接 + Clash 二维码 + 复制 + 一键导入指引 | `GET /sub/{token}` |
| `/purchase` | 套餐（流量包/时长）选规格 → 下单 → 支付 → 到账提示 | Marzban invoice API + 支付回调 |

管理侧无改动：运营在 Marzban 建用户/调配额，用户中心自动可见。

## 3. 关键实现

1. **token 安全**：HttpOnly + SameSite=Lax + Secure cookie；登录限流（每 IP 10 次/分）；
   token 泄露 = 该用户配额，Marzban 可 regenerate（用户重粘）；CSP 默认 deny + self
2. **部署**：Dokploy 反代 `user.<domain>` 指 Go 容器；Marzban 面板域名保持内网可达
3. **多语言/品牌**：Mantine 主题 + i18n 预留；MVP 中文
4. **测试**：页面契约测试走 Marzban mock（vitest + msw）；e2e = 真实 marzban
   测试用户 token 走通 登录→看流量→复制订阅 一条线（纳入 work-8 e2e-full）

## 4. 排期归属

MVP 并入 Phase 1 尾部（与 admin 页同周，**同为 Go SSR 同工具链**，~2-3 人日）；
注册/设备管理/支付网关接入 = Phase 2 可选项，不阻塞上线。
