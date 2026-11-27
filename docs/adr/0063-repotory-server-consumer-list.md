# 0063 repotory-server 的消费者清单重钉——ADR-0062 之后谁还需要本地服务，以及 provider 配置这个实质缺口

- 状态：**Proposed（2026-11-15 起草）**——消费者清单为既定事实，渠道决策待所有者拍板
- 相关：[ADR-0035](0035-jetbrains-mcp-primary-channel.md)（决策 6 把 `repotory-server` 定为「用户自行获取的可选增强、不建分发渠道」）、[ADR-0062](0062-ide-project-lock-over-mcp.md)（项目锁移出 server，依赖面收窄）、[ADR-0044](0044-ide-panel-windsurf-shape.md)（IDE 面板的 provider 配置读写服务端 `ai_chat_*`）、[ADR-0048](0048-core-contract-version.md)（契约版本语义）

## 背景

### 事实一：ADR-0035 的「可选增强」定位建立在一个已过时的消费者清单上

ADR-0035 决策 6 把 `repotory-server` 定为「用户自行获取的可选增强」，**不建** install.sh / Homebrew / Scoop 渠道。当时的理由写得很实：**「VS Code 扩展从来没有 repotory-server 也照样发布了——深链不是任何一个客户端的可用性前提」**。

但那份消费者清单是隐式的，且已经过期：**ADR-0062（2026-11-15 落码）把项目锁移出了 server**——workspace 解析改走 MCP 工具 `repotory_workspace_resolve`，两个 IDE 面板的项目锁不再需要本地服务。也就是说，ADR-0035 时代「项目锁依赖 server」这个最大的消费项已经不存在了，剩下的依赖面**不是**「深链」这一项，而是一组性质不同的能力。

### 事实二：ADR-0062 之后，server 的消费者逐项核对

逐项核对「无 `repotory-server` 时各能力的实际行为」（两端面板与桌面端的代码路径）：

| 能力 | 通道 | 无 server 时 | 性质 |
|---|---|---|---|
| 项目锁（workspace 解析） | **MCP**（ADR-0062） | ✅ 照常锁定 | **已移出**，不再消费 server |
| 检索 / 读 / 写 / 建 / 整理 / 项目列表 | MCP | ✅ 照常可用 | 不消费 server |
| **IDE 面板 AI provider 配置**（`GetConfig` / `UpdateConfig` / `ListAIModels` / `TestAIChat`） | HTTP RPC | ❌ **配不了 AI 端点**，`repotory_ask` 永远走关键词回退 | **实质缺口** |
| `MoveNote`（改笔记归属项目） | HTTP RPC | ⚠️ 降级：内容已保存、归属未变，如实提示 | 可降级 |
| `BindWorkspaceToProject`（把工作区文件夹归到项目） | HTTP RPC | ❌ 报错（写操作 human-only，不进 MCP） | 如实报错 |
| 深链（跳 web UI） | HTTP | ⚠️ 尽力而为：预检 `servesWebUi`，无则如实提示 | 增强 |
| IDE 诊断 / 健康检查 | HTTP | 信息性，不阻塞面板 | 非阻塞 |

**关键发现**：ADR-0035 把「深链」当成 server 的代表消费者，但真正构成**实质依赖**的是 **provider 配置**——IDE-only 用户（装了 `cinder-mcp`、没有桌面 App）在无 server 时**没有任何配置 AI 端点的入口**：面板的 provider 表单走 RPC 写 `ai_chat_*`，写不了；桌面 App 是另一个配置面，但这类用户没有桌面。结果就是 `repotory_ask` 永远落在「未配端点 → 关键词回退且标注这不是答案」的路径上——**能力在、答案永远不是答案**。桌面用户不受影响（桌面直接写同一个库）。

### 事实三：「可选增强」与「实质缺口」不能混在一个定位里

ADR-0035 的「不建渠道」决策针对的是**增强**：深链不可用时面板照常工作，只是少了一个跳转。而 provider 配置缺失是**功能缺口**：那一类用户的核心问答能力（`repotory_ask` 答真问题）被卡死。把两者混在「可选增强」一个词里，会让渠道决策失去依据——**为增强建渠道不值，为实质缺口建渠道则要重新定位**。

## 决策

**五项决策**：

**决策 1：本 ADR 把消费者清单钉成唯一真值源。**

