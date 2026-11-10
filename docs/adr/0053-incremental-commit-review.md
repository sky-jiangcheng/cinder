# ADR-0053: 提交后增量审查——规则筛候选，模型只复核候选

状态：**Accepted（2026-11-08，同日落地；实现偏差见文末「落地记录」；验收 2 的标注结果与五条规则修正见「补注（2026-11-09）」）**
日期：2026-11-08
关联：[ADR-0016](0016-async-batch-jobs.md)（复用「提交即返回、进度可查」的任务形态）、[ADR-0030](0030-ai-call-audit.md)（模型调用必须过审计）、[ADR-0051](0051-conversation-as-interface.md)（对话是界面：审查结论要能被追问，不另开一套 UI 语言）、[ADR-0048](0048-core-contract-version.md)（新增 MCP 工具要涨契约版本）
起始 schema：v29 → **v30**（两张新表）；`CoreVersion` 1.1.0 → **1.2.0**（加两个只读/触发工具）

## 背景

用户提出：Cinder 已有收藏项目与它们的提交记录，能否每天自动看一眼昨日提交，列出线上会炸但编译不报错的风险点。举的例子是跨项目的 Feign 契约——A 集成了 B，A 侧的 Feign 方法少了 `@RequestBody`，编译通过，联调也过，上线才丢参数。

这个诉求和「让模型读 diff 写 review」之间有两条硬约束，先量过再设计：

**① 量级：全量喂模型不成立。** 本机收藏项目（20 个，含 CBiPay 11 仓 / CBiUnion 14 仓 / lushan 13 仓）的历史提交统计是 41–94 commit、350–1035 文件、1.4 万–4.7 万行 diff 一天。按 4 字符/token 算，单日输入逾 10 万 token，且每天一次——贵只是次要问题，**不可复现**才是主要问题：同一个 commit 今天报三条、明天报两条，人就不看了。

**② 模型读 diff 抓不到这个例子。** A 项目那一行 `X foo(Req req);` 在 diff 里人畜无害；错配要到 B 侧的 `@RequestBody` 才成立，而 B 不在这一天的 diff 里。让模型自由发挥，它只能凭「看起来应该有注解」猜。

**③ 规则能捞到东西，但误报率决定生死。** 在 `cbi-operation-common` 的 feign 目录上实测：55 个 Feign 文件、113 个 POST/PUT 方法带参数，其中 **38 个方法的参数上没有 `@RequestBody`**，且全部是 `org.springframework.cloud.openfeign.FeignClient`（真 Spring Cloud，不是自研封装）。这 38 条**不能直接推给人**：有的写了 `consumes=application/json` 可能侥幸工作，有的是从未被调用的死接口，有的 provider 侧本就用 `@RequestParam` 收单字段。**误报三次，这个功能就会被关掉。**

结论：先做确定性判定，模型只做二次确认；且只报**本次变更引入**的问题。

## 决策

### D1 只做增量，且「增量」指变更影响面，不是 diff 行

只报昨日（00:00–24:00）提交引入的问题，历史欠债不进日常报告。但口径必须比「看 diff 行」宽一格：

| 口径 | 判定方式 | 后果 |
|---|---|---|
| A 只看 diff 行 | 变更行里有没有违规写法 | 漏掉「改了一侧、另一侧没跟」——provider 把方法参数从 1 个改成 2 个，consumer 文件根本没动，第二天线上 400 |
| **B 按影响面（采用）** | diff 改到的**文件/符号**为入口，反查全库引用，任一引用方契约不匹配就报 | 两个方向都覆盖，仍然只报本次变更引起的问题 |
| C 全量审计 | 每天扫全部收藏项目 | 误报淹死人，且与 A 重复；仅保留手动触发 |

代价说清楚：**历史存量永远不会出现在日常报告里**，想清欠债要手动跑一次 `--full`。这是有意的取舍，不是遗漏。

### D2 两层判定：规则筛候选，模型只复核候选

```
收藏项目 → 昨日 commit → hunks → 规则层 → 候选(十几条) → 模型复核 → 报告
                                    ↑ 零成本、可复现        ↑ 只在候选>0 时调用
```

