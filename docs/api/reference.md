---
title: API 参考
order: 22
---

# API 参考

Repotory 的对外接口是 **Wails 绑定面**：Go 方法经 Wails Bind 暴露给前端（`window.go.main.App.<方法名>`），方法名与 JSON 载荷即契约。桌面应用默认不监听 HTTP 端口；需要 HTTP 形态时由 `cmd/server` 提供仅限 loopback 的 JSON API（复用同一 service 层）。

> 绑定层是薄委托（`internal/app`），实现全部在 `internal/service`；CLI 与 MCP 复用同一实现。

## 1.7.0 契约变更

- `GetHeatmapData(projectId int64)`：新增参数，`0` 为全局，`>0` 限定该项目的仓库（此前项目详情页误用全局数据）
- 移除从未被前端调用的死方法：`ExportProjectStats`、`ExportHeatmapCSV`、`GetNoteVersion`、`ScanForRepositories`、`RefreshStats`、`RefreshAllStats`、`RefreshProjectStats`

---

## Headless HTTP 服务（`repotory server`）

面向 DeepSeek Harness dsh-plugin 等外部运行时的 JSON API，与桌面应用、CLI、MCP 共用同一 `internal/service` 实现和同一个 SQLite 数据库。

| 端点 | 方法 | 说明 |
|------|------|------|
| `/health` | GET | 服务与数据库健康状态 |
| `/api/ai_context` | GET/POST | 全知识库 Markdown（llms.txt 风格） |
| `/api/search?q=...&all=1` | GET | FTS5 全文搜索（默认仅笔记，`all=1` 含待办） |
| `/api/project/{id}/detail` | GET | 项目 + 仓库及历史统计 |
| `/api/project/{id}/overview` | GET | 知识挖掘结果（README/技术栈/依赖等） |
| `/api/project/{id}/stats?date=` | GET | 某日项目统计 |

> **信任边界**：该服务**无认证**，返回内容即用户完整本地知识库。安全性完全依赖 `cmd/server` 只绑定 `127.0.0.1`——可达性等价于「本机上的另一个进程」。**禁止**改为 `0.0.0.0` 或经反代暴露到网络；需要远程访问时应先补认证方案并提交 ADR。

### 可选：同源托管前端（`--web-dist`）

默认纯 API，与生产一致。加 `--web-dist <目录>`（或环境变量 `REPOTORY_WEB_DIST`）后，同一进程把构建好的前端一并服务，浏览器即可**单源**访问页面与 `/api/rpc`，无需另起开发服务器或跨域代理：

```bash
cd web && npm run build:server              # base /，输出 web/dist-server
cd .. && go build -o repotory-server ./cmd/server
./repotory-server --port 18766 --web-dist web/dist-server
```

- `/api/*` 与 `/health` 永远路由到 API mux（`cmd/server` 的 `isAPIPath` 与 `httpapi` 注册面由测试钉住同步）；其余路径优先匹配静态文件，未命中回退 `index.html`（客户端路由）
- 缺失的静态资源（带非 `.html` 扩展名的路径）返回真 `404`，不回退 HTML——资源缺失不会被伪装成 MIME 错误
- 相对路径在启动时解析为绝对路径，并在日志打印实际服务目录；路径穿越强制回落根，信任边界不变（仍仅 loopback）
- 前端配套只有 `build:server` 一种产物可配合此开关：桌面构建 `npm run build` 的 `web/dist` 是 base `./`（wails:// origin 专用），混用会让页面在一种运行时下白屏

---

## `/api/rpc`（JSON-RPC 桥）

`/api/rpc` 是一个**全量写面**：它把桌面 App 绑定的同一个 `App` 对象（`internal/app` 的全部导出方法）以 JSON-RPC 形式暴露出去，浏览器/独立前端模式下前端 transport 就走这条路。能力等同桌面 UI——下表列出的只读端点之外，`CreateNote`、`DeleteNote`、`UpdateConfig`、`UpdateScanRoots`、`TriggerScan`、`ReloadPlugins` 等全部方法都可经它调用。做安全评估时必须把它计入暴露面。

