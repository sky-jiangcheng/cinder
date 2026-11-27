# ADR-0044: IDE 面板改成 Windsurf 形态——动作条收进面板、补上 AI provider 与项目识别

- 状态：Accepted（2026-11-03，用户给出 Windsurf 参考截图并逐项提出）
- 日期：2026-11-03
- 关联：[ADR-0036](0036-ide-chat-console.md)（面板是对话台；两个 IDE 同构）、[ADR-0035](0035-jetbrains-mcp-primary-channel.md)（MCP 主通道 + HTTP 补缺）、[ADR-0033](0033-mcp-ask-evidence-answer.md)（`repotory_ask` 的证据答案契约）、[ADR-0009](0009-ide-presence.md)（薄客户端纪律）、[ADR-0045](0045-resident-desktop-bot.md)（桌面机器人常驻与模块切换）

## 背景

用户拿 Windsurf 的 Cascade 面板截图与 Repotory 的 VS Code 面板截图并排，提出四件事：
**照 Windsurf 改面板设计**、**修掉红框里那个被裁掉的按钮**、**简化文案与按钮**、
**补上外部 AI provider 配置让插件的对话能力追上桌面端的机器人**。
外加一条功能性的：**插件在「认不出当前项目」时应该能识别项目并设置，而不是把选择器禁掉。**

拆开看是四个独立的缺口，其中只有第一个是纯视觉的。

### 缺口一：红框那个按钮不是「排版没调好」，是宿主把它裁了

红框圈住的是 VS Code 侧栏标题栏右上角的第三个图标（滑块 = `repotory.diagnose`）。
它之所以贴着面板右边缘、只露出一半还带一个焦点方框，根因在 `ide/vscode/package.json`：

```json
"menus": { "view/title": [
  { "command": "repotory.search",      "when": "view == repotory.notesSearch", "group": "navigation" },
  { "command": "repotory.openDesktop", "when": "view == repotory.notesSearch", "group": "navigation" },
  { "command": "repotory.diagnose",    "when": "view == repotory.notesSearch", "group": "navigation" }
]}
```

VS Code 把 `navigation` 组的按钮排在视图标题栏**最右侧**，而这个视图是 webview，
面板宽度由用户自己拖；三个图标挤在标题栏里，第三个越过了面板右缘被宿主裁掉。
**插件的 webview 画不出这块地**——它属于宿主的 chrome，插件只能注册命令。
换句话说：只要动作还在 `view/title` 里，就**没有任何 CSS 能救它**，
唯一能救它的办法是**把这三个动作从宿主 chrome 里搬进面板自己画的那块地**。

而 Windsurf 的面板恰好就是这么做的：动作全在自己那一行标题栏右侧，
宿主标题栏上什么都不挂。**参考对象给出的正是解法本身。**

### 缺口二：插件的对话能力比桌面机器人少一档

桌面端的机器人有完整的外部 AI provider 配置（设置页「AI」标签：`ai_chat_base_url` /
`ai_chat_model` / `ai_chat_api_key`，OpenAI 兼容的 `/chat/completions`，
`internal/service/ai.go` 负责实际调用）。插件侧却**一个入口都没有**：
`repotory_ask` 走的是服务端同一套配置没错，但**插件没法告诉服务端「端点是这个」**——
用户只能在桌面端设置页配好，插件才能回答问题。

于是同一个产品有两套能力分级的诡异状态：**桌面端能配，插件不能配**。
用户诉求里的「具备桌面端 App 的机器人的对话能力」落在这里。

薄客户端纪律（ADR-0009）说的是**检索与排序不下沉到客户端**，
它没说**配置只能桌面端能改**。而 `/api/rpc` 桥本来就暴露了 `GetConfig` / `UpdateConfig` /
`ListAIModels` / `TestAIChat` 四个绑定（它们在 `surface.go` 里标 `SurfaceDesktopOnly`，
那个标记的含义是「**没有 MCP 双胞胎**」，不是「HTTP 桥够不着」——`surface.go` 的注释把这条
区别写在最前面）。所以插件端 provider 配置的正确做法不是重写一个 LLM 客户端，
而是**读写服务端同一份配置**。

### 缺口三：认不出项目时，用户被挡在门外

工作区路径 → 项目的解析是四级优先匹配（`internal/service/workspace.go`
`ResolveProjectByPath`：路径全等的 repository → 最深的 ancestor repository →
路径全等的 project → 最深的 ancestor project）。
命中不了时，插件面板把 scope chip 设成 `disabled`，写上 `No project`，
公告条告诉用户「这个文件夹还不在知识库里，面板会跨项目搜索」——**然后就没有下一步了**。

