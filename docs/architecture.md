# vpn-master 落地架构（Dokploy 部署）

> 目标：统一管理多个上游订阅（如 `vpn-4zx`、`rexleimo` 的 sub 链接），
> 通过二次转发对外提供 VPN 服务；用户侧用 Marzban 现成面板，自己只写
> **ingest 聚合服务**（拉取 / 解析 / 健康探测 / 负载均衡路由 / 异常剔除）。

## 1. 组件总览

```
                    ┌──────────────────── Dokploy 容器组 ────────────────────┐
  上游订阅链接 x N    │                                                       │
 (clash yaml /       │  ┌─────────────┐    轮询拉取+解析    ┌──────────────┐  │
  base64 纯链接)     │  │   ingest     │◄── 每5min ─────────│  节点库      │  │
                    │  │  (Go, 自建)  │    生成 outbounds   │ (SQLite)     │  │
                    │  └──────┬──────┘─────────────────────►└──────────────┘  │
                    │         │ 健康探测/剔除后热加载 (xray -s reload)          │
                    │         ▼                                              │
                    │  ┌──────────────────────────────┐                     │
                    │  │ xray-edge  ← ingest 独占      │  上游节点池 + LB     │
                    │  │  inbound: ws 127.0.0.1:10086 │  balancer: leastPing│
                    │  └──────────────▲───────────────┘                     │
                    │                 │ 本地一跳 (~1ms)                     │
                    │  ┌──────────────┴───────────────┐                     │
                    │  │ xray-user  ← Marzban 管理     │◄─────── Marzban    │
                    │  │  inbound: 每用户 vless(ws)     │      (面板/用户/   │
                    │  │  每用户配额/流量统计            │       流量/订阅)   │
                    │  └──────────────────────────────┘                     │
                    └────────────────────────────────────────────────────────┘
用户客户端 (Clash 等) ── 订阅链接 ──► xray-user ──► xray-edge ──► 上游池 ──► 互联网
```

**连接点（已确认拓扑）**：用户流量**不直连上游**——Marzban 的 xray 只承载 inbound
（每用户隔离/配额/统计），其出口设为一条**固定链接**指向 xray-edge（`vless://<edge-uuid>
@xray-edge:10086?security=tls&type=ws`，user outbound 方式配置一次永不再变）；
上游池 + 负载均衡 + 剔除全部发生在 xray-edge。理由：Marzban 每次用户操作都会全量重写
它管的 xray 配置，上游池写权必须在 ingest 独占的 xray-edge 上（双实例拆分的初衷）。

**为什么双 xray 实例**：Marzban 会反复重写自己管的 xray 配置（每增删用户都重写）。
把"用户层"和"上游层"拆成两个实例后，ingest 对 `xray-edge` 有**独占写权**，
热加载上游节点池不会碰用户层配置，两边互不干扰。

## 2. Dokploy 服务清单（一个 compose 项目）

| 服务 | 镜像 | 说明 |
|---|---|---|
| `xray-user` | `iann003/...` 官方 xray 镜像 | Marzban 经 Xray RESTful API 管理；outbound 固定指向 `xray-edge:10086` (ws) |
| `xray-edge` | 同上 | 配置 100% 由 ingest 生成（inbound + 上游 outbounds + balancer），ingest 触发 reload |
| `ingest` | 本仓库 Go 构建 | 拉取/解析/探测/剔除/写 xray-edge 配置 + 自带管理 API |
| `marzban` | `alienatix/marzban` | 用户中台（自带完整 Web 管理界面 + REST API） |
| `admin-web` | 本仓库前端构建 (nginx 静态) | ingest 侧多页管理界面（上游池/节点/事件） |
| `postgres` | Dokploy 内置 DB 服务 | Marzban 用 |

## 2.1 用户数据管理 & 中台 CRUD（不自己写）

| 能力 | 由谁提供 |
|---|---|
| 用户 增删改查 / 批量 | Marzban 管理界面（自带） |
| 每用户订阅链接 / 二维码 | Marzban 用户页（自带） |
| 流量/配额/到期 管理 | Marzban 用户页 + API（自带） |
| 计费/账单/支付 | Marzban 发票系统（自带） |
| 多节点切换 | Marzban 多节点 API（自带） |

