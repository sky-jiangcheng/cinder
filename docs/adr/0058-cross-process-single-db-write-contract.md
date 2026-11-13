# ADR-0058: 跨进程单库写契约——三个入口共享一个 SQLite 时的写权归属

状态：**Accepted（2026-11-12 工程评估提出，同日用户选定 A 单写者代理；同日落地时按用户改判把「桌面缺席即拒绝」换成选举式单写者——无写者时 agent 进程就地接管，见文末「落地记录」；已落码，跟踪 TODO 13.1）**
日期：2026-11-12
关联：[ADR-0002](0002-c-end-repositioning.md)（本地优先单库）、[ADR-0025](0025-backup-recovery.md)（VACUUM INTO 快照）、[ADR-0055](0055-agent-surface-contract-and-observability.md)（问题四：数据层容错）、[ADR-0057](0057-write-idempotency-and-version-history.md)（行级乐观锁）
起始 schema：v31 → **v31（无 schema 变更）**

## 背景

三个入口可以同时打开同一个 `repotory.db`：

| 入口 | 进程形态 | 典型写 |
|---|---|---|
| 桌面 `cinder` | Wails，单实例 | 笔记 CRUD、wiki 审批、设置 |
| `cinder-mcp` | stdio，每个 IDE 一份 | handoff / notes_create / chat / review |
| `cinder-server` | HTTP / headless | 导入同步、编译任务、备份 ticker |

现有保护都是「单进程内」或「事后」的：

- `db.SetMaxOpenConns(1)`（`internal/db/db.go:24`）只序列化本进程的连接；跨进程唯一手段是 `PRAGMA busy_timeout=5000`（`internal/db/db.go:35`），超时即拿到 `SQLITE_BUSY`。
- 备份在活连接上执行 `VACUUM INTO`（`internal/db/backup.go:86`），整库快照期间持锁；v31 又加了 30 分钟粒度的滚动快照（`CreateRollingBackup`）。
- MCP 进程刻意不跑 `Startup()`，注释写明是为了不与桌面端 ticker 抢库（`cmd/mcp/main.go:68-74`）——这只减少竞争，没有定义谁优先。
- ADR-0057 D2 的乐观锁是**行级**的，只覆盖 `UpdateNoteFull` 一族；ADR-0057 自己记着「Agent 侧仍是 last-write-wins（工具面不加 `expected_version`）」。
- 12.9 让 MCP 开库复用 `OpenWithRetry`（`internal/db/open_retry.go`），解决的是「打开」，不是「持锁写入」。

结论：**数据库的写权归属没有契约**。当前语义是「谁先拿到锁谁写，拿不到就 5 秒后失败」；长写（备份、批量编译、向量回填）会把这个失败暴露给 agent 工具调用。

## 决策（2026-11-12 用户选定）

**选 A 单写者代理**：写权归桌面端，`cinder-mcp` 与 `cinder-server` 的写经本地通道转发给桌面端执行。

选它的理由是**不再造第二种并发原语**：行级冲突（两个人改同一条笔记）已经由 ADR-0057 D2 的 `version` 乐观锁表达成可判定的 `conflict`，剩下的库级冲突若用租约（B）再讲一遍，同一个库里就有了两套「谁可以先写」的语义，而它们并不互相知道。而「写者唯一」本来就是桌面端的现状——它是单实例应用，另两个进程是它的客户端。

这条决策同时把下面四个待定问题回答掉了：

| 原待定问题 | 本 ADR 的答案 |
|---|---|
| 1. 是否接受「MCP 只在桌面端运行时才能写」 | **接受**，这是选 A 的直接后果，也是它唯一的硬代价 |
| 2. 长写允许阻塞别的进程多久 | **不阻塞**：写请求在唯一写者里排队，客户端看到的是「等待」而不是 `database is locked`；有界等待还是立即拒绝见下 |
| 3. 备份是否必须离开活库 | **不必**：备份由唯一写者拍，其它进程不再自己 `VACUUM INTO`——「活库上拍快照」的风险来自并发的另一个写者，写者唯一之后它消失 |
| 4. 是否引入写租约 | **不引入**，租约的崩溃残留、时钟偏斜与公平性问题没有对应收益（见第一条理由） |

