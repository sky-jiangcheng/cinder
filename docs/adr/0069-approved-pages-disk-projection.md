# ADR-0069：已批准页磁盘投影——读面镜像，不是第二个导出

- **状态**：Accepted（2026-11-23）
- **日期**：2026-11-23
- **关联**：[ADR-0065](0065-hindsight-absorb-not-replace.md)（对标 Hindsight 的吸收路线，P1 第二项）、[ADR-0014](0014-llm-wiki-knowledge-compiler.md)（决策 2：SQLite 是唯一 SSOT，文件树是只读派生——本篇继承并细化）、[ADR-0015](0015-w3-minimal-compile-loop.md)（人工批准闸门：approved 才进检索，本篇把同一闸门延伸到磁盘）、[ADR-0012](0012-semantic-search.md)（默认关的隐私授权口径——本篇说明为何此处默认开不违背它）

## 背景

ADR-0065 的差距清单（「无已批准页磁盘投影」项）：Hindsight 把记忆页 `fs mount` 成文件系统，任何 agent 用自己的文件工具就能读，不需要 MCP 往返。Cinder 的 wiki 层锁在 SQLite 里：Warden 这类 MCP 客户端能查，但 Claude Code / Cursor 在会话里「顺手翻一眼知识页」需要一次工具调用；已有的 `repotory-wiki-export` 是**人工触发的审核面**——全状态落盘、目标目录用户选、非空要 `--force`、陈旧文件只报告不删除。它回答「这库知识长什么形状」，不回答「agent 现在能读什么」。

两个问题需要一个不同的产物：**Cinder 自己维护的、只含已批准页的、随时最新的磁盘镜像**。

## 决策

1. **投影 ≠ 导出，两个面并存。** 导出（`ExportWikiTree`）保持原样：审核面，全状态带 `status` frontmatter，用户选目标，`--force` 护栏，永不删除。投影（`ProjectApprovedPages`）是读面：只含 approved 页（无 `status` 字段——这里的一切按构造已批准，留字段反而暗示别的状态可能出现），目标在 Cinder 自己的数据目录，陈旧文件**会**清理。把导出加几个 flag 改造成投影被否：审核面与读面的删除语义、目标所有权、触发方式全部相反，塞进一个函数会让两边的契约都变模糊。
2. **默认开，`pages_projection=0` 是 kill switch。** 这与 ADR-0012 / 0030 的默认关口径不冲突：那些闸门管的是**数据离开本机**或**未经审阅的内容生效**；投影是已批准内容在本机数据目录里的派生副本——不出机器、不发布未审内容，人工批准（ADR-0015）就是授权，与检索、会话简报同一条授权链。默认关反而让「极致体验」变成「先读文档再开开关」。
3. **镜像目录跟着数据库文件走。** `<数据库所在目录>/pages`，路径来自 `PRAGMA database_list`（复用 `liveDatabaseFile`），不是平台默认目录。这保证测试库投影进自己的 tempdir、评测副本永不碰真实用户的镜像；内存库没有文件，也就没有镜像（静默跳过，显式刷新报错）。布局：`pages/global/<kind>/<slug>.md`（跨项目页）或 `pages/p<id>-<名称slug>/<kind>/<slug>.md`，每层一个 `index.md`，根下 `PROJECTION-MANIFEST.json` 最后写（半途中断的刷新留下与树不符的清单，即「重跑而非信任」的信号，与导出清单同一崩溃一致性哲学）。
4. **清理只认上一份清单。** 每次全量重建（页数为人类规模、触发是人工动作，全量天然幂等；增量 diff 要处理改名/合并/跨层移动，复杂度不值）。重建后，**只有上一份 manifest 列出且本次未重写**的路径可删；路径还要过 `safeManagedPath`（相对、无 `..`、无反斜杠、clean 后不变）——被篡改的清单不能把清理器变成任意删除器。用户丢进树里的文件永不触碰；目录只用 `os.Remove`（非空即失败留下），不用 `RemoveAll`。
5. **五个变更钩子 + CLI 兜底。** 能改变 approved 集合的人工动作各挂一次刷新：批准（进集合）、合并（败者成墓碑出集合、胜者边变化）、提升到全局 / 指派回项目（跨层移动）、存答案为页的更新分支（可能覆写已批准查询页）。拒绝/删除对 approved 页本就拒绝，无需钩子。刷新失败非致命——数据库写入已成功，镜像失败不能让批准看起来失败，下一次变更或 CLI 追平。**边界**：项目删除的级联清页没有页级钩子，备份恢复同理——`cinder-capture project-pages [-db] [-out]` 是兜底，只读开库、不调模型、忽略 kill switch（运行命令本身就是显式意图，开关只管自动化）。
6. **内容边界与工具面。** 出链/反向链接过滤到 approved 目标——pending 页的反链会把它的标题漏进镜像；来源笔记段落不落盘（镜像是页层，笔记走工具）。`repotory_context` 头部在镜像存在且开关开启时加一行目录指引，agent 由此知道可以用自己的文件工具读。不新增 MCP 工具，CoreVersion 不动。