规则层是**纯函数**：输入 `(路径, 文件内容, 变更行号集合)`，输出 `[]Finding`。不碰数据库、不碰网络，每条规则一个 fixture 单测。模型层只回答「这条候选成立吗 + 一句话归因 + 修法」，输出结构化 JSON；候选为 0 时**一次模型调用都不发**（用 fake server 断言调用数 = 0）。

### D3 首批规则（Java 优先，因为被审的仓是 Java）

| id | 判定 | 级别 | 已知误报边界 |
|---|---|---|---|
| `feign_missing_body` | `@FeignClient` 接口里 `@Post/@Put/@PatchMapping` 方法有参数、**所有参数都无注解**、且至少一个参数非简单类型 | 单参数 medium / 多参数 high | `consumes=application/json` 时按 Spring Cloud 版本可能侥幸工作；provider 可能用 `@RequestParam` 收单字段 → 交模型复核降级 |
| `feign_get_body` | `@Get/@DeleteMapping` 方法参数带 `@RequestBody` | high | 无——网关与代理会丢 body，这是确定性问题 |
| `route_renamed` | diff 改了 `@RequestMapping` 类上或方法上的 path 字面量，且全库仍有引用旧 path 的地方 | high | 旧 path 可能只出现在注释/文档里 → 只认 `.java` 内的引用 |
| `feign_arity` | diff 改了 Feign 方法参数个数/类型，provider 同 path 方法参数个数不同 | medium | provider 不在本地时标 `unverified`，不猜 |
| `swallow_exception` | `catch` 块为空、只有 `printStackTrace()`、或 `log.error` 后继续返回成功 | medium | 幂等清理类代码有意的忽略 → 模型复核可驳回 |
| `tx_self_call` | 同类内 `this.foo()` 调用标了 `@Transactional` 的方法 | high | 无——Spring 代理不拦自调用 |
| `sql_injection` | 新增行出现 SQL 字符串拼接，或 MyBatis `${}` | high | 动态表名/排序字段是合理用法 → 报 medium |
| `sensitive_log` | log 语句拼接 `idCard｜phone｜mobile｜cardNo｜password｜token` 变量 | medium | 默认关，用户显式打开才报 |
| `shared_sdf` | `static SimpleDateFormat` | medium | 只用 `format()` 不改状态的历史代码也会中 → 只报新增 |
| `remote_in_loop` | `for/forEach/while` 体内出现 Feign/Dubbo 调用 | medium | 循环规模可能很小 → 归因写「可能 N+1」而不是「必然」 |

Go / TS 的规则**不在本 ADR**：先让 Java 这套在真实仓库上把准确率跑出来，语言扩展是 P4。

### D4 两张表，幂等靠唯一键

```sql
CREATE TABLE review_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_date DATE NOT NULL,          -- 审的是哪一天（昨日）
  scope TEXT NOT NULL,             -- 'collected' | 'project:3,7'
  status TEXT NOT NULL,            -- running | done | failed
  started_at DATETIME NOT NULL,
  finished_at DATETIME,
  repos INTEGER, commits INTEGER, files INTEGER,
  candidates INTEGER, findings INTEGER, skipped INTEGER,
  ai_calls INTEGER,                -- 有它就够核「候选为 0 时不花钱」
  error TEXT,
  UNIQUE(run_date, scope)
);

CREATE TABLE review_findings (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id INTEGER NOT NULL REFERENCES review_runs(id) ON DELETE CASCADE,
  repo_path TEXT NOT NULL, commit_sha TEXT, file_path TEXT NOT NULL, line INTEGER,
  rule TEXT NOT NULL, severity TEXT NOT NULL,
  verdict TEXT NOT NULL,           -- confirmed | dismissed | unverified
  evidence TEXT NOT NULL,          -- 命中的新增行 + 上下文
  message TEXT,                    -- 规则说明或模型归因
  suggestion TEXT
);
```

`UNIQUE(run_date, scope)` 让重跑变成「先删旧 findings 再插」，不需要额外锁；`status='failed'` 允许同日重试，`done` 拒绝重复跑（除非 `--force`）。**表是报告的唯一真相源，不写进 notes**——见 D6。

### D5 调度：配置在 `app_config`，触发在 ticker 或 launchd，不引入 cron 解析

