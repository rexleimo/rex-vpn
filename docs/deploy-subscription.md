# 订阅计费上线指南 (Waffo Pancake + authkit + Marzban)

设计文档: `docs/plans/2026-10-17-waffo订阅闭环-购买后自动生成订阅URL.md`
核心原则: **Waffo 是计费真相源, 我方系统是其幂等投影**; token 是购买的产物, 不是前置条件。

## 0. 本地开发(无需任何凭证)

```bash
# deploy/.env 追加:
WAFFO_STUB=1 UC_MARZBAN_STUB=1 WAFFO_PUBLIC_BASE=http://localhost:9200 UC_PUBLIC_BASE=http://localhost:9200
```
stub 收银台在 `/billing` 点「立即支付」即签发**真实 RSA 签名**的 webhook 走同一验签链路,
mock Marzban 记录 provision。重启不丢登录(链接持久在 PG, mock 自愈 token)。

## 1. 数据库 (PostgreSQL)

- compose 部署: `deploy/init/01-usercenter.sh` 首启自动建 `rexvpn` 库; `UC_PG_DSN` 已在 compose 内拼好。
- 裸机(brew PG):
  ```sql
  CREATE ROLE rexvpn LOGIN PASSWORD '...'; CREATE DATABASE rexvpn OWNER rexvpn;
  ```
  authkit 表自动建在独立 schema `authkit`; 业务表(`uc_account_links` / `uc_subscriptions` /
  `uc_waffo_events`)在 `public`, 启动时自动迁移, 无需手工 SQL。

## 2. Waffo 商户后台配置

1. 获取 **RSA 私钥**(商户)与 **webhook 公钥**(平台) → `WAFFO_PRIVATE_KEY` / `WAFFO_WEBHOOK_PUBLIC_KEY`。
2. 创建**订阅商品**(每个套餐一个), 记录 `PROD_xxx`。
3. Webhook 回调地址注册:
   ```
   https://<user-domain>/webhooks/waffo
   ```
   订阅事件全选(12 类) + `refund.succeeded`。签名头缺失/验签失败一律 400, 重复 eventId 直接 2xx 吞掉(幂等)。
4. 先 `WAFFO_ENV=test` + 测试卡号跑通, 再切 `production`。

## 3. 环境变量(生产最小集)

```
UC_PG_DSN=postgres://...@.../rexvpn?sslmode=disable
UC_TENANT_ID=<uuidgen>  UC_ISSUER=rex-vpn  UC_ADMIN_EMAIL/UC_ADMIN_PASSWORD=<种子管理员>
USERCENTER_SECURE_COOKIES=1  UC_PUBLIC_BASE=https://user.<domain>
WAFFO_ENV=production  WAFFO_MERCHANT_ID=  WAFFO_PRIVATE_KEY=  WAFFO_PUBLIC_BASE=https://user.<domain>
USERCENTER_PLANS=轻量 10GB/月:990:10:30,...   # 金额单位: 分
MARZBAN_API_BASE=http://marzban:8080  MARZBAN_ADMIN_USER/PASSWORD=  MARZBAN_INBOUND_IDS=1,2
```
不配 `WAFFO_MERCHANT_ID/PRIVATE_KEY` 且无 `WAFFO_STUB` → 计费整体关闭, 门户退回旧 token 登录(优雅降级)。

## 4. 存量用户迁移

`/bind` 页已从用户流程移除, 但保留为**管理员迁移通道**: 存量用户把旧 token 交给运营,
在 `/admin/users` 补绑(或直接 PG 写 `uc_account_links`)。已绑定用户不受计费开关影响。

## 5. 上线检查清单

- [ ] `GET /billing` 未登录 → 303 `/login`; 登录无订阅 → 显示套餐
- [ ] 测试卡购买 → `/webhooks/waffo` 收到验签通过(日志 `billing event ack`), `uc_waffo_events` 落行
- [ ] Marzban 面板出现 `uc-<acct前缀>` 用户, 到期=本期末; 门户 `/subscription` 显示 URL+二维码
- [ ] 重复投递同一 eventId → 2xx 且不重复 provision
- [ ] Waffo 后台「管理订阅」portal 链接可打开; 取消 → 镜像 `canceling`, 服务保留到期
- [ ] 退款 → `refund.succeeded` → 服务即刻停用(expires=now, 策略可调)
- [ ] 换套餐走 portal(proration), 我方只 resync 投影
- [ ] `docker compose up` 全新卷: rexvpn 库自动建, 服务健康

## 6. 回滚

`WAFFO_STUB` 取消 + 不配凭证 = 计费静默下线; 已购用户链接/镜像不动, 门户照常可用。
