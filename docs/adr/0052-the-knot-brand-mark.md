# ADR-0052: 标识重做——The Knot（一条带子穿过自己）与四域派生

状态：**Accepted（2026-11-06，用户以「就以 v6 进行落地」拍定）**；2026-11-07 修订（卡片留白，见决策⑨）
日期：2026-11-06（修订 2026-11-07）
关联：[ADR-0037](0037-brand-legibility-and-ui-language.md)（本篇取代其「不得靠具体形状建立知名度」）、[ADR-0050](0050-family-design-standard.md)（本篇取代其决策③④的母图与 Warden 符号，其余决策保留）、[ADR-0049](0049-brand-restructure-manus-style.md)（标识层冻结）、[ADR-0048](0048-core-contract-version.md)（本篇不涨契约版本）
起始 schema：不变（不改 schema、不改 MCP 工具名、不涨 `CoreVersion`）

## 背景

ADR-0050 的母图是 IBM Plex Sans Bold 字母 C 的反白印，Warden 是抽象提灯。用户连续否掉后续三轮：v2 具象拟物/徽章/卡通角色（「记不清爽，也没质感」）、v3 六组几何候选（「太 low」）、v4 The Ember Cut。诉求收敛为：**颜色极简、造型抓人、简单但是细腻**，参考 Manus 与 ChatGPT —— 质感是材质工艺（厚墨压印 + 柔投影），不是把产品画成物件。

同时 ADR-0037 禁止靠具体形状建立知名度，而「可传播」又要求符号可徒手画、16px 可辨。二者只有在一个条件下同时成立：**形状本身是一次可参数复现的发明，而不是某个具象物件的轮廓。**

## 决策

**① 母图 = The Knot / 结。** 一个标记是一条**闭合的等宽带**，沿一条闭合曲线走一圈并与自己相交；每次相交，在上的带子连续穿过，**在下的那段真的缺席**。切口平行于压过它那条带子的边缘，因此缝宽恒等于带宽，不靠手调。

| | 曲线 | 交 / 瓣 | 对称 |
|---|---|---|---|
| **Cinder** | `z(t) = e^it + 2e^-2it`（三叶结） | 3 交 3 瓣 | 3 重 |
| **Warden** | `z(t) = cos t + i·sin 2t`（8 字 / 双纽线） | 1 交 2 瓣 | 2 重 |

家族规则四条：**等宽带 / 断口编织 / 缝宽=带宽 / 墨场纸字形**。改的是圈数 N，不是织法。Cinder 把浏览 / 编辑 / 审批三件事系在一起，Warden 一边读一边跑、自己不在中间。

**② 不用 mask。** 单色 + 无 mask 要读出真交织，唯一办法是让 under 分支真的缺席；mask 会破坏 `glyphSvg()` 只替换 fill 的单色字形域取值。母图因此是多段 path（Cinder 3 段 / Warden 1 段），path 顺序无关（无重叠）。

**③ 等宽带是量出来的。** 两枚归一化带宽同为 **27.25 u / 512**（Warden 的 `CURVES.band = 0.073104` 按归一化结果反解），着墨率 17.4% / 18.1%。原先 Warden 保持 39.48u 会把 8 字的负空间压成窄缝、20px 糊成一团。

（2026-11-07 修订：卡片留白由 24u 改为 56u，带宽与着墨率随归一化比例重算 —— 变小的是字形在卡片上的占比，不是笔重本身。）

**④ 四套资源，一套构造。** brand 母图（平、纯色、token 可绑）· app 域（squircle 容器 + `lift` 纸色渐变 + `press` 柔投影）· 字形域（单色、透明底）· **小尺寸域（≤20px，同一条中心线的实心剪影、笔画 1.86× = 50.68u、不断口）**。母图与小尺寸母图都落在 `build/`，由 `scripts/generate-icons.mjs` 派生全部产物；`renderPng()` 按渲染尺寸自动切域，不在每个调用点重复判断。

**⑤ 色板不变。** 沿用 ADR-0050 的 `#34322D`（墨场）/ `#F7F4EE`（纸字形）；签名色（赤陶 / 深青）不进标记，只存在于 UI token 与排版层。

**⑥ 四处守门按多 path 改写。** `extractBrandParts()` 的 fill 替换改为全局；`check-brand.mjs` 第⑤门从「只比第一个 path」升级为**逐字节比对全部 path 与 `<g transform>`**；`BrandMark.tsx` 的几何改由生成器写入 `web/src/components/brandMarkPaths.ts`（纯数据），组件只负责渲染；`brandTokens.test.ts` 的两色绑定不动。守门比原来更强，不是放宽。

（2026-11-07 补：VS Code webview 面板没有 `localResourceRoots`，标记只能内联进 HTML —— 那份内联原本是**手抄副本且无人守**，v6 换版时它独自留着上一版字形，逃过了所有门。现在它由 `generate-icons.mjs` 生成，第⑤门一并逐字节比对；负例已验：塞回旧路径，门当场红。）

**⑦ 文案与标识同源。** `build/brand-copy.json` 是 25 条中英基线的真相源，`scripts/check-brand-copy.mjs` 校验结构、禁用词、定位句与母图结构参数。

**⑧ 一次原子提交。** 母图一改同步门当场红，35 个派生件 + 新增资源必须同批。

**⑨ 卡片留白 56u。** 母图本身就是「暗方卡 + 挖空结」的构图：字形落在 512 格的 **78%**，四面各留 56u（Manus ≈74%、ChatGPT ≈70%、Apple HIG 内容圆 80%）。原先留白 24u 时字形铺到 91%，派生到 squircle / 市场方卡 / JetBrains 印章 / favicon / 导航栏后一律显挤 —— 标记读起来像被卡片裁了一下，而不是坐在卡片里。这个比例定在母图里：一个数改对所有面；小尺寸域笔画同步 1.6× → **1.86×**，补偿母图缩小后的绝对宽度（27.25u × 1.86 = 50.68u，16px 时 1.58px）。

## 后果

- **取代**：ADR-0050 决策③（字母 C 反白印）与决策④（Warden 提灯）；ADR-0037 的「不得靠具体形状」改为「不得靠具象物件，靠可参数复现的构造」。两篇原文保留并加横幅。
- **新增资源**：`build/icon-small.svg`、`build/icon-warden-small.svg`、`build/brand-copy.json`、`web/src/components/brandMarkPaths.ts`、`scripts/check-brand-copy.mjs`。
- **重生成**：`build/icons` 9 件、`ide/vscode/media` 2 件、JetBrains 4 件、favicons 4 件、`warden/src-tauri/icons` 18 件、`warden/src/assets/warden.png`。JetBrains Marketplace 反白对与条纹图标必须全局替色，否则多 path 会花色。
- **应用内**：Warden companion 的拟人提灯（带眼睛/笑脸）与 v6「无脸无字」冲突，换为 8 字标记；交互 id 与拖动手势不变。
- **不涨** `CoreVersion`、不改 MCP 工具名、不动 schema、不改 `brand.json` 四个产品名。
- **2026-11-07 修订**：母图留白 24u → 56u，35 个派生件 + `warden/src/assets/warden.png` 同批重生成；小尺寸域笔画 1.6× → 1.86×；`build/brand-copy.json` 与本 ADR 的带宽 / 着墨率数字同步；`check-brand.mjs` 第⑤门新增 webview 内联标记的比对。
- 品牌手册 `brand/brand-system-v6.html` 留在工作区（两仓之外）作为设计存档；两仓内的真相源是 `build/icon*.svg` 与本 ADR。
