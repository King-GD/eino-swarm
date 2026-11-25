# FOFA 风格标准（测绘控制台）

本文是 `palette: fofa` 的视觉标准。执行者（人或 agent）按本文改代码。
参照样稿：[design/fofa-console-v4.html](design/fofa-console-v4.html)（页面）与
[design/fofa-components.html](design/fofa-components.html)（组件对照），浏览器直接
打开，可切换对话 / 空状态 / 组件 / 深浅色。

## 1. 来源与边界

- 品牌依据是 FOFA 主站：`fofa-frontend-v5/app/styles/var.scss` 的 `html.dark` /
  `html.light`、首页搜索框 `components/Search/index.vue`、`styles/common.scss`
  的表格与弹窗。
- **不参照** FOFA 站内的 AI 对话页（`components/AISearch/*`）。它不是品牌基准。
- 只改 `fofa` 配色。`zwai` 配色的外观必须保持不变。
- 仓库规则照旧：颜色只写在 `frontend/src/styles/palettes.css` 的变量里，组件只引用变量，
  不写 hex；缺变量就加变量（见 `AGENTS.md` 前端规则）。
- 配色不从外部改写组件。`ui/*` 组件读带回落值的组件 token（如
  `rounded-[var(--radius-surface,var(--radius-md))]`）：zwai 不定义这些 token，
  回落值即原样式；fofa 在自己的 token 块里赋值。调用方传入的 `className`
  仍由 tailwind-merge 覆盖组件默认值，两种配色下都生效。

## 2. 设计原则

1. **夜空底，不是灰底。** 深色基底是蓝黑 `#040c1f`，所有中性色都带蓝相（色相 202–225）。
   不出现纯灰（色相 240 / 饱和度 0–6% 的 zinc 系）。
2. **青色是信号。** 青色只表示"活的、可操作的、当前的"：主按钮、链接、焦点环、
   选中项标记、运行中状态。不用青色铺大面积背景。
3. **渐变只给品牌时刻。** 蓝→青渐变（`--brand-from` → `--brand-to`）只用于：
   输入框描边、空状态大标题、上下文进度条、Logo。其他地方不用渐变。
4. **文字三级，正文不是纯白。** 标题和加粗用最亮的一级；正文用蓝灰；辅助信息再降一级。
5. **用细线分层。** 层级靠 1px 细线和明暗差，不靠阴影、不靠大块灰底。
6. **状态色各有含义，互不借用。** 运行中 = 青；等待人工 = 橙；完成 = 绿；失败 = 红。
   不使用紫色。
7. **组件就是 FOFA 的组件。** 不新造控件形态：按钮、输入、页签、表格、弹窗、标签、
   下拉都按 FOFA 主站 element-plus 的规格改 shadcn 组件（第 6 节）。

## 3. 颜色令牌

值写成 `index.css` 用的 HSL 三元组，括号内是对应 hex。
"新增"表示当前 `index.css` 还没有这个变量，需要加到 `@theme`（`--color-*`）和
`zwai` 的 `:root` / `.dark`（取值与现状等价，保证 zwai 不变）。

### 3.1 深色 `html[data-palette="fofa"].dark`

