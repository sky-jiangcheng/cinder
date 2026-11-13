# ADR-0059: 三端行为契约——单一权威与对等门

状态：**Accepted（2026-11-12 工程评估提出，同日用户选定 B + C 双门；未落码，跟踪 TODO 13.2）**
日期：2026-11-12
关联：[ADR-0048](0048-core-contract-version.md)（契约版本语义）、[ADR-0055](0055-agent-surface-contract-and-observability.md)（出口契约与文档门）、[ADR-0033](0033-mcp-ask-evidence-answer.md)（agent 与桌面端拿到同一个答案）、[ADR-0036](0036-ide-chat-console.md)（对话台的渲染与转义）
起始 schema：v31 → **v31（无 schema 变更）**；`CoreVersion` 不变

## 背景

桌面端、VS Code 插件、JetBrains 插件（再加 Warden）消费同一份记忆，但**只有「名字与版本」被门看住**：`scripts/mcp-tools.fixture.json` 钉住 22 个工具名与 `CoreVersion`，`check-version-drift.mjs` 校验文档里的工具数。**行为对等没有任何门**。

已经查实的三处行为漂移（与具体某端谁对谁错无关）：

- **作用域**：JetBrains 发问带工作区路径（`ide/jetbrains/src/main/kotlin/dev/repotory/jetbrains/SharedChatSession.kt:217-218`），VS Code 只传 `query`（`ide/vscode/src/chatSession.ts:462`）。同一句话在两个面板得到不同取证范围，表现为「会话同步、答案不同步」（TODO 11.1）。
- **兜底文案落库**：JetBrains 把 `answer` 原样当 `assistant` 消息写进共享表（`SharedChatSession.kt:196`），并明确保留关键词兜底的措辞（`:222`）；另一端会把它读成一次真回答（TODO 11.2）。
- **轮次配对**：共享轮次的分组/配对在两端各写一遍（`ChatModel.kt:233` 的 `sharedMessageGroups` vs `ide/vscode/src/sharedChat.ts:36` 的 `sharedTurns`），是否完全等价没有门（TODO 11.3）。

后果不是理论上的：JetBrains 面板「首次打开即构建中断」被记为**每一版都带的存量崩溃**（`CHANGELOG.md:75`）；11.4 的存储型 XSS 也是先在插件里落地才发现。`go test ./...` 看不到 Kotlin / TS 面板，`internal/app` 绑定层在 2026-11-11 评估时覆盖率 15.8%——**CI 的 19 道门里没有一道覆盖用户真正点击的那一层**。

## 决策（2026-11-12 用户选定）

**B 与 C 都要**：一份 conformance fixture 管「规则是否对等」，一个 headless E2E 冒烟管「面板还能不能打开」。两者回答的不是同一个问题，缺一个就有一半盲区：

| 门 | 回答的问题 | 输掉时看得见什么 |
|---|---|---|
| B conformance | 同一输入，三端是否按同一条规则产出同样的作用域 / 轮次 / 兜底标记 | 哪一端、哪条规则、期望值与实际值 |
| C E2E 冒烟 | 用户真正点击的那一层是否还活着（检索 → 深链 → 发问） | 整个面板崩了（含「首次打开即构建中断」那类） |

B 的 fixture 是**目标契约**，不是「当前两端的行为快照」——把现状钉进 fixture 等于把漂移固化成标准，那样门永远是绿的。由此引出一条落地顺序：**fixture 只能收已拍板的规则**。

- 已定、现在就能钉的：11.2（关键词兜底必须标注且**不落库**，ADR-0033 已要求标注、ADR-0036 决策 8 要求渲染走转义）、11.3（轮次配对只有一份权威规则，契约在 Cinder 侧）。
- 未定、fixture 里显式 skip 并注明原因的：11.1（作用域带不带 `path`——共享主题属于工作区还是属于人，是产品决定，不在本篇）。C 的冒烟路径**不依赖** 11.1 的答案：两端各自按自己的参数发问，断言的是「能检索到、深链能打开、发问能回」，不是「答案逐字相同」。

**权威仍放服务端**（A 的精神），但本轮不做架构改动：B 的期望值全部按「服务端口径」写，哪端实现不出来，那就是要修的漂移——这正是 B 相比 A 的价值：A 需要三端同时升级，B 先把差距量出来。

## 待定问题（已被上面的决策回答，原文保留）

1. 权威放哪：Cinder 服务端（客户端只渲染）还是各端各自实现、只靠契约约束？
2. 「对等」的判据是什么：同一输入必须同一答案、同一作用域、同一轮次切分，还是仅要求不崩？
3. 门放哪：CI 里的端到端冒烟、共享 conformance fixture，还是两者都要？
4. 11.1–11.3 是产品决定（共享主题属于工作区还是属于人），不在本篇替它们拍板。

## 选项与取舍