## 落地前仍需确认（实施时定，不阻塞本决策）

1. **桌面不在时的写行为**。A 的语义是「没有写者就没有写」。实施时必须在两条里选一条并写死：**明确拒绝并返回可行动错误**（推荐——两条路径同时存在就等于没有契约），或者让 MCP 在桌面缺席时就地降级成临时写者（那就等于在 A 之上叠了 C，必须把切换条件写进契约）。**默认按前者实施**。
2. **代理的传输形态**。回环 HTTP（复用 `internal/httpapi` 与既有的 `loopbackGuard`）还是 Unix domain socket。判断依据是「谁能伪造这个通道」：回环端口是任何本机进程都能连的，socket 文件权限才是真的边界。

## 待定问题（已被上面的决策回答，原文保留）

1. 是否接受「MCP 只在桌面端运行时才能写」？这直接决定选项 A 是否可行。
2. 长写允许阻塞其它进程多久：5s / 30s / 不阻塞？
3. 备份是否必须离开活库（在副本或只读快照上做）？
4. 是否值得引入写租约，以及能否接受租约的崩溃残留、时钟偏斜与公平性问题？

## 选项与取舍

| 选项 | 做法 | 优点 | 代价 / 风险 |
|---|---|---|---|
| A 单写者代理 | 桌面端独占写；MCP / server 的写经本地 HTTP 或 Unix socket 转发 | 写权唯一，跨进程锁问题消失；与行级乐观锁天然一致 | 桌面不在时 agent 不能写，与「IDE 一键起本地服务」的现状冲突；多一跳延迟；桌面崩溃即断写 |
| B 跨进程写租约 | 租约表或文件锁 + 短 TTL，写前抢租约 | 保留三进程独立 | 崩溃残留、时钟偏斜、公平性都要处理；非租约写者仍绕不过 `busy_timeout`；复杂度最高 |
| C 退避 + 长写拆小 | 提高 `busy_timeout`、写失败指数退避；备份改到副本、编译分批提交 | 改动最小，可立即做 | 只把失败推迟，不设上界；无公平性；延迟尖刺仍在 |
| D 读写分离 | MCP 用只读连接 + WAL 快照读；写仍走桌面 / server | 读不被写阻塞 | 写工具（handoff / notes_create）仍需写路径，最终回到 A；只读连接与 `busy_timeout` 的交互要验证 |

## 明确不决

- ~~不在本篇选定 A/B/C/D~~ —— **已选定 A（2026-11-12）**；`busy_timeout`、备份路径与 MCP 开库策略在实施时按 A 重排，本篇不动代码。
- 不引入外部数据库或分布式锁服务（本地优先、零账号是产品前提）。
- 不做写租约（B）、不在本轮叠加读写分离（D）——D 可以在此之后作为「读不被写阻塞」的独立优化，它不改变写权归属。

## 验收判据

- 双进程（桌面 + MCP）并发写，同时触发一次备份与一轮编译，30 分钟 soak：`database is locked` 不以工具错误形式冒泡。
- 任一进程被 `SIGKILL` 后，另一进程在 ≤ 10s 内恢复可写；**代理实现下需额外断言「写者重启后排队中的写请求要么被重放要么被明确拒绝」，不允许静默丢弃**。
- 有能红的测试：模拟长写占锁，断言的是选定契约行为（排队 / 明确拒绝 / 代理转发），而不是随机超时；另配一条「桌面缺席时 MCP 的写返回可行动错误」的断言，因为它现在是契约的一部分而不是意外。

## 落地记录（2026-11-12）

