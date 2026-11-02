# ADR-0027: JetBrains 插件立项——薄客户端纪律下的第二个 IDE 分发面

状态：**Accepted（2026-10-22）**——落稿当日 4 项开放问题经项目所有者委托拍板（见「开放问题拍板」），同日晋升；排期另议
日期：2026-10-22
关联：[ADR-0009](0009-ide-presence.md)（本 ADR 修订其决策 4：JetBrains 由「缓议」转「立项」）、[ADR-0007](0007-session-memory-protocol.md)（MCP 唯一执行接口）、[ADR-0006](0006-scope-freeze.md)（分发面不是平台化）、[ADR-0017](0017-one-kernel-two-entry-points.md)（headless API 是插件消费面）、VS Code 扩展（`ide/vscode`，第一个 IDE 分发面）

## 背景

[ADR-0009](0009-ide-presence.md) 决策 4 将 JetBrains 插件定为缓议：「独立 Kotlin/Gradle 代码库，双倍维护面；在收到真实需求信号（issue/star）并补写 ADR 论证前不立项、不写代码」。该门槛里的「issue/star 数量级」是**公开分发场景下的代理指标**——用陌生用户的声量估计真实需求，避免为还不存在的用户群维护第二个技术栈。

2026-10-22 项目所有者本人直接给出需求信号并拍板立项。代理指标在此失去意义：所有者本人即 JetBrains IDE 的日常用户，需求信号以最直接的形态（本人明确指示）给出，不存在「为不存在的用户维护代码」的风险——维护者与用户是同一人。ADR-0009 缓议的实质理由（双倍维护面）依然成立，但它成立的前提（需求不确定）已经消失；本 ADR 即其遗留条款「收到真实需求信号后补写 ADR 论证」的执行。

能力侧事实（逐条核实过）：

- headless server（`cmd/server`，默认 `127.0.0.1:18765`，loopback-only）经 `/api/rpc` 通用桥暴露与桌面 Wails 端**完全一致**的绑定面，另有 REST 端点供 dsh 插件 / agent 使用——插件需要的全部能力（检索、笔记、wiki、证据取证）现成可用，且 `/api/rpc` 已被 Web 前端日常负载验证。
- MCP server（`repotory-mcp`）可注册进 JetBrains AI Assistant / 内置 MCP 客户端（ADR-0009 背景已确认 JetBrains 原生支持），但那只服务 agent 流；「人」在 IDE 里依然零原生 UI——命令面板、工具窗、状态栏上 Repotory 不存在，这正是 ADR-0009 要解决的可见性盲区在第二大家族上的延续。
- VS Code 扩展（`ide/vscode`）已落地并验证了薄壳范式：命令面板命令、状态栏、侧边栏检索，全部经共享服务层零逻辑复制。JetBrains 插件是该范式的第二次复用，不是新模式探索——设计风险已被第一个 IDE 分发面消化过一轮。

## 决策

1. **立项，承接薄客户端纪律**：插件是薄壳——经 headless HTTP（`/api/rpc` 为主）调共享服务层，零逻辑复制；**人侧界面调的是绑定面，不是执行接口**（MCP 保持唯一 **AI** 执行接口，ADR-0007；限定词由 [ADR-0035](0035-jetbrains-mcp-primary-channel.md) 决策 1 钉死）；插件只随对外 API 演进，不随内部实现演进。违反此纪律的子方案一律不入 MVP。（**通道形态已由 ADR-0035 决策 2 改为「MCP 主通道 + HTTP 补缺」**——本条描述的是 MVP 落码当时的形态。）
2. **MVP 范围（平移 VS Code 扩展范式）**：
   - ① 知识库检索工具窗：笔记 + 待办的 FTS/hybrid 搜索（走既有检索绑定 `SearchAll`；语义与图感知随 `semantic_search` / `wiki_graph_search` 门控自然生效，插件不自带门控逻辑、不重复实现排序）；
   - ② 结果跳转：在系统浏览器打开 Repotory SPA 定位到该结果（同源 server 地址；深链路由以前端现状为准，不足处随 MVP 补最小路由参数，不在插件内嵌完整 UI）；
   - ③ 设置页：server 地址（默认 `http://127.0.0.1:18765`）+ 连接健康检查 + 可选的 `repotory-server` 二进制路径（空 = 自动探测）；
   - ④ MCP 一键注册（复用 `repotory-init` 的注册思路，写 JetBrains 侧的 MCP 客户端配置）——若实现时发现 IDE 配置面过于不稳定，降级为文档指引，不阻塞 MVP。