Marzban 的 admin UI 本身就是我们要的"中台"，是完整 Vue 应用（多页面），
**用户数据全部存它，ingest 不碰用户表**。我们只通过 Marzban REST API
读用户流量做展示（admin-web 里聚合，不做写）。

## 2.2 自建 admin-web（多页管理界面，非单页 SPA 糊弄）

只做"上游侧"真正缺的那块，React + react-router，独立路由多页：

```
/admin/            总览: 节点在线率, 用户流量 Top, 事件流
/admin/upstreams    上游订阅 CRUD: 添加 sub URL, 拉取状态, 节点清单
/admin/nodes        节点详情: 延迟曲线, ACTIVE/EVICTED 状态, 手动摘除/恢复
/admin/events       剔除/回池/拉取失败 事件时间线
/admin/users        用户流量看板 (只读, 数据来自 Marzban API)
```

- 构建产物走 nginx 静态容器，`/api/*` 反代到 ingest
- 登录：ingest 侧内置会话登录（cookie + 邮箱 magic link，不引入完整 auth 栈）

## 2.3 客户端（用户侧）

**架构原则：我们交付的是“每用户一条 vless 订阅链接”，客户端只是消费者，不绑定任何一家。**

| 阶段 | 内容 |
|---|---|
| MVP | 不发自研客户端；用户装 Clash/Clash Verge(mihomo)/Stash 导入 Marzban 订阅链接即用 |
| Phase 2 | Windows 自研轻量客户端：单 exe 内嵌 sing-box，启动拉取用户订阅 + 自动 TUN，双击即用 |
| Phase 3 | Android（VPN API 包 sing-box）；Mac 需 Apple 开发者签名+公证，后置；iOS 推 Stash/Ivory |

自研客户端与第三方客户端消费同一条订阅链接，后端零改动。


Dokploy 操作要点：
- `xray-user`、`xray-edge` 用 volume 挂配置文件（marzban 写 user，ingest 写 edge）
- 敏感项全部走 Dokploy 的 env/secrets：两个上游 sub URL（含 token）、Marzban API key、
  客户端订阅 token 前缀。**token 不进代码库**
- `ingest` 从仓库 Dockerfile 构建（Dokploy 支持 repo 构建）
- 健康检查：ingest 暴露 `/healthz`（DB 可写 + 至少 1 个上游节点 ACTIVE）

**统一开发环境（重要约束：线上 Dokploy = Linux + Docker，开发必须全容器化同构）**：

```
deploy/docker-compose.yml      # 生产同源 compose (Dokploy 直接导入)
deploy/docker-compose.dev.yml  # 开发覆盖层: 源码热挂载(air) + 调试端口 + dev-shell(golang 镜像挂全仓库)
deploy/env.example             # env 模板 (开发 cp 成 deploy/.env; 线上走 Dokploy secrets)
.devcontainer/                 # VS Code devcontainer = 一键进开发环境
Makefile                       # make up / dev / logs / shell / build-ingest
```

- 开发态 `make dev`：ingest 源码挂载 + air 热重载，`dev-shell` 里可直接访问
  `postgres:5432`、`xray-edge:10086`（服务名寻址，与线上完全一致）
- 线上/开发唯一差异：热挂载与调试端口，镜像与配置路径相同，不存在"本地能跑线上崩"
- `.env` 永不进 git；sub URL token 只在 env/Dokploy secrets 里

## 3. 路由 / 负载均衡机制（nginx 等价物）

用 Xray 官方 balancer，效果等价于 nginx upstream 池：

```json
// xray-edge routing
{
  "rules": [
    { "type": "field", "outboundTag": "upstream_lb" }
  ],
  "balancers": [
    { "tag": "upstream_lb", "strategy": "leastPing" }
  ]
}
```

每个上游节点 = 一个 outbound（tag 形如 `up_<id>`），全部汇入 balancer：