| 变量 | 值 | 用途 | 现状 → 改动 |
|---|---|---|---|
| `--background` | `222 77% 7%`（#040c1f） | 对话区画布 | 不变 |
| `--sidebar` | `222 77% 5%`（#030a17） | 侧栏、图标栏，比画布深一级 | 原同画布，**改** |
| `--card` / `--popover` | `222 50% 10%`（#0d1527） | 浮层、卡片、输入框底 | 不变 |
| `--secondary` / `--muted` | `221 42% 14%`（#151f34） | 次级块、代码底、表头 | 不变 |
| `--accent` / `--sidebar-accent` | `221 42% 14%` / `221 33% 20%`（#222d44） | 悬停、选中底 | 不变 |
| `--border` / `--sidebar-border` | `222 25% 18%`（#232a3a） | 分隔细线 | 原 `220 18% 23%`，**改**（更细） |
| `--input` | `220 18% 23%`（#303745） | 输入控件边框，比分隔线强 | 原 `221 42% 14%`，**改** |
| `--foreground` | `0 0% 100%` | 标题、加粗、用户指令 | 不变 |
| `--content-foreground` | `210 31% 74%`（#a7bcd1） | Markdown 正文 | **新增** |
| `--muted-foreground` | `210 12% 56%`（#7e8e9e） | 时间、耗时、说明、占位符 | 原 `210 31% 74%`，**改** |
| `--sidebar-foreground` | `210 31% 74%` | 侧栏条目 | 不变 |
| `--primary` / `--ring` | `180 96% 48%`（#05f2f2） | 主按钮、焦点、选中标记 | 不变 |
| `--primary-foreground` | `222 77% 7%` | 青底上的字 | 不变 |
| `--link` | `180 96% 48%` | Markdown 链接 | **新增** |
| `--accent-text` | `180 96% 48%`（= `--primary`） | 主色文字：选中页签、标签、悬停字 | **新增** |
| `--dropdown` | `223 38% 15%`（#171f33） | Select / 下拉菜单底 | **新增** |
| `--tag` | `209 78% 9%`（#051728） | 标签底（FOFA `--color-tag-bg`） | **新增** |
| `--brand-from` / `--brand-to` | `213 99% 57%` / `180 96% 48%` | 品牌渐变 | 不变 |
| `--running` | `180 96% 48%` | 运行中 | 原橙 `38 92% 50%`，**改** |
| `--ask` | `23 91% 56%`（#f57527） | 等待人工、关键词高亮 | 原青，**改** |
| `--done` | `156 49% 49%`（#3fb98a） | 完成 | 原 `142 71% 45%`，**改** |
| `--failed` | `0 87% 69%`（#f56c6c） | 失败 | 原 `0 84% 60%`，**改** |
| `--destructive` | `0 84% 60%` | 删除等危险按钮底色 | 不变 |

### 3.2 浅色 `html[data-palette="fofa"]`

| 变量 | 值 | 用途 | 现状 → 改动 |
|---|---|---|---|
| `--background` | `210 20% 98%`（#f9fafb） | 对话区画布 | 不变 |
| `--sidebar` | `0 0% 100%`（#ffffff） | 侧栏，与 FOFA 白色导航一致 | 原同画布，**改** |
| `--card` / `--popover` | `0 0% 100%` | 浮层、卡片 | 不变 |
| `--secondary` / `--muted` / `--accent` | `204 20% 95%`（#f0f3f5） | 次级块、悬停 | 不变 |
| `--border` | `224 15% 86%`（#d5d8e0） | 分隔线 | 不变 |
| `--sidebar-border` | `223 18% 92%`（#e8eaef） | 侧栏分隔 | 不变 |
| `--foreground` | `234 30% 18%`（#21243d） | 标题、加粗 | 不变 |
| `--content-foreground` | `202 12% 36%`（#515f67） | Markdown 正文 | **新增** |
| `--muted-foreground` | `210 8% 46%`（#6c7680） | 辅助信息 | 原 `202 12% 41%`，**改** |
| `--primary` / `--ring` | `180 95% 37%`（#05b5b6） | 主按钮、焦点 | 不变 |
| `--primary-foreground` | `222 77% 7%` | 青底上的字 | 不变 |
| `--link` | `181 94% 26%`（#047f80） | 链接文字 | **新增** |
| `--accent-text` | `181 94% 26%`（= `--link`） | 主色文字（浅色下不用 #05b5b6 写字） | **新增** |
| `--dropdown` | `0 0% 100%` | Select / 下拉菜单底 | **新增** |
| `--tag` | `204 20% 95%`（#f0f3f5） | 标签底 | **新增** |
| `--running` | `180 95% 37%` | 运行中 | 原橙，**改** |
| `--ask` | `23 77% 49%`（#e0661c） | 等待人工 | 原青，**改** |
| `--done` | `155 72% 30%`（#158255） | 完成 | 原 `142 76% 36%`，**改** |
| `--failed` | `0 72% 51%` | 失败 | 不变 |

### 3.3 偏离 FOFA 原值的地方

- 浅色 `--muted-foreground`：FOFA 三级字 `#9b9ea7` 在 `#f9fafb` 上对比度约 2.6:1，
  不满足 WCAG AA。改用 `#6c7680`（约 4.6:1）。
- 浅色链接：FOFA 主色 `#05b5b6` 做白底文字约 2.6:1，链接改用加深的 `#047f80`；
  按钮底色仍用 `#05b5b6`，字用深蓝 `--primary-foreground`。
- 浅色正文：用比 FOFA 二级字 `#5d6d76` 略深的 `#515f67`，保证正文比辅助信息深。

