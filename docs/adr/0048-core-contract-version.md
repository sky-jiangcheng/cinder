# ADR-0048: 记忆层契约有自己的版本号——core version 与发布版本分开走

- 状态：Accepted（2026-11-05，用户在「把 core 拆成第三个产品」的四个选项里选 (a)「先把两件零成本的做掉」）
- 日期：2026-11-05
- 关联：[ADR-0047](0047-resident-agent-separate-host.md)（两个产品各担一件事；其补注已定「运行时自证 + 人话警告」，本篇把那个「版本」定成契约号）、[ADR-0035](0035-jetbrains-mcp-primary-channel.md)（MCP 主通道）、[ADR-0032](0032-approved-wiki-page-visibility.md) / [ADR-0033](0033-mcp-ask-evidence-answer.md)（知识页工具面进 MCP 的那两次改动）、[ADR-0044](0044-ide-panel-windsurf-shape.md)（IDE 面板消费同一套对外接口）

## 背景

### 一个版本号在回答两个不同的问题

`internal/version/version.go` 里只有一个 `const Version = "2.30.0"`（SSOT 在 `wails.json` 的 `info.productVersion`，`scripts/bump-version.sh` 同步过去）。它同时挂在两个语义完全不同的位置上：

- **「这是哪个构建」**——发布版本。桌面 App 每几天发一版，改一点界面就涨一位；升级器（`internal/service/update.go`）拿它跟 GitHub 上最新的 release 比，安装信息（`internal/service/install_info.go:47`）拿它写进本地记录。
- **「客户端可以依赖什么」**——契约版本。`cmd/mcp/main.go:37` 的 `server.NewMCPServer("repotory-mcp", version.Version)` 把这个数报进 MCP `initialize` 响应的 `serverInfo.version`，**它本该回答「我连上的是一个还能对上的面吗」**。

把这两个数塞进一个变量，后果不是「不好看」，而是**两个都失去意义**：客户端拿到 2.30.0，读不出工具面有没有变过。

### 事实一：契约面很少动，构建面经常动

桌面端最近一串改动（ADR-0038 首页工作台化、ADR-0039 tab 化、ADR-0041 可拖拽机器人、ADR-0042 页面骨架）**全是前端布局**，`cmd/mcp` 那 18 个工具一个没动。反过来，`repotory_wiki_search` / `repotory_wiki_read` 进 MCP 工具面的时候（ADR-0032 / ADR-0033），桌面 App 的界面几乎没动。

**所以从版本号里读不出契约有没有变，是结构性的，不是巧合。**

### 事实二：客户端此前把 `serverInfo` 整个丢了

独立仓 `repotory-agent` 的 `src-tauri/src/mcp_client.rs` 拿到 `initialize` 响应只 `?` 掉、不接变量（ADR-0047 补注里记的原话：「服务端本来就在报，客户端此前把它整个丢了」）。两个 IDE 插件也不读它（全仓 grep `serverInfo` 只命中 ADR 与一条 JetBrains 测试里的假响应）。

**所以今天还没有任何生产代码依赖这个数的含义**——现在改它的语义，迁移成本是零。**这个窗口是暂时的**：等客户端真的开始读它，语义就不能随便换了。

### 事实三：发布版本号早就不是「构建号」

`Version = "2.30.0"` 是 semver 的形状，用途却是「发布序号」——桌面端一个月发了 30 个版本，中间大量是内部动作（`fix(test)` / `chore(packaging)` / `docs(changelog)`）。这本身没问题，问题只在于**没人能从一个 2.30.0 里读出契约有没有变**。

### 事实四：`Version` 是个 `const`，发布流水线里那条 ldflags 是空操作

`.github/workflows/release.yml` 传 `-X repotory/internal/version.Version=$APP_VERSION`，而 `-X` **只能改 `var`**，改不了 `const`——那条参数**一直是空操作**，版本号一直来自 `version.go` 里的字面量。

这不修（本篇与它无关），但它说明**发布版本号本来就只有一个真值**，因此本篇新增的契约版本号也必须**只有一个真值**——两份拷贝由 CI 兜住（见决策⑥）。

## 决策

**七项决策**：

① **契约版本单独一个数** —— 新增 `internal/version/core.go` 的 `const CoreVersion = "1.0.0"`。它与 `Version` **各走各的**：发布版本按发布节奏走（`bump-version.sh` + `wails.json`），契约版本按**契约面**走。