但服务端其实已经把路铺好了：`ProjectResolution.Candidates` 会带回候选项目列表；
`db.UpsertRepositoryTx(tx, path, projectID)` 也已经存在，
它就是扫描时把仓库目录登记到某个项目名下的那一步。**缺的只是一个「让用户选」的动作。**

### 缺口四：文案与按钮在讲产品，不在讲动作

当前空状态是四段营销文案（"Repotory keeps what your coding agents learn — why a decision was
made, what gotcha you hit, what to do next — in a local, searchable store…"）外加一段快捷键提示。
面板标题行还有一行 `The local-first memory layer for AI coding agents` 的 slogan。
Windsurf 那张截图里对应位置是：一句欢迎语 + 一个 `Learn More` 按钮 + 一张 `DID YOU KNOW` 提示卡 +
底部一个 `Advanced` 链接。**它讲的是「现在能干什么」和「下一步去哪」，不是「我是什么」。**

## 决策

**① 面板的动作条收进面板自己画的那一行，`view/title` 一条不挂。**
VS Code 侧删掉 `contributes.menus["view/title"]` 的三条注册；那三个命令**保留**
（命令面板与快捷键仍然可用），只是不再由宿主画。
JetBrains 侧本来就用自己的 Swing 头部，不受此条影响，但**头部图标集合要与 VS Code 一致**。

**② 头部 = 品牌 + 右侧一排图标按钮，图标用内联 SVG。**
左边是品牌标记与字标（不再挂 slogan 那一行——Windsurf 的标题行也只有标题）；
右边四个图标，顺序即 Windsurf 的视觉权重：
**搜索**（切到 Notes 模式）、**设置**（AI provider 面板）、**诊断**（环境诊断）、
**桌面端**（打开 Repotory 桌面应用）。
图标内联进 webview 的 HTML（和品牌标记同一手法），**不依赖 `media/` 资源根**——
webview 的 `localResourceRoots` 没配，引用外部文件会 404，而 404 的图标在 VS Code 里
表现为**一个空白的方块**，比缺一个按钮更难排查。

**③ 底部 = 一个输入框 + 一条工具行；发送键放进输入框。**
输入框右下角是发送键（Windsurf 的形态：⏎ 键在输入框内）。
输入框下方是工具行：左边是**模式组**（Ask / Notes / Pages 三态），
右边是**项目 chip**。两者都是 chip 形态（Windsurf 底部工具行也是 chip 行）。
**模式组与项目 chip 分处两端**，中间留白——Windsurf 的底部行是三段等距的 chip，
而 Repotory 的模式组有三个成员、项目 chip 只有一个，两端布局比挤在一起更稳。

**④ 文案只讲动作。**
空状态从四段营销改成**一句提问 + 三行可点的示例**（示例就是模式切换后的真实入口）。
标题行的 slogan 整行删掉。
**判据**：面板上每句话要么在描述「现在是什么状态」，要么在告诉用户「点这里会怎样」；
不两者皆是的句子删掉。

**⑤ 插件端 AI provider 配置 = 读写服务端的 `ai_chat_*`，不建第二份配置。**
插件里的「AI 设置」面板读 `GetConfig`、写 `UpdateConfig`、用 `ListAIModels` 拉模型清单、
用 `TestAIChat` 测连通。**关键值只有服务端那一份**——
插件不把端点/密钥存在自己的 `globalState` 或 VS Code settings 里。
理由：存两份就一定会漂（用户在桌面端改了密钥，插件还在用旧的），
而密钥**本来就属于服务端**（桌面端的 `ai_chat_api_key` 也是存在 `config` 表里）。
VS Code 的 `contributes.configuration` **不新增 AI 键**——
那会变成第三份真值。

**两端的落点形态，以及差异的边界。**
两端的能力与措辞一一对应，**只有承载形态不同**：
VS Code 端是**面板内的一个浮层**（webview 里画的一层 sheet，点遮罩或再点一次关闭）；
JetBrains 端是**一个 `DialogWrapper` 对话框**（`ProviderDialog.kt`，由面板头部动作条唤起）。
原因是渲染能力不同，不是功能不同：JetBrains 的工具窗里能塞表单的地方只有 Swing 对话框，
把 provider 配置摊进设置页会离「点图标就该配」的路径太远。
**判据**：表单的字段、按钮、状态文案在两端必须逐字相同，
差异只允许存在于「怎么把这张表单画出来」这一层（与 ADR-0036 决策 8 同一把尺子）。
**两端都读服务端同一份配置**——所以在 VS Code 里配好，JetBrains 里打开就是同一个端点。

