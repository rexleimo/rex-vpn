# ADR-0007: UI 设计语言换肤为 Lantern 风 + 公开落地页(取代 ADR-0002)

## 背景

用户要求 UI 改进, 目标风格 = lantern.io/zh(暖白/深墨绿/teal/黄公告条/全胶囊按钮/发丝边卡片)。
范围决策(用户确认): 换肤现有功能页(门户 + admin)+ 新增一个产品落地页; 技术栈维持
**zero-build Go SSR**(html/template + 内联 CSS, 不引入 Node 构建链)。
提取方法: lantern.io 用 styled-components(CSS 走 CSSOM 注入, dump-dom 拿不到), 故 tokens
经 headless Chrome 截图 + ffmpeg raw RGB 像素聚类/行扫描实证提取(docs/research/lantern-tokens.md)。

## 决策

1. **单一权威**: 全部视觉 tokens 收敛在 `services/webui/theme.css`(go:embed, 两服务共用);
   `--wm-*` 变量名保留(存量模板内联样式依赖), 值全部换 lantern; Bootstrap 5 仍作骨架。
   不引 Urbanist webfont(zero-build + 封锁环境离线可用, 拉丁回落系统栈)。
2. **落地页**: `ingest /` 路由从 302→/admin 改为公开营销页(landing.go 独立文件, 不并入
   pages.go——营销页与 admin 壳职责不同, 且 pages.go 已超行数预算)。文案为自有产品叙述,
   插画为自绘同语言 SVG(不复刻 lantern 素材, 版权边界)。CTA 指向 user-center 门户
   (`PORTAL_URL` env 可配)。渲染走 `webui.WritePage` 而非 `c.HTML`(gin 未 SetHTMLRender,
   c.HTML 会 nil panic——实测踩坑)。
3. **测试弱断言修复**: TestWebUISnapshot 的 "list-group" 历史上只匹中 theme.css 注入文本
   (空库 HTML 无此组件)。改为 seed 一条事件使断言真正作用于 HTML; 注释标注勿删 seed。
4. **响应式修复**: Bootstrap `row g-5` 负边距(-24px)超出 container 默认 px(12px)造成
   全宽度横向溢出(500px 视口实测 scrollWidth=512); 容器统一 px-4 抵消。xs 导航链接折叠
   (d-none d-md-inline)。已知限制: headless Chrome 新无头最小窗宽 500px, 真机 390px
   未截图验证(xs 断点 <768 内布局逻辑一致, 溢出量测已归零)。

## 被拒选项

- **独立前端工程(React/Vite 等)像素级复刻**: 用户选 A(Go SSR); 引入第一条 Node 构建链
  与 zero-build 约束冲突。
- **只换 CSS 不加落地页**: 用户明确要落地页。
- **自托管 Urbanist woff2**: 违反 zero-build 精神且收益(字形微调)不抵体积/管线成本。

## 后果

- 正面: 一处 theme.css 同时换肤 admin + 门户; 落地页公开可分享; 弱断言变实断言。
- 负面: 视觉与 lantern 相似但非像素级(自绘插画/系统字体); ingest `/` 语义变化
  (302→200 公开页)——依赖 "/" 跳 admin 的旧脚本需改指 /admin(本仓库脚本核查无此依赖)。
- ADR-0002(Apple 风)标记被取代; ADR-0003/0004 的 Bootstrap 骨架与交互决策不变。