| 键 | 默认 | 说明 |
|---|---|---|
| `review.enabled` | `0` | 默认关：它会读源码并发给模型，属于须显式开启的能力 |
| `review.at` | `09:30` | 昨日报告几点跑 |
| `review.scope` | `collected` | `collected` 或 `project:3,7` |
| `review.ai` | `1` | 关掉就是纯规则模式，零外发 |
| `review.notify` | `high` | `high` / `all` / `off` |
| `review.max_diff_lines` | `2000` | 单文件 diff 超此数跳过并计入 `skipped` |

桌面端：一个 10 分钟的 ticker，判断「今天是否已过 `review.at` 且 `review_runs` 里没有今天的 `done`」，是就跑一次。CLI 侧：`repotory review --since yesterday`，由 launchd / 系统计划任务调，**不实现 cron 表达式解析**（过度设计，且跨平台行为不一致）。

### D6 报告是数据，不是笔记

不把报告写进 notes / wiki。理由：note 是人的东西，机器产物混进去以后「我的笔记」这个语义就没了；而且每天一条 note 会把列表冲垮。报告留在 `review_findings`，两个读口：桌面「审查」页与 MCP 的 `review_latest`。对话里追问「昨天 CR 出了什么」，模型调工具拿的是同一份数据——**一个真相源，两种渲染**。

### D7 交付面

| 面 | 内容 |
|---|---|
| CLI | `cinder-review [--since yesterday] [--dry-run] [--no-ai] [--force] [--json] [--severity]` |
| MCP | `review_latest {days?, severity?}`、`review_run {date?, dry_run?}`（新增 2 个 → `CoreVersion` 1.2.0） |
| 桌面 | 设置页「每日审查」分区（开关/时间/范围/是否用 AI/通知级别）；「审查」页按文件分组列出 findings，点开看证据行 |
| 通知 | 只推 `severity=high AND verdict=confirmed`；没有就不打扰。通道是**左侧导航的红点**，不是系统通知 |

首页与仪表盘**不加任何东西**——刚做完的面板精简（ADR-0051 修订）就是这个教训。

### 这个 ADR 明确不做的事

1. **不做 PR 评论机器人**。Cinder 不是 CI，审查结论给人看，不阻断合并。
2. **不做定时全量审计**。`--full` 只手动。
3. **不实现 cron 表达式**。
4. **不改 CI 门禁**。审查跑在本地，与 GitHub Actions 无关。
5. **不追求覆盖所有语言与框架**。只认显式契约（Spring Cloud Feign / Dubbo 接口注解）。

## 代价

- **误报是唯一决定生死的指标。** 38 处存量里到底有几处是真 bug，现在没有答案；因此 D2 才规定「候选必须交模型复核」，且首版通知阈值取最保守的 `high AND confirmed`。
- **增量会漏三类**：历史债、provider 不在本地（记 `unverified`）、超大 diff（跳过并计数，不静默丢）。
- **规则会随框架过期。** Spring 升级、公司自研封装演进都会让某条规则失效；每条规则必须带 `rule_rev` 注释说明它假设的框架行为。
- **多一个每日任务**：git 进程 + 可能的模型调用，机器闲置时跑，成本可忽略；但「每天多一份需要看的报告」本身是注意力成本，所以默认关。

## 未决（落地时记）

1. **模型复核的分组粒度**：按文件（一次调用看该文件的全部候选）还是按候选（一次一条）？前者便宜、后者准。倾向按文件，单文件候选 > 5 条才拆分。
2. **`verdict` 是否允许人工改**：允许的话，user 驳回要能反过来调规则（否则同类误报天天出现）。首版只做「全部驳回」一个动作，不做逐条编辑。
3. **是否要在 commit 后用 hook 实时触发**：会打断节奏且噪音即时化，倾向不做。
4. **provider 侧索引**：`route_renamed` 与 `feign_arity` 目前靠「全库 grep 旧字符串」兜底，Phase 4 再做 `route_decls` 索引表以支持双向比对。
5. **历史债清理入口**：`--full` 跑完只出报告，是否要生成一份「存量清单」待办，留到有人真的要用时再说。

## 落地清单（分批，每批结束跑守门）