### 3.4 其余变量

`--chart-*`、`--syntax-*`、`--diff-*`、`--find*`、`--nuwa-*` 维持现状。
它们已在蓝青色系内。`--file-folder` 例外：文件树的文件夹图标改用
`--content-foreground`，不再用高饱和亮蓝；其他 `--file-*` 保持。

## 4. 用色规则

| 场景 | 用 | 不用 |
|---|---|---|
| Markdown 正文 | `--content-foreground` | `--foreground` |
| Markdown 标题、`strong`、表头 | `--foreground` | 正文色 |
| Markdown 链接 | `--link`，悬停 `--foreground` | 继承正文色 |
| 引用、时间戳、耗时、工具参数 | `--muted-foreground` | — |
| 运行中的点、进度指示 | `--running` | — |
| 进行中的文字（含浅色） | `--accent-text` | 用 `--running` 写字（浅色对比不足） |
| 待确认问题 | 2px `--ask` 左边条 + `--ask` 标题 | 青色或透明底 |
| 选中会话 | `--sidebar-accent` 纯色底 | — |
| 主按钮 | `--primary` 底 + `--primary-foreground` 字 | 渐变 |
| 输入区描边 | `--brand-from` → `--brand-to` 渐变 | 纯色 |
| 发送按钮 | `--primary` 底 + `--primary-foreground` 字 | 渐变 |
| 表格行悬停 | `--accent`（FOFA `--select-hover`） | 青色透明叠加 |

## 5. 分阶段执行

每阶段单独提交，阶段之间不混改。

### 阶段 1：颜色（只改颜色，不动布局）

状态：已实现（2026-11-24）。

范围：
- `frontend/src/index.css`：按第 3 节改 `fofa` 两个块；新增 `--content-foreground`、
  `--link`，并在 `@theme` 加 `--color-content-foreground`、`--color-link`；
  在 `zwai` 的 `:root` / `.dark` 给两者写入与 `--foreground` 相同的字面值。
- `.md` 工具类：正文 `color: hsl(var(--content-foreground))`；`h1–h6`、`strong`、`th`
  用 `--foreground`；`a` 用 `--link`。`.md` 在多处使用（transcript、agent-transcript、
  file-preview、knowledge-page、memory-panel、tool-result、document-preview 等），
  逐处目视确认。
- 状态点、ask 卡片等只要已引用 `--running` / `--ask` 变量，改值即可；
  若有组件绕过变量，改成引用变量。

不做：间距、字号、圆角、边框粗细、组件结构、消息形态。

验收：
- `npm run lint`、`npm test` 通过；`frontend/e2e/settings.spec.ts` 的配色切换用例通过。
  该用例断言 `--sidebar` 与 `--background` 不相等、`--content-foreground` 与
  `--foreground` 不相等，防止配色回退成"只换色值"的平面重着色。
- `zwai` 浅色/深色截图与改动前一致。
- `fofa` 浅色/深色各截一张对话页，与样稿的颜色关系一致（侧栏比画布深/白、
  正文蓝灰、运行中青、待确认橙）。
- 文档：`docs/CONFIG.md` 的 `palette` 一行描述与第 2 节一致；`CHANGELOG.md` 记 Changed。

### 阶段 2：组件形态（按样稿）

状态：已实现（2026-11-24）。2026-11-25 起改为组件 token（见 6.3），
不再有层外的 `html[data-palette="fofa"]` 覆盖规则；`zwai` 的外观不变。

- 用户消息：保留原来的右对齐气泡，不改结构，不提 "指令" 标签。
- 执行过程：左侧刻度虚线时间轴（`[data-testid="single-work"]::before`），
  日志行右侧等宽时间戳（`data-tool-time`，取自 `Block.at`，只显示时分，
  `formatClockTime`）。工具块目前没有完成时间，因此不显示耗时；不要用相邻
  事件推算。
- 表格：`.md` 内只留横向细线，表头无底色、左右零内边距，列间留 12px。
- 当时的选中会话样式为 12% 青色渐隐底，现采用第 4 节的纯色底规则。
- 圆角分级：控件 4px（`--radius-md`，由现有 `--radius: 0.375rem` 派生）、
  面板 8px（fofa 块内 `--radius-lg: 0.5rem`）、输入框 15px（既有
  `[data-composer-box]` 规则）、弹窗 16px（`[role="dialog"]` /
  `[role="alertdialog"]`）。不改全局 `--radius`。
