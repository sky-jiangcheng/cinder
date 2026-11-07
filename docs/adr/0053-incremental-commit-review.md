# ADR-0053: 提交后增量审查——规则筛候选，模型只复核候选

状态：**Proposed（2026-11-08，设计稿，等用户拍板后落码）**
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
| CLI | `repotory review --since yesterday [--dry-run] [--no-ai] [--full] [--json]` |
| MCP | `review_latest {days?, severity?}`、`review_run {date?, dry_run?}`（新增 2 个 → `CoreVersion` 1.2.0） |
| 桌面 | 设置页「每日审查」分区（开关/时间/范围/是否用 AI/通知级别）；「审查」页按文件分组列出 findings，点开看证据行 |
| 通知 | 只推 `severity=high AND verdict=confirmed`；没有就不打扰 |

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