3. **技术形态**：Kotlin + Gradle（IntelliJ Platform Plugin）；独立代码库。与 ADR-0009 预判一致的双倍维护面由此**明知接受**，其边界由薄客户端纪律压在 API 层。

## 开放问题拍板（2026-10-22，所有者委托统一裁定）

1. **目标 IDE 家族与版本基线 → IntelliJ IDEA CE 先行，平台级兼容，基线 242（2024.2+）**。本机实测所有者的 JetBrains 日常 IDE 为 IntelliJ IDEA CE（`/Applications/IntelliJ IDEA CE.app` = 2025.2.6.1，支持目录 `IdeaIC2025.2`，另有 `IntelliJIdea2024.3-backup`；全机无 GoLand——原问题「如 GoLand」的预设不成立）。插件声明 `com.intellij.modules.platform`：MVP 所需 API（工具窗 / 设置页 / 浏览器跳转 / HTTP）均为纯平台 API，同一构建即可装入全部 IntelliJ 家族（含本机 PyCharm CE），不按产品分叉。`sinceBuild=242` 覆盖所有者近期的 2024.3 与当前 2025.2 并留余量；开发与验收以 IDEA CE 2025.2 为准；公开分发前复核基线。
2. **分发方式 → Marketplace 公开上架 + GitHub Release zip 双轨**（2026-10-22 修订原「私有分发先行」）：插件进入 JetBrains Marketplace，随产品 tag 用 `gradle publishPlugin` 发布同一版本号；zip 继续作为 GitHub Release 资产，供离线/内网手动安装。签名可选（缺失则发布未签名包，Marketplace 接受），首次上架需所有者用 vendor 账号人工完成一次、并由 Marketplace 审核。
3. **会话捕捉 → 不入 MVP**：严格对齐 VS Code 扩展已验证的能力面，终端/捕捉集成留待真实痛点（采纳原建议）。
4. **server 生命周期 → 连接优先，允许插件一键启动本地实例**（2026-10-22 可用度修订，取代原文「仅连接」）：默认复用已在 `serverUrl` 上应答的实例；没有任何实例应答时，插件可以启动一个**本地** `repotory-server`。边界四条，缺一不可：① 只认用户已有的二进制（设置项 → `REPOTORY_SERVER_BIN` → `PATH` → 常见安装目录），插件不下载、不构建、不代管包管理器；② 只对 loopback 地址启动，远端地址一律拒绝；③ 只在 `/health` 无人应答时启动，绝不与既有实例抢端口；④ IDE 退出时只停止自己拉起的子进程，用户自启的实例不动。发布侧相应地新增 `repotory-server-<platform>` 资产（二进制 + `web/`，插件自动传 `--web-dist`），否则「一键启动」对最终用户不可兑现。ADR-0009 遗留的生命周期托管问题在 JetBrains 侧按此落定；VS Code 扩展仍以 MCP + 按需 headless 连接为主，不在本轮扩围。

## 后果

