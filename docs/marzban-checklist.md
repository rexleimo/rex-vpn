# Marzban 环境验证 Checklist（work-6）

> 记录 6.1/6.2/6.3 验证过程与 API 契约。验收脚本见 `scripts/`，架构同步见 `docs/architecture.md` §2/§7。
> **本轮环境约束**：本机 Docker Hub 拉取 `alienatix/marzban` 被代理拦截 → 改用 **PyPI/GitHub(codeload) 源码 + 真实 xray Windows 二进制** 本地验证，功能等价于容器部署（Postgres 接线与 Dokploy 上线归 work-9）。

## 6.1 跑通 ✅

- [x] 源码安装：`Gozargah/Marzban`(master) @ PyPI 依赖 + `venv`，SQLite 默认库（PG* 留空，Postgres 归 work-9）
- [x] 真实 xray 内核：`XTLS/Xray-core v26.3.27 windows-64`（GitHub releases 可下载）→ Marzban 日志 `Xray core 26.3.27 started`
- [x] Web/API 起于 `:8090`（`UVICORN_PORT=8090`）；admin 创建：`marzban-cli.py admin create -u admin --sudo`（口令 env `MARZBAN_ADMIN_PASSWORD` 非交互）
- [x] **面板登录 = API 登录**：`POST /api/admin/token`（**form 编码** username/password）→ `access_token`(JWT)；**无 `/api/login`、无 `/healthz` 端点**（本版本）。"healthz 活" 等价验收 = `GET /api/admin` 持 JWT 200
- 本地坑（已解）：
  - Windows 无 `readline` → 桩 `readline.py`
  - `apscheduler` 需 `pkg_resources` → `pip install "setuptools<81"`
  - `pyOpenSSL` 100 年 notAfter 超 32-bit → 本地改 10 年
  - 部分迁移残留致 `table tls already exists` → 清 `db.sqlite3` 重跑 `alembic upgrade head`
  - 默认 `xray_config.json` 只有 Shadowsocks → 补 VLESS WS TLS / VMess WS / SS inbound 模板（REALITY 需真实 x25519 密钥，本环境去掉）

## 6.2 用户流 ✅

- [x] 建测试用户：`POST /api/user` **`{"username":"testuser1","expire":30,"data_limit":107374182400(100GB),"proxies":{"vless":{}}}`** + `PUT /api/user/{u}` 绑 `{"inbounds":{"vless":["VLESS WS TLS"]}}`
  - 契约坑：不建 proxies → 用户可建但 `GET` 响应校验失败("Each user needs at least one proxy")；proxies 有但 inbounds 为空 → 订阅 `proxies: []` 空节点。**必须绑 inbound 模板**
  - 字段名是 `expire`/`data_limit`(字节)，**非** `expire_in`/`limit_bytes`；**`expire` 是 unix 时间戳（0=不限时），不是天数**——2026-10-17 全栈实测纠错：传 30 被当 1970-01-01，用户秒变 `expired`、内核配置不注入 clients（表现为客户端连上但无回包）；本仓生产代码 `provision.go` 一直按 unix 秒传，未受此误记影响
- [x] 订阅 token：`GET /api/user/{username}` → `subscription_url` = `/sub/{token}`；**无 get-tokens/renew-tokens 端点**
  - token 结构（实测解码）：`base64url("username,ceil(now)") + sha256(b64+secret)[:10]`，**本地可解码出 username**（HMAC 需 Marzban jwt 表密钥，拉取时服务端校验）
- [x] `GET /sub/{token}` 按 **User-Agent 选格式**：`clash-verge`/`stash` → Clash YAML（节点齐全：vless uuid/ws path/sni）；`YAC`/空 UA → 空 body。`subscription-userinfo` 头带 `upload/download/total/expire`
- [x] user-center 真实客户端端到端：`POST /login(token)` → 303 dashboard；`/api/status` JSON 正确；`/qr.png` PNG 200（`services/user-center` 已按本契约改写并通过真实联调）

## 6.3 集成模式判定 ✅ → 选「内置 xray（备用 A 路线）+ xray API」

- [x] 实测：Marzban 自管 xray 内核（`xray_api/` 目录 + xray 进程由 Marzban 拉起/重启），**无需 SSH 外挂**；每增删用户 Marzban 重写内核配置并重启（日志 `Restarting Xray core`）
- 结论：**用户层 = Marzban 内置 xray**（双 xray 拓扑：xray-user 即 Marzban 内核；ingest 只写 xray-edge，互不干扰，架构 §1 前提成立）
- 未做（归 work-6.4/work-8）：用户出口指向 `xray-edge:10086`（需 xray-edge 运行 + 真实上游池；本机 xray-edge 可用本地 teddysun 镜像起，上游流量需真实 sub 凭据）

## 6.4 全栈本地实机验证 ✅（2026-10-17，Mac 原生栈；Docker Hub 被拦截故走源码路线，等价 compose 拓扑）

- [x] Marzban 源码(codeload tarball) + venv(py3.12, TUNA bottle) + xray-core **源码自编**(GOPROXY=goproxy.cn, darwin/arm64)；面板 :8080，CLI 建 admin
- [x] 用户层内核配置：VLESS WS :2053 入站 + `edge-out` 出口 + routing 全量→edge-out；**坑：outbound vless 必须 `vnext` 结构，写成 inbound 的 `clients` 形态内核启动失败**
- [x] 本地 xray-edge：`watcher-mac.sh`（mtime 轮询替代 inotify，语义同 deploy/edge/watcher.sh）；edge api 改 18080 避面板
- [x] hosts API 改 LAN IP 后订阅节点 server=192.168.x.x（同网手机可用）；hosts 值是**列表**形态（对象形态 422）
- [x] 客户端数据面：socks5 → user-xray(2053) → edge(10086) → internet，出口 IP 实测返回；上游池空时 edge 走 direct（出口=本机出口），池填充后由 xraygen 重写切上游
- [ ] L3 最终断言（出口 IP = 上游节点 IP）：待真实 sub 凭据入池

## 6.5 客户端接口冒烟（user-center 契约定版）✅

| user-center 客户端 | 端点 | 说明 |
|---|---|---|
| admin 登录 | `POST /api/admin/token` (form) | `access_token` 缓存 23h；env `MARZBAN_ADMIN_USER/MARZBAN_ADMIN_PASSWORD` |
| 用户解析 | sub token 本地解码 → `GET /api/user/{username}` (Bearer) | **单数 /user/**；响应无 `id` 字段 → 判活看 `username` |
| 订阅内容 | `GET /sub/{token}` UA=`clash-verge/1.0`(env `MARZBAN_SUB_UA`) | 返回 Clash YAML |
| 发票 | 本版本无 invoice API | `CreateInvoice/ListInvoices` → `ErrNotSupported`/空；购买页 = "联系渠道加量"（plan §0 既定） |

> **遗留观察项（非阻塞）**：`GET /api/user/{username}` 的 `status` 偶报 `expired`（用户刚建、xray 在线态未上报所致），user-center 展示层不受影响；上线前复查一次。

## 本地产物位置（非仓库）

- `E:/vpn-master-marzban/`：Marzban 源码 + venv + `db.sqlite3` + `xray_config.json`（admin=`admin`/`admin-dev-2026`，:8090）
- `E:/vpn-master-marzbin/xray-windows/`：xray.exe v26.3.27 + assets
