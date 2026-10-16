# ADR-0002: 门户视觉地基 = Bootstrap 5（vendored）+ Apple 风轻量定制

日期: 2026-10-16
状态: 已确认（用户拍板 "A"）

## 背景

现有 UI 被用户判定"基本等于没有 UI"：`services/webui/app.css` 是自写 token（52 行），
reset 仅 `*{box-sizing}` + `body{margin:0}` 两行级别，无 normalize、无成熟组件层、无设计语言参照。
约束：零构建 SSR（无 Node 管线，ADR-0001 保留此约束）；页面为 gin + html/template（迁移后）。
候选路径：A=Bootstrap 5 vendored + Apple 风定制；B=Bootstrap + Material 风；C=不引框架手写完整设计系统。

## 决策

1. 采用 **A**：Bootstrap 5 官方编译产物 **vendored 进仓库**（`services/webui/dist/` 之类静态直拷，禁 CDN），
   获得正经 reset、栅格、表单、按钮、表格、导航组件层。
2. 在其上做 **Apple 风轻量定制**（Bootstrap Theme 变量层，不写重型覆盖）：
   大留白、圆角卡片、系统字体栈、克制配色、层次靠间距而非边框。
3. `--wm-*` 自定义 token 收敛为 Bootstrap 变量的映射层，两服务共享（落点仍是 webui 共享样式层）。
4. 范围假设（Q1 未显式回答，按默认记录）：**先用户门户 4 页**（登录/概览/订阅/购买），
   admin 后台同套视觉作为后续独立工作项跟进，不在第一切片内。

## 后果

- 消灭"原生 HTML 感"的根因（无 reset/组件层），代价是引入一份约 200KB 级 vendor CSS/JS（无构建，静态资产）。
- 视觉验收从"口头审美"变为可比对物：每页改造前后截图（`.shots/`），以 Apple 风基线判过/不过。
- Bootstrap 类名进入模板意味着 `wm-*` 类逐步退役；迁移期间允许两套类共存，门户 4 页完成后删除旧 token。
- 功能回归门槛绑定 ADR-0001：e2e-full 线2（登录→流量→订阅→QR）必须持续绿。

## 验收标准（用户可观察行为）

- [ ] 打开登录/概览/订阅/购买任一页：无原生裸 HTML 观感；栅格对齐、统一圆角/阴影/留白层级、系统字体一致
- [ ] reset 生效：跨浏览器默认样式一致（表单项/标题间距/列表归零），无 UA 默认样式露出
- [ ] 移动端（≤640px）不破版：导航折叠可用、卡片单列、token 输入行纵排
- [ ] 5s 轮询（流量/在线状态/订单）行为与改造前完全一致；e2e-full 线2 绿
- [ ] 仓库内无任何 Node/npm 依赖；Bootstrap 来自 vendored 静态文件（grep 不到 cdn URL）

## 非目标

- 不迁移 React/Vue/Tailwind-build 或任何需要打包前端的方案
- 不改 Marzban 集成、订阅业务逻辑、路由语义（那是 ADR-0001 的事）
- 不在第一切片内做 admin 后台（同套设计系统，后续跟进）
- 不产出 Figma/设计稿交付物——直接以代码+截图为设计载体
