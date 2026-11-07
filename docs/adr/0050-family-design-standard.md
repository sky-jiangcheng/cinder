# ADR-0050: 家族设计标准——中性极简色板、母图重画（反白印）、Warden 去漂移、布局减法

状态：**Accepted（2026-11-05，用户在「UI / 主题色 / 图标 / 品牌进化设计，参考 Manus Studio 与 Cue」的四个分叉上逐项拍定；具体色值、母图构造与 Warden 签名色见下）**
日期：2026-11-05
关联：[ADR-0049](0049-brand-restructure-manus-style.md)（品牌重排——本篇**落地其明确留下的两个未决项**：「UI 减法与主次重构」与「母图是否再画」）、[ADR-0037](0037-brand-legibility-and-ui-language.md)（品牌辨识度与界面语法——本篇**修订其「暖纸 editorial」基调与「方边+发丝线就是风格」的母图构造**）、[ADR-0047](0047-resident-agent-separate-host.md)（两个产品各担一件事——Warden 是独立产品，故有独立字标与签名色）、[ADR-0006](0006-scope-freeze.md)（范围冻结与冻结标识）、[ADR-0048](0048-core-contract-version.md)（契约版本——本篇不涨契约）
起始 schema：v28（**本 ADR 不改 schema、不改 MCP 工具面、不涨 `CoreVersion`**）

> **Superseded (部分) by [ADR-0052](0052-the-knot-brand-mark.md)**：决策③（字母 C 反白印）与决策④（Warden 提灯）由 v6 The Knot 取代；色板、标识层冻结、守门架构等其余决策继续有效。
## 背景

### 起因

用户要求：**cinder 与 warden 两仓的 UI、主题色、应用图标、品牌进行进化设计，参考 Manus Studio 与 Cue，最好主题颜色等标准对齐**。ADR-0049 已把「Manus / Manus Studio / Cue」定为本品牌的命名架构原型（引擎 = 母品牌 / 桌面 = 母品牌+限定词 = Cinder Desktop / Agent = 独立词 = Warden），并明确把「UI 减法与主次重构」与「母图是否再画」留成**独立的一篇 ADR**。本篇就是那一篇。

### 事实一：Cinder 已是成熟系统，且与 Manus *官方品牌*同源

Cinder 桌面端的 `web/src/styles/design-system/tokens.css` 是一套完整的「暖纸 editorial」token 系统：奶油纸 `--brand-paper #F5F0E4` + 近黑墨 `--brand-ink #1B1B1B`、暖青铜 accent `#7A3E12`（暗色 `#E09155`）、`data-theme` 三态暗色、WCAG 属性断言（`brandTokens.test.ts`）、`semanticInk.test.ts` 的级联顺序不变量。查证 Manus 官方品牌：墨是**暖近黑 `#34322D`**、底是 **off-white `#F8F8F8` + 白**、字体是 **Libre Baskerville（衬线）+ DM Sans（无衬线）的编辑感搭配**。**Cinder 与 Manus 官方品牌本就同一取向**，且 Cinder 已比 Manus 更进一步——ADR-0037 主动去蓝化，用暖青铜替掉冷 Tailwind 蓝。所以对 Cinder，"参考 Manus"不是换色，是**收敛与精修**。

### 事实二：Warden 是 Cinder *去蓝化之前*的旧快照——真正的缺口

Warden（`src/styles.css`，无框架）还停在冷蓝 accent `#2563eb`（暗色 `#60a5fa`）、纯白卡 `#ffffff`、冷灰暗色 `#17181a`；暗色只靠 `prefers-color-scheme` 被动切换、无 `data-theme` 机制、无任何品牌一致性测试、缺 `danger`/`info` 语义色；图标直接复用 Cinder 的「C」母图（`generate-icons.mjs --agent-repo` 派生）。**只有品牌层（奶油纸色带 + 墨黑）与 Cinder 同源，功能层已系统性漂移**。这就是"标准没对齐"的实体。

### 事实三：Manus 家族可迁移的是「系统」，不是某个 hex

Manus 自家产品的 accent 各处描述并不一致（官网 web 用蓝 `#0081f2`，社区拆解给 teal `#00D4AA`/gold）。所以"主题色对齐"**不能读成照抄某个蓝**——那会推翻 Cinder 已做的去蓝化决策（`brandTokens.test.ts` 的 `LEGACY_HEXES` 黑名单正在全域拦截 `#2563eb` 回潮）。可迁移的是**系统**：暖中性墨 + 纸底、一个克制的功能强调色、编辑感排版、留白与极简 chrome、有纪律的暗色。

