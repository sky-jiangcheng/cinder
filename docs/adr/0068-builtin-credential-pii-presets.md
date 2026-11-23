# ADR-0068：内置凭证/PII 预设规则库——结构化格式是信号，关键词不是

- **状态**：Accepted（2026-11-23）
- **日期**：2026-11-23
- **关联**：[ADR-0065](0065-hindsight-absorb-not-replace.md)（对标 Hindsight 的吸收路线，P1 第一项）、[ADR-0030](0030-ai-call-audit.md)（AI 调用审计与内容审查，本篇修订其「不内置黑名单」口径的适用范围）、[ADR-0012](0012-semantic-search.md)（默认关的隐私授权口径——默认开等于替用户做授权决定）

## 背景

ADR-0065 的差距清单 §2.3：Hindsight 的 Memory Defense 出厂带 45 条 secrets/PII 模式，写入前自动脱敏；Cinder 的 ADR-0030 审查机制只有用户自写正则，出厂零规则。评测基线也确认：交接与导入内容里出现真实密钥形状时，没有任何一层会拦。

ADR-0030 当时拒绝内置黑名单的理由是「关键词匹配是很弱的控制」——这个判断对**关键词**依然成立。但它没有区分另一类目标：**结构化凭证**。`AKIA` + 16 位大写字母数字、`-----BEGIN ... PRIVATE KEY-----` 块、`postgres://user:pass@host` 连接串、18 位身份证号——这些的**格式本身就是信号**，不依赖语义改写，正则可以做到高精度而几乎不误伤。把两类目标混在一个「不内置」口径里，是把正确的拒绝理由用过了边界。

## 决策

1. **出厂预设库，只收结构化格式。** `internal/aiaudit/presets.go`：20 条命名预设（私钥块/私钥头、AWS/GitHub/GitLab/Slack/Anthropic/Stripe/OpenAI 形状/Google/npm/HuggingFace/SendGrid/Twilio 的 key、JWT、带密码的数据库连接串、secret 赋值、身份证号、手机号、银行卡号）。**邮箱与人名刻意不收**：开发记忆里合法邮箱密度极高（维护者联系方式、git 作者），收了必然误伤——这正是 ADR-0030 拒绝关键词的理由在结构化领域内的残留边界。
2. **两个独立闸门，全部默认关。** `ai_review_presets=1` 把预设**追加**到用户规则之后（`Resolve()` 内拼接，`ai.go` 三个审查调用点一行不改）；`memory_redact=1` 在**入口侧**落库前把命中片段替换为 `[redacted:<name>]` 标记。出口审查与入口脱敏各自独立开关——有人只想审计不想改写入，有人只想入库干净不想拦调用。默认全关时行为与之前逐字节一致（ADR-0030 的承诺原样继承）。
3. **入口侧覆盖全部机器写路径。** service 三个创建函数（`CreateNote` / `CreateNoteWithMeta` / `createNoteWithKey`，覆盖 MCP 笔记、handoff、capture）+ 插件运行时的 `upsertDoc`（八个导入器的直写漏斗）。导入路径在**任何查找之前**脱敏，使内容哈希跳过与幂等键两侧比较的都是已脱敏文本，4 小时同步不会因脱敏产生假更新。**边界**：已有笔记不回溯清洗；桌面端手动编辑不脱敏——用户亲手写的内容用户自己负责，机器批量写入才是泄露面。
4. **标记幂等且自识别。** `[redacted:aws-access-key-id]` 不匹配任何预设，二次脱敏计数为 0；标记带规则名，读者能知道这里原来是什么类别的东西，而不是一个黑洞。bounds 校验在脱敏**之后**做——标记可能比原片段长，落库文本才是需要满足上限的那份。
5. **与 MatchRule 的小写化兼容。** 出口审查路径先 `ToLower` 再匹配（ADR-0030 实现），所以每条预设都带 `(?i)` 且字符类同时覆盖大小写，保证同一条规则在入口（原文）与出口（小写）两侧命中一致。有测试钉住这个双形态兼容。
6. **不动工具面。** `repotory_ai_review_rules` 只改描述与返回字段（`presets_enabled` / `preset_count` / `redact_enabled`），工具名集合不变（fixture 只钉名字），CoreVersion 不动。`rule_count` 语义为生效规则总数（用户 + 预设），`preset_count` 说明其中多少来自预设。

## 代价（接受）

- 20 条对 Hindsight 的 45 条：覆盖面窄是刻意的——每条都要过「不误伤开发记忆」的负例测试（git SHA、UUID、毫秒时间戳、普通 prose、邮箱全部必须放行），宁缺毋滥。
- 顺序替换意味着重叠片段只标第一个命中的类别（如私钥块内的 base64 不会再被 key 形状二次标记）——脱敏目的是消除泄露，不是精确分类。
- `secret-assignment` 是最宽的一条（`password = "..."` 形状），可能命中文档里的示例假密码。这是精度-覆盖权衡里唯一偏覆盖的一条，因为赋值形状本身就是人类写密钥的方式。
- 每次笔记写入多三次配置读 +（开启时）20 次正则替换；关闭时只有配置读，微秒级。

## 翻转触发

- 预设被实测证明误伤合法记忆（北极星：人工纠正次数上升）→ 收窄或移除对应条目，而不是加白名单机制。
- 出现可靠的本地小模型 PII 识别（P3 triage 路线）→ 重估正则库是否降级为其兜底。

## 落地记录（2026-11-23）

- 代码：`internal/aiaudit/presets.go`（20 预设 + `PresetRules` / `Redact` / `RedactIfEnabled`）、`aiaudit.go`（`Policy.PresetsEnabled` + `Resolve` 追加）、`internal/service/config.go`（两个新键）、`note.go`（`redactForStore` 三路径）、`runtime.go`（`upsertDoc` 前置脱敏）、`ai_audit_query.go`（状态三字段）、`cmd/mcp/tools_audit.go`（描述 + 返回字段）。
- 测试：aiaudit 7 个（逐预设正例、负例放行、标记幂等、小写兼容、闸门默认关、Resolve 追加不替换）+ service 3 个（默认不变、三写路径脱敏、状态上报）；前端 AiAuditSection 双开关 + 预设计数测试，vitest 431 全绿，tsc 通过。
- 前端：设置页两个新开关（内置预设 / 写入前脱敏）+ 提示文案 + 统计行，双语文案同步；「不内置任何词表」口径改为「不内置关键词词表」。
- 文档：ADR-0030 补注、`features/ai-integration.md` 审查段落双语修订。