**先记一条对原决策的用户改判，因为它改变了本篇的一处语义**（原决策与待定表保留原文，冲突处以本节为准）：落地当天确认写权归属改为**选举式单写者**——写权不绑定「桌面端」这个进程，而是绑定 `<datadir>/repotory.writer.lock` 这把 OS 文件锁：**谁持锁谁是唯一写者**。桌面启动时仍按原 ADR 优先接管（向自举的 agent 发 yield，成功即接手；agent 正忙则桌面退化为客户端，契约不破）。改判的理由是 README 第一屏的对外承诺——「`cinder-mcp` 无需打开桌面应用即可独立使用」：写工具（handoff / notes_create / chat）是这条产品的存在理由，按原决策的默认拒绝，headless 安装第一天就断写。选举式保留了选 A 的全部结构收益（同一时刻仍只有一个写者、不造第二套并发原语、备份只由写者拍），把「桌面不在」从「断写」改成「就地接管」。原「落地前仍需确认」两问的答案因此变为：**① 缺席行为 = 接管，接管不能时才明确拒绝并返回可行动错误（`writer_unavailable`，retryable）；② 传输形态 = 回环 HTTP + 一次性 token**（token 写在 0600 的端点文件里，与 repotory.db 同一边界：能读端点文件的进程本来就能读库，通道没有新增可攻击面；UDS 在 Windows 上不通用，而 `x/sys/windows` 锁原语已经要用，两端一份语义更便宜）。

**已落码**（`go build ./...` + 全量 `go test` + 两条负例注入验证）：

- **`internal/writer`**——选举与传输核心，不认识 Service：flock（Windows 走 LockFileEx，`golang.org/x/sys` 从 indirect 升为直接依赖）+ `repotory.writer.lock`（文件内容写明 holder pid，「谁挡了我」可答）+ `repotory.writer.endpoint`（addr / token / pid / 进程名 / 契约与 schema 版本）。锁由 open file description 持有，SIGKILL 即释放——恢复不靠清理代码。`TestLockSurvivesProcessDeath` 用真子进程自杀验证了这条。
- **`internal/writerapi`**——**写目录（catalogue）**：十个具名方法（notes.create/update、handoff.create、chat.create/append/rename、scan.run、review.run、capture.claude_session、audit.record），一方法一个 args 结构体，**不走反射**——RPC 桥那种「凡存在即可调」在这里就是契约的反面。`Manager` 按角色路由：写者本地执行、客户端转发；**同一个 Executor，两种模式共用一条代码路径**。转发错误按写者的分类走（`errcode.Remote` 保 code），conflict 不会在跳进程后被降级成 internal。
- **桌面缺席的接管链**：客户端发现写者不可达（连接被拒 = 请求从未执行，`StaleError.PreExecution`）→ 重新选举 → 自己成为写者。**「从未送达」与「死在请求中途」严格分开**：后者绝不自动重放（ADR-0057 的幂等键之外，这是防止非幂等写二次生效的第二道闸），报回歧义由上层决定。
- **`repotory_scan` 的两处写**（seed roots 的 `EnsureDefaultScanRoots` 与扫描本体对 projects / repositories / daily_stats 的落库）归并进 scan.run 一个目录方法——「一次工具调用 = 一个目录方法」是防漏闸的前提。
- **audit 行走同一条契约**：`Service.SetAuditForwarder` 让非写者进程的 `ai_call_log` INSERT 经通道落库，行内 `surface` 保持**发起进程**声明的值（mcp 的调用在桌面的审计面里仍写 `mcp`）。审计失败照旧吞：它不该弄挂模型调用，但也不该在并发下撞锁丢行。
- **接入点**：`main.go`（桌面，AssertWriter，启动即尝试接管）、`cmd/mcp`（六个写工具、8 个目录调用点全部改走 Manager）、`cmd/repotory-capture`（SessionEnd 钩子——它以前不经仲裁：会话一结束就自己开库写交接，而桌面和 MCP 常常正持有连接）。`cmd/server` 本轮未接入，仍按旧语义直写；作为收敛记 TODO。
- **能红的门**（验收判据逐条对上）：`TestNoDirectServiceWritesOutsideTheContract` 用 AST 扫 `cmd/mcp` 全部非测试文件，任何绕过目录的 `svc.<写方法>` 直呼即红（注入验证过）；`TestWriteToolsCoveredByCatalogue` 反向钉「目录里没有工具到达的方法 = 死契约面」；`TestLongWriteQueuesInsteadOfFailingLocked` 是 30 分钟 soak 的**单元化替身**——写者持事务超过 `busy_timeout=5000` 时，客户端的转发写**排队成功**而不是拿到 `database is locked`；`TestUnreachableWriterIsActionableError` 钉住拒绝文案必须给出可行动路径（起桌面 / 等接管），分类必须是 `writer_unavailable` + retryable；`TestConcurrentForwardedWritesAllLandOnce` 证明排队不丢写。SIGKILL ≤10s 恢复由 `TestWriterDeathTriggersTakeover`（进程内替身）+ 子进程死亡测试共同覆盖。