- 发送按钮：`data-send-button` 在可用状态使用 `--brand-from` → `--brand-to`
  渐变；禁用状态保留 50% 透明度，不伪装成可点击。

验收：`npm test` 覆盖日志时间戳；`settings.spec.ts` 断言 fofa 下
控件的计算圆角小于 zwai、面板 `--radius-lg` 为 0.5rem。

其中 `.md` 表头无底色、发送按钮渐变、弹窗 16px 三项已被第 6 节（阶段 5）取代；
日志时间轴继续有效。

### 阶段 3：数据呈现

- IP、网段、端口、域名做成实体标记：等宽字体 + 实体底色（深色 `#051728`，浅色 `#eef7f7`）
  + 青色细边。需新增 `--entity`、`--entity-border` 变量。
- FOFA 查询语句卡：字段名青色、运算符弱化、字符串橙色、逻辑符蓝色；带复制和
  "在 FOFA 搜索"按钮，下方统计行。
- 关键词高亮用 `--ask` 同色系。
- 识别实体与语句不得依赖示例文本（见 `AGENTS.md` 非协商条款 2），按语法结构识别。

### 阶段 4：空状态

- FOFA 首页大标题在深浅模式下共用 VIP 的 90° 渐变，亮青 `#05f2f2` 过渡到亮蓝 `#3b82f6`。系统强制颜色模式使用系统文字颜色。
- 起步建议用 FOFA 热门搜索式的标签（`--color-tag-bg` 底、主色字），不做卡片，
  也不加网格或刻度环等自造纹理。
- 输入框仍停在底部（见第 6 节输入区）。

### 阶段 5：shadcn 组件按 FOFA 规范重绘

状态：标准与样稿已定（2026-11-24），代码未实现。规格见第 6 节。

## 6. shadcn 组件 → FOFA 组件

FOFA 的界面全部由 element-plus 组件组成。fofa 配色不发明新形态，而是把
`frontend/src/components/ui/*` 的 shadcn 组件改成 FOFA 的对应组件；只作用于
`html[data-palette="fofa"]`，zwai 不变。逐项样张见
[design/fofa-components.html](design/fofa-components.html)，应用后的页面见
[design/fofa-console-v4.html](design/fofa-console-v4.html)。

### 6.1 总则

- **圆角阶梯**：控件 4px（`--radius-md`）、表单与菜单 6px（`--radius`）、
  内容模块 8px（`--radius-lg`）、输入区 15px、弹窗 20px。
- **焦点**：FOFA 全局 `outline: none`，焦点靠边框变主色。fofa 下输入类控件
  `ring-0` + `border-primary`；按钮保留 1px 主色内描边（可访问性所需的最小偏离）。
- **浅色主色文字**：`#05b5b6` 在白底约 2.6:1。fofa 浅色下，主色文字（链接、
  选中页签、标签、悬停文字）用 `--link` `#047f80`；主色只做底色、描边和指示条。
  新增 `--accent-text`：深色 = `--primary`，浅色 = `--link`，`@theme` 同步
  `--color-accent-text`。
- **新增 `--dropdown`**：`#171f33`（深）/ `#ffffff`（浅），下拉与 Select 内容底。
- **新增 `--tag`**：`#051728`（深）/ `#f0f3f5`（浅），标签底（FOFA `--color-tag-bg`）。
- **标签不做透明底**：状态只换字色，不叠 `bg-ask/15` 这类透明底。

### 6.2 对照表