**P1 骨架（无 UI）**
- `internal/review/`：`diff.go`（取 hunk）、`rules_java.go`、`rule.go`（Finding 结构 + 级别判定）
- `internal/db/review.go` + migrate v30
- `internal/service/review.go`：collect → rules → 落库（`ai` 关）
- `cmd/` 增加 `review` 子命令
- 门禁：规则 fixture 单测、幂等测试、跳过计数测试

**P2 模型复核**
- `service/review_ai.go`：按文件分组、结构化输出、`ai_calls` 计数；过 ADR-0030 审计
- 门禁：候选 0 时 fake server 调用数 = 0

**P3 交付面**
- `cmd/mcp/tools_review.go` 两个工具 + `CoreVersion` 1.2.0 + 工具清单测试
- 桌面设置分区 + 审查页（i18n 中英同批）
- 通知：只推 high+confirmed

**P4 扩展（按需）**
- Go / TS 规则包
- `route_decls` 索引与双向契约比对

## 验收（全部可测）

1. 在本机真实库跑昨日：`done`，`skipped` 有计数，报告条数人工可读（高危 ≤ 5）。
2. 人工标注 20 条候选，precision ≥ 0.7 才允许打开通知；低于此值先改规则，不加规则。
3. 同一天跑两次：第二次拒绝或覆盖，`review_findings` 不翻倍。
4. `review.ai=0` 与候选为 0 两种情况，`ai_calls` 均为 0。
5. `cargo`/`go test ./...`、`npm test`、`check-i18n-keys`、`check-docs-mirror`、`check-version-drift` 全绿。

## 落地记录（2026-11-08）

按 P1 → P3 一次做完：`CoreVersion` 1.2.0、schema v30、MCP 20 → 22 个工具。
三处与上面的设计稿不一致，记在这里。

**① CLI 是独立二进制 `cinder-review`，不是主二进制的子命令。** 设计稿写的是
`repotory review --since yesterday`，但主二进制从 ADR-0049 起就不派发任何 CLI 子命令
（`cinder` 只启动桌面应用），加子命令会把「双击启动」与「命令行工具」两种语义挤进同一个入口。
独立二进制也让 launchd / 计划任务直接调它，不必 `open -a` 绕一圈。

**②「通知」的通道是红点，不是系统通知。** 本仓没有系统通知基础设施，而每个平台的授权弹窗
都要求用户先做一次决定才能看到价值。红点复用更新检查那套（`SideRail` 的 `updateDot`）：
`high` 数 `high AND confirmed`、`all` 数全部、`off` 不画；**计数为 0 或取数失败都不画**——
导航不能依赖它。阈值判定放在 `service.ReviewNotifyCount` 而不是 UI：谁都可能忘了规则，后端不会。

**③ `--full` 没做。** D7 的 CLI 行列了它，同一份 ADR 的「明确不做的事」第 2 条又写了「不做
定时全量审计」——两处自相矛盾，以「不做」为准：全量审计不是增量审查的一个开关，它是另一个
问题（存量清理），等有人真要用时另开 ADR。

两条硬约束，写进代码注释也写在这里：

- **候选为 0 时模型调用必须是 0 次。** `refineWithCandidates` 只在有候选时才发请求，测试用
  计数 `httptest` handler 钉住了这一点。省钱的实现很容易做成「先问模型这个文件有没有问题」，
  那等于把 §背景①的量级问题又请回来。
- **`unverified` 不等于有问题。** 规则命中但没被模型复核的候选允许出现在报告里，但不许进红点、
  不许在措辞上说成「确认」。

**仍然开着的口子（不在这次范围内）**：验收第 2 条「人工标注 20 条候选、precision ≥ 0.7」没做。
所以首版只按最保守阈值出红点，`review.notify` 的 `all` 在标注完成前不要用。这条是给未来的
自己留的话：规则不是越多越好，误报率不达标时改规则，别加规则。

## 补注（2026-11-09）：验收 2 做完了，门没过——先修了五条规则，再量

标注跑完了。做法是在活库的 `.backup` 副本上跑 40 天的真实提交（**活库一次没碰**），
拿到候选逐条打开真代码判定。结果分三段，第一段是坏消息。