- 正面：JetBrains 家族获得 IDE 内可见存在（工具窗 / 设置页），传播货架补齐第二块；薄客户端纪律使维护面限于 API 层演进；检索后端直接受益于 lane 2 生产管线（`semantic_search` + 图感知取证已开启，2026-10-22），插件零额外成本继承全管线口径。
- 负面：+1 个 Kotlin/Gradle 独立代码库（双倍维护面——0009 缓议的原始理由，由所有者在需求信号明确的前提下明知接受）；一份构建只覆盖 JetBrains 一家，无 VSIX 式 fork 家族摊薄。
- 中性：不触碰范围冻结（`repotory` 二进制名 / 数据目录 / MCP 前缀 `repotory_*` 不变；插件是分发面不是平台化，ADR-0006 的调和条款继续适用）。
- 遗留：Marketplace 上架材料（描述 / 图标 / change notes）已随 2026-10-22 上架配置补齐，签名走 CI secrets（可选）；剩余的两件外部动作——所有者创建 vendor 账号并完成首次上传、Marketplace 侧的首版审核——不在代码库内，记于 `TODO.md`。

## 实施修正记录（2026-10-22 MVP 落码时逐条核验）

MVP 已落码于 `ide/jetbrains/`（Kotlin + Gradle，8 个源文件），`buildPlugin` 与 `verifyPlugin`（对 IDEA CE 2025.2）均通过。以下记录实现期实测到的事实与对决策文本的修正。

### 决策 2① 的「wiki 页」检索 —— 实测两端都缺中间未接，已修正决策文本

决策 2① 原文写「笔记 + wiki 页的 FTS/hybrid 搜索（走既有检索绑定）」。落码时逐层核验，真实情况是**三段各自的状态**：

| 层 | wiki 页检索的状态 |
|---|---|
| db 层 | ✅ **齐备**——`db.SearchWikiPages`（`internal/db/wiki_fts.go:91`）：`wiki_pages_fts` FTS5 trigram 索引 + bm25(3.0, 1.0) 排序 + **仅 approved** 过滤（ADR-0015 的核心规则：未经人审的页不得进入检索）+ 与笔记路径共用的 `escapeFTS` / `escapeFTSOR` 松弛逻辑（中文整句走 trigram 展开）。ADR-0014 W2 落的，有测试（`wiki_compile_test.go` / `wiki_community_test.go`） |
| service / app 绑定层 | ❌ **未暴露**——`SearchAll`（`internal/db/search.go:205`）只覆盖 notes + todos；app 绑定的检索类只有 `SearchProjects` / `SearchNotes` / `SearchAll`，**无 wiki 页检索入口**。ADR-0014 注释里那句「search is the first thing a consumer needs, and W2 is that consumer」——W2 早落了，**消费者一直没来** |
| 前端 | ❌ **无浏览面**——`web/src` 里没有任何组件渲染已批准 wiki 页的内容（`Review.tsx` 只有 pending 审核队列与 rejected 墓碑两处读 `p.content`），`/knowledge` 与 `/review` 都不接受页面参数，路由表（`web/src/App.tsx:324-333`）无 wiki 页条目。真库里 **27 个 approved 页存在但 UI 上完全不可见** |

VS Code 扩展（第一个 IDE 分发面，ADR-0027 要求「平移」其范式）走 `repotory_notes_search`，**同样只搜 notes**。

**定夺：修正决策 2① 为「笔记 + 待办」（走既有 `SearchAll` 绑定），wiki 页检索另立待办。** 依据三条：

1. **薄客户端纪律要求插件零逻辑复制**。插件要搜 wiki 页，服务层就必须有入口——而现有入口不存在。插件自己写 FTS 查询（哪怕复制 db 层那几十行）就是**逻辑复制**，直接违反决策 1。
2. **补后端绑定单独做并不能闭环**。即使 service/app 层暴露了 `SearchWikiPages`，插件点开一个 wiki 命中后**无处可落**——前端没有已批准页的浏览面与可寻址路由。决策 2② 要求「在系统浏览器打开 Repotory SPA 定位到该结果」，落点不存在时这条要求无法兑现。真正的缺口是**「已批准 wiki 页对用户不可见」**这一独立产品缺口（27 个页在 UI 上不可见），它不是插件的问题，补它属于新功能而非本 ADR 的收口。
3. **平移原则优先**。ADR-0027 决策 2 的标题即「MVP 范围（**平移 VS Code 扩展范式**）」，而 VS Code 侧能力面就是 notes。MVP 与其范式对齐，比在 MVP 里塞一个范式之外的半成品（搜得到、跳不过去）更符合决策本意。

