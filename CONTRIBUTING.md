# 贡献指南

Repotory（仓库与包标识 `repotory`）是本地优先的 Git 知识库桌面应用。欢迎 Issue 与 PR——动手前请花两分钟读完本文，尤其是「范围冻结」与「命名分层」两节，它们是本仓库最容易被好心 PR 违反的约定。

## 环境要求

| 依赖 | 版本 | 用途 |
|------|------|------|
| Go | 1.25+ | 后端（零 CGO，内嵌 SQLite） |
| Node.js | 24+（见 `.nvmrc`） | 前端构建与测试 |
| Wails CLI | v2.13+（可选） | `wails dev` 开发模式 |

> Node 版本以仓库根的 `.nvmrc` 为准（`nvm use` 会自动读它，CI 的 `actions/setup-node` 也读它）。这是本仓库 Node 版本的**唯一来源**——`Dockerfile` 里有一份同值默认，`scripts/check-version-drift.mjs` 会在两者漂移时报红。node 20 已于 2026-04 结束支持（EOL），所以下限是 24 而不是 20。

## 本地开发

```bash
# 前端依赖与构建（web/dist 被 go:embed 进二进制，改前端后需重新构建）
cd web && npm install && npm run build && cd ..

# 开发模式（前端热更新 + Wails 绑定注入）
wails dev

# 纯后端构建（桌面应用 + MCP server）
go build -ldflags="-s -w" -o repotory .
go build -o repotory-mcp ./cmd/mcp/
```

提交前请跑齐三件套：

```bash
go test ./...            # Go 全量测试（service/db/knowledge/scanner/diff…）
cd web && npm test       # vitest
cd web && npm run build  # tsc 严格检查（ESLint 现状见 TODO.md）
node scripts/check-docs-mirror.mjs  # 双语文档 1:1 镜像守卫（docs/** ↔ docs/en/**，blog/*.html ↔ blog/en/*.html）
node scripts/check-i18n-keys.mjs    # locale 词条覆盖守卫（key 缺失 / 语言漂移 / 英文残留中文）
```

> 双语文档镜像守卫：`docs/**/*.md`（中文源）与 `docs/en/**/*.md`（英文源）必须互为 1:1。改一边、只改一边的「双语漂移」会被 `scripts/check-docs-mirror.mjs` 在 CI（`docs-mirror` job）拦下。有意只出中文的研究文档（`competitive-positioning.md`、`positioning-brief.md`、`product-review/*`）在该脚本的 `ZH_ONLY` 白名单里；新增此类文档时同步登记白名单，否则守卫会误报。**同一道门也管 `blog/`**：`blog/*.html` 是手写的杂志风交付物（不是构建产物），每篇必须有 `blog/en/<同名>.html` 英文 twin——第一篇就漂过：品牌改名两年后仍写着旧名，还有已经不存在的旧路径。

> **locale 词条覆盖守卫**：`scripts/check-i18n-keys.mjs`（CI `i18n-keys` job）断言四件事——① 代码里静态引用到的每个 `t()` key 在每个 locale 都存在；② 各 locale 的 base key 集合一致（没有「只加一边」的漂移）；③ 用了复数变体（`_other` 等）就必须有 base key；④ 英文 locale 的值里不残留中文。**新增界面文案时，中英两份 `web/src/locales/*/common.json` 必须同批改**——只改一边会在 CI 上报红。

> **绑定面契约守卫**：`internal/app/api_contract_test.go` 会在 `go test ./...` 里核对 `docs/api/reference.md`（中英双份）与 `*App` 的绑定面——文档写到的绑定必须真实存在、已删除的绑定不得复活、两边文档的绑定集必须一致。改绑定面时**只改代码不同步文档会被这条门拦下**。

> **构建产物清理**：`web/dist` 被 `go:embed all:web/dist` 整个打进二进制，所以那里多一份文件就等于二进制大一份。`npm run build` 末尾会跑 `scripts/prune-web-dist.mjs`，按 Vite 的 `.vite/manifest.json` 精确删除 `assets/` 下的孤儿 chunk（历史包袱：曾累积 15 份 elk chunk，21MB 直接进了可执行文件）。清理范围严格限定在 `assets/`——`dist` 根下的 `index.html` / `favicon.*` 不在 manifest 里，按「未列入 manifest 即孤儿」删会连页面 HTML 一起删掉。