上表（「ADR-0062 之后 server 的消费者」）是 `repotory-server` 依赖面的唯一权威描述。后续任何关于 server 的讨论（渠道、定位、文档措辞）以这份清单为准，不再从 ADR-0035 的散文里反推。清单的维护纪律：**能力通道变了（如 ADR-0062 移走项目锁），同批更新本表**——清单漂移正是本轮需要重钉的原因。

**决策 2：provider 配置单独归类为「实质缺口」，不与深链同列。**

`GetConfig` / `UpdateConfig` / `ListAIModels` / `TestAIChat` 经 HTTP RPC 暴露，是 IDE-only 用户配置 AI 端点的**唯一**入口。它缺失时不是「少一个增强」，是「核心问答能力不可用」。文档（IDE README、`packaging/README.md`）与后续渠道讨论必须按这个分类说话——`packaging/README.md` 当前写的「手动获取（深链增强）」措辞需随本决策更新。

**决策 3：现在不建全渠道——维持 ADR-0035 决策 6 的「无 install.sh / Homebrew / Scoop」立场。**

理由：实质缺口（provider 配置）**不是加一个渠道就能闭合的**——用户装了 server 还得知道它存在、会去启动它，渠道只解决「装得上」，不解决「被使用」。在配置面的解法确定之前建渠道，是把一个定位问题包装成分发问题。渠道决策**推迟到决策 4 拍板之后**。

**决策 4：配置面的两条路——经 2026-11-15 复核，B 不可行，改推荐 A（分两步）。**

- **方案 B（provider 配置走 MCP）——经复核否决**。初稿曾推荐 B 并判「配置写入不是知识内容、风险低」，该判断**只对了技术半边**。两条已登记的承重决策直接否决 B：
  - **surface 注册表**：`GetConfig` / `UpdateConfig` 是明文 `SurfaceDesktopOnly`，理由「changing privacy gates must never be something an agent can do to itself」（`internal/app/surface.go:150-151`），且有完整性测试强制登记。暴露为 MCP 工具 = 反转这条决策——AI 端点 / 模型 / 密钥决定「用户的记忆查询发往哪里、用谁的 key」，是控制面变更，与 MoveNote / `BindWorkspaceToProject` 的 human-only 立场同族。
  - **单写者契约（ADR-0058）**：`UpdateConfig` 在 `bindingWrites` 中但**无目录方法**，被归类为 human-gate write——`DecideWrite` 对客户端进程返回 `WriteRefuse`、`GuardedLocal` 明确拒绝。落地 B 必须给配置写**新增目录方法**（动 ADR-0058 的写权分类），远超「加两个工具」的量级。
  - 技术风险低（agent 本机已有文件访问权；`GetConfig` 已掩码 `********`）不改变政策成本：B 是一次政策反转，不是功能实现。**B 仅在所有者明确决定反转隐私闸门立场时才重新评估。**
- **方案 A（给 `repotory-server` 加分发渠道 + 补一键启动）——推荐，分两步**：
  - **第一步 A-light**：给 VS Code 补一键启动 server（移植 JetBrains `RepotoryServerManager` 的模式：二进制解析 + 子进程 + `--web-dist`；server 二进制仍从 release 手动获取一次）。之后两个 IDE 的 provider 配置缺口都变成「点一下」，不需要任何渠道。
  - **第二步 A-full（按需）**：若一步后用户仍嫌「先下载一次 server」麻烦，再给 install.sh `--with-server` / Homebrew / Scoop 渠道。渠道是第二步，先验证一键启动是否已把摩擦降到可接受。
  - A 不碰工具面、不碰 surface 注册表、不碰写权契约——既有纪律原样保留；代价是 server 的定位从「可选增强」改为「IDE 用户的配置面」。
- **最终选择由所有者拍板**：本 ADR 推荐 A（A-light 先行）。

**决策 5：深链维持「尽力而为的增强」，本 ADR 不动。**

深链的不可用有预检、有如实提示、不阻断面板，符合「增强」的定位。ADR-0035 决策 6 对深链的结论继续有效，不因本 ADR 的清单重钉而改变。

## 代价（接受）