### 事实四：现有图标体系整套机制围绕「发丝框」构建——重画构造 = 重写四处

用户选了「重画母图构造」+「反白印（去掉发丝框）」。而现状的母图是「奶油方卡 + 9 单位发丝墨框 + 字母」，一整套机制**依赖那个框**：

- `scripts/generate-icons.mjs` 的 `extractBrandParts()` 用正则从母图提取**场色 + 发丝框 box + 字形**三件，三域派生（品牌域方边 / OS 应用域 squircle / 字形域单色）都以框为锚；
- `web/src/components/BrandMark.tsx` 的 `MIN_FRAME_PX` 重新加权装置，整个是为「让 9 单位发丝线在 26px 下不糊成灰线」而存在；
- `scripts/check-brand.mjs` 第⑤门逐字节比对 BrandMark 与母图的 **path `d` / `<g transform>` / `MASTER_STROKE` / `MASTER_INSET` / 两个 hex**；
- `web/src/styles/brandTokens.test.ts` 的 `readMaster()` 从 `<rect x="26" y="26" … stroke="…">` 那条**框**读墨色。

**去掉框，这四处都要按新构造重写**——这是本篇最大的成本，不是配色。

### 事实五：色板与母图是硬绑的，故必须原子同批

`brandTokens.test.ts` 把 `--brand-paper` = 母图 field、`--brand-ink` = 母图 ink、`--text-primary`(light) = 母图 ink、`--text-primary`(dark) = 母图 field 逐字绑死，并断言全套色的 WCAG AA/3:1 **属性**（不是魔数）。`check-brand.mjs` 又把 `BrandMark.tsx` 与母图绑死。两仓约 30 个图标产物由母图派生、被 CI 同步门盯着。**所以母图 / tokens / BrandMark / 生成器 / 守门 / 全部派生图标必须一次落地**，中途态守门必红。

## 决策

用户在可视提案（家族中性基座 + 签名 accent + 三个母图构造 + before/after + 布局减法）上逐项拍定：

### ① 色板方向：中性极简，向 Manus 官方品牌收敛

从「暖纸 editorial」收敛到**暖墨 `#34322D` + 中性 off-white**：Cinder 的奶油纸 `#F5F0E4` 调向更中性的暖 off-white（保留一丝暖，避免读作冷灰）。**弱化彩色 accent**——交互主色回归墨色本身，签名色只用于关键锚点（主操作、激活态、品牌时刻），不再大面积铺色。

### ② Warden 身份：共享家族标准 + 自留签名 accent（Cue 式）

Warden 采用**同一套家族中性基座**（暖墨/off-white、中性阶、圆角、`data-theme` 暗色、WCAG 纪律、排版），但保留**一个属于自己的签名 accent** 作独立产品识别——正如 Cue 之于 Manus。Warden 签名色 = **Nightwatch Teal `#2F6B63`**（暗色 `#5FB3A6`）：与 Cinder 的暖 ember 形成「同家族、不同产品」的即时区隔，守夜/沉静的助手调性。Cinder 签名色 = **Ember `#A2441A`**（暗色 `#E09155`），延续余烬意象与去蓝化。

### ③ 母图重画：概念 2「反白印」（inverse seal）

实心暖墨场 `#34322D` + 字形/符号反白镂空 `#F7F4EE`，**去掉发丝框**（实心场本身就是标记）。对比最强、store 列表与小尺寸最醒目、最贴 Manus 的单色极简。**签名色不进标记**（标记是单色墨+纸），只活在 UI accent 里——呼应决策①的「弱化彩色 accent」。

### ④ Warden 字标：抽象提灯/守夜符号（不用字母 W）

Warden 是独立产品（ADR-0047 决策③、ADR-0049 决策②），**不再复用 Cinder 的「C」**。共享「反白印」构造，字标换成一个**抽象的提灯/守夜符号**（bold、极简、16px 可读），与 Cinder 家族一致但身份独立。

### ⑤ 排版：编辑感衬线 + 无衬线（Manus 式 Libre Baskerville + DM Sans 的对应）

**自托管 IBM Plex Serif 作显示体**（品牌名 / 页标题 / 关键数字），正文沿用系统无衬线栈。理由：母图字形本就取自 IBM Plex Sans Bold，用 IBM Plex Serif 与它同族、SIL OFL 可自托管；本产品 local-first，**不引 Google Fonts CDN**（离线优先）。定义模块化字号刻度与**间距刻度 token**（现状无，各组件硬编码 px）。