- **请求**：`POST /api/rpc`，体 `{"method": "<App 导出方法名>", "args": [按位置排列的 JSON 参数]}`；`args` 可省略（缺省参数为零值，`context.Context` 参数以请求上下文填充）
- **响应**：`{"result": <第一个返回值>}`；方法返回 `(T, error)` 且 error 非 nil 时为 `422` + `{"error": "..."}`
- **状态码**：`400` 参数不合法 / `403` 生命周期方法被拒 / `404` 方法不存在 / `405` 非 POST / `413` 请求体超过 1 MiB / `501` 不支持的签名（变参或非 `(T, error)` 双返回）/ `503` 绑定对象不可用
- **生命周期方法黑名单**：`Startup`、`Shutdown`、`Service` 属 Wails 运行时内部钩子，经 RPC 调用返回 `403`（此前一发 `{"method":"Shutdown"}` 即可无崩溃痕迹地关闭整个服务的数据库句柄）
- **请求体上限**：1 MiB（合法载荷——单条笔记/交接记录——远低于此）

---

## 项目

| 方法 | 签名 | 说明 |
|------|------|------|
| `GetProjects` | `(date string, starredOnly bool) → ProjectResponse[]` | 项目列表（默认昨天；今/昨无数据时按需触发单日刷新） |
| `GetProjectDetail` | `(id) → ProjectDetail` | 项目 + 仓库列表及各自历史统计 |
| `GetProjectStats` | `(id, date) → DailyStat[]` | 某日（默认昨天）项目统计 |
| `GetProjectOverview` | `(id) → ProjectOverview` | 知识挖掘：README/技术栈/语言/依赖/贡献者/活跃度/最近提交（缓存于 repo_meta，未命中异步挖掘） |
| `ResolveWorkspacePath` | `(path) → ProjectResolution` | 只读：把一个绝对目录解析为归属 project，以及当该目录落在被扫描仓库内时对应的 repository（四层路径解析）。供 IDE 侧栏显示命中范围；无 MCP 对应面，agent 改为把 `path` 传给 `repotory_context`。见 [AI 集成](../features/ai-integration.md) |
| `SearchProjects` | `(query) → ProjectResponse[]` | 名称/路径模糊搜索（含昨日统计增强） |
| `ToggleStar` | `(id) → bool` | 切换收藏，返回新状态（原子 UPDATE） |
| `UpdateProjectLevel` | `(id, "up"\|"down", confirmed) → {success, new_level}` | 合并/拆分项目（单事务）。`"up"` 会吸收并**删除**同级项目（连带其仓库/笔记/待办），必须 `confirmed=true`，未确认直接报错且零改动；`"down"` 按仓库拆分，忽略该参 |
| `PreviewProjectMergeUp` | `(id) → MergePreview` | 只读预览：`"up"` 将吸收删除哪些同级项目及随迁的仓库/笔记/待办计数——确认弹窗的数据源，与合并共用同一份兄弟清单（`db.MergePlan`），预览无法少报。无 MCP 对应面（与 `UpdateProjectLevel` 同为 desktop-only） |
| `RefreshProjectHistory` | `(id) → {success}` | 回填该项目近 365 天统计 |
| `ListOrganizeGroups` | `() → OrganizeGroup[]` | 知识库整理：列出仍是落点（`import_placeholder` / `default`）、还挂着未归类笔记的项目，含笔记数与「是否有被扫描仓库指向它」。只读，无 MCP 对应面（整理是人的动作，见 [ADR-0023](../adr/0023-note-ownership-knowledge-organize.md)） |
| `ReassignLandingNotes` | `(sourceId, targetId) → int` | 把某落点项目的全部笔记归口到目标真项目（或「未分类」），返回移动条数。源必须是落点项目，目标必须是扫描项目/默认。无 MCP 对应面 |
| `DeleteLandingProject` | `(id, deleteNotes bool) → error` | 删除一个 `import_placeholder` 落点项目；仍带笔记时须 `deleteNotes=true` 显式确认（连带删除其笔记）。默认项目与扫描项目一律拒绝——删除是确认动作，不自动级联。无 MCP 对应面 |