| nginx 概念 | 本方案对应 |
|---|---|
| upstream 池 | 每个节点一个 vless outbound，`balancers` 汇集 |
| 权重 / 轮询 | `strategy: roundRobin`（可选）或 `leastPing`（自适应，默认选它） |
| max_fails / fail_timeout 被动剔除 | ingest 主动探测 + 剔除状态机（下节） |
| 摘除/恢复 | 剔除 → 更新 xray-edge 配置 → `xray -s reload`（不断现有用户连接） |

策略默认 **leastPing**：延迟最低的节点自然多分流量，比固定权重更贴合 VPN 场景。

## 4. 异常剔除状态机（"有异常会暂时剔除"）

每个节点三态：`ACTIVE / EVICTED / COOLDOWN`

```
 探测周期 30s: TLS 握手(超时 3s) + 记录延迟
 ACTIVE ──连续 3 次失败──► EVICTED (立即从池摘除, 热加载)
 EVICTED ──每 2min 重试, 连续 2 次成功──► ACTIVE (回池, 热加载)
 特例: 全池被摘除时保留最后一次可用池(不置空) + 告警
```

- 探测用 TLS 握手而非裸 TCP：vless+ws+tls 节点裸 TCP 通不代表协议可用
- 全部变化都是"写新配置 → `xray -s reload`"，秒级生效且不踢现有用户
- ingest 决策日志落 SQLite + stdout，Dokploy 日志可直接看

## 5. ingest 服务职责（唯一自研组件）

```
in/
  sub_poller.go    // 拉取: 支持 base64(clash yaml) 与 base64/明文(纯链接: vless/vmess/ss/trojan/hysteria2/tuic)
  parser.go        // 归一化节点结构 (id, addr, port, proto, stream, sni, tag, region)
probe/
  prober.go        // TLS 握手探测 + 延迟
  state.go         // ACTIVE/EVICTED 状态机
out/
  xraygen.go       // 生成 xray-edge config.json (inbound + N outbounds + balancer)
  reload.go        // xray -s reload / xray api
db/
  store.go         // SQLite: 节点, 探测记录, 事件
api/
  http.go          // /healthz, /nodes (调试), /events
```

单二进制、单体即可，规模到了再拆。

## 6. 测试流程

**L1 单元（Go test，CI 每次 push 跑）**
- 解析器：用两个真实 sub 链接的样本做 fixture（脱敏后存 `testdata/`，
  原始 token 版 .gitignore）：断言解析出的节点数（vpn-4zx = 42）、字段映射
- 状态机：fake 节点，验证 3 败摘除 / 2 成回池 / 全灭保池 三条路径

**L2 集成（本地 docker compose 起 xray-edge + ingest）**
- ingest 写入后，`xray api getSystemStatus` 可见 outbounds 数量正确
- 通过本地 ws 入站发真实请求，出口 IP 属于上游池
- ingest API：CRUD 上游（POST /upstreams 拉取成功）、手动摘除/恢复节点接口
  各跑一遍（admin-web 就是打这组接口，接口先于 UI 验收）

**L3 端到端（staging 全链路：Marzban 用户 → xray-user → xray-edge → 上游）**
- Marzban 建测试用户，取订阅链接进 Clash，`curl -x` / 测速，
  断言出口 IP ≠ 本机 IP（证明流量确实走上游池）

**L4 故障演练（chaos）**
- 停掉 / iptables DROP 某个上游 IP → 2 个探测周期内被摘除（看 ingest 日志）
  且服务仍可用（L3 请求不断）；恢复后自动回池
- kill xray-edge → 重启后 ingest 自动重新加载

**L5 负载**
- 50 并发 `curl` 过全链路，统计出口 IP 分布，验证多节点都分到流量（leastPing 生效）

**L6 长稳**
- staging 跑 24h：marzban 流量统计 vs ingest 探测日志 vs 上游账单三方对账，
  观察是否有节点被上游静默限流/封禁（这是最大运营风险，见 §7）

## 7. 遗留风险（测试也测不出来的）

1. **上游 ToS / 单 IP 并发限流**：出口 IP 承载所有用户并发，上游可能限流或封 IP。
   → L6 长稳期重点盯；优先选允许转售的上游
2. **token 泄露** = 上游流量白送 → 只在 env 里存，日志脱敏
3. 面向中国大陆收款的合规问题不在本架构范围内，业务层先解决