- **本 ADR 不改任何代码**：它是重钉 + 决策框架。选中的方案（A 或 B）各自需要独立的实现与门禁，不在本篇范围。
- **清单需要持续维护**：能力通道变化时同批更新本表——这正是本篇存在的理由，维护成本是这份清单的定价。
- **若选 B**（已否决，仅记录代价）：工具面扩张一格（`CoreVersion` minor）、两个面板的 provider 表单改传输层、skill 工具签名与文档同步；**且须反转 `UpdateConfig` 的 `SurfaceDesktopOnly` 登记**（surface 注册表 + 完整性测试）、**给配置写新增单写者目录方法**（动 ADR-0058 写权分类）、修订 ADR-0035「provider 配置走 HTTP」的定位——一次政策反转，不是功能实现。
- **若选 A（推荐）**：分两步。**A-light**（VS Code 一键启动 server，移植 JetBrains 模式）：两端 provider 配置缺口变为「点一下」，无渠道、无工具面改动；**A-full**（install.sh / Homebrew / Scoop 渠道，按需）：server 从「可选增强」改定位为「IDE 用户的配置面」，`packaging/README.md` 与 IDE README 的措辞、安装脚本、CI 打包面跟着动，维护面扩大。
- **不动作的代价**：IDE-only 用户的 `repotory_ask` 继续只能关键词回退——这是当前真实存在的状态，本决策不掩盖它。

## 落地清单（实施要点，非实施计划）

| 位置 | 改了什么 |
|---|---|
| `docs/adr/0063-repotory-server-consumer-list.md` / `docs/en/adr/...` | 本 ADR 中英双份 + 两份索引各一行 |
| `packaging/README.md` | `repotory-server` 的消费者描述从「手动获取（深链增强）」改为按本 ADR 决策 2 的分类（深链 = 增强；provider 配置 = 实质缺口） |
| IDE README（vscode / jetbrains） | 「哪些能力需要本地服务」按本 ADR 的清单口径表述（随选中方案同批更新） |
| TODO.md 5.6 | 标注本 ADR 已落、渠道决策待拍板 |

**选中方案后的追加**（不在本轮）：方案 A → 安装脚本 / tap / bucket 与 CI 打包面；方案 B → MCP 工具 + 两端 provider 表单 + `CoreVersion` minor + skill 签名 + 文档。

## 回归

本 ADR 无代码改动：`docs-mirror` 门（中英同批）与 `check-version-drift` 的 ADR 索引对齐检查全绿即可。选中方案的实现按各自门禁回归。

## 落地记录（2026-11-16，A-light 已落码）

**A-light 已按决策 4 的第一步落码**（所有者拍板 A，先做一键启动）：

1. **`ide/vscode/src/serverLauncher.ts`（新文件）**：移植 JetBrains `RepotoryServerManager` 模式——① 二进制解析顺序 `repotory.serverBinary` → `REPOTORY_SERVER_BIN` → PATH → 常见安装目录（`cinder-server` 优先、`repotory-server` 兼容名在后），配置了但文件不存在时**跳过继续**而非报错；② `start()` 先探 `/health`（**不与已运行实例争**）、只接受回环地址、按 URL 端口（默认 18765）构造 `--port`，二进制旁有 `web/` 时自动加 `--web-dist`；③ 健康等待 30s / 250ms 轮询，区分「已退出」（带日志尾部，`address already in use` 单独点名）与「活着但无应答」（`healthy=false`，不冒充成功）；④ `stopOwned()` 只停自己拉起的进程，`deactivate` 时清理。spawn / ping / sleep / 超时 / 日志路径全部可注入，单测无需真进程。
2. **接线**：新命令 **`Cinder: Start Local Service`**（命令面板）；诊断面板「本地服务」行在服务不可达时一键启动；面板 provider 表单读配置失败时公告条挂「启动本地服务」按钮（webview 按 `announce.action.target` 分派 `startServer` 消息，宿主执行命令）；新设置 `repotory.serverBinary`；`serviceOfflineHint` 文案同步指向一键启动。
3. **测试**：`ide/vscode/src/__tests__/serverLauncher.test.ts` 15 例——解析顺序（设置 > 环境 > PATH、配置缺失回落、全无则 null + 查找清单）、参数构造（`--port` 恒在、`--web-dist` 仅当旁有 `web/`）、回环与端口解析、`start()` 四个分支（已在运行 / 非回环 / 缺二进制 / 成功）、退出与端口占用的失败文案、活着但无应答不报健康、`stopOwned` 只杀自己拉起的。`npm test` **69 例全绿**（含既有 54 例）。
4. **文档**：`ide/vscode/README.md` 的需求段与设置表同步（一键启动的三个入口、解析顺序、获取路径）。