**OrganizeGroup**：`{ project_id, name, root_path, kind, note_count, repo_bound }`。

**MergePreview**：`{ target_id, target_name, new_root, siblings: MergeSibling[], repos, notes, todos }`，其中 `MergeSibling`：`{ id, name, root_path, repos, notes, todos }`；空 `siblings` 表示没有可吸收的同级项目。

**ProjectResponse**（节选）：

```json
{
  "id": 1, "name": "my-project", "root_path": "/Users/me/code/my-project",
  "is_starred": true, "repo_count": 2, "project_kind": "scanned",
  "total_added": 1200, "total_deleted": 300,
  "my_added": 800, "my_deleted": 200, "my_files": 15,
  "my_added_7d": 4200,
  "is_workday": true, "below_standard": false
}
```

`my_added` 是**单日**个人新增（锚点同 `GetProjects` 的 `date`，默认昨天），供仪表盘日目标与 `below_standard` 判定使用；`my_added_7d` 是同一锚点结束的**近 7 天窗口**个人新增，供「按最近活跃排序」的界面使用——单日在无个人提交日恒为 0，会让排序退化成字母序。

## 扫描

| 方法 | 签名 | 说明 |
|------|------|------|
| `TriggerScan` | `() → {success, task_id}` | 异步全量扫描，立即返回 |
| `GetScanStatus` | `() → ScanStatus` | `{running, backfilling, message, progress, total}` |

## 摘要与状态

| 方法 | 签名 | 说明 |
|------|------|------|
| `GetSummary` | `(date) → Summary` | 全局日摘要（团队/个人新增删除、文件、仓库数、是否工作日） |
| `GetHeatmapData` | `(projectId) → {days: HeatmapDay[]}` | 近一年热力图；`projectId>0` 限定项目 |
| `GetStatusBar` | `() → StatusBarData` | 当前时间 + 最近提交（30s 缓存） |
| `GetTodoCounts` / `GetNoteCounts` | `() → counts[]` | 各项目待办（未完成/总数）与笔记计数 |

## 搜索

| 方法 | 签名 | 说明 |
|------|------|------|
| `SearchNotes` | `(query) → SearchHit[]` | 笔记 FTS5 搜索（bm25 + snippet 高亮） |
| `SearchAll` | `(query) → SearchHit[]` | 笔记 + 待办联合搜索 |

## 笔记

| 方法 | 说明 |
|------|------|
| `ListNotes(projectID)` / `ListAllNotes()` / `ListAllTags()` | 项目笔记 / 全局笔记（含项目名）/ 全部标签 |
| `CreateNote(projectID, content)` | 创建（默认 kind=other, source=manual） |
| `CreateNoteWithMeta(projectID, title, content, tags, kind, source)` | 带元数据创建 |
| `UpdateNote(noteID, content)` / `UpdateNoteMeta(noteID, title, tags, kind, pinned)` | 更新内容 / 元数据 |
| `PinNote(noteID, pinned)` / `MoveNote(noteID, projectID)` / `DeleteNote(noteID)` | 置顶 / 迁移 / 删除 |
| `ListNoteVersions(noteID)` | 版本列表（最近 50） |
| `RestoreNoteVersion(noteID, versionID)` | 恢复到历史版本 |
| `DiffNoteVersions(noteID, versionID)` | 版本 vs 当前行级 diff（`+/-/空格` 前缀） |