因此 **wiki 页检索与浏览面作为独立待办记入 `TODO.md`**，需先落 ADR 再动工：涉及新增已批准页的浏览界面与可寻址路由、暴露 `SearchWikiPages` 绑定、以及让两个 IDE 分发面都消费它。

### 决策 2② 的深链参数 —— 以前端现状为准，已核

插件初版按 guess 写了 `?note=` 与 `?page=`，实测前端真值是 `?noteId=`（`web/src/pages/ProjectDetail.tsx:58` 读 `searchParams.get('noteId')`）与 todo 走 `?tab=todos`（`web/src/App.tsx:323-337` 路由表）。已按前端现状修正，并明确「深链路由以前端现状为准」不是可以自造参数——深链存在的唯一理由是**跳到用户能看见的地方**。

### 落码期修掉的四处真实缺陷

编译验证（`buildPlugin` + `verifyPlugin` 对 IDEA CE 2025.2）暴露了四个初版代码里的真实问题，均已修：

| 缺陷 | 症状 | 修法 |
|---|---|---|
| `IntelliJPlatformType` 未导入 | Gradle 脚本期编译失败 | 补 `import org.jetbrains.intellij.platform.gradle.IntelliJPlatformType` |
| Kotlin 编译器版本低于平台 metadata | 大量「Module was compiled with an incompatible version of Kotlin（binary 2.2.0, expected 2.0.0）」+ 内部编译器错误 | Kotlin 插件 2.0.21 → **2.2.0**（编译器不得比它要读的 metadata 更旧） |
| `RepotoryPanel` 实现 `Disposable` 却当 `JPanel` 用 | `layout` / `add` 全部「Unresolved reference」 | 改为 `JPanel` + `Disposable`（工具窗需要 Swing 组件，同时要能随 content 释放） |
| `getToolWindow` 入口用错 | 「Unresolved reference 'getToolWindow'」 | 正确入口是 `ToolWindowManagerEx.getInstanceEx(project).getToolWindow(id)`（`com.intellij.openapi.wm.ex` 包，`WindowManager` 上没有带 project 的重载） |

另修一处 `plugin.xml` 缺 `<description>` —— Plugin Verifier 判定为「Invalid plugin descriptor」（`<description>` 是插件描述符的必填项），已补。

`verifyPlugin` 对 IDEA CE 2025.2 报 **无 compatibility problem**；`sinceBuild=242` 低于构建目标 252 是有意的（决策 1 要求覆盖 2024.2+），已在 `build.gradle.kts` 注释中写明「构建目标是最新的 IDE，声明的底线是插件承诺支持的最低版本」——插件只用了 242 就存在的平台 API（工具窗、设置页、Swing、`java.net`），这正是 Verifier 要证明的事。

## 可用度修订（2026-10-22，用户实测反馈后）

用户实测反馈两条：① 不清楚项目用 JDK 8 时插件能否使用；② 插件必须先在终端把服务跑起来，纯界面操作走不通。前者是文档缺口，后者是决策 4 的产品缺口——「仅连接」把最麻烦的一步留给了用户。本轮修订如下。

### `untilBuild = "243.*"` 是真实缺陷，已移除

初版声明 `sinceBuild=242` + `untilBuild="243.*"`，本意是「覆盖 2024.2 到 2024.3」。实际语义是**上限锁死**：2025.1 及以后（包括构建目标 2025.2 本身）都会被判为不兼容而无法加载。`build.gradle.kts` 的注释还写着「untilBuild 243.* 让插件也能装在 2024.2」——方向写反了。已改为只声明 `sinceBuild=242`、不设上限。技术上站得住：插件只用 242 就存在的平台 API，未来平台若破坏这些 API，由 Plugin Verifier 而不是一个写死的年份来发现。

