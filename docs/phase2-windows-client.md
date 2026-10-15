# Phase 2：Windows 自研轻量客户端开发方案

> 定位：单 exe，双击即用。内嵌 sing-box 内核 + TUN 全系统接管，
> 用户只需粘贴一次我们面板发给的客户端 token，后续自动拉取节点、自动刷新。
> 目标工期：**10 个工作日（1 人）**，MVP 先不做 Android/Mac。
> **实现级设计（模块/接口/时序/配置生成/坑）见 `docs/client-impl.md`。**

## 1. 技术选型

| 决策点 | 选择 | 理由 |
|---|---|---|
| 内核 | **sing-box**（以 Go 库方式进程内嵌入） | 与上游节点协议兼容（vless/vmess/trojan/ss/hysteria2），TUN 在 Windows 上成熟；用 sing-box 的 library 子项目，**不产生第二个进程**，守住"单 exe" |
| GUI | **Wails**（WebView2） | 系统托盘 + 状态窗口最简单；Win10/11 自带 WebView2，无依赖分发 |
| 语言 | Go（全栈与 ingest 一致） | 交叉编译单文件，团队一条语言线 |
| 敏感数据 | 系统凭证存 **DPAPI**（%LOCALAPPDATA%\vpnmaster\creds.bin） | token 不落明文，不上传 |
| 自更新 | 面板下发 update.json（版本/URL/sha256），Inno Setup 静默安装 | 与自托管面板同源，无需第三方 CDN |
| 签名 | 申请 OV 代码签名证书（前置 1-2 周办理） | 否则 SmartScreen 会吓跑普通用户 |

## 2. 客户端架构

```
┌──────────────────── vpnmaster.exe (单进程) ────────────────────┐
│  Wails UI (托盘 + 状态窗: 开关/流量/延迟/版本)                     │
│  ┌────────────┐  ┌──────────────┐  ┌────────────────────┐     │
│  │ 凭证管理    │  │ 订阅拉取      │  │ 路由策略(内置默认)    │     │
│  │ DPAPI 存token│ │ GET sub?tok  │  │ LAN/面板域名→直连     │     │
│  └────────────┘  └──────┬───────┘  │ 其余→代理(防 DNS 泄漏) │     │
│                         ▼          └────────────────────┘     │
│              ┌──────────────────────┐                          │
│              │ sing-box(进程内)      │  TUN 接口(需管理员)        │
│              │ outbound: vless+ws   │                          │
│              └──────────┬───────────┘                          │
└─────────────────────────┼──────────────────────────────────────┘
                          ▼
        xray-user(Marzban) → xray-edge → 上游池（架构不变）
```

### 关键机制

1. **首次使用**：Web 面板（Marzban 用户页）显示"客户端专用 token"，
   用户复制粘贴进客户端 → DPAPI 加密保存 → 结束。
2. **节点选择**：客户端不做节点池管理（那是 xray-edge 的事），
   只连 xray-user 的 ws 入口；拉订阅后**测延迟取 Top1**，失败自动切下一个，
   每 5 分钟刷新一次订阅（跟随上游摘除/回池结果）。
3. **TUN 接管**：sing-box TUN 模式（系统虚拟网卡），DNS 走代理防泄漏，
   LAN/面板域名/sub 域名走直连（避免刷新订阅时自己套自己）。
4. **权限**：`requestedExecutionLevel=requireAsAdministrator` 清单，
   点"启动"弹一次 UAC；MVP 不做提权 helper service（后置优化）。
5. **流量看板**：轮询 Marzban REST API（user token）显示本月流量/剩余，
   与 §6 L6 长稳对账口径一致。

## 3. 仓库结构（并入 vpn-master）

```
vpn-master/
  services/ingest/...          # 已规划
  client/windows/
    app/        wails.go, ui 模板
    internal/
      kernel/   sing-box 嵌入封装 (启停/配置注入/统计读取)
      sub/      订阅拉取与解析 (vless/vmess/trojan/ss)
      route/    默认路由策略模板
      cred/     DPAPI 存储
      update/   自更新
    installer/  Inno Setup 脚本
  deploy/dokploy/...          # update.json 由面板静态托管
```

## 4. 任务拆解（10 天）

| 天 | 交付物 | 验收标准 |
|---|---|---|
| D1–2 | 工程骨架：Wails 托盘/状态窗、DPAPI 凭证存取 | 粘贴 token 后重启仍保留；删文件即重置 |
| D3–5 | sing-box 内核嵌入 + sub→配置生成 + TUN 启停 | 点"启动"后浏览器经 TUN 走代理，出口 IP=上游池 IP；关闭后恢复正常 |
| D6–7 | 路由策略（LAN/直连白名单/DNS 防泄漏）+ 流量看板 | DNS 泄漏检测通过；局域网设备互访不代理 |
| D8 | 自更新 + 安装器 + 代码签名 | 旧版点"检查更新"完成静默升级 |
| D9–10 | 打磨 + UAT（Win10/Win11 各一） | 开关无感知切换 <2s；空闲 CPU <2%；跑 8h 无断流 |

**前置（不占 10 天，先启动）**：OV 代码签名证书办理（1–2 周）；
**"客户端 token" 零后端改动**：直接用 Marzban 的用户订阅 token（同一个 token 既是
`/sub/{token}` 拉节点，又是 `/api/users?token=` 拉流量的鉴权），只需运营在
Marzban 用户页把该 token 展示/复制给用户。

## 5. 风险与对策

| 风险 | 对策 |
|---|---|
| TUN 与 Windows Defender 等杀软冲突 | 常见误报集中在 TUN 驱动行为；MVP 用 sing-box 成熟 TUN 实现，发布前过一遍主流杀软扫描；签名可缓解 |
| sing-box library 版本 API 变动 | 锁版本，升级走回归（§测试矩阵） |
| SmartScreen（未签名期） | 发布说明引导用户点"仍要运行"；签名证书到位后自动消除 |
| 用户机无管理员权限 | MVP 明确说明需管理员；后续版本做提权 helper（不进 MVP） |

## 6. 测试矩阵

- **单元**：sub 解析（复用 ingest 的 fixture，两种链接格式）；配置生成快照测试
- **集成**：容器起 xray-user+edge，客户端连真链路；kill 上游节点观察自动切换
- **平台矩阵**：Win10 LTSC / Win11 家庭版 / Win11 企业版（组策略环境）各一
- **安全**：DNS 泄漏（nextdns leak test）、TUN 重连后 DNS 恢复、杀软兼容扫描
- **性能**：iperf3 对比直连损耗 <10%；空闲 CPU/内存基线；24h 长跑