创建成功会发出插件事件 `note.created`。

## 主题（项目 → 主题 → 笔记）

主题是项目与笔记之间的一层：一个项目可以持有多个主题，笔记归类到其中之一。主题是**独立实体**（自己的行、自己的 id、自己的生命周期），因此改名是一次 UPDATE，而不是对每条笔记的重写，「这个主题下有多少条笔记」也才答得上来。主题**从属于且仅从属于一个项目**——同名主题出现在两个项目里就是两个主题，因为主题的意义来自项目的上下文。

| 方法 | 说明 |
|------|------|
| `ListTopics(projectID)` | 列出项目的主题及其笔记数。只读，无 MCP 对应面 |
| `CreateTopic(projectID, name, description)` | 在项目下新建主题；名称归一化（去首尾空白、折叠内部空白）后，同项目内重名被拒 |
| `UpdateTopic(topicID, name, description)` | 重命名主题 / 改说明；落到同名主题上被拒 |
| `DeleteTopic(topicID)` | 删除主题。**其下笔记保留**（`topic_id` 置空回落「无主题」）——删一个归档标签不该毁掉知识 |
| `SetNoteTopic(noteID, topicID)` | 把笔记归入某主题；`topicID=0` 清除归类。跨项目的主题被拒（同项目校验在 db 层再查一次） |

**NoteTopic**：`{ id, project_id, name, description, sort_order, note_count, created_at, updated_at }`。

主题的写入是**人的归档动作**（与 `MoveNote` 同性质），因此五个绑定一律 desktop-only、无 MCP 对应面。

## 待办

`ListTodos(projectID)` / `CreateTodo(projectID, title)` / `ToggleTodo(id)` / `DeleteTodo(id)` / `ReorderTodos(ids[])`（单事务重排）。

## 知识页面审核与作用域

编译产出的知识页先落在 pending，由人批准才进检索（ADR-0015）。**作用域**决定这一页「在哪里说话」：项目层（默认）或全局层（`project_id = 0`，进入 `repotory_context` 的 L3 跨项目画像，并对所有项目的取证可见）。两者都是**人的动作**，因此这一族绑定一律 desktop-only、无 MCP 对应面。

| 方法 | 说明 |
|------|------|
| `ListPendingWikiPages(projectID)` | 待审队列（每项带来源笔记与边，供批准前对照）；`projectID=0` 为全部项目 |
| `ApproveWikiPage(pageID)` | 批准一页，使其进入检索。编译器与 lint 都到不了这里 |
| `RejectWikiPage(pageID)` | 把待审页替换为错误页墓碑（不删行，引用它的链接仍可解析）；已批准的页被拒 |
| `ListRejectedWikiPages(projectID)` | 错误页清单 |
| `DeleteCompiledPage(pageID)` | 彻底删除从未发布的页（待审或已拒绝）；已批准的页被拒 |
| `ListApprovedWikiPages(projectID)` | 已批准页清单（作用域面）；`projectID=0` 含全局页 |
| `PromoteWikiPageToGlobal(pageID)` | 把已批准页提升为全局页。**仅已批准页可提升**（未批准页在项目范围的审核队列里不可见，提升会造出没人能批准的孤儿页）；幂等 |
| `AssignWikiPageProject(pageID, projectID)` | 把全局页移回指定项目（提升因此可逆）；目标项目必须存在 |

提升与移回**不改页面的状态与内容**，也不改 `updated_at`（新鲜度语义属于内容修订）。全局页在库里存为 SQL `NULL`——`project_id` 带外键且库以 `PRAGMA foreign_keys=ON` 打开，字面量 0 会被约束拒绝；Go 侧统一投影为 0。详见 ADR-0031。

## 已批准知识页的读取（ADR-0032）

上面一族的写侧动作是人做的；这一族是**读侧**——过闸之后的页对人可读。**只列 approved**：pending 与 rejected 仍然只在审核页出现。

