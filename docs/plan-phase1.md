# Phase 1 计划（rex-planning）

> 范围：一阶段（ingest + xray-edge + Marzban + admin-web + Dokploy 上线）。
> 客户端（Phase 2，Wails+sing-box）不在本计划内，见 docs/phase2-windows-client.md。
> 每个子任务都是独立 commit 粒度；回退点 = `git revert` 对应 commit。

## 0. Decision Ticket —— 技术栈选定

| 项 | 决策 | 依据 |
|---|---|---|
| 核心语言 | **Go 1.23+** | 团队偏好；Xray/sing-box 生态同语言；单二进制部署 |
| ingest 存储 | **GORM + 驱动可换**：MVP 用 `modernc.org/sqlite`（纯 Go 无 CGO），接口层只依赖 GORM，**迁移 Postgres 时只换 DSN+驱动，不动业务代码** | 不写裸 SQL；数据访问收敛在 repo 层（`internal/db` 只导出 repository 接口，不暴露 *sql.DB/GORM db） |
| xray 配置 | **json.Marshal 模板直出**（不用 xray-go SDK） | schema 稳定；Dockerfile pin xray 版本 |
| 转发内核选型 | **服务端 = Xray**（balancers 多 outbound 池 + `-s reload` 热加载是原生能力；Marzban/Remnawave 工具链均驱动 Xray；本场景瓶颈在上游带宽而非内核性能）。**客户端 = sing-box**（嵌入单 exe + 性能，见 phase2 文档） | 双内核分工，不重复维护 |
| 热加载 | **deploy/edge 镜像 = xray + inotify watcher**（容器内 `xray -s reload`） | 不依赖 docker socket；不断用户连接 |
| 用户中台 | **Marzban 原样使用 + Dokploy Postgres** | 用户 CRUD/流量/配额/订阅全现成 |
| 上游接入 | **sub 拉取（base64 纯链接 + base64 Clash YAML 双格式）** | 已实测两个真实 sub 为 base64 纯链接 vless+ws+tls |
| Web 界面（admin + user-center） | **不做前后分离**：Go 服务端渲染多页（html/template + go:embed，vanilla JS 仅轮询），单二进制部署，不引入 Node 构建链 | 运维面板/用户门户不是产品级前端；多页=独立路由页面（非 SPA），页面即 API 同二进制，少一整套构建/反代维护 |
| 鉴权（MVP） | 各 web app 管理员/用户会话（cookie + 口令，HttpOnly） | 不阻塞功能 |
| CI/git 托管 | **不需要**（用户确认）；本地验证 = `make dev` 全容器 + 手工跑 scripts/*.sh | 开发/验证全在 Docker 内闭环 |
| 开发环境 | 全 Docker 同构（deploy compose 已落地） | 与线上 Dokploy(Linux) 一致 |

## 1. Work Items（子任务级拆解）

### work-2 仓库骨架与镜像构建（dependsOn: work-1 决策签核）

| id | 任务 | 产物（文件/命令） | 验收 |
|---|---|---|---|
| 2.1 | ingest 工程初始化 | `services/ingest/{cmd/ingest/main.go, go.mod, internal/{sub,probe,out,db,api}/}` 空壳 + 各包 README | `go build ./...` 过 |
| 2.2 | ingest 双 Dockerfile | `Dockerfile`（golang:1.23-alpine 多阶段→scratch 或 alpine + ca-certs）、`Dockerfile.dev`（装 air） | `make build-ingest` 出镜像可 `docker run` |
| 2.3 | edge 镜像 | `deploy/edge/Dockerfile`：pin xray 官方镜像（25.x）+ `watcher.sh`（inotifywait /configs/config.json → `xray -s reload`，失败重试）+ 基础 config 模板（inbound ws :10086 + api 段 + 空 outbounds 占位） | `docker run` 后手改 config，日志见 reload |
| 2.4 | 仓库卫生 | `.gitignore`（.env/二进制/ingest.db/node_modules）、`Makefile` 补目标、`scripts/` 目录（后续 5.4/8.x 脚本放这） | `git check-ignore deploy/.env` 命中 |

### work-3 订阅拉取 + 解析 + 节点库（dependsOn: work-2）

| id | 任务 | 产物 | 验收 |
|---|---|---|---|
| 3.1 | 数据模型（**GORM 模型 + repository 接口**） | `internal/db/store.go`（GORM AutoMigrate + `UpstreamRepo/NodeRepo/EventRepo` 接口，`NewStore(driver)` 注入：`sqlite` 默认 / `postgres` 预留）+ 表：`upstreams`、`nodes`(去重键 hash)、`events` | 单测：建库/迁移幂等；repo 接口层单测跑在 in-memory sqlite，**接口零驱动泄漏** |
| 3.2 | Poller | `internal/sub/poller.go`：周期拉取（env interval），HTTP 15s 超时，429/5xx 指数退避（1m→2m→4m 封顶），成功回写 last_ok；**只存 URL 掩码进 DB** | `poller_test`：httptest mock 成功/429/超时三路径 |
| 3.3 | Parser | `parse_links.go`（vless/vmess/ss/trojan 四协议 URL 解析；base64 自动识别：raw 或 base64 后判定）、`parse_clash_yaml.go`（proxies 数组，含 vless/vmess/trojan/ss/hysteria2）→ 统一 `Node` 结构 | 每协议 ≥1 fixture + ≥1 非法输入断言报错 |
| 3.4 | 幂等 upsert + 消失节点 | 去重键 3.1 的 hash；重复拉取不新增；节点连续 2 轮未出现 → stale_count++，2 轮后删除并记 event | `upsert_test`：3 轮拉取断言行数稳定 + 删除路径 |
| 3.5 | 真实样本 fixture | 取 vpn-4zx(42 节点)/rexleimo 各拉一次样本，**uuid/token 脱敏**（替换为 fake 常量）后存 `testdata/` | fixture 断言：节点数=42、字段 golden |
| 3.6 | 联调 | ingest 指真实 sub URL（env）跑一轮 | DB 节点数 = 上游实际数（日志对账） |

### work-4 探测 + 剔除状态机（dependsOn: work-3）

| id | 任务 | 产物 | 验收 |
|---|---|---|---|
| 4.1 | Prober | `internal/probe/prober.go`：TLS 握手探测（sni 用节点 sni，3s 超时），并发 16；测 RTT 写回 nodes.latency_ms；仅探 ACTIVE/RETRY | `testutil/fake_node.go`（自签 TLS listener）驱动单测 |
| 4.2 | 状态机 | `internal/probe/state.go`：转移表——ACTIVE→EVICTED（连续 3 败）；EVICTED→ACTIVE（2min 重试周期，连续 2 成）；**全池摘除时保留最后可用配置 + ALERT 日志 + /healthz degraded** | `state_test`：全矩阵转移 + 全灭保池 |
| 4.3 | 事件落库 | 每次转移写 `events`（供 admin-web 时间线） | `SELECT count` 断言 |
| 4.4 | 手动操作接口 | `Store.Evict(id)/Recover(id)`（admin-web 按钮用，立即触发一次 xraygen） | 单测 |

### work-5 xray-edge 联动（dependsOn: work-4）⭐ 核心验收

| id | 任务 | 产物 | 验收 |
|---|---|---|---|
| 5.1 | xraygen | `internal/out/xraygen.go`：inbound(ws :10086) + 每个 ACTIVE 节点一个 vless outbound（tag `up_<hash>`）+ routing（catch-all → balancer `upstream_lb`, **leastPing**）+ api 段；**原子写**（.tmp+rename） | 快照测试：给定 3 节点输入 → golden config.json |
| 5.2 | 触发链 | 状态变化/手动摘除 → xraygen → watcher 自动 reload；reload 失败记 event + 告警 | 日志顺序断言（config 写 → reload ok） |
| 5.3 | 护栏 | 无 ACTIVE 节点时**不写**新 config（保持旧池），/healthz 返回 degraded | 单测：全灭场景 config 文件 mtime 不变 |
| 5.4 | E2E 脚本 | `scripts/e2e-edge.sh`：compose up → 等 /healthz → 经 :10086 的 `curl -x` 出口 IP 查询（ifconfig.me）∈ 上游池 IP 集合 | 脚本退出码 0 |

### work-6 Marzban 环境验证（dependsOn: work-2，**与 3/4/5 并行**）

| id | 任务 | 产物 | 验收 |
|---|---|---|---|
| 6.1 | 跑通 | compose 起 marzban+postgres，初始化 admin，/healthz 活 | 面板可登录 |
| 6.2 | 用户流 | 建测试用户（100GB/30d）→ 取 sub token → `curl /sub/{token}` 内容正确 | checklist 记录 |
| 6.3 | **集成模式判定（关键分叉，降级链见 §5）** | 按 §5 顺序逐级尝试，选中的写进 checklist | `docs/marzban-checklist.md` 记录判定过程，架构 §2 同步更新 |
| 6.4 | 流量指向 xray-edge | 按 6.3 结果把用户流量桥到 xray-edge（xray-user outbound / user outbound = xray-edge:10086） | Clash 导入 sub，流量统计增长且出口 IP=上游池 |
| 6.5 | 客户端接口冒烟 | `GET /api/users?token=`（未来客户端流量看板用）返回该用户流量字段 | 响应断言字段存在 |

### work-7 ingest 管理页（SSR 多页）+ API（dependsOn: work-3，**与 work-5 并行**）

> Web 不做前后分离：页面由 ingest 二进制内 html/template + go:embed 渲染，
> vanilla JS 只做轮询，无 Node 构建链（见 §0 决策）。

| id | 任务 | 产物 | 验收 |
|---|---|---|---|
| 7.1 | 页面框架 | `internal/web`：模板布局（导航）+ 5 个独立路由页（`/admin` 总览、`/admin/upstreams`、`/admin/nodes`、`/admin/events`、`/admin/users` 只读）+ 会话（cookie+env 口令，HttpOnly） | curl 走查：未登录 302→登录页，登录后 5 页 200 |
| 7.2 | 页面功能 | 上游 CRUD+手动拉取；节点表（状态/延迟/摘除恢复按钮）；事件时间线；用户页服务端取 Marzban API | 逐页走查记录 docs |
| 7.3 | 轮询 | vanilla JS 5s 轮询 JSON 端点（同二进制），刷新表格区 | 轮询不整页刷新 |
| 7.4 | JSON API | `/api/upstreams` `/api/nodes` `/api/events` `/api/healthz`（供未来客户端/监控） | httptest 契约测试绿 |
| 7.5 | 部署 | ingest 容器直接服务 web，Dokploy 反代指 ingest，无 nginx 静态容器 | 浏览器可访问 |

### work-10 user-center（终端用户自助平台，dependsOn: work-6.2，**与 work-7 并行**）

> **子任务级拆解与技术栈选定见独立计划 `docs/plan-user-center.md`（wc-1..wc-7）**，此处只留索引。

| 摘要 | 依赖 | 验收 |
|---|---|---|
| Go SSR 单容器（wc-1 脚手架→wc-2 登录/概览→wc-3 订阅页 ∥ wc-4 购买 ∥ wc-5 安全→wc-6 测试→wc-7 部署），**无前后分离、无 Node** | wc-1 可 mock 先行；联调需 work-6.2/6.5 | 真实 token 登录→看流量→复制订阅 e2e 绿（并入 work-8） |

### work-8 全链路验收（dependsOn: work-5 + work-6）

| id | 任务 | 脚本 | 验收 |
|---|---|---|---|
| 8.1 | E2E 全链路 | `scripts/e2e-full.sh`：Marzban sub → curl -x → 断言出口 IP ∉ 本机 ∧ ∈ 上游池；另加 user-center 线：真实 token 登录→看流量→复制订阅 一条绿 | 绿 |
| 8.2 | 故障演练 | `scripts/chaos.sh`：iptables DROP 一上游 → ≤2 探测周期 events 出现 EVICT 且请求成功率不掉 → 恢复 → RECOVER 出现 | 绿 + 截图日志 |
| 8.3 | 负载分布 | `scripts/load-50.sh`：50 并发 × 20 次出口 IP 聚合，断言 ≥2 节点均分到流量（leastPing 生效） | 分布表打印 |
| 8.4 | 长稳说明 | `docs/soak.md`：24h 观察项（节点封禁率/延迟漂移/上游账单对账） | 文档 + 跑 24h 结论 |

### work-9 Dokploy 上线（dependsOn: work-8 全绿）

| id | 任务 | 产物 | 验收 |
|---|---|---|---|
| 9.1 | compose 导出 | 线上版（去 dev 覆盖）+ secrets 清单（sub URL/marzban 密钥/admin 口令） | Dokploy 导入无告警 |
| 9.2 | 网络与可观测 | 反代域名（panel/admin/sub）、健康检查、日志轮转、资源限制 | 各域名可达 |
| 9.3 | 上线 + 回滚演练 | staging 跑 `e2e-full.sh` curl 变体；验证 Dokploy 回滚到上一版本可用 | 上线 checklist 全勾 |

## 2. Dependency Graph

```
work-2 ─┬─► work-3 ─► work-4 ─► work-5 ────────┐
        ├─► work-6（并行）──► work-10（user-center）┼─► work-8 ─► work-9
        └─► work-7（仅需 work-3 数据模型）────────┘(work-7/10 在 8 前汇入)
```

- **frontier（ready）**：work-2；**无 blocked**
- **parallelGroups**：P1=[work-6 ∥ work-3→4→5]；P2=[work-5 ∥ work-7 ∥ work-10]；work-8 gate=5+6+7+10
- **最短关键路径**：2→3→4→5→8→9（估 5–6 工作日）；work-6/7 不占路径
- **convergenceGate（work-8）**：e2e-edge 出口断言 ∧ e2e-full 出口断言 ∧ chaos 摘除演练 ∧ load 分布 ≥2 节点
- **completionClaim**：**soft**（规划产物；hard 需 work-9 线上验证）

## 3. Ledger（本轮）

```
ledger: {
  is_complete: false,
  in_progress: [],
  facts: [
    "技术栈决策: docs/plan-phase1.md §0",
    "work items 已拆到子任务级(2.x–9.x), 每项带验收命令",
    "上游 sub 格式实测: base64 纯链接 vless+ws+tls, vpn-4zx=42 节点",
    "Marzban xray 集成模式未验证: work-6.3 为关键分叉, 双回退分支(A/B)已预案, 不阻塞 3/4/5"
  ],
  assignment: { work-2: implementer, work-6: implementer/* 可并行 */ },
  note: "work-10(user-center) 已从 work-7.6 拆为独立 work item, dependsOn work-6.2, 与 work-7 并行"
}
```

## 5. work-6.3 备用方案降级链（各方案改动仅限用户层一侧，不影响 work-3/4/5）

| 级别 | 方案 | 做法 | 代价 |
|---|---|---|---|
| 首选 | Marzban SSH 外挂管理 xray-user | xray-user 容器内置 sshd，marzban 以远程节点模式管理它 | 容器多一个 sshd，小 |
| 备用 A | Marzban 内置 xray + per-user outbound | docker 镜像自带 xray；用户出口设为 `xray-edge:10086`（docker 网络解析），新/存量用户用 marzban admin API 批量设置 | 依赖该功能版本能力 |
| 备用 B（推荐稳态） | 用户层换 **Remnawave** | xray-user 开 Xray RESTful API，注册为 Remnawave node；用户 CRUD/配额/订阅同 Marzban 等价，拓扑干净 | 换面板容器 + 用户数据迁移脚本（若已建用户） |
| 兑底 C | 用户层自建 | 脚本生成每用户 vless inbound + xray stats API 读流量 | 等于自己做中台，仅保命用 |

验证顺序：SSH 外挂 → 内置+user outbound → Remnawave，每级 ≤30min 试错；选定后 1 天内完成切换。

## 6. 风险登记

| 风险 | 影响 | 处置 |
|---|---|---|
| Marzban user outbound/外挂 xray 能力不确定 | 拓扑要改 | work-6.3 第一个验证点，分支 A/B 预案 |
| 上游单 IP 并发限流 | 上线静默降速 | work-8.4 soak 盯；运营风险 |
| ~~git 托管/CI~~ | — | 用户确认不需要部署 CI，已移除 |
