# ADR-0003: 交互载体 = vanilla JS + Bootstrap 组件（不引入声明式 JS 库）

## 背景

双端同修（用户门户 + admin 5 页）需要一套 CRUD 交互：确认弹窗、操作反馈 toast、行内编辑局部刷新、表格搜索/过滤/分页、轮询。零构建约束（ADR-0002 同源）排除 Node/npm 管线。候选：A) 纯 vanilla JS + Bootstrap modal/toast；B) vendored htmx 单文件声明式驱动。

## 决策

选 **A**：确认弹窗用 Bootstrap modal、反馈用 Bootstrap toast、局部刷新手写 fetch 胶水，不引入 htmx/Alpine 等声明式库。胶水以共享形式落在 `services/webui`（vendored `bootstrap.bundle.min.js` 已含 modal/toast 实现；新增一个 `wm-ui.js` 提供 `toast()/confirmAction()/fetchJSON()` 三件套，两服务共用）。

## 后果

- 正面：零新依赖；交互行为完全可控可测（web_test 可断言 DOM 契约）；与 ADR-0001"用成熟框架但别多一层魔法"的取向一致。
- 负面：每页的 fetch→更新 DOM→toast 胶水要手写，admin CRUD 页面 JS 量比 htmx 方案多；胶水质量由 `wm-ui.js` 共享层担保，禁止各页复制粘贴。
- 约束：Bootstrap JS 组件（modal/toast）依赖已 vendored 的 bundle，无额外资产。