| 方法 | 说明 |
|------|------|
| `BrowseWikiPages(projectID, limit)` | 已批准页的浏览清单（按更新时间倒序）；`projectID=0` 为全部项目 |
| `SearchWikiPages(projectID, query, limit)` | 检索已批准页（bm25 + CJK 松弛回退）；作用域语义与取证路径一致：选项目 = 该项目的页 **+ 全局页**，不选 = 全部 |
| `GetWikiPage(pageID)` | 读一页的正文、slug、kind、作用域、出边 / 入边与来源笔记 id；**非 approved 的页一律拒绝**（读取也被闸门管着） |

三个绑定都有 MCP 孪生（`repotory_wiki_search` / `repotory_wiki_read`），并被两个 IDE 分发面消费（VS Code 对话台、JetBrains 工具窗）——插件只调绑定，不自己写检索。

**人浏览不写 `access_count`**：那一列的含义是「这一页被塞进过某次回答的证据块」，人打开一页是另一种访问，混进去会让同一个信号一义两用。详见 ADR-0032。

## 配置

| 方法 | 说明 |
|------|------|
| `GetConfig()` | `{config: map, scan_roots: []}` |
| `UpdateConfig(key, value)` | 允许键：`daily_code_standard` / `scan_depth` / `git_author` / `auto_import`（数值键校验数字）；更新检查相关键 `update_check_enabled`（0/1）/ `update_proxy`（自由文本代理地址）；备份相关键 `backup_enabled`（0/1）/ `backup_retain`（整数）/ `backup_copy_dir`（自由文本绝对路径，异盘副本目录）/ `backup_remote_enabled`（0/1）/ `backup_remote_endpoint` / `backup_remote_bucket` / `backup_remote_prefix` / `backup_remote_access_key` / `backup_remote_secret_key`（自由文本；`backup_remote_secret_key` 在 `GetConfig` 里以 `********` 脱敏返回） |
| `UpdateScanRoots(roots[])` | 原子替换扫描根列表 |
| `CheckBackupCopyTarget(path)` | 检查候选异盘副本目录（ADR-0025 L3），返回 `{path, exists, writable, same_device, device_known, code, blocking}`。`path` 为空串 = 检查当前已配置的目录。`code` 为机器可读原因（`not_configured` / `relative_path` / `not_a_directory` / `unwritable` / `no_ancestor` / `ancestor_unwritable` / `stat_failed` / `same_device`，空串 = 可用），文案由前端按语言渲染；`blocking` 区分「副本根本写不进去」与「能写但和活库同盘（只是提醒）」。Windows 上 `device_known` 恒为 false——该平台无法可靠判定卷号，宁可说「不知道」也不误报「异盘」 |

## 版本更新

仅桌面端可用（desktop-only）：headless 与 MCP 用户通过各自的部署渠道更新。

| 方法 | 说明 |
|------|------|
| `CheckUpdate()` | 查 GitHub Releases 的 `latest`，返回 `{current, latest, has_update, release_url, published_at, notes, assets[], download_url, download_name}`。`download_url` 指向与当前平台匹配的 app bundle（MCP server / IDE 插件产物已排除），该 release 无本平台产物时为空，前端回落发布页 |
| `OpenBrowserURL(url)` | 用系统浏览器打开 URL（仅允许 https），供更新卡片的下载按钮使用；不下载、不替换二进制 |
| `StartUpdateDownload(url, fileName, sha256, version)` | 把本平台的 release bundle 下载到系统下载目录并校验 SHA256。`url` 必须是 https；`version` 写进文件名（`repotory-2.25.0-darwin-arm64.dmg`）。下载在后台跑，绑定的 `UpdateDownloadState` 轮询进度 |
| `UpdateDownloadState()` | 下载进度快照 `{phase, version, file_name, downloaded, total, speed, remaining_seconds, path, error, checksum_verified, installer_ejected}`。应用重启后仍会恢复上一次完成的下载（文件已不在则不恢复） |
| `CancelUpdateDownload()` | 中断正在进行的下载 |
| `OpenDownloadedUpdate(path)` | 把已下载的 bundle 交给系统安装器（macOS 挂载 dmg，Windows/Linux 在文件管理器中定位它）。不替换正在运行的程序 |
| `RevealDownloadedUpdate(path)` | 在系统文件管理器中选中该文件（macOS `open -R`、Windows `explorer /select,`、Linux `xdg-open`）——回答「下载到哪了」 |
| `EjectInstallerVolume()` | 推出 macOS 上挂载的安装包卷，返回是否真的推出了。只推出装着本应用 bundle 的卷 |
| `GetInstallInfo()` | 返回 `{version, platform, install_path, executable_path, download_dir}`——安装路径与下载目录，由运行中的二进制自报 |

