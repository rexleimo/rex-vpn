# ADR-0004: admin CRUD 走完整后端化（服务端搜索/分页 + RESTful 端点）

## 背景

admin 现状交互缺陷（删除无确认、操作无反馈、编辑靠删后重加、无搜索/过滤/分页）。范围决策"双端同修"（见 CONTEXT.md）。在 API 面上有两个选项：A) 最小后端改动（只加编辑端点，搜索/分页客户端做）；B) 完整后端化（服务端搜索/分页/过滤 + RESTful 全套）。用户 Q3 明确拍板 **B**——为几千节点规模做准备。

## 决策

1. **新增/变更端点（ingest）**：
   - `POST /admin/upstreams/update`（改名/URL，编辑不再删后重加）
   - `GET /api/nodes?q=&status=&page=&size=`（服务端搜索+状态过滤+分页，响应含 `total`）
   - `GET /api/events?before=&limit=`（时间游标分页）
   - `GET /api/users?q=`（Marzban 用户搜索透传）
   - 操作类端点（evict/recover/fetch/delete/update）对 `HX-`? 否——对 `Accept: application/json` 或 `fetch` 请求返回 JSON `{ok, error, ...}`，表单提交保持 303 重定向兼容
2. **契约兼容**：现有脚本（chaos.sh / load-50.sh / e2e-full.sh）依赖 `/api/nodes`、`/api/events` 的字段名与"无参数=全量"语义——新参数全部可选，默认行为不变（page=1, size=全量），分页字段以新增 key 方式加入。
3. **门户侧**：登录失败改为页内渲染 alert（保留输入），复制/更换 token/下单走 toast；门户 API 契约不变。

## 后果

- 正面：规模上限解除；分页/搜索语义在服务端统一，前端只渲染；未来接监控/CLI 复用同一 API。
- 负面：ingest web_test 与三个验收脚本需要同步扩展（新增分页断言）；后端工作量大于 A。
- 风险：`/api/*` 是本仓库对外契约的一部分（客户端 Phase 2 会用），字段演进必须保持向后兼容——新增字段自由，改语义需版本化。
