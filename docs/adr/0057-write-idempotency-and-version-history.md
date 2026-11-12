# ADR-0057: 写入幂等、乐观锁与版本历史补全——schema v31

状态：**Accepted（2026-11-12 设计定稿，同日落地）**。schema v31 迁移、四条写入路径、滚动快照与 `cinder restore` 均已落码，见文末「落地状态」与其中的三处实现修正。
日期：2026-11-12
关联：[ADR-0055](0055-agent-surface-contract-and-observability.md)（D5 的 schema 部分）、[ADR-0025](0025-backup-recovery.md)（本地快照分层）、[ADR-0015](0015-w3-minimal-compile-loop.md)（模型产出先待审）、[ADR-0054](0054-rolling-checkpoint-handoff.md)（milestone 与幂等键的冲突）
起始 schema：v30 → **v31（已执行）**；`CoreVersion` 2.0.0 → 2.0.0（**MCP 工具面不变**；`UpdateNoteFull` 绑定增一个 `expectedVersion` 参数，见「落地状态」）

## 背景

ADR-0055 的 D5 有一半是「不改 schema 就能修」的，那部分已在 2026-11-12 落地：`RestoreBackup` 显式恢复入口、`FileAnswerAsPage` 落 pending、编译任务部分成功标记、导入失败冒泡、drainer 指数退避。剩下四条都必须动表结构，本篇只定设计。

### 1 · 写入没有幂等键，重放就是两条记录

`internal/service/handoff.go:84` 的 `CreateHandoffNote` 走 `CreateNoteWithMeta`，append-only，没有任何去重键。SessionEnd 钩子、`repotory_handoff` 的重试、以及 ADR-0054 的 milestone 滚动检查点，都可能对同一个会话写两次；`handoff_capture.go` 的自动捕获与人工交接还会各写一条。结果是 `BuildProjectContext` 里并列两条近似交接，读者无法判断哪条是最终态。

### 2 · 写路径是 last-write-wins，跨进程没有保护

`internal/db/notes.go:253` 的 `UpdateNoteFull` 是 `UPDATE … WHERE id = ?`，不带任何前置条件；`internal/db/notes.go:265` 只改 `updated_at`。同一时刻桌面端（Wails）与 IDE 里的 `cinder-mcp` 是两个进程、两个连接，WAL + `busy_timeout` 只保证「不会报 SQLITE_BUSY」，**不保证不互相覆盖**。笔记内容层的冲突检测完全缺席：后写的赢，先写的连痕迹都不留（版本历史只记它自己写下的那一版）。

### 3 · 版本历史缺「创建态」，且快照存的是改后值

`internal/db/migrate.go:169` 的 `note_versions_snap` 是 `AFTER UPDATE`，v11（`migrate.go:204-215`）把它的触发条件收窄到「四个业务列真的变了」，但两件事没变：

- **创建永不入历史**：`CreateNoteWithMeta` 不触发它，所以一条笔记的 v1 只存在于 `project_notes` 本身；一旦被编辑或删除，初始内容无从恢复。
- **快照存的是 `new.*`**：更新到 v2 时插入的是 v2 的内容，于是历史里躺着的是「改后态」序列，与 `project_notes` 的当前值重复，少了最初那一版。想回滚到「编辑前」需要反推，而行级历史本来就是为了不做这种反推。

### 4 · 快照粒度仍是「每天第一份」

`internal/db/backup.go:57-60` 当天快照存在且可读就直接返回（这是刻意的日幂等）。恢复入口有了以后，这个粒度暴露成问题：一天里改坏了，能回到的最近一点是「今天首次打开前」，中间几小时的工作只能靠版本历史逐条捡——而版本历史又缺创建态（第 3 条）。

## 决策

四条都排在 **schema v31 一次迁移**里，不分批（迁移不是免费的，分三次意味着三次全表重写与三次回滚风险）。

### D1 幂等键：`project_notes.idempotency_key`

新增列 `idempotency_key TEXT DEFAULT ''`，加唯一索引 `UNIQUE(project_id, idempotency_key) WHERE idempotency_key <> ''`（部分索引，空串不参与）。写入方按语义生成稳定键：

| 写入方 | 键 |
|---|---|
| `CreateHandoffNote` | `handoff:<project_id>:<session_id>`，无 session_id 时退化为 `handoff:<project_id>:<summary 的 sha256>` |
| 自动捕获（`handoff_capture.go`） | 同上，与人工交接共用命名空间——同一会话的自动与人工记录不应并存两条 |
| 插件导入（`runtime.upsertDoc`） | `<source>:<doc 外部 id>:<content sha256>`，内容变了就是新键，与 12.12 的内容哈希跳过互补 |