## 架构约定

- **业务逻辑只写在 `internal/service`**：Wails 桌面、headless HTTP（`cmd/server`）、MCP（`cmd/mcp`）三端共享同一实现；`internal/app` 是薄绑定层，每个方法 1-3 行委托，不放逻辑
- **改绑定面必须同步文档**：`internal/app` 的方法签名是对外契约，变更需同步 [docs/api/reference.md](docs/api/reference.md)
- **重大决策走 ADR**：在 [docs/adr/](docs/adr/index.md) 新增编号文件，说明背景、决策与代价；历史决策（分层、FTS5、块编辑器、范围冻结）见 ADR 索引

## 品牌图标三域规则（macOS 适配 2026-10-23 立，2026-10-28 增补字形域）

品牌母图 `build/icon.svg` 是**方边**奶油卡——方边是品牌识别（「印出来的卡」），但 macOS 从不给 icns 位图自动套圆角：方边母图直接当 app 图标，Dock 里就是一张方角卡片夹在一排圆角邻居里；而 VS Code 活动栏这类槽位**由宿主做遮罩渲染**。因此图标产物分三个域，**同一字母、三种容器**：

| 域 | 产物 | 形状 | 理由 |
|---|---|---|---|
| 品牌域 | favicon（docs/web）、Marketplace 商店图标、JetBrains 工具窗内的品牌标记 PNG（96，渲染为 44） | 方边原卡 | 容器本身是方形或展示位，卡边即识别；squircle 在小尺寸下只会读作「破掉的矩形」 |
| OS 应用域 | `build/appicon.png`（Wails 默认图标源）、`build/icons/` 阶梯、`build/icons/icon.ico` | squircle 容器 | macOS 规范：源图必须自带 squircle（≈22.37%，超椭圆 n≈5.2 近似），系统不裁 |
| 字形域 | VS Code 活动栏（24，框+字母）、JetBrains 工具窗侧栏图标（16，仅字母） | 单色字形，**透明底** | 这两类槽位都不接受一张卡：VS Code 活动栏**遮罩渲染**（取 alpha 通道用主题前景色重绘，母图的奶油底不透明 → mask 出来是一整块实心方块，R 消失，把已发布的文件丢进 CSS mask 即可复现）；JetBrains 侧栏把插件 SVG 当**位图**光栅化（不遮罩，2026-10-29 按 IDEA 2025.2 的 `SVGLoader` → `StripeButton.setIcon` 链路核实），并在深色主题下自动取 `_dark` 同名兄弟文件（平台自己的 `icons/statisticsEventLog.svg` + `_dark.svg` 就是这个约定）。所以侧栏图标是 **16px 单色字母 + 透明底**，浅色主题用品牌墨色、深色主题用品牌纸色——13px 的奶油卡在 40px 深的侧栏里是一块发亮的方块，hairline 框在该尺寸不足一个像素，读作变形的光斑 |

规则条目：