| shadcn 组件 | FOFA 依据 | 规格 |
|---|---|---|
| `Button` default | `.el-button--primary` / `.smart-recommend-btn` | 底 `--primary`、字 `--primary-foreground`、4px；hover 透明度 .9（现有 `bg-primary/90` 等价）；禁用 50%。只需去掉 2px 焦点外环。 |
| `Button` outline / secondary | `.el-button` 默认、消息框默认按钮 | 透明底、1px `--border` 边框、`--secondary-foreground` 字；hover 边框与字变 `--accent-text`，**不加底色**。 |
| `Button` ghost / link | `.el-button.is-link` | 无底无边、`--muted-foreground` 字；hover `--accent-text`，不加底色。 |
| `Button` destructive | `.el-button--danger` | 底 `--destructive`、白字、4px；hover 透明度 .9。 |
| `Input` / `Textarea` | `.el-input` | 6px；底 `--card`；边框 `--input`；字 14px `--foreground`；占位符 `--muted-foreground`；焦点只把边框变 `--primary`，无 ring；禁用 50%。 |
| `Select` / `DropdownMenu` | `.el-select-dropdown` | 内容底 `--dropdown`、边框 `--border`、6px、阴影；项高 32px、`--secondary-foreground` 字；hover 底 `--accent`（即 FOFA `--select-hover`）；选中字 `--accent-text` 并带勾。 |
| `Tabs` | `.el-tabs` | 去掉 shadcn 的灰底胶囊容器（透明、无内边距）；项 `--secondary-foreground`，hover/选中 `--accent-text`；选中项底部 2px `--primary` 指示条；不画 header 底线。 |
| `Table`（Markdown 与面板） | `.el-table` | 表头底 `--secondary`（level5）、`--secondary-foreground` 字、12px/400；行透明；单元格下边框 `--border`；行 hover 底 `--accent`；单元格内边距 9px 12px。 |
| `Badge` | `--color-tag-bg` 标签 | 底新增 `--tag`（`#051728` / `#f0f3f5`），无边框、4px、高 20px、11px；默认字 `--accent-text`；变体只换字色（ask / done / failed / muted）。 |
| `Dialog` | `.el-dialog` | 20px；底 `--card`；标题 18px `--foreground`，头部下边框 `--border`；正文 14px；底部主按钮通栏 42px、16px 字、4px；遮罩深色 `rgba(4,10,22,.8)`、浅色 `rgba(255,255,255,.72)`。 |
| `Tooltip` | `.el-popper.is-dark` | 黑底白字、12px、4px、内边距 5px 9px、最大宽 350px；两个主题相同。 |
| `Switch` | `.el-switch` | 标准轨道 36×20 全圆角，关闭用蓝灰 `--switch-track-off`，开启用 `--primary`。滑块为白色，标准尺寸 16px。 |
| Toast | `.el-message` | 底 `--card`、6px、轻阴影、`--secondary-foreground` 字；状态只染图标。 |
| Composer | 首页搜索框 | 15px、96° 渐变描边（深色 青→蓝 / 浅色 蓝→青）、`--card` 底、内文 15–16px；后缀控件为 link 按钮，排在右下角；发送 = 小号主按钮，运行中换成描边停止按钮。 |
| 分段控件 | `.search-mode-switch` | 轨道 `--secondary` 全圆角、内边距 2px；项 12px 全圆角；选中底 `--card` + `--accent-text`；当前模式主色底 + 深字。 |

### 6.3 实现落点

- `frontend/src/styles/palettes.css`：zwai 与 fofa 的全部 token，含组件 token
  （`--radius-surface`、`--field-*`、`--menu-*`、`--tabs-*`、`--dialog-*`、
  `--badge-*`、`--tooltip-*`、`--toast-*`、`--switch-*`、`--composer-*`、
  `--work-rail*`、`--tool-time-display`、`--md-*`、`--chart-tip-*`、上下文用量浮层的
  `--hint-*`、等待回答卡片的 `--ask-card-border`）。只有首页标题
  文字渐变是 `@layer components` 里的一条 fofa 规则。
- `ui/*`、`toast-stack`、`composer`、`tool-row`、`work-fold`、`lib/glass.ts`
  和 `.md` 表格读这些 token，回落值等于 zwai 原样式。
- 被本节取代的阶段 2 规则：`.md` 表头无底色 → FOFA 表头带；发送按钮渐变 →
  实心主色；弹窗 16px → 20px。
- 沿用用户气泡、日志时间轴与等宽时钟。

### 6.4 验收

- `npm run lint`、`npm test`；新增 fofa 组件规格断言：按钮 4px、输入框 6px、
  弹窗 20px、标签无透明底、页签无胶囊容器、表格表头有 `--secondary` 底。
- `frontend/e2e/settings.spec.ts` 的配色切换用例仍通过；zwai 深浅色截图不变。
- fofa 深浅色各截图：设置弹窗（Dialog + Input + Switch + 主按钮）、模型下拉
  （Select）、右栏表格与页签、输入区后缀。

## 7. 已决定

- 默认配色保持 `zwai`，不改 `DefaultPalette`。`fofa` 由用户在 Settings →
  General → Color theme 里选择。