### ⑥ 暗色机制升为家族标准

`data-theme` 属性（Cinder 现有）成为家族标准；Warden 从 `prefers-color-scheme`-only 迁到 **`data-theme` + 手动切换 + system 三态**。暗色是**暖近黑（非纯黑）**。

### ⑦ 布局减法与主次重构（ADR-0049 遗留项）

- **内容优先，元数据退后**：卡片首屏给标题与状态，提交数/时间戳等元数据降一级（更小、更淡、可折叠）。
- **常驻右栏 288px → 上下文化**：默认收起为窄条，仅在有选区/上下文时展开或改浮层，把主栏宽度还给内容。
- **悬浮机器人不压正文**：`AgentBot` 浮球锚到中栏边缘并预留安全边距，展开面板走侧滑/浮层而非覆盖正文；正文测度维持 `max-width:72ch`。
- **chrome 减法**：合并重复描边、统一圆角到更柔和档位、阴影更轻。

### ⑧ 家族中性基座（具体 token）

> WCAG AA/3:1 由 `brandTokens.test.ts` 的**属性断言**现场校验；落地批次按断言微调 `--text-tertiary`/`--border-default` 等临界值，但不得改动下面的品牌锚点（`--brand-ink`/`--brand-paper` = 母图两色）。

| Token | Light | Dark |
|---|---|---|
| `--brand-ink`（= 母图实心场） | `#34322D` | *（不在暗色重定义）* |
| `--brand-paper`（= 母图反白字形） | `#F7F4EE` | *（不在暗色重定义）* |
| `--bg-primary`（页面底） | `#F4F2EE` | `#1A1712` |
| `--bg-secondary`（卡片） | `#FBFAF8` | `#221E18` |
| `--bg-tertiary` | `#ECEAE4` | `#2A251E` |
| `--bg-hover` / `--bg-active` | `#E7E4DD` / `#E1DED6` | `#302A22` / `#373027` |
| `--border-subtle` / `--border-default` | `#E0DCD3` / `#857E6C` | `#3A352E` / `#7A7260` |
| `--text-primary` | `#34322D` | `#F7F4EE` |
| `--text-secondary` / `--text-tertiary` / `--text-muted` | `#57524A` / `#6E6759` / `#8C8577` | `#B8B1A2` / `#9B9484` / `#7E7768` |
| Cinder `--accent` / `-light` / `-dark` / `-soft` | `#A2441A` / `#B8541F` / `#8A3A14` / `#F0E2D6` | `#E09155` / `#F2B278` / `#C07B3D` / `#3A2E1D` |
| 语义 `danger` / `warning` / `info` / `success` | `#8B4A4A` / `#6B5A18` / `#456380` / `#2D6A2D` | `#DE7676` / `#E0C24A` / `#7A9AB8` / `#4EB352` |
| 圆角 `sm/md/lg/xl` | `8 / 12 / 18 / 24px`（chip `999px`） | 同 |
| 间距刻度（新增） | `4 / 8 / 12 / 16 / 24 / 32 / 48px` | 同 |
| 动效 `fast/base/slow` | `0.15s / 0.25s / 0.35s ease` | 同 |

**Warden 侧**：采用上表全部家族基座（去冷蓝、去纯白卡、去冷灰暗色、补 `danger`/`info`），签名 accent 换成 **teal `#2F6B63`（light）/ `#5FB3A6`（dark）** + `--accent-soft`；`--brand-field`/`--brand-ink` 对齐家族锚点。

### ⑨ 不动的东西

标识层（`repotory_*` / `repotory` / `repotory-mcp` / `repotory-server` / 数据目录 / `com.jiangcheng.repotoryagent` / 插件 id / npm 包名 / Go module / DB sentinel）**一字不动**；`build/brand.json` 四个品牌词（Cinder / Cinder Desktop / Warden / Cinder Memory）**不改**（本篇只改视觉，不改品牌词）；schema / MCP 工具面 / `CoreVersion` 不动；历史 ADR / CHANGELOG 不改写。

## 代价（接受）