**代价与残留，照实记**：
- 每个进程的本地读仍要过 SQLite 自己的锁——契约管写，不管读；`busy_timeout` 保留为读路径的兜底。
- **13.6 收口（2026-11-13）：`cmd/server` 接入 + 桌面绑定面经 RPC 桥门控**——server 选举语义与 agent 面一致（`SelfElectAllowed=true, AssertWriter=false`）：独自跑时当写者（tickers 照常，headless 现状零损失），桌面在时转客户端（不抢占）。**写分类表** `bindingWrites`（`internal/app/surface.go`，56 条，与 131 条 surface 注册表同邻同 review）+ 完整性门 `TestBindingWritesAreComplete`（双向：漏分类的写绑定 / 指向不存在的方法，负例注入验证）。**RPC 桥三分流**（`DecideWrite`）：写者本地执行；目录覆盖的 8 类绑定（notes / handoff / scan / review）经 `App` 层转发给写者——转发走与写者**同一个 Executor**，`CreateHandoff` 的 `Source="desktop"` 随跳保留（出处记录写者而非执行进程）；**human-gate 写**（approve / merge / config 等 48 条）客户端角色拒绝并返回可行动错误（指向桌面）——ADR-0015 的边界在 headless 面重申。**tickers 是写者私产**：client 角色 server 只注册 importer 不跑 Startup（与 MCP 的既定 non-Startup 同构），决策函数 = `IsWriter()`。验收门：`cmd/server/write_contract_test.go` 三例（写者角色全功能 / 客户端转发落库 + human-gate 403 可行动 / ticker 降级判定），负例（拆掉桥上拒绝→ApproveWikiPage 变 422 直写）验证过。
- **30 分钟 soak：已跑三次，部分完成（2026-11-13）**——`scripts/soak-writer-contract.sh` + `scripts/soak/worker.mjs` + `scripts/soak/seed.mjs`：真二进制（`cinder-server` 当写者跑 tickers，N 个 `cinder-mcp` 并发写）、隔离 HOME（**绝不碰用户真实库**——第一版门就栽在这里）、阶段 A 反复 `RunBackupNow` 造真实长持锁、阶段 B 写入中途 `SIGKILL` 验接管。**写者用 server 而非 GUI**：契约绑文件锁不绑进程（本篇改判），两者走同一条 `writerapi` 路径。

  **核心判据三次全部成立**：每次 1.6–1.8 万次写，**`database is locked` 到达调用方 0 次**。

  **soak 抓到并已修的真缺陷**：

  | | 现象 | 根因 | 修法 |
  |---|---|---|---|
  | 缺陷 | 一次 30 分钟里 **1108 次** `writer_unavailable`（每次恰好 ~3.0s），写者**全程活着**；锁冒泡仍为 0 | 备份 `VACUUM INTO` 持锁时转发写失败 → `reelect(failedAddr)` 发现锁仍被持、**endpoint 地址没变**，于是空转 15×200ms 等一个「不同的地址」，超时报「没有 Cinder writer 可达」——把「忙」说成「不在」 | 地址仍是失败地址时**探一次活**（`writer.Probe`，走新增的无副作用 `writer.ping` 控制方法，不碰数据库、不触发让位）：活着 → 返回 `errBusyWriter`（分类 `retryable`）；探不通 → 才报 `writer_unavailable` |

  **为什么两者必须分开**：`writer_unavailable` 的语义是「没有进程持锁，得叫一个来」，agent 收到会去起第二个写者；「写者活着但忙」只是要重试。混为一谈会让 agent 在每次备份期间制造新写者。`writer.Probe` 也解释了 endpoint 文件为何不能当活证明——它由写者写入、`SIGKILL` 后残留（锁则是进程死亡即释放）。回归门 `TestBusyWriterIsNotReportedAsAbsent`（负例：撤掉修复 → 3.04s 红，与 soak 观测吻合）。

  **脚本自身修了五处**：① 判定接管时探 `/health`，但接管者是 `cinder-mcp`（**没有 HTTP 服务器**），527 次写全成功的运行被判红 → 改判「`SIGKILL` 前后写是否全部落地」（接管对调用方透明，没有延迟可测；<10s 要件仍由 `TestWriterDeathTriggersTakeover` 钉住）；② `set -u` 下 `wait "${PIDS[$i]}"` 空数组崩在判据之前；③ 每轮新建 note 让库涨到 9MB、`VACUUM INTO` 变成分钟级（单次写 861s），测的成了 SQLite 真空吞吐 → 改为固定行原地 UPDATE + 固定线程；④ `printf` 管道 seed 让 stdin 提前 EOF，MCP 关停时取消未返回的工具调用（形似并发 bug）；⑤ 线程种子缺失。

  **未解决，如实记**：三次 soak 都还有 200–1100 次 `writer_unavailable` 误报（每次恰好 ~3.0s），写者全程活着。**已证伪的假设**：长写期间 `writer.Probe` 实测 **1.2ms** 就能应答（写者没被 SQLite 卡住），所以 busy 分支本应命中却只有 1/40 命中——失败发生在**更早的连接层**（`writer.ReadEndpoint` 或 `Post` 的拨号），`Probe` 帮不上。**影响有界**：误报只让 agent 多起一个写者，文件锁保证同一时刻仍只有一个写者，**不是数据损坏**。**下一步**：给 `forwardOnce` 的各失败出口加计数（地址读失败 / 拨号失败 / 写者返回错误各多少次），一次 instrumented soak 即可定位。**这条不阻塞本契约的成立**，但也不能声称「soak 通过」。