上架前复核打包产物时又发现第二层同一个坑：**只删掉 `untilBuild = "243.*"` 并不够**。IntelliJ Platform Gradle Plugin 的 `patchPluginXml` 在属性未被显式赋值时会自动补 `<idea-version until-build="<构建目标>.*"/>`（本仓库实际产出 `until-build="252.*"`），也就是把上限悄悄改回「构建它的那个 IDE」。必须显式写 `untilBuild = provider { null }` 才会真正省略；已按此修正，并从打好的 zip 内解出 `plugin.xml` 确认只剩 `<idea-version since-build="242" />`。

### Java 8：与项目 JDK 无关，文档已写明

插件运行在 IDE 自带的 JBR（2024.2+ 为 Java 21），不加载项目 JDK、不编译用户代码、不依赖 Java 8 API。因此 **JDK 8 项目可以正常使用插件**；唯一的 Java 21 要求出现在「从源码构建插件」这一步（`jvmToolchain(21)`），与最终用户无关。这条已写入 `ide/jetbrains/README.md` 的 Requirements 与 Compatibility 表，避免再被当成安装前提。

### 一键启动本地服务（决策 4 修订的落地）

新增 `RepotoryServer.kt`（应用级 service）：

- 解析顺序：设置项 `Server binary` → `REPOTORY_SERVER_BIN` → `PATH` → Homebrew / Scoop / `~/.local/bin` 等常见目录；找不到时弹「Set Path… / Open Download Page / Cancel」，**不下载、不构建**。
- 启动命令：`repotory-server --port <serverUrl 的端口>`；若二进制同目录存在 `web/index.html`，自动追加 `--web-dist <dir>/web`（发布包即此布局），深链因此开箱可用。
- 安全边界：先 `ping`，有人应答就复用；非 loopback 地址拒绝启动；只停止自己拉起的子进程（`Disposer` 挂在 application 上，IDE 退出时清理）。
- 入口：工具窗检索失败时状态行旁出现 **Start local service** 按钮（仅传输层失败时出现，4xx/5xx 不出现），设置页也有同名按钮；成功后自动重跑当前查询。

发布侧同步：`release.yml` 新增 `Build headless server bundle`，构建 `repotory-server` + `npm run build:server` 产物 `web/`，按平台打成 `repotory-server-<target>.tar.gz` / `.zip`；`packaging/README.md` 资产表已补。没有这项，「一键启动」对最终用户仍然是一句空话。

### 未随本轮改动

- MCP 一键注册仍不在 JetBrains MVP（决策 2④ 的降级条款继续生效，`repotory-init` 只打印手动指引）。
- VS Code 扩展不再扩围到「启动 headless 服务」——它的 MCP 路径已能独立工作，且本轮已改为多源二进制解析 + 静默注册 + 诊断面板。

## Marketplace 上架（2026-10-22，修订拍板 2）

所有者明确「IDEA 插件也要上插件商店」，拍板 2 由「私有分发先行」改为「Marketplace 公开上架 + GitHub Release zip 双轨」。代码与发布侧改动：

| 面 | 改动 |
|---|---|
| 构建 | `build.gradle.kts` 增加 `publishing { token = providers.environmentVariable("JETBRAINS_MARKETPLACE_TOKEN") }`，`gradle publishPlugin` 上传 `buildPlugin` 产物；三个签名环境变量（`CERTIFICATE_CHAIN` / `PRIVATE_KEY` / `PRIVATE_KEY_PASSWORD`）**仅在齐全时**配置 `signing`，缺省则发布未签名包（Marketplace 接受，只少「verified vendor」徽章） |
| 元数据 | `pluginConfiguration.changeNotes` 指向 `CHANGELOG.md`（不复制第二份发布叙事，ADR-0017 准则 3）；新增 `META-INF/pluginIcon.svg` / `pluginIcon_dark.svg`（40×40，同一 R 字形几何重排） |
| CI | `release.yml` 新增 `publish-jetbrains-marketplace`：`needs: create-release`，只在 tag 上跑；`JETBRAINS_MARKETPLACE_TOKEN` 缺失时 `::warning::` 跳过，zip 仍挂在 GitHub Release，发布不会因缺凭据失败 |
| 版本 | 不向 `publishPlugin` 传版本参数：`build.gradle.kts` 的版本由 `scripts/bump-version.sh` 从 `wails.json` 同步，Marketplace、GitHub zip 与桌面端共用同一个号 |