- **本篇最大成本 = 重写图标体系，不是配色**：`generate-icons.mjs` 的 `extractBrandParts()` 与三域派生要去掉「发丝框」这个锚、`BrandMark.tsx` 的 `MIN_FRAME_PX` 重新加权装置随框一起删除、`check-brand.mjs` 第⑤门与 `brandTokens.test.ts` 的 `readMaster()` 都要按「反白印」重写读取逻辑与语义映射（field=墨、glyph=纸）。
- **Warden 需要自己的母图 SSOT**：字标不再是 C，故 `--agent-repo` 不能再从 Cinder 母图派生——要新增一份 Warden 母图（如 `build/icon-warden.svg`），生成器与两仓守门要认「两母图、同构造」。
- **原子耦合**：批次 1 内部不可拆，中途守门必红；且要重生成两仓约 30 个二进制图标（含 `.icns` 走 `iconutil`、`.ico` 手工容器）。
- **品牌资产再清零一次**：ADR-0037 决策 1 的「可复述构造」是「方边+发丝线」，本篇换成「反白印」，可复述性要重新验证（实心场在小尺寸/store 列表是否仍读作一个印刷物而非一块深色）。
- **自托管字体有构建面成本**：woff2 进仓、`@font-face` 与 FOUT/FOIT 处理、docs 站与两仓各自加载。
- **布局减法会动到多处组件与其 CSS Module**，且 `semanticInk.test.ts` 的级联顺序不变量、`--context-rail-width` 的 FAB 锚定注释都要跟着校。

## 未决（本篇明确不决，留给落地批次或后续）

- **Warden 提灯/守夜符号的确切几何**：批次 1 出 SVG 草案再定（要求 bold、16px 可读、与 C 同光学面积）。
- **临界色值的最终定档**：`--text-tertiary` / `--border-default` 等以 `brandTokens.test.ts` 属性断言为准现场微调。
- **右栏「上下文化」的确切交互**（窄条常驻 vs 浮层 vs 选区触发）：批次 4 出方案。
- **是否给两仓一份共享的 token 源文件**（现状两仓各自维护 CSS，靠 `brand.json` 防漂移）：本篇不强行合并，先在 Warden 侧对齐值；共享源留作后续评估。

## 落地清单（分批，每批结束跑守门）

1. **批次 1 · 母图 + 色板（原子核心）**：重画 `build/icon.svg`（反白印）+ 新增 `build/icon-warden.svg`（提灯符号）→ 重写 `generate-icons.mjs` 的 `extractBrandParts()` 与三域（去框锚）→ 同步 `BrandMark.tsx`（删发丝框与重新加权）→ `tokens.css` 换 §⑧ 色板 → 更新 `brandTokens.test.ts` 的 `readMaster()` 与语义映射、`check-brand.mjs` 第⑤门 → 重生成两仓全部图标 → 跑 `vitest run`（brandTokens/semanticInk）+ `node scripts/check-brand.mjs` + `generate-icons.mjs --check`。
2. **批次 2 · Warden 去漂移**：`src/styles.css` 迁到家族基座、去冷蓝、`data-theme` 暗色 + 切换开关、接入 teal 签名 accent、换自己的提灯图标（`src-tauri/icons/` 由批次 1 生成器重生成）。
3. **批次 3 · 排版**：自托管 IBM Plex Serif、模块化字号 + 间距刻度、应用到两仓标题与关键数字。
4. **批次 4 · 布局减法与主次重构**：右栏上下文化、悬浮机器人让位、元数据降级、chrome 减法。
5. **批次 5 · 验证**：两仓守门全绿（`check-brand` / `brandTokens` / `semanticInk` / `docs-mirror` / `i18n` / `npm run build`+`lint`+`test` / `cargo fmt`+`clippy`+`test`）；起 dev server 在浏览器/真机核对 UI（**改 UI 必须实跑，不空口报绿**）。
6. **同批写**：CHANGELOG（中英各一条）、`TODO.md` 现状速查、本篇「落地记录」段、ADR 索引（中英各一行，下一个编号从 `0051` 递增）。

## 落地记录

**批次 1（2026-11-05）· 母图 + 色板（原子核心）**