| 选项 | 做法 | 优点 | 代价 / 风险 |
|---|---|---|---|
| A 服务端权威 | 作用域、配对、兜底标记等规则全部在服务端决定，客户端只渲染 | 一份实现，漂移面最小；与「薄客户端、零逻辑复制」纪律一致 | 需要契约显式携带 scope / pairing 等字段；改一处要三端同时升；离线降级路径要另定 |
| B 共享 conformance 套件 | 一份 golden fixture（请求 → 期望答案 / 轮次 / 标记），各端跑自己的实现 | 能定位到具体哪端漂移；不强制改架构 | 三套 harness 的维护成本；fixture 会随产品演进而腐化；不覆盖真实 UI |
| C 只加 E2E 冒烟门 | CI 起 headless，驱动两个面板走「检索 → 深链 → 发问」 | 直接证明「用户点的那层还活着」 | 慢、易 flaky、需要 GUI/IDE 运行环境；单次覆盖路径少 |
| D 现状 + 文档 | 人工同步 + 各自单测 | 零成本 | 已被两次发布级缺陷证明不够 |

## 明确不决

- ~~不选 A/B/C/D~~ —— **已选定 B + C（2026-11-12）**；不修改任何客户端代码直到门立起来（先有红，再改行为），不新增 MCP 工具，不改 `CoreVersion`。
- 不把 11.1–11.3 的产品决定写成已决定；它们仍以 TODO 11.1–11.3 的形式待拍板，其中 11.1 在 fixture 里是显式 skip。
- 不要求三端共享同一份 UI 代码。

## 验收判据

- **B**：`scripts/` 下有一份 conformance fixture（请求 → 期望作用域 / 轮次 / 兜底标记），三端各有一个 runner 读它；注入式负例——改一端的参数或配对规则，门必须红**并指出是哪一端、哪条规则**。fixture 与 `CoreVersion` 的关系写进文档：规则变了要涨 minor，删规则涨 major。
- **C**：新增一个 CI job，起 headless 服务，驱动 VS Code / JetBrains 面板各完成一次「检索 + 深链 + 发问」，任一端失败即红；**必须能在无显示环境下跑**（headless IDE 或 webview 的 DOM harness），否则它就是一条会长期飘红然后被忽略的门。
- 文档里写清「权威在哪、规则版本是多少」，且该描述与 fixture 同源（沿用 `check-version-drift.mjs` 的做法：数字从 fixture 派生，不写死）。

## 落地记录（一）（2026-11-13，门 B）

**门 B（conformance fixture）已落码**；门 C 于同日随后落地，见下文「落地记录（二）」。验收判据逐条对账：

- **「`scripts/` 下有一份 conformance fixture（请求 → 期望作用域 / 轮次 / 兜底标记）」——已落**：[`scripts/shared-chat-conformance.fixture.json`](../../scripts/shared-chat-conformance.fixture.json)，结构守卫在 [`internal/conformance`](../../internal/conformance/fixture.go)（settled 必带 expect、skipped 必带 `assert_forbidden` 与 TODO 编号、规则数 = `contract_version.rules`）。**settled 5 条**（只收已拍板的规则）：`pairing.attach` / `pairing.order` / `fallback.not_persisted` / `fallback.not_persisted.zero_hit` / `fallback.local_render`；**skipped 3 条**：`scope.path`（等 TODO 11.1）、`pairing.orphan_reply` 与 `pairing.multiple_replies`（等 TODO 11.3 的产品决定）。**fixture 与 `CoreVersion` 的关系写进文档并落进门**：工具面不变 CoreVersion 不动（2.0.0）；规则数变化涨 `contract_version.rules`（`check-version-drift.mjs` 第⑪门核对），删规则视为行为破坏须另走 ADR。
- **「三端各有一个 runner 读它」——已落，三端 = JetBrains / VS Code / Go**：Kotlin `SharedChatConformanceTest`（真实 `sharedMessageGroups` + `SharedChatSession`）、TS `ide/vscode/src/__tests__/conformance.test.ts`（真实 `sharedTurns` + `SharedChat.send`）、Go `cmd/mcp/conformance_test.go`（fixture 引用的兜底文案 = 服务端 `keywordFallback` 实际产出——fixture 引文与服务端互相钉住）。三个 runner 各自接进既有 CI job（`gradle test` / `npm test` / `go test`），未新增 job；fixture 文件三处同读，不复制。
- **「注入式负例——改一端的参数或配对规则，门必须红并指出是哪一端、哪条规则」——已验证**：TS 侧翻转 `pairing.order` 的期望顺序 → 红（点名 VS Code 面板 diverged）；删除 `sharedChat.ts` 的兜底拒绝 → `fallback.not_persisted` / `.zero_hit` 双红；KT 侧把 `sharedMessageGroups` 退回旧 createdAt 排序 → `pairing.order` 红；fixture 重复规则 id → 第⑪门红。全部恢复后全绿。
- **「文档里写清权威在哪、规则版本是多少，且与 fixture 同源」——已落**：权威 = Cinder 服务端契约（本文件「决策」节），规则版本 = `contract_version.rules`（现 8），第⑪门保证 fixture ↔ TODO 叙述的规则 id 一致。

**先有红的真实发现（fixture 落地当天抓到并修掉，正是本 ADR 预言的差距）**：

