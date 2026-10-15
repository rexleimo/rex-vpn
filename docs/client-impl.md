# 客户端（Windows）实现设计

> 前置：选型与工期见 docs/phase2-windows-client.md。本文是实现级设计（模块/接口/时序/配置生成）。
> 依赖 Phase 1 的 ingest 稳定（订阅格式、sub 拉取协议）后再启动。

## 1. 模块结构

```
client/windows/
  main.go                 # wails 入口: 窗口绑定 + systray + UAC manifest
  wails.json
  ui/                     # Wails 前端: **原生 html+vanilla JS, 无构建链**（与“不做前后分离”同原则, wails build 直接嵌入静态资源）
  internal/
    kernel/               # sing-box 内核封装 (进程内嵌入, 非子进程)
      engine.go           #   Start(cfg)/Stop()/Reload(cfg): 调 sing-box library API
      cfggen.go           #   NodeSet+RoutePolicy → sing-box config.json (内存生成, 不落盘)
      stats.go            #   轮询 sing-box API: 总流量/当前节点延迟
    sub/                  # 订阅拉取
      client.go           #   GET {panel}/sub/{token} → 解析 (复用与 ingest 共享的解析包)
      picker.go           #   并行 TLS 握手探测 → Top1 主用 + url_test 自动切换
    route/                # 路由策略
      rules.go            #   默认策略: LAN/内网→direct, 面板&订阅域名→direct(防自我嵌套), 其余→proxy; DNS 走 proxy 防泄漏
    cred/                 # 凭证
      store.go            #   DPAPI 加密, %LOCALAPPDATA%\vpnmaster\creds.bin (token + 面板地址)
    tray/                 # 系统托盘
      tray.go             #   getlantern/systray: 状态图标/快捷开关/退出
    update/               # 自更新
      updater.go          #   GET {panel}/update.json {version,url,sha256} → 下载校验 → Inno /SILENT 静默替换
  build/
    manifest.xml          #   requestedExecutionLevel=requireAsAdministrator (TUN 需要)
    installer.iss         #   Inno Setup 脚本 (含 UAC elevate)
```

**共享解析包**：`services/ingest/internal/sub/parser` 抽到仓库级 `pkg/subparse`，
ingest 与客户端共同依赖，保证"面板/上游/客户端"三方对订阅格式的理解永远一致。

## 2. 启动时序（连接一次）

```
托盘"连接" → UAC 提权(manifest)
  → cred.Load() 无 token? → 打开浏览器引导用户去面板复制客户端 token
  → sub.Fetch(token) → NodeSet{primary, fallbacks}
  → picker: 并行 TLS 握手(超时3s, 并发8) → 选出 Top1 + 2 备用
  → cfggen: 生成 sing-box 配置(§3)
  → kernel.Start() → TUN 建立(系统接管流量)
  → stats 协程: 2s 轮询流量/延迟刷 UI
后台: 每 5min sub.Fetch 刷新 → Top1 变化则 kernel.Reload(热切, 不断网)
异常: 主节点握手失败 → sing-box url_test 自动切备用 → 仍失败则 UI 显示"重连中"
```

## 3. sing-box 配置生成（核心）

`cfggen.go` 输出的 JSON（模板常量 + 节点参数注入）：

```jsonc
{
  "inbounds": [
    { "type": "tun", "tag": "tun", "address": ["10.8.0.1/28"],
      "stack": "system", "dns": { "listen": "10.8.0.2:53", "strategy": "prefer_ipv4" } }
  ],
  "outbounds": [
    { "type": "url_test", "tag": "proxy",
      "outbounds": [                     // 主用 + 备用, 自动切换
        { "type": "vless", "server": "...", "server_port": 443,
          "uuid": "...", "tls": { "enabled": true, "server_name": "..." },
          "multiplex": { "enabled": false } },
        ...
      ] },
    { "type": "direct", "tag": "direct" }
  ],
  "route": {
    "rules": [
      { "inbound": ["tun"], "ip_is_private": true, "outbound": "direct" },
      { "inbound": ["tun"], "domain": ["panel.vpn-master.xx", "sub.vpn-master.xx"], "outbound": "direct" },
      { "inbound": ["tun"], "protocol": ["dns"], "outbound": "proxy" }
    ]
  },
  "dns": { "servers": [{ "tag": "p", "address": "tls://1.1.1.1" },
                        { "tag": "l", "address": "223.5.5.5", "detour": "direct" }] }
}
```

注意：**客户端连的是 xray-user（Marzban 的固定入口）的 vless 链接，不是 42 个上游节点**——
节点选择/剔除全在服务端 xray-edge 完成，客户端只保留"面板→xray-user"一条链路的 Top1+备用。
（修正 §1 picker 描述：探测的是面板下发的 vless 入口 IP 列表，不是上游池。）

## 4. 关键实现要点与坑

| 点 | 做法 |
|---|---|
| sing-box 嵌入 | 用 sing-box 的 Go library 模式（同仓 `library` 子模块），单进程单二进制；版本 pin |
| TUN 提权 | manifest 直接 requireAsAdministrator，避免 helper service；点"连接"才弹 UAC |
| DNS 泄漏 | DNS 规则强制走 proxy + tun 内置 DNS 监听；测试：DNS 泄漏检测页（§测试矩阵） |
| 防自我嵌套 | 面板/订阅域名 → direct（刷新订阅不能套自己） |
| token 更换 | UI 设置页重粘 → DPAPI 覆写 → 立即重拉 sub |
| 自更新 | update.json 由面板静态托管；下载后 sha256 校验；Inno `/SILENT` 替换需提权（沿用 manifest） |
| 日志 | 本地文件轮转 5MB×3，供用户报障；脱敏（不写 token 明文） |

## 5. 测试矩阵（对应 phase2 文档 §6 的实现落点）

| 层 | 对象 | 手段 |
|---|---|---|
| 单元 | `cfggen` | golden 快照：NodeSet+策略 → config.json |
| 单元 | `subparse`(共享包) | 复用 ingest fixture（两 sub 脱敏样本） |
| 单元 | `cred` | DPAPI 加解密 roundtrip（CI 跳过，仅 Windows runner） |
| 集成 | 全链路 | 起 Phase1 compose → 客户端 token 用测试用户 → 断言出口 IP=上游池 |
| 平台 | Win10/11 | UAC/DNS 泄漏/杀软扫描（phase2 §5 风险表） |