- **开库路径不变**：`db.OpenWithRetry` 仍在每个进程里跑（WAL pragma、`Ensure*` 重放、迁移盖章都是文件级写）。它本来就在多进程间靠 busy_timeout 串行，且是「读到迁移后的库」的前提——非写者不能不开库只读。本轮不动它；若 13.6 把 server 接进来时再评估「非写者只读开库 + 写者负责迁移」。
- `repotory_scan` 这类分钟级写现在在写者进程里排队，**转发端的取消会传导**（ctx 过 HTTP），但两个 agent 同时 scan 时后者等前者，这是契约的本意，不是回归。
- 桌面从「唯一写者」变为「优先写者」：agent 自举期间桌面没在跑 backup ticker，快照密度短暂下降；桌面一启动即接管，窗口有界。
- **桌面的 UI 写面本轮没有接入目录**：它 normally 就是写者，所以没有区别；只有「桌面启动时 agent 正忙、接管被拒」这个窗口里，桌面的绑定写与 agent 写者会并发——退化成契约前的旧行为（busy_timeout 兜底），不是新增的洞，但也不能声称已闭环。收敛方向与 `cmd/server` 同批：绑定层在 `!IsWriter()` 时把写转给持锁者。
- **`CoreVersion` 不动（2.0.0）**：工具名、参数、成功线形全部未变；新错误 code 走 2.0.0 已声明的开放 code 枚举。fixture（`scripts/mcp-tools.fixture.json`）无需改。