冲突时返回既有笔记的 id 与 `Deduped=true`，不报错、不新建。**键只由服务端生成**，MCP 工具不接受客户端传入——让 Agent 自己编 key 等于没有 key。

### D2 乐观锁：`project_notes.version`

新增 `version INTEGER NOT NULL DEFAULT 1`。`UpdateNoteFull` 增加 `expectedVersion` 参数，SQL 变成 `WHERE id = ? AND version = ?` 并 `SET version = version + 1`；`RowsAffected() == 0` 时返回 `ErrVersionConflict`（由 `internal/errcode` 归到新的 `conflict` 码，**不可重试**，必须先重读）。UI 编辑拿「打开编辑器时读到的 version」提交；批量导入 / compile job 这类无交互写入传 `0` 表示「不校验」（显式跳过，不是默认行为）。

### D3 版本历史补全：`AFTER INSERT` + 存 `old.*`

v31 重建两个触发器：

- `note_versions_snap_ins AFTER INSERT`：插入一条初始快照。
- `note_versions_snap_upd AFTER UPDATE`（保留 v11 的四列 `WHEN` 条件）：插入 **`old.*`**，即「改前状态」。

于是 `note_versions` 成为完整的「历史状态」序列（v1 … vN-1），当前值在 `project_notes`，回滚第 N 版就是「把第 N 条历史写回」。`note_versions` 增加 `version INTEGER`，与 D2 的列对齐，便于「回到某一版」时同时回退版本号。

### D4 滚动快照：日内多份，按份数保留

`CreateBackup` 的每日幂等保留为「日快照」，另加 `snapshot-<YYYYMMDD-HHMM>.db` 的滚动快照，触发条件是「距上一份滚动快照 ≥ N 分钟且库有写入」（`note_versions` 的 `MAX(created_at)` 可作廉价的写入标记，不新表）。保留策略按份数（滚动 6 份 + 每日 14 份），文件名仍是幂等键。

### D5 恢复必须显式且可校验

`db.RestoreBackup(src, dst)`（本批已落地，`internal/db/backup.go:306`）只写调用方指定的目标路径，原子 rename，失败清理临时文件，**绝不自动替换正在使用的 `repotory.db`**。v31 同批只补一个 CLI 入口 `cinder restore --from <snapshot> --to <path>`，仍不接 UI、不自动执行——恢复是破坏性动作，不给它「顺手就跑了」的机会。滚动快照选哪一份由用户决定，工具只负责校验与提示落点。

## 代价

- **迁移是一次全表重写**：加两列 + 建部分唯一索引 + 回填 `version`，DB 越大越慢；迁移必须单事务可重入（现状已如此，`migrate.go` 的 style 不能破），并在迁移日志里打出耗时。
- **乐观锁带来新的失败面**：并发编辑从「静默覆盖」变成「明确冲突」，UI 必须给出可行动文案（重读 / 合并 / 覆盖），否则用户第一次遇到会觉得是回归。`conflict` 码不可重试，这一点要写进 MCP 工具 description，否则 Agent 会盲目重试。
- **幂等键改变历史语义**：同一会话重复 handoff 从「两条」变成「一条」，过去靠条数判断「写了多少次」的用法失效；已有重复不会自动清理（迁移不做去重，那是用户数据）。
- **历史存 `old.*` 是语义反转**：任何按「快照 = 当时状态」读取 `note_versions` 的代码与 UI 都要跟着改，且**已丢失的创建态无法回溯补齐**——v31 只保证此后的历史完整。
- **滚动快照提高 IO 与磁盘占用**：多 6 份全库副本，对大库（>1GiB）要重新算保留策略。

## Alternatives

- **用 `updated_at` 做乐观锁**：毫秒精度，同毫秒并发写会漏检，且 `updated_at` 已被 `UpdateNoteFull` 无条件刷新，语义不自洽。拒绝。
- **`BEGIN IMMEDIATE` 串行化所有写**：解决的是锁竞争，不是覆盖；跨进程仍有「读-改-写」窗口，且会把只读路径也拖慢。拒绝。
- **幂等用「先查再插」**：TOCTOU 窗口，两个进程同时查不到就都插。唯一索引是唯一可靠的做法。拒绝。
- **不建 version 列，用 note_versions 最新一条做冲突检测**：历史缺创建态（第 3 条），最新一条不可靠；且比较内容哈希会把「改回来」误判为无冲突。拒绝。
- **把滚动快照交给用户手动备份**：违反 ADR-0025 的「默认本地有副本」前提，2026-10-14 那次损坏就是这么来的。拒绝。

## 落地状态（2026-11-12）

**全部落地。** 一处迁移（`internal/db/migrate.go` 的 `EnsureNoteWriteContract`，v31）、四条写入路径、滚动快照、恢复入口，均有能红的断言。

