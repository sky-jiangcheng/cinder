# ADR-0056: 桌面对话台历史落本地快照，不并进跨端共享会话

状态：**Accepted（2026-11-11，用户报障「现在对话都没保存对话历史，切下 tab 就没了」，同日落地）**
日期：2026-11-11
关联：[ADR-0055](0055-agent-surface-contract-and-observability.md)（同批的四维加固）、[ADR-0051](0051-conversation-as-interface.md)（对话即界面）、[ADR-0047](0047-resident-agent-separate-host.md)（浮球是工作台内的视图，不是常驻助手）
起始 schema：v30 → **v30（无 schema 变更）**；`CoreVersion` 1.5.0 → **1.5.0**（不动 MCP 工具面 / 绑定面）

## 背景

### 根因：lazy 路由被卸载，不是 key 变化，也不是后端没写

`web/src/App.tsx:21` 把 `Home` 声明成 `lazy(() => import('./pages/Home'))`，`App.tsx:277` 的 `<Routes>` 按 pathname 换 element；左侧栏 `SideRail.tsx:105-123` 的 `<Link>` 指向六个顶层入口（`navEntries.ts:15-22`）。**用户口中的「tab」是这六个顶层入口**：点一下就卸载 `Home` → 卸载 `Home.tsx:51` 的 `HomeKnowledgeAssistant` → `HomeKnowledgeAssistant.tsx:240`（落地后行号顺延）的 `useState<DialogueMessage[]>([])` 重新执行一次，历史归零。

反向证据排除了另外两种解释：知识页的真 tab 用 `hidden` 隐藏而非卸载（`Knowledge.tsx:330,342`）；首页「问 / 搜」只 `setMode`（`switchMode`），不清 `messages`。悬浮 `AgentBot`（`App.tsx:255`）挂在 `<Routes>` **之外**，切页根本不卸载它——但它自己 `AgentBot.tsx:173-175` 每次提问覆盖式重置，本来就没有多轮历史。

对话状态全部是组件内 `useState`（`HomeKnowledgeAssistant.tsx:232-241`、`AgentBot.tsx:112-128`），无 Context / store，无 localStorage 聊天快照。前端走 `askAIWithEvidenceStream`（`transport.ts:296-303`），请求体只有 `{ project_id, question }`，**服务端无状态、不落库**；UI 里的「存为笔记 / 存为页面」是把单条回答另存成知识条目，不是对话历史。

### 后端其实有一套会话表，但语义对不上

migration v29 已建 `chat_threads` / `chat_messages`（`internal/db/chat.go:7-35`），`internal/service/chat.go` 五个方法齐备（List / Create / Read / Append / Rename），MCP `repotory_chat` 暴露给 Warden 与两个 IDE 插件（`cmd/mcp/tools_chat.go:13-108`）。但 `internal/app/bindings.go` **没有任何 chat 方法**，`internal/httpapi/server.go:52-57` 没有 `/api/chat`，`web/src/api/endpoints.ts` 从未封装过这四个调用。桌面 UI 与这套跨端共享会话之间从来没接过线。

直接把桌面对话台塞进 v29 有四处硬冲突：

1. `source` 枚举只允许 `warden` / `vscode` / `jetbrains`（`db/chat.go:24`、`service/chat.go:240`），桌面只能伪装成 `warden`，会混进 IDE 与 Warden 的共享线程列表。
2. 消息不可变（`db/chat.go:33-34` 的 `no_update` 触发器），而流式回答每个 delta 都在改同一条消息——只能在 `onDone` 写一次。
3. `ChatMessage` 只有 6 个字段（`service/chat.go:40-47`），装不下 `evidence` / `projectCards` / `knowledgeCards` / `searchGroups` / `isStreaming` / 作用域；恢复后引用列表与搜结果卡全丢，只剩纯文本。
4. `content` 非空校验（`service/chat.go:243`）会直接拒绝搜模式消息（正文为空，结果在 `searchGroups` 里）。

**用户要的是「切 tab / 刷新别丢」，不是「桌面与 IDE 同步对话」**。后者是另一个产品能力，需要扩契约、扩字段、定 source，不该塞进一次缺陷修复。

## 决策

### D1 首页对话台用 localStorage 快照，只存已完成的消息

- 单一 key `repotory.home.dialogue.v1`，值 = `DialogueMessage[]` 的 JSON；**完整保留富字段**（证据、项目卡、搜结果分组、作用域、回退标记）。
- 惰性 `useState` 初值从快照读；写入走 `useEffect` + 200ms 去抖（流式每 delta 都触发，不能每 token 序列化整个数组）。
- 只存 `!isStreaming` 的已完成消息，末尾截 50 条；坏 JSON / 非数组 / 字段畸形一律安全回落空数组。
- 「清空对话」同时删 key。读写做成两个模块级纯函数 `serializeDialogue` / `loadDialogue`，各自配测试（每个缺陷一道能红的门）。
- 零新依赖、零后端改动、零新类型。

### D2 明确不写 `chat_threads`