② **语义按契约变化定，不按发布定** —— `major` = 破坏性（工具被删或改名、字段含义变了）；`minor` = 增量（新工具、新可选字段、新接口）；`patch` = 契约没动。**界面改动不涨它，工具面改动必须涨它。**

③ **MCP 的 `serverInfo.version` 报契约版本** —— `cmd/mcp/main.go` 改成 `server.NewMCPServer("repotory-mcp", version.CoreVersion)`。理由：问「我能依赖什么」的人要的是契约号；发布号在启动日志与 `/health` 里都拿得到。MCP 的 `serverInfo` 只有 `name` 与 `version` 两个字段，没有第二个位置可放。

④ **`/health` 多带一个 `core_version`，`version` 一字不动** —— `version` 仍是发布版本，因为**升级器与安装信息都依赖它**（`update.go` 拿它跟最新 release 比），动它会改掉升级语义；`core_version` 是**新增键**，老客户端忽略未知键。

⑤ **契约版本钉在工具清单旁边** —— `cmd/mcp/main_test.go` 的 `TestToolsRegistered` 里钉一份字面量（`wantCoreVersion`）。**加/删工具时，那一行必须同时被改动**——这是「版本号与工具面一起动」这条规则的物理形态：它让「忘了涨版本号」变成一道红，而不是一个隐患。

⑥ **两份拷贝不许漂** —— `scripts/check-version-drift.mjs` 新增**第⑧道检查**：`internal/version/core.go` 的 `CoreVersion` 必须等于测试里钉的那一份。**负例已验**：把测试里那份临时改成 `9.9.9`，脚本立刻报红并 `exit 1`（验完已改回 `1.0.0`，重跑全绿）。

⑦ **不动 schema / 不新增 MCP 工具 / 不改任何工具的入参出参 / 不改发布流程** —— 契约版本是**描述**契约的数，不是契约本身。

## 代价（接受）

- **多了一个版本号，就多了一处该更新的地方** —— 加/删工具的人若忘了改 `wantCoreVersion`，第⑧道检查会红。这是**故意的**：它把「记得更新契约号」从记忆负担变成绕不过的关卡。
- **两个号从此可以各说各话** —— 用户看到「记忆层 v1.0.0」而桌面 App 是 v2.30.0，会问「哪个更新」。答案要写在明面上：**契约号回答「面变了没」，发布号回答「构建新不新」**，两者放在一起比较没有意义。
- **`serverInfo.version` 的语义变了** —— 今天没有生产代码依赖它（事实二），但**以后依赖它的客户端读到的将是契约号**。若将来有人在 stdio 通道上要发布号，MCP 的 `serverInfo` 没有第二个字段可放，只能另想办法（例如工具参数或 `instructions`）——**那时的取舍留给那时**。
- **`/health` 多了一个键** —— 老客户端忽略未知键，无害；但**任何把整份 `/health` 响应反序列化成固定结构体的代码**需要容错（本仓与两个 IDE 插件都没有这样的代码，grep 过）。
- **`Version` 仍是 `const`，发布流水线的 `-X` 仍是空操作** —— 本篇不修（与契约版本无关），但事实四照旧成立：发布号也只有一个真值。

## 落地

| 位置 | 改了什么 |
|---|---|
| `internal/version/core.go` | 新增 `CoreVersion = "1.0.0"`（含语义说明） |
| `cmd/mcp/main.go` | `serverInfo.version` 改报 `version.CoreVersion`；启动日志同时打发布号与契约号 |
| `internal/service/service.go` | `Health()` 多带 `core_version`，`version` 一字不动 |
| `cmd/mcp/main_test.go` | `TestToolsRegistered` 里钉 `wantCoreVersion = "1.0.0"` |
| `main.go` / `cmd/server/main.go` | 启动日志同时打发布号与契约号 |
| `scripts/check-version-drift.mjs` | 新增第⑧道检查（契约版本两份拷贝必须相等） |
| `docs/api/reference.md` / `docs/en/api/reference.md` | `/health` 的两个版本键写清各答什么 |

## 回归

`go build ./...` / `go vet ./...` 通过；`go test ./cmd/mcp/... ./internal/service/... ./internal/httpapi/...` 全绿（`/health` 的既有断言只认 `"status":"ok"`，新增键不影响它）；`node scripts/check-version-drift.mjs` 全绿；**第⑧道检查的负例验过**（改成 `9.9.9` 必红）。