- **D1 幂等键** —— `project_notes.idempotency_key` + 部分唯一索引 `(project_id, idempotency_key) WHERE <> ''`；`db.CreateNoteIdempotent` 用「插入 → 唯一冲突 → 取既有行」而不是先查再插；键由服务端生成，MCP 工具**不接受**客户端传入。键的三种形态写在 `service.handoffIdempotencyKey`：会话级 `handoff:<project>:<session>`、milestone 级 `handoff:<project>[:<session>]:milestone:<digest>`、无 session 时 `handoff:<project>:sha256:<digest>`；插件导入是 `import:<project>:<source>:<title 摘要>:<content 摘要>`。
- **D2 乐观锁** —— `project_notes.version`；`UpdateNoteFull(..., expectedVersion)` 在同一条 UPDATE 里比对（不在 Go 里先读后写），`RowsAffected()==0` 时再查一次存在性，存在即 `errcode.ErrVersionConflict`（`conflict` 码，不可重试）。`UpdateNote` / `UpdateNoteMeta` / `RestoreNoteVersion` 也自增版本号——它们改的正是被快照的那四列。
- **D3 版本历史** —— v11 的 `note_versions_snap` 被删除，换成 `note_versions_snap_upd`，插入 `old.*`；`note_versions.version` 记下这份快照是哪一版的状态（v31 之前的历史为 0 = 未知）。
- **D4 滚动快照** —— `db.CreateRollingBackup` + `service.rollingBackupOnce`，挂在原来的小时 tick 与启动点上：`minGap = 30min`、`retain = 6`、与每日快照各自轮转。写入标记用 `note_versions.created_at` **加** `project_notes.updated_at`（见修正②）。
- **D5 恢复** —— `cinder restore --from <snapshot> --to <path>`（`cli.go`）。子命令在 Wails 启动前分流，**拒绝**写 live 库路径，目标已存在就失败。

### 实施修正记录（三处与上文决策不同）

**① D3 只保留一个 `AFTER UPDATE` 触发器，不做 `AFTER INSERT`。** 设计里两个都想建：插入时存 v1，更新时存 `old.*`。但存 `old.*` 之后，「第一次编辑」写下的正是创建时的状态（`old.version = 1`），再让 INSERT 触发器也存一份 v1 就是同一份内容占两条历史、吃掉 50 条预算的一半。而「按 version 去重」的折中更糟：脚本插件拿着 `ctx.DB()` 直接 UPDATE 时不会自增 version，去重会把真实快照静默丢掉。现在的语义是「历史 = v1..vN-1，当前值在 `project_notes`」，与设计文末那句话一致，创建态在它第一次成为历史时入表。

**② D4 的写入标记不能只用 `note_versions`。** 它只在**编辑**既有笔记时才有新行，一整天只新建笔记的库看起来「没有任何写入」，滚动快照就永远不触发。加上 `project_notes.updated_at` 才能覆盖「新建 + 编辑」。两者都在 SQL 里与 `datetime(?, 'unixepoch')` 比较——`CURRENT_TIMESTAMP` 写的是 UTC、文件 mtime 是本地时间，在 Go 里比会整整差一个时区，UTC+8 的机器上「有没有新写入」会永远答「没有」。

**③ 恢复不回退版本号。** D3 原话是「`note_versions.version` 与 D2 对齐，便于回到某一版时同时回退版本号」。真回退会让所有持有较新版本的客户端显得「最新」，下一次写入就变成静默覆盖——正好是这个计数器的存在理由。所以恢复是一次**前进**的写入（n → n+1），历史里也因此留下「恢复前」那一版。

### 与设计的其余差异

- **滚动快照文件名**是 `repotory-<YYYYMMDD-HHMM>.db`，不是 `snapshot-<YYYYMMDD-HHMM>.db`：轮转的安全性建立在「只删本产品写过的文件名形状」上，与每日快照同前缀 + 各自的完整正则，比多一个前缀更容易审计。
- **`CoreVersion` 不动，MCP 工具面不动。** `repotory_notes_update` 不新增 `expected_version`：工具面加参数要动 fixture、文档与版本门，而 Agent 手里没有「打开时的版本」这种东西——它刚读完就写。跨进程保护落在**人**这一侧（桌面编辑器提交版本号）。代价照实记：Agent 的写入仍是 last-write-wins。
- **桌面编辑器的冲突处理只做到「明确拒绝 + 可行动文案」**（`errors.noteConflict`）：不自动覆盖，也不做就地合并——编辑器没有 diff 界面，加一个合并视图是另一条 UI 决定。
- **`note_versions` 的历史语义反转**没有迁移补偿：v31 之前的历史行内容是「改后态」，无法回溯成「改前态」，`version` 留 0 以示未知。

跟踪项：TODO `12.14`（已勾选）。

