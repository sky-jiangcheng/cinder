# ADR-0009: IDE 存在感——薄客户端分发策略（一键注册 → VS Code 扩展 → JetBrains 缓议）

- 状态：Accepted（M4 `repotory-init` 已落地：`scripts/repotory-init/`，2026-10-02）
- 日期：2026-10-02
- 关联：[ADR-0006](0006-scope-freeze.md)（范围冻结的调和条款）、[ADR-0007](0007-session-memory-protocol.md)（MCP 单一执行接口）、TODO 会话记忆路线（M4-M5）

## 背景

Repotory 现有的分发资产——MCP server、dsh Harness 插件、llms.txt——服务的对象全部是 agent，没有一样「跟人见面」：MCP 工具只出现在 agent 的聊天面板里，命令面板、状态栏、侧边栏这些 IDE 原生界面上，Repotory 不存在。这个盲区构成传播上的硬限制：

1. **agent 不会主动发现我们的插件**。没有任何机制让一个新用户的 AI agent 主动说「你应该装 repotory-mcp」——分发必须由人来完成，而人只在自己天天打开的界面里发现工具。
2. **口口相传需要可见的货架**。「你用什么管理项目记忆？」——如果答案是 IDE 里的一个面板、一条命令，对话有落点；如果答案是「一个装完就隐形的 MCP server」，传播链在第一步就断了。

能力侧并不缺：VS Code（Copilot Chat）与 JetBrains（AI Assistant / 内置 MCP 客户端）均已原生支持 MCP 注册，`docs/features/ai-integration.md` 也已写明手动接入路径。缺的是**接入摩擦**（手动改 JSON）与 **IDE 内「人」的体验**（零原生 UI）。TODO 会话记忆路线的 M4（`npx repotory-init` 一键注册）已立项未做，正是该缺口的第一优先解。

同时必须正面调和 ADR-0006：范围冻结冻结的是「平台化基础设施」（插件 SPI、HTTP server、多后端存储），不是「跟人见面的分发面」。本 ADR 以薄客户端纪律确保新分发资产不重启平台化。

## 决策

1. **薄客户端纪律**：MCP 保持唯一 **AI** 执行接口（ADR-0007；限定词「AI」由 [ADR-0035](0035-jetbrains-mcp-primary-channel.md) 决策 1 钉死——人侧界面调的是**绑定面**，不是执行接口），`internal/service` 保持单一事实源。一切新 IDE 资产（init 脚本、扩展、插件）都是薄壳——经 headless HTTP 或 MCP stdio 调共享服务层，零逻辑复制（与 `dsh-plugin-repotory` 同一范式）。违反此纪律的方案默认不进入路线图。
2. **第一步：M4 `repotory-init`**：一条命令完成 MCP 注册——写 `.mcp.json` / 客户端配置、探测 repotory-mcp 二进制、提示可选的 SessionEnd hook 配置——覆盖 VS Code 与 JetBrains 两大 IDE 家族的全部 MCP 客户端。纯脚本 + 文档，不新增基础设施，不与 ADR-0006 冲突。
3. **第二步：VS Code 扩展（唯一的 IDE 扩展）**：命令面板三条命令（context / handoff / search）、状态栏显示上次交接时间、侧边栏笔记检索并跳转文件、首次使用引导一键注册 MCP。一个 VSIX 同时覆盖 VS Code / Cursor / Windsurf 整个 fork 家族——这是「一次投入、全家族见面」的唯一形态。
4. **JetBrains 插件缓议**：独立 Kotlin/Gradle 代码库，双倍维护面；在收到真实需求信号（issue/star）并补写 ADR 论证前不立项、不写代码。
5. **传播原则（新增评估项）**：今后每个分发资产立项时必须回答「人在哪个界面上看见它」；只有 agent 能消费、人不可见的资产，需说明其服务的是存量用户的深度而非获客。

## 理由

- 分发限制是产品级约束而非营销问题：agent 不会替我们装插件，可见性必须由产品自己购买；IDE 扩展市场（VS Code Marketplace / JetBrains Marketplace）是唯一现成的、开发者主动逛的货架。
- VS Code 先行不是偏好是算术：一份 VSIX 覆盖 fork 家族（VS Code / Cursor / Windsurf），JetBrains 插件一份构建只覆盖一家且需要独立技术栈。先做覆盖面大的。
- M4 先行是因为它对所有人（包括不装扩展的终端用户）都去掉同一段摩擦，且成本是脚本级；扩展的 UI 投入建立在此之上。
- 薄客户端纪律让 ADR-0006 的维护成本担忧保持有界：IDE 扩展只随对外 API 演进（headless HTTP 端点 / MCP 工具面），不随内部实现演进。

## 后果

- 正面：产品在 IDE 内获得可见存在（命令面板 / 状态栏 / 侧边栏），新用户接入从手动改配置降到一条命令；一次 VSIX 投入覆盖整个 VS Code 家族；市场列表成为口口相传可以指认的落点。
- 负面：+1 个 TypeScript 扩展代码库（薄壳，维护面限于 API 层）；JetBrains 用户在扩展阶段仍走手动配置。
- 遗留：JetBrains 需求信号阈值（issue/star 数量级）待 VS Code 扩展上线后按真实转化数据定；扩展对 headless 服务的生命周期托管策略（自启 / 复用）沿用 dsh 插件的实践，实现时落文档。

## 修订记录（2026-11-02，[ADR-0036](0036-ide-chat-console.md)）

本 ADR 的决策原文保留。ADR-0036 **修订了决策 3 里侧边栏的形态**，不修订它的存在：

- **决策 3 的「侧边栏笔记检索」改成了「侧边栏对话台」**。面板不再是「输入框 + 缩略图列表 + 详情窗格」的检索器，而是一段对话流：一问一答是一轮，旧的轮次折叠起来，详情就地展开在那一轮里（ADR-0036 决策 1 / 3 / 7）。**侧边栏这个位置不变，变的是它装什么。**
- **面板第一次能回答问题**：`repotory_ask`（ADR-0033）接线，答案带来源清单。检索面早就有了，问答面是这一轮才有的——而「能回答问题」正是把记忆读成序列的前提。
- **作用域从全局变成「默认锁定当前项目」**：chip 上显示 `🔒 <项目>`，点开可解锁回跨项目（ADR-0036 决策 2）。锁定是默认值，不是限制。
- **命令面板的命令保留**，但 `Search Knowledge` / `Search Approved Knowledge Pages` 改为**往对话流追加一轮**而不是替换结果，并新增一条 `Ask the Knowledge Base`。
