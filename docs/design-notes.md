# SeekWell 管理界面改版设计说明

日期：2026-11-12。范围：`apps/web/` 视觉与组件层；不改数据面。

## 方向

Q1 选 B：初版为单一深色开发者工具风。近黑底、面板分层、1px 微描边、弱阴影，品牌绿只做唯一强调色；数字、ID、耗时统一等宽 + `tabular-nums`。**该「单一深色」结论已被 Issue #22 覆盖：现支持跟随系统 / 浅色 / 深色三态切换。** Q2 选 A：18 个图标位统一用彩色 emoji，且每个 emoji 都与可见文字同屏，装饰性图标对读屏隐藏。

## 参考与取舍

| 参考（已核实可访问） | 借鉴 | 拒绝 | 原因 |
| --- | --- | --- | --- |
| [Linear](https://linear.app) | 低对比背景层级、细描边、紧凑信息密度、键盘焦点清晰 | 其品牌紫与营销式大留白 | 本项目是本地运维工具，信息优先；唯一强调色保持品牌绿 |
| [Vercel Geist](https://vercel.com/geist) | 近黑底、等宽数字、克制的按钮与边框体系、系统字体栈 | 其对外字体包 | 本地工具不依赖境外 CDN；初版不做主题切换，Issue #22 已补上系统 / 浅色 / 深色切换 |
| [shadcn/ui](https://ui.shadcn.com) | token 化变量、组件边界清楚、状态与空态成组设计 | 直接引入 Radix/Tailwind 依赖与整套组件代码 | 保持零新增依赖，只取结构与 token 思路 |
| [Radix Colors](https://www.radix-ui.com/colors) | 深色主题的 3 档文本/表面层级与语义色分档 | 复制整套色阶 | 只保留主/次/弱文本 + 成功/警示/危险，避免多色装饰 |
| [Raycast](https://www.raycast.com) | 工具栏动作紧凑、emoji/图标与文字并排、深色浮层 | 其玻璃拟态与动效强度 | 减少视觉噪声，并遵守 `prefers-reduced-motion` |

## 落地

- `theme.css` 是唯一色值与尺寸 token 来源；`base.css` 负责排版、焦点与响应式；组件只消费 token。
- 字体改为系统栈 + 显式 emoji 字体族，删除 Google Fonts；页面不发外部请求。
- 文案集中到 `copy.ts`，产品事实（可用性未验证、计费提示、目录实时但需验证、密钥不回显等）保留并由组件引用。
- 结构拆为 Shell / Overview / ChannelsTable / ChannelDialog / ModelsDialog / SearchPanel / ActivityTable；`main.tsx` 只挂载。
- 保留 800px 图标条与 450px 断点；补 Esc 关闭、焦点圈定、`aria-current` 与 `prefers-reduced-motion`。
- 主题 token 仍集中在 `theme.css`：深色值保持不变，浅色通过 `:root[data-theme="light"]` 覆盖全部颜色与 `color-scheme`；组件 CSS 不出现颜色字面量。
- 主题模式（`system` / `light` / `dark`）保存在 `localStorage['seekwell-theme']`；首屏由 `index.html` 的内联脚本在 React 挂载前解析，避免错误主题闪烁，`system` 模式监听 `prefers-color-scheme` 变化。
- 深色实测对比度：主文本 16.47:1，次文本 9.44:1，弱文本 6.72:1（均对 `#0B0D10`）；主按钮深色字对强调绿 12.37:1。
- 浅色对比度（按 token 值计算，均对 `#F5F7F9`）：主文本 16.95:1，次文本 7.55:1，弱文本 5.29:1；主按钮白字对强调绿 `#0B6B3F` 为 6.58:1；焦点环 `#0A5C36` 为 7.53:1；状态色成功 5.03:1、警告 5.52:1、危险 6.12:1。