- 母图重画为「反白印」：`build/icon.svg` = 实心暖墨场 `#34322D` + 字形反白 `#F7F4EE`，**去掉发丝框**；新增 `build/icon-warden.svg`（提灯/守夜符号：环提手 + 外张罩 + 鼓腹玻璃 + 外张足，`fill-rule="evenodd"` 切出提手内环）。
- `generate-icons.mjs` 的 `extractBrandParts()` 改读「场 rect 的 fill = 墨、字形 path 的 fill = 纸」；三域去框锚：brand = 方印逐字、app = squircle、glyph = 单色透明（活动条/条纹改用 field/glyph，删旧描边常量）。
- `BrandMark.tsx` 删发丝框与 `MIN_FRAME_PX` 重新加权整套装置（反白印是平涂场，无描边可加权）；≤16px 仍塌缩为 `currentColor` 字形。
- `tokens.css` 换 §⑧ 家族色板；新增间距刻度 `--space-1..7` 与 `--transition-slow`。
- `brandTokens.test.ts` 的 `readMaster()` 改读场/字形两色，语义映射翻转（`--brand-ink`=场、`--brand-paper`=字形、亮 `--text-primary`=墨、暗 `--text-primary`=纸）。
- **临界色值定档**：亮 `--accent` 由 §⑧ 草案 `#A8471A` 微调为 `#A2441A`——属性断言要求对 `--bg-active` 达 4.5:1（草案仅 4.36:1）；§⑧ 表与签名色叙述同批改为 `#A2441A`，暗 `#E09155` 不动。
- 重生成两仓全部图标（含 `.icns`/`.ico`/tray）；`generate-icons --check --tracked-only`（本仓）与 `vitest run` 418 过、`tsc` 过。
- **2026-11-06 复核订正**：当时写的「`check-brand` 第⑤门已按反白印重写」不成立——该文件零 diff，靠「母图无框 → 旧发丝框分支成死代码」碰巧绿；Warden 那套图标也**不在 CI 的 `--check` 范围**（CI 从不传 `--agent-repo`），只能人工带参验证。两处已在同批补强：Marketplace 40×40 对纳入生成器与 `--check`，`check-brand` 增加窗口标题/`productName`/跨仓 `brand.json` 的精确比对。
- 负例：活动条 glyph 域在亮底近乎不可见是**设计意图**（VS Code 活动条恒暗），接触表已目验。

**批次 2（2026-11-05）· Warden 去漂移**

- `src/styles.css` 迁家族基座（与 Cinder §⑧ 同值），去冷蓝 `#2563eb`/`#60a5fa`；补 `--danger`/`--info`。
- 签名 accent = Nightwatch Teal `#2F6B63`/暗 `#5FB3A6`，实测对全部落点 ≥4.5:1。
- `prefers-color-scheme`-only → `data-theme` + 三态（system/light/dark）切换：`index.html` 预绘脚本防闪、`main.ts` `initThemeToggle()` 解析与持久化（键 `warden-theme`）、OS 变更仅在 system 态跟随。
- 品牌带改反白印场（墨场 + 纸字），实测 11.7:1。
- 提灯图标由批次 1 生成器 `--agent-repo` 重生成（app/tray/icns/ico）。
- 实跑：`vite` 起 dev，浏览器目验暗/亮/跟随三态循环与持久化；`tsc`、`check-brand`、`check-docs-mirror` 绿。

**批次 3（2026-11-05）· 排版**

- 自托管 IBM Plex Serif（SIL OFL 1.1）：latin 400/600 woff2 入 `web/src/assets/fonts/`（Cinder）与 `src/assets/fonts/`（Warden），licence 随行；**不引 Google Fonts CDN**，woff2 进两仓构建产物。
- 新增 `--font-display` 与模块化字号刻度（`--text-display`…`--text-micro`）；Cinder 的 h1/h2 与 markdown 标题、Warden 的品牌名/卡标题应用衬线；h3 及以下保持无衬线（小字号衬线读作装饰而非层级）。
- 实跑：浏览器确认 `document.fonts` 已载 600 且 h1 计算族为 IBM Plex Serif；CJK 经通用 `serif` 回退（宋体系），属预期编辑感。

**批次 4（2026-11-05）· 布局减法**

- 右栏上下文化定案：**默认折叠**为 30px 手柄（首跑不再常驻 288px），仅显式展开才钉宽（存 `0`）；折叠手柄带待审计数徽标，等待中的上下文不靠占位也能到达用户。
- 悬浮机器人让位与 72ch 正文上限**维持现状**（已满足 §⑦）；元数据降级评估为现有 quiet 样式已达标，本批不再动 chrome，避免半落地回归。
- 实跑：浏览器确认首跑无 `aside`、手柄存在、点击展开并持久化 `0`。

**批次 5（2026-11-05）· 验证**

- Cinder：`check-brand` / `check-docs-mirror` / `check-i18n-keys` / `check-version-drift` / `generate-icons --check`（本仓；Warden 那套需手动带 `--agent-repo`，CI 不覆盖）/ `vitest run` 418 / `tsc` / `vite build` 全绿。
- Warden：`check-brand` / `check-docs-mirror` / `tsc` / `vite build` 全绿。
- UI 实跑核对：Cinder dev（暗/亮、反白印标记、折叠右栏）与 Warden dev（三态主题、墨带、teal）均浏览器目验。