- **单点改动**：只改 `build/icon.svg`，`scripts/generate-icons.mjs` 自动派生三域全部产物，CI 同步门（`--check`）拦截任何半落地
- **遮罩槽位必须透明底**：凡是宿主按遮罩渲染的槽位，产物的 alpha 形状**就是**标记本身——不能有不透明的底。生成器对字形域断言「油墨覆盖率 < 75%」：一张实心卡的覆盖率是 100%，门会直接报 `opaque slab`
- **同心原则**：OS 域的内部 hairline 框不是「随机半径圆角矩形」，而是按容器曲线内缩的同心 squircle（Apple concentricity：嵌套圆角必须与容器相关）
- **新增图标面前先分类**：OS 应用图标 → 继承 squircle；品牌展示 → 母图原样；**遮罩槽位 → 字形域（透明底单色）**。**不要手养副本**——本品牌历史上四次半落地（活动栏/工具窗/favicon/README 各漏一次改版）根因全部是手养副本
- **小尺寸要重新配重**：母图 hairline 在 512 网格上是 9 单位，到 24px 只剩 0.42px（不足一个像素，糊成一条灰线）。字形域的框按小尺寸重新配重（34 单位 = 24px 下 1.6px）
- **macOS 26+ Liquid Glass `.icon` 分层格式**（系统自动掩码 + 玻璃效果）是未来方向，但 Wails 仍产 icns 位图阶梯，在迁移前本规则是唯一适配路径；迁移时删掉应用域的预裁 squircle，改供方形图层
- **CI 顺序**：同步门先跑 `--check --tracked-only`（只查**已提交**的产物），再生成、再全量 `--check`。先生成后检查等于拿生成器跟它自己比，什么都抓不到

## 前端测试的异步落地（ADR-0026）

组件在 effect 里用 promise 填状态，所以「元素已挂载」不等于「数据已到位」：`findBy*` 等一个**无条件挂载**的锚点时会立刻 resolve，随后同步读 effect 状态就是拿时序赌绿红。

- **默认地板**：`web/src/test/renderReady.tsx` 的 `renderReady()`（内部 `settle()` 跨一次宏任务边界，微任务会先全部排空）。`Review` / `Home` / `Dashboard` / `Search` 四个组件测试文件已接入。
- **边界（不覆盖哪些）**：`AiTab`（无 router、必传 props）、`NotFound` 与 `App.smoke`（用真实 i18n 实例）**不接 `settle()`**——真实 i18n 返回译文、mock 的 `t` 返回 key 或 `defaultValue`，两种断言口径不塞进同一个 helper。在这三个文件里写交互测试，必须自己等「就是数据的那个元素」。
- **`settle()` 在 `vi.useFakeTimers()` 下会死锁**：它靠真实 `setTimeout(0)`，假计时器里永不 resolve，测试会挂到 `waitFor` 超时。用假计时器的都是 `renderHook` 测试、不经 `renderReady`——别把两者混用。
- **等数据要等对东西**：锚点选错（仍然等静态标题）helper 救不回来。参照 `Review.test.tsx` 的 `pickProject()`，以及「先 `await` 目标 `option` 再改值」的写法。
- **验收探针**：`cd web && REPO_TEST_LAG_MS=300 npx vitest run` 会让 `Review`/`Home` 的加载 promise 延后落地、把竞态窗口重开，必须全绿；不设该变量时 `lagged()` 是 no-op。
- **别用「`beforeEach` 默认给非空 mock 列表」治这个病**——2026-10-18 已实测否证：踩中的用例本就喂了非空数据（问题在**何时**落地，不是**是否**存在），且该法直接破 `Home`/`Search`/`useKnowledgePage` 三处空态用例。详见 [ADR-0026](docs/adr/0026-frontend-test-async-readiness.md)。
- **不要再手写 `afterEach(cleanup)`**：vitest 的 `test.globals` 已置 `true`，testing-library 的自动卸载随之注册（此前 7 处手写 cleanup 是 globals 关着时的复制品，2026-10-19 已清空）。
- **`eslint-plugin-testing-library` 是形态守卫，不是时序保证**：它接在 `web/eslint.config.js`（v7 已无 `flat/recommended`，取 `flat/dom` + `flat/react`），只作用于**真正用 testing-library 的文件**——清单是显式的 `testingLibraryFiles`，因为默认 glob 会把纯逻辑测试里的 `const result = renderMarkdown(...)` 误判成 RTL 的 `render`。**新增一个非 `hooks/` 目录的 `.test.ts` 若引入 RTL，要把它登记进那份清单**，否则规则对它完全不生效。实测过：同一句 `heading.closest('section')` 写在使用例里被报 `no-node-access`，挪进 helper 函数后不报——规则看上下文，别把它当成上面那类竞态的兜底。
- **有一条规则是有意不采的**：`render-result-naming-convention` 显式设为 `off`，理由（纯命名、不承载正确性、已证会误报）写在配置注释里。**不要「顺手把它打开」**——那 12 处 `a`/`b`/`c`/`d` 表达的是共享同一 cacheKey 的多个消费者。

