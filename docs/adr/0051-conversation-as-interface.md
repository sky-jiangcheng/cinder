# ADR-0051: 对话即界面——以 Function Calling 工具面作为统一操作层做界面减法

状态：**Accepted（2026-11-06，用户按「用户体验最强、UI 最清爽」批准）**；2026-11-08 修订（桌面助手面板去 chrome，见文末修订记录）
日期：2026-11-06
关联：[ADR-0036](0036-ide-chat-console.md)（IDE 聊天台：共享会话的薄客户端纪律）、[ADR-0041](0041-draggable-agent-bot-and-shared-dashboard-scope.md)（可拖拽机器人与共享面板作用域）、[ADR-0045](0045-resident-desktop-bot.md)（机器人面板决策）、[ADR-0047](0047-resident-agent-separate-host.md)（两个产品各担一件事：桌面工作台 vs 助手）、[ADR-0048](0048-core-contract-version.md)（契约版本：本篇不涨契约、不改 MCP 工具名）、[ADR-0009](0009-ide-presence.md)（薄客户端纪律）
起始 schema：不变（本 ADR 不改 schema、不改 MCP 工具名、不涨 `CoreVersion`）

## 背景

### 起因

用户提出：**「Cinder 助手和 Warden 都使用 Cinder 的 Function Calling 能力简化界面设计，各种 IDE 插件的对话界面也可以简化。」**
即：把对话（配合 Function Calling 工具面）作为执行操作和获取数据的统一入口，界面只保留「对话承载不了」的东西。

### 事实一：Cinder 桌面端已有完整的 Function Calling 循环，但两张面各缺一半

桌面端内核已有 Function Calling 工具注册表（`internal/service/aitools.go`，`service.go` 构造时一次性注册，流式 `tool_call` / `tool_result` 事件以 Wails 事件回前端），8 个工具全是面板统计向（`query_daily_stats` / `list_projects` / `get_project_stats` / `query_project_stats` / `get_recent_commits` / `get_heatmap` / `get_project_overview` / `search_notes`），消费者为浮动机器人 `AgentBot` 与首页知识助手 `HomeKnowledgeAssistant`；但桌面注册表**没有**记忆/检索侧工具，MCP 工具面（20 个）又**没有**桌面那套从盘现算的统计（热力图、最近提交等）——两张面各缺一半，「Cinder 的 Function Calling 能力」今天并不是一套跨端的能力。

### 事实二：Warden 的模型请求里没有任何工具

Warden 的 LLM 请求体只有 `{model, messages, stream}`（`llm.rs`），没有 `tools`，没有 system 提示词，模型若返回 `tool_calls` 会被静默丢弃；`conversations.rs` 的 `context()` 显式拒绝 system 角色。Rust 侧已有一个前端**零调用**的通用命令 `mcp_call_tool`（`lib.rs`），可作为 FC 执行后端。Warden 因此长出了一批只读展示面板（量化面板等）和操作入口——本质上是「对话不会干活」的补偿。

### 事实三：两个 IDE 插件是薄壳，零 LLM 代码；但面板里还有大量 chrome

VS Code 与 JetBrains 插件是 repotory-mcp 的薄壳（`repotory_chat` 做跨端共享会话，`repotory_ask` 做问答），零 LLM 代码；但面板里留了 provider sheet/ProviderDialog、内联编辑器、与 Notes 模式重复的搜索钮、Copy 钮、VS Code 状态栏 handoff 计时器、JetBrains 面板的 Rename 按钮等 chrome——这些对话本身承载不了，却占着面板最显眼的位置。

## 决策

### D1 工具面对齐：MCP 工具面是跨端真相源，桌面注册表补记忆侧

MCP 工具面（`repotory_*`，`tools/list` 动态拉取，服务端给 schema）是跨端真相源，**不抄、不改、不另建一份**；桌面内部 FC 注册表补记忆/检索侧工具（`notes_search` / `wiki_search` / `ask` 映射到同一 service 层），让 Cinder 助手能答知识问题而不只是统计。两张面的对齐关系写进 `check-version-drift` 的断言，防止漂移。不动 schema、不动 MCP 工具名、不涨 `CoreVersion`（ADR-0048）。

### D2 Warden：Rust 侧加只读白名单的 FC 循环（本篇最大增量）