1. **KT `sharedMessageGroups` 按 createdAt 重排**——三个客户端三台钟，跨钟字典序不保时序；真机探针证实时钟偏斜下轮次顺序**倒转**（q1 先问被排到 q3 后面）。已改为保留服务端读序（seq ASC），配对仍按 `replyTo`；旧测试「tolerates interleaving reversed order」测的是被模拟出来的服务端乱序，已改为钉契约。
2. **两个 IDE 面板都把关键词兜底文案当答案落库**（TODO 11.2 的两端半边）——VS Code `SharedChat.send` 与 KT `SharedChatSession.transportAsk` 拿到 `No AI endpoint is configured…` 匹配列表后照常 `append` 成 assistant 消息，另一端把它渲染成知识。均改为拒绝存储（抛错走本地失败标记；**零命中分支 `No results found for: …` 无该前缀，单独匹配**——这正是 fixture 把它单列为一条规则的原因）。

**诚实边界**：门 B 只证明「规则对等」；「面板还打得开」由门 C 接住（见下）。`fallback.local_render` 在 TS 侧是结构性安全（DOM `textContent`）+ 不落库断言，未做逐字节转义比对；`scope.path` / 孤儿 / 多回复仍等产品决定。

## 落地记录（二）（2026-11-13，门 C）

**门 C（headless E2E 冒烟）已落码**。验收判据：**「起 headless 服务，驱动 VS Code / JetBrains 面板各完成一次『检索 + 深链 + 发问』，任一端失败即红；必须能在无显示环境下跑」——已落**。

- **驱动的是面板自己的模型，不是复制一份逻辑**：两侧都用各自产品代码里的会话模型（TS `ChatSession` / Kotlin `ChatSession`）经各自 `Transport`/`ChatTransport` 端口驱动。ADR-0036 当初把模型放在端口后面就是为了这一天——所以门里没有 IDE、没有 webview、没有显示，跑在 `ubuntu-latest` 上。
- **`ChatSession` 驱动真的二进制**：`cinder-mcp`（stdio JSON-RPC）+ `cinder-server`（回环 HTTP，含 `--web-dist` 与 API-only 两种形态）。门自己构建二进制（[`scripts/e2e-gate-c.sh`](../../scripts/e2e-gate-c.sh)），不吃 artifact——陈旧产物会测昨天的代码。
- **三程各自的断言**：① 检索——真实 MCP 返回可渲染的命中（标题与摘要都要有）；② 深链——命中产出 SPA 路由**且指向检索真正返回的那一行**；UI 服务被识别为可打开，**API-only 服务必须被识别为不可打开**（ADR-0035 决策 6 的静默 404 防线）；③ 发问——答案来自模型桩（本地假 OpenAI 端点，走完整个工具调用环）**且带来源清单**。
- **发问用的假端点**：`/chat/completions` 返回一条固定回答，因此这一程不需要模型、不需要网络、不花钱，但仍走真实的取证 + 提示装配 + 工具环代码路径。
- **注入式负例（逐条验证过，全部恢复后全绿）**：KT 侧把 `servesWebUi` 改成恒 true → 深链拒绝分支红；TS 侧让 `parseSearchHits` 返回空 → 第 1、2 程红（第 3 程仍绿，说明各程断言独立）；TS 侧裁掉来源清单 → 第 3 程红。
- **CI**：新增独立 job `e2e-gate-c`（唯一同时需要 Go + Node + JDK 21 的任务；塞进任一 IDE job 都会把工具链拖进一个刻意保持精简的 job）。本地一条命令：`bash scripts/e2e-gate-c.sh [vscode|jetbrains|both]`。

**门 C 落地当天抓到并修掉的三个真实缺陷（都是门自己先红）**：

1. **MCP 子进程继承真实 HOME** —— 两个 MCP 客户端都无法指定子进程环境，门跑起来读到的是**用户真实的 Cinder 库与配置**，于是「发问」拿真实库去答，答案与种子数据无关。两个客户端补 `env`（生产不设，保持继承语义），门显式传隔离 HOME。**这条最值得记**：一道会污染用户真实数据目录的门，比没有门更糟。
2. **Gradle 门被缓存成假绿** —— 第二次运行报 `Task :test UP-TO-DATE`，929ms「通过」，实际一个进程都没起。配置了门变量时强制 `outputs.upToDateWhen { false }`。**「缓存绿」与「没跑」不可区分**，这道门必须每次真跑。
3. **门依赖纯净数据库** —— 种子笔记 id 被硬编码成 1，同一 HOME 复跑时 id 变了就红（错因指向产品，实际是门写得脆）。改为从写入结果读回真实 id。另有一条：Gradle **守护进程**持有启动时的环境，`export` 对构建不可见（第一次「通过」其实是静默跳过），因此门变量走 **Gradle 属性**而非环境变量。

**诚实边界**：门 C 证明三程在无显示环境走通，**不等于** webview 渲染像素正确（那仍归 `preview-console.mjs` 目视 + 三端 DOM 断言）；JetBrains「首次打开即构建中断」那类**只在真实 IDE 里复现**的崩溃不在本门覆盖范围（本门连 IDE 都不启动）；深链只验证「URL 正确 + 服务端是否服务 UI」，不验证打开后的页面内容。