## 范围冻结（重要）

[ADR-0006](docs/adr/0006-scope-freeze.md) 冻结了产品范围：**核心是「发现 → 理解 → 记录 → 检索 → 交给 AI」闭环**，仪表盘与统计是支持能力；插件平台基础设施与 PWA 不再默认扩展。新增大功能前请先开 Issue 讨论，避免与范围声明冲突的投入。

## 命名分层（重要）

品牌展示层与机器标识**有意不一致**，完整规约见 [README 命名分层](README.md#命名分层)。速记：

- 品牌文案写 `Repotory`，完整展示名 `Repotory: Local Git Knowledge Base`，仓库/包标识写 `repotory`
- **不要「顺手统一」冻结标识**：二进制/命令名 `repotory`、数据目录 `repotory`（`internal/platform` 的 `dirName`）、MCP server 名 `repotory-mcp` 与工具前缀 `repotory_*`。它们绑定用户数据迁移史与 AI 客户端对外契约，改名是破坏性变更

## 默认值变更须先过评测门

把一个功能从「默认关」改为「默认开」（或收紧/放宽任何面向用户的默认值），必须先在对应评测设施上跑出达标数据并在 PR 里贴结果（检索类用 `internal/search/abeval` + 标注集，编译类用审核留存率，备份类用恢复演练）。理由：默认值是产品行为，「感觉更好」不是证据——2026-10 起 M3（语义检索默认关）与 M7 lane 2（图检索强化门槛）两次拍板均以 delta 数据为准，此惯例成文以防回退。

## 提交与版本

- 提交信息遵循 [Conventional Commits](https://www.conventionalcommits.org/zh-hans/)（`feat:` / `fix:` / `docs:` / `refactor:` …），scope 用受影响的域（如 `feat(server):`、`fix(kb):`）
- 版本号 SSOT 是 `wails.json` 的 `info.productVersion`，用 `scripts/bump-version.sh` 同步到 `web/package.json`、`internal/version`、两个 IDE 分发面、包管理器清单与 `TODO.md` 的现状速查，不要手改散点。**「不要手改散点」靠 CI 兜底**：`node scripts/check-version-drift.mjs`（CI `version-drift` job）从 SSOT 重新算一遍，任何一处抄写没跟上就报红
- **计数型事实不要抄写**：ADR 份数、MCP 工具数这类会随开发变化的数字，写「见 `docs/adr/index.md`」这类指向事实源的链接，而不是把数字抄进正文——抄进去的那个数字下次就会过期
- 面向用户的变更请同步更新 [CHANGELOG.md](CHANGELOG.md) 的 `[Unreleased]` 条目与对应手册页（`docs/**/*.md` 是唯一内容源，`docs/**/*.html` 是构建产物，**不要手改**）

## PR 检查清单

- [ ] `go test ./...`、`npm test`、`npm run build` 全绿；`node scripts/check-docs-mirror.mjs`（双语文档 1:1 镜像守卫，CI `docs-mirror` job）、`node scripts/check-i18n-keys.mjs`（locale 词条覆盖守卫，CI `i18n-keys` job）与 `node scripts/check-version-drift.mjs`（版本/计数漂移守卫，CI `version-drift` job）通过
- [ ] 不触碰范围冻结与冻结标识（或已在 Issue/ADR 中说明理由）
- [ ] 绑定面变更已同步 API 参考；用户可见变更已更新手册与 CHANGELOG
- [ ] 新增依赖有明确理由（本项目依赖极简：wails、mcp-go、minio-go、chromem-go、modernc sqlite）

## 报告问题

Bug 与功能建议走 [GitHub Issues](https://github.com/sky-jiangcheng/repotory/issues)，请附版本号、平台与日志片段（日志位置见[故障排查](docs/troubleshooting.md)）。**安全漏洞不要开公开 Issue**，走[私密安全报告](SECURITY.md)。
