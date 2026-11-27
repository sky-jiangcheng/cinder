# 0062 IDE 面板的项目锁走 MCP——workspace 解析进 MCP 工具面，项目锁不再需要本地服务

- 状态：**Accepted（2026-11-15 起草即拍板、同日落码）**
- 相关：[ADR-0035](0035-jetbrains-mcp-primary-channel.md)（MCP 主通道 + HTTP 补缺的通道分层，本篇是其决策 2 的补缺项）、[ADR-0036](0036-ide-chat-console.md)（项目锁的界面与降级措辞）、[ADR-0044](0044-ide-panel-windsurf-shape.md)（项目识别与 `BindWorkspaceToProject`）、[ADR-0048](0048-core-contract-version.md)（契约版本语义）、[ADR-0059](0059-three-surface-behavior-parity.md)（三端行为对等门）

## 背景

### 现象：一句与面板实际状态不符的提示

两个 IDE 面板打开时，`refreshScope()` 会尝试把当前工作区文件夹解析成项目并锁定面板。解析走 HTTP 桥的只读绑定 `ResolveWorkspacePath`（VS Code [chatSession.ts:150-188](../../ide/vscode/src/chatSession.ts#L150-L188)，JetBrains [ChatSession.kt:109-147](../../ide/jetbrains/src/main/kotlin/dev/repotory/jetbrains/ChatSession.kt#L109-L147)）。**一旦这次 HTTP 调用抛异常（连接拒绝/超时），面板顶部就挂出**：

> The local Cinder service is not reachable at http://127.0.0.1:18765 — the project lock needs it, so the panel searches across all projects.

这句话在两个事实面前是失真的：

1. **面板并没有坏**。ADR-0035 之后，检索、读、写、建、整理、项目列表全部走 MCP，不经过 18765——服务不在时面板的八项数据能力照常可用，只有项目锁拿不到。提示的措辞把「面板降级」讲成了「面板不可用」。
2. **这个服务对多数用户本来就不在**。监听 18765 的是 `repotory-server`（`cmd/server`）；桌面 App 是 Wails 进程、不监听任何端口（ADR-0001 决策 M1），`scripts/install.sh` 只装 `repotory` + `repotory-mcp`、**不装 `repotory-server`**；而 `repotory-mcp` 有 install.sh / Homebrew / Scoop **三条分发渠道**，`repotory-server` **一条都没有**（ADR-0035 遗留 ①）。也就是说，用户按官方渠道装完，项目锁是**唯一**一个必然要依赖一个没被安装的东西的能力。

而且这条横幅**不会自动消失**：webview 只在点 ✕ 时清空（[chatConsoleHtml.ts:1197-1199](../../ide/vscode/src/chatConsoleHtml.ts#L1197-L1199)），`refreshScope` 只在激活、面板就绪、工作区文件夹变化时各跑一次——服务是后启动的，横幅会一直挂着，直到用户手动关掉或换工作区。

### 事实一：项目锁留在 HTTP 是 ADR-0035 的刻意选择，但它当时面对的问题不是传输层

ADR-0035 决策 2 定下「八走 MCP，两留 HTTP」：`MoveNote` 留 HTTP 是因为「re-filing knowledge is a human decision an agent must not make」；另一处留 HTTP 的是「服务存在性 / web UI 探测、深链」。`ResolveWorkspacePath` 作为项目锁的传输，随后者一起留在了 HTTP 侧——**它当时需要的不是论证，是它没有 MCP 孪生**。ADR-0035 决策 8 的边界是「不新增 MCP 工具」，所以这个依赖点被原样继承了下来。

### 事实二：`SurfaceDesktopOnly` 那条标注反对的是「第二套匹配器」，不是「第二套传输」

`internal/app/surface.go:258` 把绑定标为：

```
"ResolveWorkspacePath": {SurfaceDesktopOnly, "", "agents pass `path` to repotory_context instead; a second client-side matcher is the disagreement this avoids"},
```

[bindings.go:973-980](../../internal/app/bindings.go#L973-L980) 的注释给出同源理由：匹配规则必须留在服务端，客户端复制一份「这个目录属于哪个项目」的匹配器，正是 agent 与侧边栏对「一次会话该拿到哪些知识」发生分歧的根源。

**这个理由反对的是「客户端（插件内）复制匹配逻辑」，不是「经 MCP 传输调用服务端同一份解析」。** MCP 工具与 HTTP 绑定打的是同一个 service 层（ADR-0035 事实五），`repotory-mcp` 进程内直接调 `svc.ResolveProjectByPath`——匹配规则仍在服务端，面板拿到的仍是同一份 `ProjectResolution`。**经 MCP 调用不产生第二套匹配器**：它和今天经 HTTP 调用拿到的是同一个函数、同一个结构体、同一份规则。

### 事实三：人侧面板经 MCP 调绑定面，是 ADR-0035 已授权的形态，不是新接口

ADR-0035 决策 1 把术语钉死：「MCP 是唯一执行接口」一律读作「MCP 是**唯一 AI** 执行接口」，**人侧界面调的是绑定面，两种通道都合法**。两个 IDE 面板本身就是人侧客户端——八项数据能力今天就是经 MCP 调用的。给面板加一个 MCP 只读解析工具，是既有授权形态的延伸，不违反任何一条既有决策。

### 事实四：agent 不需要这个工具，这恰好是它能安全暴露的理由

`repotory_context` 的 `path` 参数已经在服务端完成同一份解析（ADR-0033 决策 ④：`path` 由服务端用同一个匹配器解析）。新工具不改变 `repotory_context` 的任何行为，只是把同一个**只读**解析暴露给需要**显示**解析结果的人侧客户端。它是纯读、无写、无副作用——即使模型出于好奇调用它，也不会触及任何写面或闸门（ADR-0015 的写侧闸门与它无关）。surface.go 那条理由真正的担忧（agent 拿它当第二入口、与 `repotory_context` 的解析分叉）用**工具描述 + 契约测试**守住，而不是靠不暴露来回避。

## 决策

**七项决策**：

**决策 1：新增 MCP 只读工具 `repotory_workspace_resolve(path)`。**

- 入参 `path`（绝对目录），长度上限用 `bounds.MaxPathLen`——与绑定、`repotory_context` 的 `path`、`BindWorkspaceToProject` **同一个包里的同一个限值**。三条面对同一条路径的可接受长度不能给出不同答案（绑定注释已写明这个约束，[bindings.go:982-986](../../internal/app/bindings.go#L982-L986)）。
- 返回与绑定 `ResolveWorkspacePath` **逐字段同构**的 `ProjectResolution`（`Project` / `Repository` / `MatchedOn` / `Candidates`），调用同一个 `svc.ResolveProjectByPath`。
- **纯读、无写、无副作用**。工具描述必须写明：agent 常规不需要它——`repotory_context` 的 `path` 参数已包含同一份解析；它给需要**显示**「这个文件夹属于哪个项目」的人侧客户端（IDE 面板的项目锁）用。

**决策 2：两个 IDE 面板的项目锁改走该 MCP 工具，HTTP 绑定保留不删。**

- VS Code `ChatSession.refreshScope` 与 JetBrains `ChatSession.refreshScope` 的 `transport.rpc("ResolveWorkspacePath", …)` 改为调用 `repotory_workspace_resolve`。两端的 `Transport` / MCP 门面（VS Code 的 `callTool`、JetBrains 的 `RepotoryMcp`）本来就承载八项数据能力，解析只是第**九**项走同一条通道。
- HTTP 绑定 `ResolveWorkspacePath` **不删除**：桌面端与 web UI 仍在用它。本 ADR 只是让面板不再依赖它；删绑定是破坏性变更，不动。`internal/app/surface.go` 中该条目的标注**改为 `SurfaceMCPBacked, "repotory_workspace_resolve"`**——surface 表的语义是「这个绑定的 agent 可达性」，有了 MCP 孪生后标注必须跟上，理由列同步更新。

**决策 3：项目锁的降级语义不变，但提示措辞必须改。**

- 解析成功 / 无项目 / 有候选的三种状态与措辞**一字不动**（ADR-0036、ADR-0044 决策 6 的现状全部保留）。
- 触发条件改变：**只有 MCP 通道本身不可达时才挂横幅**，且措辞如实指向 MCP 通道（如「the MCP channel is not reachable — the project lock needs it…」），**不再说「local Cinder service」**——项目锁从今天起不再需要本地服务。横幅的手动关闭行为与 `refreshScope` 的重跑时机（激活 / 面板就绪 / 工作区文件夹变化）不变。

**决策 4：`MoveNote`、`BindWorkspaceToProject`、深链保持 HTTP，本篇一条不碰。**

- `MoveNote` 是 human-only，不进 MCP（ADR-0035 决策 2 与遗留 ②，产品口径未变）。
- `BindWorkspaceToProject` 是写操作、human decision（ADR-0044 决策 6），**项目锁的写侧（把未识别的文件夹归到项目）仍然需要服务**。这一点必须在文档里说清：本 ADR 消灭的是项目锁的**读侧**依赖，不是写侧。
- 深链物理上只有 `repotory-server --web-dist` 能提供（ADR-0035 决策 6），保持「尽力而为的增强」定位。

**决策 5：契约版本 minor bump——`CoreVersion` 2.0.0 → 2.1.0。**

- 新增工具属增量（ADR-0048 决策 ②：minor = 新工具 / 新可选字段 / 新接口）。`internal/version/core.go` 的 `CoreVersion` 与 `CoreVersionChange = "minor"` 同步推进，`cmd/mcp/main_test.go` 的 `wantCoreVersion` 一并更新（决策⑤⑥：契约版本钉在工具清单旁边，第⑧道检查兜两份拷贝）。ADR-0055 的 envelope 语义不受影响。
- 同步文档：`docs/features/ai-integration.md` 中英双份的 MCP 工具目录，以及随包分发的 skill 工具签名文档（`skill/repotory/SKILL.md`、`skill/repotory/references/mcp-tools.md`——ADR-0035 落地更正二的教训：签名取自 `tools/list` 实际响应，加了工具不同步就是漂移）。

**决策 6：同源契约测试 + 三端对等门跟上。**

- `TestToolsRegistered` 钉住新工具（工具数 +1）与新 `wantCoreVersion`。
- **新工具对同一输入返回的结果与 HTTP 绑定逐字段一致**——防两条面漂移，这是 ADR-0059「同源」思想在这条绑定上的具体化：解析规则改了，两条面必须在同一批测试里给出同一份答案。
- ADR-0059 的 conformance fixture 加一条规则：面板项目锁经 MCP 解析（把现有 `scope` 规则的传输层钉死，防将来有人把面板改回 HTTP 而不自知）。
- 两端的会话单测（VS Code `chatSession.test.ts`、JetBrains `ChatSessionTest.kt`）的假传输改为经 `callTool` 返回解析结果，覆盖「解析成功 / 无项目带候选 / 无候选」三态与 MCP 不可达的措辞。

**决策 7：不新增绑定、不改 schema、不动 `repotory_context`、不动桌面端与 web UI。**

本篇只做三件事：加一个 MCP 工具、两个面板的解析改走该工具、surface 标注与文档同步。

## 代价（接受）

- **工具面扩张一格**：agent 可见面多一个工具。尽管描述里写明「agent 不需要」，模型仍可能调用——它是纯读、无副作用，最坏情况是上下文里多一次冗余调用，不触及任何写面或闸门。
- **项目锁的依赖从 `repotory-server` 换成 `repotory-mcp`**。对按官方渠道安装的用户（repotory-mcp 三条渠道都在）是**净改善**——项目锁从「必然不可用」变成「随 MCP 一起可用」；理论上存在「只装 server 不装 mcp」的用户，但面板的八项数据能力本就依赖 repotory-mcp，这样的面板用户实际不存在。
- **项目锁的写侧仍依赖服务**：`BindWorkspaceToProject`（未识别文件夹归项目）无服务时仍不可用，面板如实报错。这是有意保留的（写操作 human-only），但意味着「无服务全功能」的边界仍然有一角——文档必须写明，否则用户会以为项目锁彻底无服务了。
- **横幅文案与两端措辞要改**，且按本仓惯例中英同批改（界面文案镜像门 `i18n-keys` 不直接覆盖 IDE 面板，但两面板的文案必须一致，ADR-0036 决策 ⑧）。
- **契约版本 minor bump**：当前没有任何生产代码对 `CoreVersion` 做严格校验（ADR-0048 事实二），无迁移成本；代价是版本号多走一格。

## 落地清单（实施要点，非实施计划）

| 位置 | 改了什么 |
|---|---|
| `cmd/mcp/`（新文件或并入 `tools_context.go`） | 注册 `repotory_workspace_resolve(path)`，调 `svc.ResolveProjectByPath`，`bounds.MaxPathLen` 校验，`makeJSONResult` 返回 |
| `cmd/mcp/main_test.go` | `TestToolsRegistered` 工具清单 +1；`wantCoreVersion` → `"2.1.0"`；新增「与 HTTP 绑定同字段一致」的契约用例 |
| `internal/version/core.go` | `CoreVersion` → `"2.1.0"`，`CoreVersionChange` → `"minor"` |
| `internal/app/surface.go` | `ResolveWorkspacePath` 条目改 `SurfaceMCPBacked, "repotory_workspace_resolve"`，理由列更新 |
| `ide/vscode/src/chatSession.ts` | `refreshScope` 改走 `callTool("repotory_workspace_resolve")`；不可达措辞指向 MCP 通道 |
| `ide/jetbrains/.../ChatSession.kt` | 同上，经 `RepotoryMcp` 门面 |
| `ide/vscode/src/__tests__/chatSession.test.ts`、`ide/jetbrains/src/test/.../ChatSessionTest.kt` | 假传输改经 `callTool`；补 MCP 不可达措辞用例 |
| `docs/features/ai-integration.md` / `docs/en/features/ai-integration.md` | MCP 工具目录加 `repotory_workspace_resolve` |
| `skill/repotory/SKILL.md`、`skill/repotory/references/mcp-tools.md` | 工具签名同步（取自 `tools/list` 实际响应） |
| `docs/features/` 与 IDE README 中「project lock needs the local service」的表述 | 改为「项目锁经 MCP，写侧归项目仍需服务」（中英同批） |

## 回归

`go build ./...` / `go vet ./...`；`go test ./...` 全绿（含 `TestToolsRegistered` 与新契约用例）；`node scripts/check-version-drift.mjs` 全绿（第⑧道检查负例仍有效）；`ide/vscode` 的 `npm test` 与 `ide/jetbrains` 的 `gradle test` 全绿；`docs-mirror` 门全绿（本篇中英同批）。手工核验：停掉 `repotory-server`、保留 `repotory-mcp`，打开两个面板——项目锁正常锁定，横幅不再出现。

## 落地记录（2026-11-15，同日落码）

决策一条未改，全部按上文落地：

1. **`cmd/mcp/tools_context.go`**：`repotory_workspace_resolve(path*)` 注册进 `registerContextTools`，调 `svc.ResolveProjectByPath`，`bounds.MaxPathLen` 校验 + 空路径拒绝，`makeJSONResult` 返回。
2. **`internal/version/core.go`**：`CoreVersion` 2.0.0 → **2.1.0**，`CoreVersionChange = "minor"`；`cmd/mcp/main_test.go` 的 `wantCoreVersion` 同步。
3. **`cmd/mcp/main_test.go`**：工具清单 +1；新增 `TestWorkspaceResolveMatchesBinding`——对仓库精确 / 仓库祖先 / 项目精确 / 项目祖先 / 未匹配五个路径，断言 MCP 工具的返回与 `svc.ResolveProjectByPath` 的 `MarshalIndent` **逐字节相等**，并钉住超长路径、缺参、空参三个拒绝分支。
4. **`internal/app/surface.go`**：`ResolveWorkspacePath` 条目改 `SurfaceMCPBacked, "repotory_workspace_resolve"`，理由列更新。
5. **两端面板**：VS Code `chatSession.refreshScope` 改走 `callTool("repotory_workspace_resolve")` + 新增 `parseWorkspaceResolution`（headless.ts，非 JSON 即抛错）；JetBrains 经 `RepotoryMcp` 门面 + `McpPayloads.parseWorkspaceResolution`（新增 `WorkspaceResolution` 数据类）。不可达措辞统一改为「The Cinder MCP channel is not reachable — the project lock needs it…」。
6. **测试**：两端会话单测的假传输改为经 `callTool` 合成解析结果；各补「MCP 通道不可达」措辞用例与「项目锁经 MCP 工具解析」的传输层断言。JetBrains 侧假传输合成 JSON 曾出现尾逗号（`null,}`）导致解析失败，已修——这正是「解析不成 JSON 必须报错」纪律的现场验证。
7. **文档**：`docs/features/ai-integration.md` 中英工具目录 + 「工作区路径解析」段、`skill/repotory/SKILL.md` 与 `references/mcp-tools.md`（22 → 23 个工具）、两个 IDE README 的「project lock needs the local service」表述、`scripts/mcp-tools.fixture.json`（+1）。
8. **回归实测**：`go test ./...` 全绿；`gradle test` 97 例全绿、`gradle buildPlugin` 通过；`npm test` 54 例全绿、e2e 门 C（真实二进制）3/3 通过；`check-version-drift`（23 工具 / 契约 2.1.0 / 文档计数）与 `check-docs-mirror` 全绿。

**顺带的一处环境事实**：本机 `~/.gradle` 的原生缓存损坏（`libnative-platform.dylib` 加载失败），用 `GRADLE_USER_HOME=/tmp/gradle-home` + Gradle 8.10.2（与 CI 同版本）绕过；`~/go` 的模块缓存写入被沙箱拦截，构建走 `/tmp` 下的 `GOCACHE`/`GOMODCACHE`。均为环境问题，与本 ADR 无关。