> **2026-11-26 修订：provider 的承载形态改成设置页的子页，不是对话框。**
> 用户对 JetBrains 面板的验收结论是「UI 太 low」，参照 VS Code 的隐藏式布局重排：头部只留
> 品牌 + 一个通道状态点 + 一排 24px 图标（历史 / 新建 / 设置；「桌面端」这一枚在下面第二次修订里删掉），文字按钮与带边框的
> `online` 胶囊全部删除；面板内的三行式工具行改成输入框内嵌发送键 + 一行胶囊（模式组 / 项目 chip）。
> 与决策⑤ 有关的两条随之改变：
> ① **provider 表单落在设置树里**（`Settings ▸ Tools ▸ Cinder ▸ AI Provider`），不再由头部动作
> 直接弹 `DialogWrapper`；头部齿轮打开设置树，`AI Provider` 是它的子页。原文「摊进设置页会离
> 『点图标就该配』太远」这条判据被用户否决——配置只有一个家，比少一次点击重要；头部齿轮仍在，
> 路径长度没变。② **设置页本身也分了组**：`CHANNEL`（`cinder-mcp`）/ `LOCAL SERVICE`（可选）/
> `AI PROVIDER`（子页入口），原来的平铺表单读不出「哪个按钮属于哪个字段」。
> 「Start local service」「Refresh」这两个设置类动作从头部移出：前者在设置页，后者由状态点承担
> （点一下重新探测通道）。决策⑤ 的实质不变——仍然只读写服务端 `ai_chat_*`，插件不留第二份，
> 密钥不进 IDE 设置。落地清单里的 `ProviderDialog.kt` 相应改为 `RepotoryProviderConfigurable.kt`。
>
> **2026-11-26 同日二次修订：删掉「打开桌面端」入口，并把面板的视觉语言补齐。**
> 用户的判断是插件不该有一个「点一下跳去 Cinder 桌面」的按钮——面板在这个窗口里就是产品本身，
> 一个只能把读者送走的按钮等于说「这里只是预览」。于是两端头部都只剩三枚图标
> （历史 / 新建 / 设置）：JetBrains 删 `PanelIcons.desktop()` 与 `openDesktopApp()`，
> VS Code 删 `#act-desktop` 与 `openDesktop` 的 webview 消息（命令面板里的
> `repotory.openDesktop` 当时保留，理由是「它不属于面板」——同日第四次修订把这个理由也推翻）。
> 卡片上的 **Open in Cinder** 不变——它打开的是
> 那一条具体的笔记/页面，不是「整个桌面端」。
> 同一次改动补齐了三处「一眼就是原生控件」的地方，两端同一套做法：
> ① 模式组从一个边框胶囊变成**分段控件**（`SegmentTrack` / `.modes`）：三个模式是一选一，
> 三个各自带框的按钮读起来像三个互不相干的开关；② 发送键用**线描箭头**
> （`PanelIcons.send()` / `ICON_SEND`）而不是 `↵` 字符——`↵` 是 Enter 的图例，不是发送键；
> ③ 项目 chip 的锁**画出来**（`PanelIcons.lock()` / `ICON_LOCK`），不再用 🔒 emoji——
> emoji 在部分平台是别的厂商的彩色字形，是面板上唯一不跟随主题的像素。
> 另外：问题的标题行加了一层极淡的底衬（取主题前后景 6% 混色，VS Code 用
> `textCodeBlock-background`），模式标签按面配色（Ask 蓝 / Notes 琥珀 / Pages 紫，
> 与卡片 badge 同源）；没有这一层，问题与它自己的回答会连成一列同色文字。
>
> **2026-11-26 同日三次修订：卡片不再往外跳，改成「就地问」。**
> 上一轮保留了卡片上的 **Open in Cinder**，理由是「它打开的是那一条具体的笔记/页面」。
> 用户的追问撤掉了这个理由：卡片的正文本来就已经就地打开
> （`repotory_notes_read` / `repotory_wiki_read`），那个按钮唯一提供的动作是**离开面板**——
> 读者想多知道一点时，得到的是一次跳转，而不是一个回答。于是换成 **Ask about this**：
> 点击即问：一条**关于这张卡片的问句**（`followUpQuestion` / `followUp`，两端逐字相同）先进输入框，
> 再走与 Enter 相同的提交路径，所以这一轮本身就写着问了什么。第一版只草拟不发送（怕误点花掉一次
> 模型调用），同日按用户判断改成直接发送——按钮写着 "Ask about this"，却要读者再按一次 Enter，
> 是标签没兑现；而共享会话正在跑时提交会被拒、草稿留在输入框里，点击并不会静默丢失。
> 随之删除的是整条深链链路：`RepotoryClient.deepLink` / `servesWebUi` / `openCard` /
> `offerFallback`（JetBrains）、`openHit` / `servesWebUi` / `open` 消息 /
> `Transport.openExternal`（VS Code）、`RepotoryHit.route` 与 `StartOutcome.webUi`，
> 以及设置页里「API and web UI both reachable」这条只为深链存在的判据
> （ADR-0035 决策 6 的深链入口由此作废，见该篇修订）。
> 门 C 的第二程从「深链指向检索真正返回的那一行」改成「卡片就地读出整篇正文」——
> 面板现在真正依赖的是这条路径。启动器里那条「二进制旁有 `web/` 就自动加 `--web-dist`」
> （`RepotoryServer.kt` / `serverLauncher.ts`）也一并删掉：它是给同一个入口开的窗，
> 脚本去掉了 flag 而启动器继续自动加，等于留了半条路；两端的启动命令现在只有 `--port`。
> 顺带删掉一处同源的死代码：`NoteDetailHtml.kt` 里那套静态
> 详情面板（`promoHtml` / `noteHtml` / `wikiPageHtml`，连同它那句 "Open in Cinder is the way
> out of the pane"）自 ADR-0036 起就没有调用方，文件只剩共享的转义与 Markdown 渲染，
> 改名 `HtmlText.kt`；同样没人接线的 `copyCard`、`snippetHtml` 一并删除。
>
> 同轮按同一标准回扫，又收掉三处「说了没做」。**① 空态那句示例问句点了没反应**：
> `→ Why was the retry budget raised?` 原先只把焦点移进空输入框（模式本来就是 Ask），
> 读者得自己把整句重打一遍。规则收敛成一条：**标签是一句问句，点击就问**——两端抽出
> `askNow(text)`，卡片动作与示例共用；JB 的问句抽成 `ChatConsoleHtml.EXAMPLE_ASK`，
> label 与真正发出去的那句同源（用例钉住）。**② 状态点的绿点说了它没测的事**：
> tooltip 原写「MCP channel ready — asking, searching, reading and writing work now」，
> 但探针只做 `McpBinaryResolver.resolveHere`（查文件在不在，不 spawn、不调工具），
> 二进制损坏或版本不匹配时照样是绿的；现在只说「repotory-mcp 找到了」，
> 而从未被赋值过的 `LinkState.STARTING` 直接删除（README 的 green/amber/red 随之改口）；tooltip 那句「Click to locate it」也终于兑现——点击直接开文件选择框，而不是落进那个每面板只弹一次的 balloon（第二次之后点了等于没点）。
> **③ 两处文案指向不存在的入口**：设置页 `StartOutcome.BinaryMissing` 说
> 「or download a release bundle」，可那一页既没有下载也没有链接——同源的
> `offerServerBinaryHelp()`（含 `Open Download Page` 按钮）自一键启动改造后就没有调用方，
> 连同 `RELEASES_URL` 一起删；`RepotoryToolWindow.kt` 里那句悬空的
> 「Where the desktop app is downloaded from」也一样。
>
> **2026-11-26 同日四次修订：命令面板里的 `Cinder: Open Desktop App` 也删。**
> 二次修订留它的理由是「它不属于面板」，可这个理由只说明它不在被清理的那块地上，
> 没说清它为什么该存在。插件在这台机器上能做的事已经有入口——`Cinder: Start Local Service`
> 起服务、`Cinder: Setup & Diagnostics` 装探修、`Cinder: Register MCP Server` 写配置——
> 剩下的「打开另一个窗口里的同一个产品」只是一次窗口位移，而且它失败时给出的唯一出路
> 是让读者去 GitHub 下载安装包，正是本轮删掉的那类把读者送走的文案。于是删掉
> `repotory.openDesktop`：VS Code 的 `openDesktop()`、`exec` import、`package.json`
> 里的命令条目，以及只为 `view/title` 配的那两份 `media/icon-desktop-*.svg`。
> 两端的动作面至此回到同一套——装、探、起、写 MCP、设路径，没有一条是「去那边看」。