编排点在 `conversations::send`；连接时从 `tools/list` 过滤出只读白名单（`repotory_metrics` / `repotory_context` / `repotory_projects_list` / `repotory_projects_stats` / `repotory_notes_search` / `repotory_notes_read` / `repotory_wiki_search` / `repotory_wiki_read`），按 OpenAI 兼容格式塞进模型请求；模型若点名白名单外的工具（含服务端 advertised 的写工具），**客户端拒绝，给 tool 结果塞拒绝信息，永不执行**；`repotory_chat` 整个不进模型工具面——回复落库是 Warden 自己做的事，模型若能调 `append` 就是双写。轮数上限 5（与桌面 `maxToolRounds` 一致），超限后强制一轮纯文本作答，绝不空答。tool_call / tool_result 作 Tauri `Channel` 事件推给前端，前端按工具名渲染卡片；工具事件只在 UI 展示，**不写 `repotory_chat`**（它是 UI 层过程，不是会话产物）。无 Cinder 的临时对话不带工具、不带 system 提示词，单轮契约不变。

系统提示词由本地组装（工具使用规则 + 当前仓库路径，roadmap M2 欠下的「仓库上下文进模型请求」由此补上），不接受服务端注入的任何角色（保留 `context()` 对 system 角色的拒绝，改为只接受本层组装的 system）。

写类能力（`notes_create/update`、`repotory_chat` 的写操作等）留待 Phase 3 的确认机制（人在环回路：确认卡、命令全显、执行留日志），本篇不动。

### D3 Cinder 助手（桌面）：通用工具卡片取代每页手写卡

桌面已有 FC 循环；补一个按工具名渲染的通用工具卡片，把 `agentContext.ts` 每页手写的快捷问答卡逐步退役。**未配 AI 时的本地即时卡保留**——离线可用性是 AgentBot 的明文特性，不是冗余：未配 AI 走本地卡，已配 AI 走 FC 循环，同一张卡片样式。

### D4 IDE 插件：纯做减法（薄壳不引 LLM）

删除：provider sheet / ProviderDialog、面板内诊断按钮、与 Notes 模式 100% 重复的搜索钮、卡片内 Copy 钮、VS Code 状态栏 handoff 计时器、JetBrains 面板的 Rename 按钮（与 VS Code 对齐，rename 能力本身不删，只是不在面板暴露）。
保留：输入框+Send（唯一写路径）、消息流（历史即产品）、最小 scope 指示（Ask/Pages 服务端过滤、Notes 客户端过滤，无指示答案语义不明）、一条公告/错误通道、Sources 折叠列表（证据可见性是明文决策）、线程下拉。
顺手修四处跨端不一致：VS Code/JetBrains `open -a` 应用名统一、共享问答的 scope 语义统一（都传线程 path）、轮询口径文案 2.5s/3s 统一。

内联编辑器**本轮不退役**：它退役的前提是服务端 `repotory_ask` 升级为可带工具的问答（薄壳保持零 LLM 代码），留给后续立项；到那时用「对话指令 + 服务端提案 + 确认卡」代替，ADR-0015 的保存闸门收窄成一次确认，不删。

### D5 不可替代（写进守门注释）

对话输入与消息流、机密录入（API Key 只进系统钥匙串、永不回传 UI）、审批动作（永远在 Cinder Desktop 做）、系统级交互（托盘/窗口/拖拽/通知）、ADR-0015 保存闸门、ADR-0036 的 Sources 证据可见性。

### 这个 ADR 明确不做的事

- 不把 Warden 改成对 Cinder 服务端 AI 的调用端：Warden 的模型请求走用户自己的 provider（Rust 后端唯一客户端），Function Calling 是把服务端工具面作为工具提供给该模型。
- 不把 IDE 插件改成带 LLM 客户端的胖客户端：插件继续零 LLM 代码；插件侧「对话内指令」留给服务端 `repotory_ask` 的可选工具循环（后续立项）。
- 不动 Cinder 桌面工作台的浏览/编辑/审批主路径：那是桌面的产品身份（工作台 vs 助手），本篇只碰助手面板本身。

## 代价

- Warden Rust 侧需新增工具流解析（SSE `tool_calls` 片段重组）、循环、只读白名单、system 组装，并新增 e2e 测试（fake LLM / fake MCP 基础已就绪）。
- 界面减法会删除用户可能习惯的入口（量化面板、provider sheet 等）；配置入口收敛到桌面/CLI，插件里需保留「未配置时的引导文案」。
- 两张工具面的对齐要持续维护，靠 `check-version-drift` 的断言兜底。

## 未决（本篇明确不下，落地时记）

