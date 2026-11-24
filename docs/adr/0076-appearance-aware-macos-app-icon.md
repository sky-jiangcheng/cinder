# ADR-0076：系统外观自适应 App 图标——浅色纸底，深色纯黑

- 状态：**Accepted，但决策 1 / 2 的「纯黑」部分已于 2026-11-24 当日回退为品牌墨色 `#34322D`（见「落地记录」末条）——保留明暗系统适配，深色不再用纯黑；字形放大回母图满幅。文内历史决策段保留原样，回退以落地记录为准**
- 日期：2026-11-24
- 关联：[ADR-0050](0050-family-design-standard.md)（家族色板）、[ADR-0052](0052-the-knot-brand-mark.md)（The Knot 母图与四域纪律）、`scripts/generate-icons.mjs`、`scripts/apply-macos-app-icon.sh`

## 背景

Wails v2 只从 `build/appicon.png` 生成一张 `iconfile.icns`，并由 `CFBundleIconFile` 引用。单张位图没有浅色/深色外观位，系统只能自己猜——在 macOS 26+ 上会出现 Dock 与 Spotlight 不一致。用户要求桌面 App 图标像 Claude 一样跟随系统：当前深色模式下背景必须是**纯黑**。

约束不变：品牌母图 `build/icon.svg` 是唯一真相源，The Knot 的字形、路径与两色 token 不动；改动只发生在 **OS 应用域**。

## 事实与决策

1. **新增现代 macOS 图标子域**：`build/AppIcon.icon/` 由生成器从母图派生，结构与颜色特化写进 `icon.json`，字形是一张透明底的 `knot.svg`（按 0.8 缩放，让 Icon Composer 自己加安全区与外框）。
   - 浅色（Aqua）：背景 `#F7F4EE`（品牌纸色），字形 `#34322D`（品牌墨色）。
   - 深色（Dark Aqua）：背景 **`#000000` 纯黑**，字形 `#F7F4EE`。
   - 不加玻璃、阴影与半透明；禁用这些效果是为了保持 The Knot 的剪影优先，也让黑色是真的黑。
2. **经典回退图标改纯黑**：Wails 读取的 `appicon.png` 与 `build/icons/` 尺寸阶梯、`icon.ico`、`iconfile.icns` 使用黑色 squircle + 纸色结。旧 macOS 的深色模式因此直接得到纯黑，代价是旧系统的浅色模式仍看到黑底——现代系统由第 3 条覆盖。
3. **发版后处理注入**：`wails build` 之后、签名之前运行 `scripts/apply-macos-app-icon.sh`：用 `xcrun actool` 编译 `.icon` 为 `Assets.car`，复制进 `.app/Contents/Resources/`，并把 `CFBundleIconName=AppIcon` 写进 `Info.plist`。`CFBundleIconFile` 保留，作为旧系统回退。脚本用 `assetutil` 断言 `NSAppearanceNameDarkAqua` 存在；缺深色位就失败，不静默发版。
4. **同步门覆盖**：`icon.json` 与 `knot.svg` 是提交物，`generate-icons.mjs --check --tracked-only` 逐字节比对，与 favicon / 插件图标同一条纪律；手改派生文件会被 CI 拦下。
5. **不迁框架**：不为了图标升级 Wails v3。若将来迁到 v3，改用其原生 `appicon.icon` 支持并删除后处理脚本。

## 后果

- 得：深色 Dock 图标是纯黑，浅色是纸色；同一份母图派生，不产生第二个品牌真相源；旧系统仍有可用的黑底图标。
- 失：CI 的 macOS runner 需要 Xcode 26+ 的 `actool`；工具缺失时发版失败而不是降级。浅色/深色之外的 Tinted / Clear 外观本轮不单独设计，由系统从现有层推导。
- 边界：Windows / Linux 的 OS 图标、favicon、IDE 插件图标全部不变；应用内 `BrandMark` 仍是母图的墨底纸字，不变。

## 翻转触发

- 若 Wails v3 落地 → 用原生 Icon Composer 支持替换后处理脚本。
- 若用户要求 Tinted / Clear 单独设计 → 在 `icon.json` 加专用层与 appearance 特化，不动母图。
- 若旧 macOS 浅色模式黑底成为真实投诉 → 重新评估经典回退的颜色（当前刻意优先深色纯黑）。

## 落地记录

- 2026-11-24：生成器中新增 `.icon` 派生与黑色经典回退；新增 `scripts/apply-macos-app-icon.sh`；release workflow 在 Wails 构建后注入图标。本地 `actool` 编译、`assetutil` 深色位断言、模拟 `.app` 注入、`ictool` 明暗预览、`generate-icons --check --tracked-only` 与 `check-brand.mjs` 全过。
- 2026-11-24（同日回退，用户反馈「偏差太大、图案缩小、不适配」）：
  - **深色背景纯黑 → 品牌墨色 `#34322D`**（`icon.json` 的 dark `fill-specializations` 改用母图 ink token，`appicon.png` / 尺寸阶梯 / ico / icns 的 `cinderAppField` 从写死 `#000000` 改为 `parts.field`）。系统明暗适配与「深色位必须存在」的发布门**不变**——`assetutil` 仍断言 `NSAppearanceNameDarkAqua`，只是深色那层的底色不再是纯黑。
  - **字形去掉 `scale(0.8)`**：原注释称「Icon Composer 自带安全区所以先缩小」，实测这是**双重留白**——母图结已占 400/512（78%）并留 56 单位边距，Icon Composer 再套一层 mask 后字形在 Dock / Launchpad 里明显偏小。现在把母图路径满幅交给 `.icon`，不再预缩。
  - 决策 3 / 4 / 5（发版注入、同步门、不迁框架）与浅色纸底不变；中文标题「深色纯黑」为历史措辞，以本条为准。
  - 校验：`node scripts/generate-icons.mjs` 重生成 26 项；`--check --tracked-only` 与 `check-brand.mjs` 全过。
- 2026-11-24（第二轮，装到本机后 Dock 里图案仍偏小）：上面那条「去掉 `scale(0.8)` 就够」的判断不完整——真正的压缩来自**图层画布尺寸**。Icon Composer 把每层按 1:1 铺在 **1024pt** 画布上，再由系统裁到 824pt squircle；我们的 `knot.svg` 只声明 `viewBox/width/height = 512`，于是图层被当成半尺寸摆在 1024 画布里，实测结只占方块宽 **36.4%**（= 母图 72.7% × 512/1024）。修法：`knot.svg` 声明 1024 画布并把母图 512 网格 `scale(2)`，实测回到 **72.8%**，与经典 PNG 阶梯（72.7%）一致。
  - 度量口径：用 `NSWorkspace.icon(forFile:)` 在指定 `NSAppearance` 下把 bundle 图标渲染成 512px，再按 alpha 取 squircle bbox、按亮度取字形 bbox 求比值——这是 Dock / Launchpad 的真实路径，光看 `knot.svg` 单独渲染会漏掉画布缩放。
  - `scripts/generate-icons.mjs` 注释同步改写，避免以后有人又按「512 就够」回退。