**⑥ 项目识别 = 候选项目 + 一个绑定动作。**
工作区没命中项目时，面板不再把 chip 禁用，而是显示「选择项目」，
点开列出 `ResolveWorkspacePath` 返回的 `Candidates`（外加一个「全库」项）。
用户选中某个项目后，插件调新绑定 `BindWorkspaceToProject(path, projectID)`：
它把工作区目录按 path upsert 进 `repositories` 表并指到该项目
（复用的就是扫描时那条 `db.UpsertRepositoryTx`），
于是**下一次解析会命中 repository-exact**，面板自动锁到该项目。
**绑定是写操作，所以它是 desktop-only 绑定**（与 `MoveNote` 同性质：
把知识归到哪个项目下，是人的决定），不进 MCP 工具面。

**⑦ 不新增 MCP 工具、不改 schema、不改 `repotory_ask` 的输出契约。**
`TestToolsRegistered` 钉死的工具数不变；插件新增的四个 provider 绑定与一个绑定绑定
都是 HTTP 桥可达的桌面专属能力。
两个 IDE 面板同构（ADR-0036 决策 8 继续有效）：**同一套结构、同一套措辞**，
差异只允许存在于「怎么把 Swing 画成 HTML」这一层——
JetBrains 的 `JEditorPane` 只支持 HTML 3.2，没有 flexbox、没有 CSS 变量，
所以它的头部图标行用 Swing 组件画，工具行用 HTML 表格画，**措辞与功能一一对应**。