1. Warden 主窗量化面板：对话能答后，面板是**删**还是保留为常驻可视？（本 ADR 立场：删，伴侣窗「N 行 · N 次提交」指标行保留——那是提灯形态的一部分，且对话替代面板后常驻面板的价值只剩「常驻可视」本身）
2. 服务端 `repotory_ask` 带工具循环：是否立项（决定插件内联编辑器能否退役）
3. 桌面无 AI 时的本地即时卡：**保留**（确认：离线可用性是明文特性）

## 落地清单（分批，每批结束跑守门）

1. **P1 · Warden 最小 FC 循环**：`llm.rs` 请求带 `tools`、SSE `tool_calls` 片段重组、回灌；`conversations.rs` 只读白名单循环 + system 组装；前端工具事件渲染；主窗量化面板删除，伴侣窗指标行保留，记忆层工具清单卡收敛为单行计数；新增 e2e 测试（fake LLM 返回 `tool_calls` → fake MCP 执行 → 回灌 → 再生成；白名单守门：写类工具名不出现在模型请求里）。
2. **P2 · 工具面扩展与插件第一轮减法**：Warden 白名单扩到只读面；VS Code 删 provider sheet/搜索钮/Copy/诊断面板钮/状态栏 handoff 计时器；JetBrains 删 ProviderDialog/DiagnosticsDialog/搜索动作/诊断动作/Rename 钮/卡片 Copy；修四处跨端不一致；两插件 `tsc`/测试全绿。
3. **P3 · 服务端 `repotory_ask` 带工具循环 + 插件确认卡；插件内联编辑器退役；桌面注册表补记忆侧工具**（与桌面并行会话的那一方协调；本 ADR 不动桌面主路径）。


## 修订记录

### 2026-11-08：桌面助手面板只剩对话（推翻未决 3 的「本地即时卡保留」）

用户看过落地后的面板，判断「小机器人的界面还是太过于冗余，现在完全可以使用 Function Calling 去获取相关界面的信息了，清理掉一些对话框之外的元素」。**改的是 D3 的边界，不是 D3 的方向**：D3 说「补通用工具卡片，把每页手写的快捷卡**逐步**退役」，落地时却在那条路上多长出一层界面——一排模块 chip（侧栏已经有一份）、一个作用域选择器（模型用 `list_projects` 就能自己收窄）、每页的快捷问答 chip 与手写卡片（`agentContext.ts`，576 行，全部从本地 API 取数）、页脚的「记一笔 / 打开对话台」。同一份数据在面板里有了第二个副本，而副本会漂。

落地结果：

- 面板只剩四件东西：标题栏、消息区（问题 / 工具调用行 / 回答 / 依据）、输入框、发送。
- `agentContext.ts` 与其测试、`QuickCapturePanel.tsx` 退役（前端净删约 900 行）。「记一笔」能力仍在项目页的 `NoteSection` 与首页知识助手，不随面板一起消失。
- **未决 3 翻案**：无 AI 时的本地即时卡一并退役。理由不是「不重要」，而是那张卡展示的每一种数据（提交量、交接记录、待审页面、安装信息）都是同一份数据的第二个副本；而在没有配模型时，面板本来就没有答案——用卡片当安慰剂，只会让人以为它能答。未配置现在只留一行引导（去 设置 → AI）。
- 自由提问的作用域固定为全库（`project_id = 0`）：要收窄就由模型调工具，面板不再自带作用域控件（这一步同时简化了 ADR-0045 决策 4 的落地形态——那条决策的**语义**不变：桌面机器人仍是全局助手）。
- 顺带修好工具名的文案映射：locale 的键是 camelCase（`ai.tool.queryDailyStats`），事件里拿到的是线名（`query_daily_stats`），此前**永远走回退分支显示裸函数名**；现在先归一化再查表。
- 「存为笔记 / 存为页面」保留：它们作用在**这一次回答**上，属于对话的一部分，而不是面板的 chrome。

回归：`AgentBot.test.tsx` 13 例（打开/关闭、拖拽、流式回答、工具行、依据、归档、未配置引导、窗口钳制），`npm test` 395 例全绿，`npm run build` 与 `npm run lint` 全绿，`check-i18n-keys` / `check-i18n-interpolation` / `check-docs-mirror` 全绿；`agent.*` 词条从 17 条剪到 4 条（中英同批）。

## 同批写

- CHANGELOG（cinder 中英）、`TODO.md` 现状速查、ADR 索引（中英各一行，下一个编号从 `0052` 递增）。