## 被否的替代方案

- **每仓库 `.cinder/pages/` 目录**：知识页跟着代码仓库走看似顺手，实际是 gitignore 噪音 + 把文件系统范围授予自动化——ADR-0063 已钉死「授予文件系统范围是另一个决定，保持 human-gate」（`UpdateScanRoots` 先例）。数据目录内的派生物不需要新的授权面。
- **扩展 `ExportWikiTree` 加 `--approved-only --prune` flag**：见决策 1。两个面的所有权与删除语义相反，合并实现等于两边契约都打折。
- **增量刷新（只写变更页）**：合并/改名/跨层移动会让增量逻辑追着边和路径跑；人类规模的页数下全量重建毫秒级，幂等性白送。

## 代价（接受）

- 每次人工页面动作多一次全量重建 + 磁盘写；页数上千时会开始可感（届时再谈增量，翻转触发见下）。
- 镜像与数据库之间存在毫秒级窗口（写库成功、刷新未完成）；清单最后写 + 非致命刷新把窗口后果限制为「旧镜像」而非「假镜像」。
- 默认开意味着升级后数据目录会多出一个 `pages/` 目录——这是刻意的可见性，不是副作用。

## 翻转触发

- 页数增长使全量重建可感（北极星：人工操作延迟被抱怨）→ 增量刷新，以 manifest 为 diff 基线。
- 出现「用户拿 Obsidian 改镜像并期望改回来」的真实信号 → 回到 ADR-0014 决策 2 的另开 ADR 条款，双向同步单独决策，本篇不预支。

## 落地记录（2026-11-23）

- 代码：`internal/service/wikipage/projection.go`（核心 + 清单清理 + 安全路径校验）、`internal/service/wiki_projection.go`（开关 / 目录推导 / 非致命刷新 / 显式刷新）、五个钩子（`wiki_compile.go` / `wiki_merge.go` / `wiki_read.go`×2 / `wiki_evidence.go`）、`config.go`（`pages_projection` 键）、`context.go`（头部指引行）、`cmd/repotory-capture/projection.go`（`project-pages` 子命令）。
- 测试：wikipage 7 个（approved-only 与分层、边过滤、清单范围清理 + 用户文件存活、敌意清单不能越界删除、确定性哈希、`safeManagedPath` / 路径守卫表测试）+ service 6 个（批准建镜像、kill switch、合并清败者、提升/指派跨层移动、内存库无镜像、context 头部指引）+ CLI 3 个（显式 `-out`、默认目录、缺库退出码）；合并守卫测试白名单登记新测试文件。全量 `go build` / `go vet` / `go test` 绿。
- 前端：本轮无 UI——kill switch 是配置键，文档说明；设置页开关留待有真实用户请求时再加。