## 后果

- **正面**：
  - 红框那个被裁掉的按钮**从根上消失**——不是调 CSS 调宽，是那个位置不再有宿主画的按钮。
  - 插件与桌面机器人的能力差从「能不能配端点」缩到「配在哪」。
  - 工作区认不出项目时，用户第一次有了**可执行的下一步**（选一个项目），
    而不是一条只读的公告。
  - 面板的文案不再讲产品定位——定位该在商店页与手册里讲，面板该讲动作。
- **代价（接受）**：
  - **面板自己的动作条会占宽度**。四个图标 + 品牌标记在窄侧栏（< 260px）里会挤，
    所以品牌标记在窄宽度下隐藏，图标按钮保持 22px 固定尺寸不缩。
  - **AI 设置面板是插件内新增的一块 UI**，两份插件（VS Code / JetBrains）都要画一遍，
    且它们的渲染能力不同——这是 ADR-0036 决策 8 已知的代价，本篇照单全收。
  - **`BindWorkspaceToProject` 是一个写绑定**。它写 `repositories` 表，
    不写知识内容，但确实改了库；因此它是 desktop-only（无 MCP 双胞胎），
    理由写进 `surface.go`（该表由 `surface_test.go` 强制完整）。
  - **插件端的 provider 配置要求本地服务在跑**（`/api/rpc` 是 HTTP 桥）。
    MCP 通道能跑不代表 HTTP 桥在跑——面板设置面板要**如实说这一点**，
    而不是给一个必然失败的表单。
- **不能假装解决的**：**插件的对话能力仍然受服务端配置约束**。
  插件端能配端点，但**没有本地向量库、没有知识编译、没有审核队列**——
  桌面机器人在「知识是怎么长出来的」这一层领先插件，本篇只把「怎么问」这一层拉平。

## 落地清单

| 面 | 文件 |
|---|---|
| 面板 HTML / CSS / JS | `ide/vscode/src/chatConsoleHtml.ts`（头部图标行、工具行、AI 设置面板、项目候选） |
| 消息契约 | `ide/vscode/src/chatTypes.ts` |
| 会话模型 | `ide/vscode/src/chatSession.ts`（provider 读写、候选项目、绑定） |
| 宿主装配 | `ide/vscode/src/extension.ts`、`ide/vscode/src/chatConsole.ts` |
| 清单 | `ide/vscode/package.json`（删 `view/title`；命令与图标保留） |
| JetBrains 同构 | `ide/jetbrains/.../ChatConsoleHtml.kt`、`ChatModel.kt`、`ChatSession.kt`、`RepotoryToolWindow.kt`（头部动作条、作用域 chip） |
| JetBrains provider 表单 | `ide/jetbrains/.../RepotoryProviderConfigurable.kt`（设置树子页，2026-11-26 起；原 `ProviderDialog.kt` 已删除）、`PanelChrome.kt`（头部图标与胶囊自绘）、`DiagnosticsDialog.kt` |
| service | `internal/service/workspace.go`（`BindRepositoryToProject`） |
| 绑定 | `internal/app/bindings.go`（`BindWorkspaceToProject`）、`internal/app/surface.go`（分类与理由） |
