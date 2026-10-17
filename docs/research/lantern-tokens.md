# Lantern 设计语言 Tokens(提取自 lantern.io/zh,2026-10-17)

提取方法:headless Chrome 1440px 全页截图 → ffmpeg raw RGB 像素聚类 + 行长连续扫描
(styled-components 走 CSSOM 注入,dump-dom 取不到 CSS,故像素级提取)。
坐标映射注意:read 工具显示图 = 原图 ×2/3。

## 色彩

| token | 值 | 用途(原站) |
|---|---|---|
| `--ink` | `#012d2d` | 标题/正文强调/按钮填充(深墨绿黑) |
| `--ink-soft` | `#002828` | 聚类主深色(与 ink 同族,按钮 hover 可用) |
| `--teal` | `#006163` | 小节标签/链接/图标描边 |
| `--cyan` | `#00bcd4` | 插画海色/强调图形 |
| `--green` | `#3ab853` | 插画陆色 |
| `--yellow` | `#ffc107` | 顶部公告条/星标 |
| `--yellow-bright` | `#ffec01` | 插画星 |
| `--bg` | `#ffffff` | 主背景 |
| `--bg-alt` | `#f8fafb` | 交替 section 背景 |
| `--card` | `#ffffff` | 卡片背景 |
| `--hairline` | `#e8e8e8` | 卡片/分隔细边 |
| `--muted` | `#60676d` | 次级正文灰(卡片内采样) |

## 字体

- 拉丁:**Urbanist**(300-700,Google Fonts link 实证);等宽 IBM Plex Mono
- 中文:原站无 CJK webfont → 系统栈 `-apple-system, "PingFang SC", "Noto Sans SC", sans-serif`
- 我们的 zero-build 约束:**不引外部 font link**(离线/封锁环境),拉丁回落系统 sans,
  视觉差异可接受;如后续要精确,自托管 woff2 进 embed

## 形状与组件

- 按钮:全胶囊 `border-radius: 999px`;主按钮 = ink 底 + 白字;高约 56-64px,左右 padding ≥40px;
  字重 600;无阴影或极轻
- 卡片:白底 + `1px solid var(--hairline)`;圆角 ~14-16px;内 padding ~28-32px;无阴影(靠边线)
- 公告条:yellow 底 + ink 字,全宽,~40px 高
- section 节奏:垂直 padding ~96-120px;交替白 / bg-alt
- 小节标签:teal、13px、600、letter-spacing ~0.08em
- H1 ~56px/700;H2 ~34px/700;正文 16-17px/1.75;muted 色
- 链接:teal + 下划线(卡片内"了解更多 →"为 ink 下划线)

## 插画语言

扁平矢量:地球(cyan 海 + green 陆 + 黑描边阴影)+ 黄色四角星散布 + 白色虚线航线。
**不复刻原站素材**(版权),自绘同语言 SVG(星/弧线路径 motif)用于落地页 hero。

## 交互

- 原站为营销页:scroll 静态 + hover 轻反馈(按钮/卡片微变);无 scroll-driven 机关
- 我们的功能页换肤只借视觉语言,交互模型不变(表单/HTMX 行为保持)