**A-full（渠道）未做**：待 A-light 在真实使用中验证「先下载一次 server」的摩擦是否可接受，再决定是否给 install.sh / Homebrew / Scoop 渠道。**B 维持否决**。

## 落地记录二（2026-11-16，方案 B 落码——配置写重分类为 catalogue-covered）

**所有者拍板 B：把配置写从 human-gate 重新分类为 catalogue-covered，provider 配置进 MCP。** 决策 4 的推荐随之翻转为 B，A-light 保留（一键启动仍是深链与诊断的补缺入口）。落码内容：

1. **writerapi 目录**：新增 `config.update` 目录方法（`ConfigUpdateArgs{Key, Value}`），`UpdateConfig` 绑定映射到它；执行器调用**同一个** `svc.UpdateConfig`（白名单与数值校验在两个面完全一致）。`Methods()` 列表 +1；`UpdateScanRoots` 保持 human-gate（授予文件系统范围是另一个决定）。
2. **surface 注册表**：`GetConfig` / `UpdateConfig` 改 `SurfaceMCPBacked`（工具名 `repotory_config_get` / `repotory_config_update`）；`TestBindingSurfaceHumanGateHolds` 与 `write_contract_test.go` 的审计清单**按重分类刻意更新**（`UpdateConfig` 从拒绝清单移入转发清单）。
3. **cmd/mcp**：新增 `repotory_config_get`（读全部配置，密钥掩码 `************`）与 `repotory_config_update`（经 `w.ExecuteInto(MethodConfigUpdate, …)` 走单写者契约；键名在工具描述里列出白名单）。`CoreVersion` 2.1.0 → **2.2.0（minor）**，fixture 23 → 25。
4. **两端面板**：provider 设置的读（GetConfig）与写（UpdateConfig ×3）改走 MCP 工具；两端 MCP 客户端不检查 `IsError`，写失败以 `error: …` 文本返回，按既有 ask 模式显式检查后抛错（新增用例钉住）。`ListAIModels` / `TestAIChatConfigured` 等**探测**保持 HTTP——它们不是配置写，无服务时如实降级。
5. **测试**：writerapi 新增 `TestConfigWriteIsCatalogueCovered`（客户端角色 `DecideWrite(UpdateConfig)` = WriteForward、`UpdateScanRoots` 仍 WriteRefuse、转发写落库）；cmd/mcp 新增 `TestConfigTools`（读掩码、写校验、未知键拒绝）；VS Code 新增 provider 三例（读走 MCP、写三键、拒绝文案）；JetBrains 新增拒绝写用例。
6. **契约面影响**：`UpdateConfig` 从「客户端进程拒绝」变为「转发给写者」——独立 server 以客户端角色运行时，配置写会转发给持写权的进程（桌面或自举的 MCP），不再需要 server 自身持锁。**代价照记**：agent 现在可以读写 provider 配置（含端点/模型/密钥）——这是所有者明确的政策反转，工具描述如实说明；`repotory_config_update` 的写仍受白名单与数值校验约束，且经单写者契约路由。

**修订（2026-11-26）：清单里的「深链」一行作废，决策 5 随之失效。**
ADR-0044 的第三次修订把卡片上的 **Open in Cinder** 换成了「就地问」，深链在两端都不再有调用方，
整条链路删除（见 ADR-0035 修订）。按决策 1 的维护纪律，本表同批更新：server 的消费者只剩
`MoveNote` / `BindWorkspaceToProject` / 一键启动 / 诊断四项，**没有「增强」类消费者了**。
决策 5（「深链维持尽力而为的增强」）不再成立——不是降级，是不存在；
`packaging/README.md` 的渠道说明按同一口径改过。

同批还把落地记录第 2 条 ① 里「二进制旁有 `web/` 时自动加 `--web-dist`」删了：两个启动器现在只传
`--port`。那个 flag 的用途就是让读者在浏览器里打开知识 UI，而深链没了之后面板从不 link 进去
（JetBrains 那条注释自己写着 "The plugin itself no longer links into that UI"，却仍在传参）；
插件起的服务唯一消费者是 human-only 的 `MoveNote`，没有理由在回环端口上多挂一份 SPA。
`--web-dist` 作为 server 自身的能力保留：想浏览的人自己带 flag 起服务（`ide/jetbrains/README.md` 教的就是这件事）。