## AI 导出与插件

| 方法 | 说明 |
|------|------|
| `GenerateLLMsTxt()` | 面向 LLM 的知识库总览 Markdown |
| `ExportNoteAsMarkdown(noteID)` | 带 YAML frontmatter 的笔记 Markdown |
| `GetKnowledgeSources()` | 知识源列表（内置 claude + 插件注册） |
| `TriggerKnowledgeImport(name)` / `TriggerAllKnowledgeImports()` | 触发导入（发出 `import.completed` 前端事件） |
| `ImportClaudeMemory()` | Claude 记忆一键导入 |
| `Health()` | `{status, version}` |

## AI 客户端接入（desktop-only）

把 Repotory 接进 Claude Code / Cursor / VS Code / Windsurf 的 MCP 客户端。**没有 MCP 对应面**：这两个绑定往用户的项目目录里写文件，是「装机器」的动作，不是 agent 的动作。

| 方法 | 说明 |
|------|------|
| `ListAgentClients()` | 可接入的客户端名列表（`claude` / `cursor` / `vscode` / `windsurf`）。界面按这个列表渲染选项，不在前端硬编码 |
| `SetupAgentClients(projectID, clients[], withHook)` | 往目标项目目录写 MCP 注册（`.mcp.json` / `.cursor/mcp.json` / `.vscode/mcp.json`；**Windsurf 例外**，它的配置是用户级的 `~/.codeium/windsurf/mcp_config.json`），可选装 Claude Code 的 SessionEnd hook。返回 `{code, blocking, binary, files[], hook?, hook_settings?, warnings[]}` |

`code` 是机器可读原因（空串 = 成功）：`no_client` / `bad_client` / `project_not_found` / `virtual_project` / `auto_grouped_project` / `missing_directory` / `binary_missing`；文案由前端按语言渲染，`blocking` 区分「做不了」与「做得了但有话说」——这是全仓一致的规矩：后端不返回写死的句子。

几处刻意的拒绝：默认项目（`repotory://memory/default`）与扫描期的自动归组容器不是能接客户端的目录；一个客户端都不勾会被拒绝（而不是「都写一遍」）；目标目录不存在会报目录缺失，而不是凭空造一个。

它与终端那条 `repotory-init` 路径是**同一套协议的两份实现**（Go 的 `internal/agentinit` 与 node 的 `scripts/repotory-init/index.mjs`），两份实现写出的文件被同一份 fixture 钉住。见 [AI 集成](../features/ai-integration.md#桌面端一键接入不用开终端)。

## 事件（Wails → 前端）

| 事件 | 载荷 |
|------|------|
| `import.completed` | `{source, created, updated, skipped, error?}` |

---

## OpenAPI

机器可读契约暂以本页表格为准（此前的手工 openapi.json 已随 D10 移除，待 CI 自动生成后回归）。桌面应用不提供公网 HTTP 服务（见 [TODO](https://github.com/sky-jiangcheng/repotory/blob/master/TODO.md)）。