跨端共享会话（v29）继续由 MCP `repotory_chat` 独占，语义是「三端共享的纯文本问答、消息不可变、与知识层隔离」。桌面对话台快照是「本机富 UI 状态的恢复」，两者**不是同一份数据的两种存法**，不互为副本、不做同步。

若将来确实要跨端同步桌面对话，另开 ADR 并先解决：扩 `source` 枚举（加 `desktop`）、定义富字段的投影或扩展、确认「流式只在完成时落库」的交互语义。本篇不预先铺路。

### D3 悬浮 `AgentBot` 本轮不动语义

它是单轮覆盖式面板（每次 ask 重置 `answer` / `evidence` / `toolCalls`），没有「历史」可保存；它挂在 `<Routes>` 外，切页不丢。刷新/重开丢是事实，但它的产品定位正待拍板（见 TODO 12.11：保留 / 转型配置与健康助手 / 交由 Warden 承接），现在给它加持久化等于为一个可能删除的面板投资。

### D4 顺带修浮球尺寸（用户同批反馈「窗口太小、高度不够」）

`AgentBot.tsx` 的 `PANEL_WIDTH` 420 → **480**、`PANEL_HEIGHT` 560 → **640**；`panelStyle` 用 `Math.min(PANEL_HEIGHT, innerHeight - 24)` 这一个实际高度同时做 top 钳制与 `maxHeight`（原来两处各用一个数，600px 最小窗下面板会贴顶又溢出底部）；CSS `.panel` 补 `max-height: calc(100vh - 24px)` + `overflow: hidden` 兜底，`.thread` 的 `min-height: 120px` 改 `0`（flex 列里这个下限会把输入区挤出矮窗口）。**不改绑定、不加工具、不动 ADR-0047 的角色边界。**

### 这个 ADR 明确不做的事

1. 不做跨端 / 跨设备同步、不做会话列表 UI、不做线程改名。
2. 不恢复未完成的流式回答（重挂时没有流可续，写半成品只会留下半句答案）。
3. 不把对话历史写进知识层（ADR-0015 那道闸门：会话产物不是知识）。
4. 不新增 Wails 绑定、MCP 工具、HTTP 路由、schema 迁移；不改 `CoreVersion`、不动标识层。

## 代价

- **只在本机这一个浏览器上下文有效**：换设备、换桌面安装、清浏览器数据都会丢。这是与 v29 跨端会话的能力差，写进文档而不是假装一致。
- **配额有上限**：50 条 + `try/catch`，富字段很大的消息会被挤掉最旧的；`catch` 静默降级（内存里的对话照常可用），不弹错打扰用户。
- **两份「对话」概念并存**：`chat_threads` 与本地快照语义不同，未来接跨端时必须先读 D2，否则容易做成互相覆盖的双真值。
- **浮球尺寸变大仍不解决定位问题**：480×640 只是体感修复，ADR-0050 §⑦ 要求的「浮层不压正文」与该面板的产品价值仍未决，登记在 TODO 12.11。

## 验收（全部可测）

1. 序列化后再读回，`DialogueMessage[]` 逐字段相等（含 `evidence` / `searchGroups` / `projectCards`）。
2. 流式草稿既不写入快照，旧快照里的 `isStreaming` 项读回时也被丢弃。
3. 80 条消息截到 50 条且保留最新的 50 条；坏 JSON / 非数组 / 字段畸形不抛异常。
4. 顶层入口来回切换后对话流仍在；点「清空」后重进为空。
5. 600px 高的窗口下面板不溢出视口，输入区可见，消息区自滚。
6. `go test ./...`、`npm test`、`npm run build`、`npm run lint`、`check-docs-mirror`、`check-i18n-keys`、`check-version-drift` 全绿。

## 落地记录（2026-11-11）
> **补注（2026-11-12，用户拍板）**：助理机器人的四个定位选项中用户选 **B = 保留现状 + 只修尺寸**（D4 已落地）。因此「收敛为唤起首页对话台」「设置页健康卡片」「删除交 Warden 承接」三个中期项**均不启动**；「帮机器人改配置」不做——`internal/app/surface.go:151` 的隐私闸门与 ADR-0047 的角色边界一字不动。TODO 12.11 由「待拍板」改为「已拍板 B」。同批 D1（ADR-0055）把 `CoreVersion` 升到 2.0.0，本篇自身的元信息记录的是 11-11 当天的契约版本，不改。


- `web/src/pages/home/HomeKnowledgeAssistant.tsx`：`serializeDialogue` / `loadDialogue`、惰性初值、去抖持久化 effect、清空删 key。
- `web/src/pages/home/HomeKnowledgeAssistant.test.ts`：新增 4 例（往返保真 / 流式排除 / 50 条上限 / 坏数据），并把 key 字面量钉在测试里——改名而不迁移等于丢弃老用户已存的对话。
- `web/src/components/AgentBot.tsx` + `AgentBot.module.css`：D4 的六处尺寸与钳制修改。
- 中英 ADR、两份 index、TODO 12.1 / 12.11、CHANGELOG `[Unreleased]` 同批。