**① 首版 precision = 0.17（20 条里 4 条成立），远不到 0.7。** 但根因不是「规则想多了」，
而是**一条代码 bug 让规则在说谎**：`matchBrace` 扫到 `} catch (Exception e) {` 这一行时，
把 try 的 `}` 和 catch 的 `{` 当成一对净零括号，于是**返回这一行就是它自己的结尾**，
catch 体读成空——`swallow_exception` 因此对**每一个多行 catch** 都报「catch 块为空」。
真实代码几乎全是多行 catch，所以 20 条里 17 条是这一行造成的。原 fixture 恰好把
`try { run(); } catch (Exception e) {` 写成一行（净 +1，正好绕过），**测试因此全绿**。
这条不修，任何 precision 数字都是在量一个坏掉的计数器。

**② 按「误报率不达标时改规则」这条改掉五处（没有新增规则）：**

| 改了什么 | 依据 |
|---|---|
| `matchBrace` 起始行只从第一个 `{` 之后开始计深度 | 上面那条；顺带让 `catchBody` 读到真 body |
| `swallow_exception` 认三种「已处理」形状：上抛 / 外发告警 / 返回失败值 | 标注里 11 条是 log+alarm 的 job 惯例，D3 早就写了这是模型复核该驳回的边界 |
| 规则整体跳过测试源码（`src/test/`、`*Test.java`） | 3 条来自 `try { fail(); } catch (Biz expected) { assertTrue(); }`——那是**断言抛异常**，不是吞异常 |
| `sql_injection` 要求 SQL 关键字在**字面量开头**，且拼接的两侧**必须一侧不是字面量** | 2 条候选是 `"select ... " + "where not exists(...)"`——两个字符串常量拼接，javac 直接折叠，**不可注入**；另 2 条是英文通知文案里的 "update" |
| `route_renamed` 用 `canonicalPath` 比路由、按声明而非文本判引用 | 一次「`"checkModify"` → `"/checkModify"`」的 30 行风格改动**产出 30 条 high**。Spring 的 `PatternParser` 允许前导斜杠可省，**这条路根本没变**；而 `GrepJava(old)` 里 `checkModify` 是 `"/checkModify"` 的子串，调用方**已经改完**也算「仍在引用」 |
| `remote_in_loop` 要求调用**碰到循环变量**，且读整条语句而不是单行 | 分页批处理（`while(true)` + 整表一次传）不是 N+1；而 builder 换行使循环变量落在续行上，只读调用行会**漏掉真的 N+1** |

顺带补上 D3 早就要求、实现里却没有的 **`rule_rev` 注释**，以及一处**契约违规**：
`sql_injection` 与 `route_renamed` 原本输出 `confirmed`，而 `rule.go` 明写
`confirmed` 只留给「没有已知误报边界」的规则——带边界的规则拿 `confirmed` 就能越过
`high AND confirmed` 的阈值把红点点亮，正是本篇「误报三次就关掉」量的是那个东西。
现在两者都是 `unverified`。

**③ 修完再量：precision = 0.44（36 条里 16 条成立），仍然没过 0.7 的门。**
红点这一路是干净的——40 天里**一条 `high AND confirmed` 都没有**，所以通知一次没误发；
不干净的是报告页：`remote_in_loop` 9 条全对，`swallow_exception` 27 条里 20 条是误报。

那 20 条不是随机噪声，是**同一个形状**：「副作用失败是有意容忍的」——通知邮件发失败不影响
主流程（源码里就写着这句注释）、批量任务单条失败不标记成功留待重试、UA/IP 解析失败回退原值、
校验降级返回空值。**这恰好是 D3 预留给模型复核去驳回的那一类**，不是规则层该继续追的：
再往下就是让正则去读注释、判意图，那会把规则变成另一个不可复现的猜测器。
**所以到 0.7 的路不在规则层，在 `review.ai`**——门要重开，得让模型真的跑在这 20 条上并
把它们驳掉，那需要一个可用的推理端点（本机 LM Studio 之前是连不上的），量的是
「复核后留在报告里的条数有多少成立」，不是规则单独的数字。

**结论与措辞纪律**：验收 2 **做了，门没过**，所以 `review.notify=all` 现在仍然不要用；
`high`（唯一会点亮红点的路径）可以开，因为它的读数在本窗口是 0。
规则层单独看是 0.44，**这个数字不许被写成「功能可用」**——它是「不再说谎」，不是「看得准」。