**仍属人工的两步**（不在代码库内）：① 所有者用 vendor 账号完成**首次**上传——Marketplace 会创建插件页并分配数值型公开 URL，此后版本可由 CI 自动发布；② 首版审核。插件 ID `dev.repotory.jetbrains` 一经首次上传不可更改，故在首次上传前定稿。

**包内核验**：`buildPlugin` 成功后从 zip 解出 jar 内的 `plugin.xml`，确认 `<idea-version since-build="242" />`（无 `until-build`）、`<change-notes>` 与两个 `pluginIcon*.svg` 都在；`gradle verifyPlugin` 仍为 Compatible。

## 修订记录（2026-10-30，[ADR-0035](0035-jetbrains-mcp-primary-channel.md)）

本 ADR 的三条决策被后续 ADR 修订，原文保留：

- **决策 1 的「唯一执行接口」补上「AI」限定词**——人侧界面调的是**绑定面**，不是执行接口。ADR-0035 决策 1 把这层含义钉死：否则本条「插件走 HTTP」与「不新增执行接口」落在同一句里，自相矛盾。
- **决策 1 的「经 headless HTTP 为主」不再是终态**——ADR-0035 决策 2 把插件改为「MCP 主通道 + HTTP 补缺」，与 VS Code 扩展同构：八项数据能力走 MCP，`MoveNote`（human-only，不进 MCP）与服务存在性探测、深链两项保留 HTTP。
- **决策 4 的「一键启动」定位降级**——ADR-0035 决策 6 把深链与一键启动定为**尽力而为的增强**，`repotory-server` 保持「用户自行获取的可选增强」，**不为此新建 install.sh / Homebrew / Scoop 渠道**。资产本身照发（决策 4 的「发布侧新增 `repotory-server-<platform>` 资产」已兑现），只是不再声称「唯一分发路径 = 插件一键启动」。设置页与工具窗的 **Start local service** 按钮保留，但它的用途从「使用前提」变成「补缺与深链的增强入口」。

## 修订记录（2026-11-02，[ADR-0036](0036-ide-chat-console.md)）

本 ADR 的决策原文保留。ADR-0036 **修订了决策 2① 的工具窗形态**：

- **决策 2① 的「知识库检索工具窗」改成了「对话台工具窗」**。工具窗不再是「搜索框 + 结果列表 + 详情窗格」，而是一段对话流：一问一答是一轮，旧的轮次折叠起来，详情就地展开在那一轮里（ADR-0036 决策 1 / 3 / 7）。**工具窗这个位置不变，变的是它装什么。**
- **工具窗第一次能回答问题**：`repotory_ask`（ADR-0033）接线，答案带来源清单——检索面早就有了，问答面是这一轮才有的。
- **作用域从全局变成「默认锁定当前项目」**：chip 上显示 `🔒 <项目>`，点开可解锁回跨项目（ADR-0036 决策 2）。Ask 与 Pages 两种模式由服务端**真过滤**，Notes 模式因工具面无项目参数而在插件侧**呈现层过滤**——界面明说过滤掉多少，不假装命中数就是全部。
- **与 VS Code 侧同构**（ADR-0036 决策 8）：同一套结构、同一套措辞、同一套折叠规则。Swing 的渲染限制（`HTMLEditorKit` 只认部分 CSS、不能画折叠箭头）只影响「怎么把 Swing 画成 HTML」这一层，不影响看得见的语义。
