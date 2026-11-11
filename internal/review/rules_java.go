package review

import (
	"sort"
	"strings"
)

// The first rule set is Java-only on purpose (ADR-0053 D3): the repositories
// being reviewed are Java, and a rule that has not been measured against real
// code is worse than no rule — three false positives and the daily report gets
// turned off. Every rule below therefore carries its known false-positive
// boundary in a comment, and the verdict says whether the pattern is
// self-evidently wrong (confirmed) or merely suspicious (unverified).

// mappingAnnotations are the Spring MVC / Cloud mapping annotations whose
// path argument is a contract: renaming it breaks callers that are not in this
// commit.
var mappingAnnotations = []string{
	"requestmapping", "getmapping", "postmapping",
	"putmapping", "deletemapping", "patchmapping",
}

// writeMappingAnnotations are the ones that carry a request body.
var writeMappingAnnotations = []string{"postmapping", "putmapping", "patchmapping"}

// OptInRule is implemented by rules that stay off until the user turns them
// on. Kept as a separate interface so the base Rule interface does not grow a
// method that nine of ten rules would answer the same way.
type OptInRule interface {
	Rule
	// OptIn reports whether the rule is disabled by default.
	OptIn() bool
}

// DefaultRules returns the rules the service runs, in a stable order. Order
// only affects the order findings are written in; the report sorts by
// severity anyway.
func DefaultRules() []Rule {
	return []Rule{
		feignMissingBody{},
		feignGetBody{},
		routeRenamed{},
		feignArity{},
		swallowException{},
		txSelfCall{},
		sqlInjection{},
		sensitiveLog{},
		sharedDateFormat{},
		remoteInLoop{},
	}
}

// isJava reports whether the file is worth running Java rules on. Tests,
// generated sources and build output are excluded: a review of `target/`
// would be noise, and generated code cannot be fixed by hand anyway.
//
// Test sources are excluded by path and by name, not by intent — the
// 2026-11-09 labelling run found three candidates in `*Test.java` under
// `src/test/`, all of them the standard
// `try { fail(...); } catch (BusinessException expected) { assertTrue(...) }`
// shape, which is a test asserting an exception is thrown, not a swallow.
func isJava(f *File) bool {
	if !strings.HasSuffix(f.Path, ".java") {
		return false
	}
	lower := strings.ToLower(f.Path)
	for _, bad := range []string{"/target/", "/build/", "/generated/", "/generated-sources/", "src/test/", "/test/java/"} {
		if strings.Contains(lower, bad) {
			return false
		}
	}
	for _, suffix := range []string{"test.java", "tests.java"} {
		if strings.HasSuffix(lower, suffix) {
			return false
		}
	}
	return true
}

// fileHasAnnotation reports whether the whole file mentions an annotation.
// A Feign interface can be recognised this cheaply: the annotation is on the
// type declaration, and a file that does not contain the string anywhere is
// definitely not one.
func fileHasAnnotation(f *File, name string) bool {
	for _, line := range f.Lines() {
		if hasAnnotation(annotationNames(line), name) {
			return true
		}
	}
	return false
}

// addedMatching returns the added lines whose text satisfies pred, in line
// order.
func addedMatching(f *File, pred func(line string) bool) []int {
	var out []int
	for _, n := range f.AddedLines() {
		if pred(f.Added[n]) {
			out = append(out, n)
		}
	}
	return out
}

// ---------------------------------------------------------------- feign ----

// feignMissingBody: a @FeignClient method with a POJO parameter and no binder
// annotation. Spring then sends the request with no body at all, the provider
// binds nulls, and nothing fails until production data looks wrong.
//
// Known false-positive boundary (ADR-0053 D3), which is why the verdict is
// unverified: a client declared with `consumes=application/json` may work by
// accident on some Spring Cloud versions, a provider may read the same field
// with @RequestParam, and a method may simply never be called.
type feignMissingBody struct{}

func (feignMissingBody) ID() string { return "feign_missing_body" }

func (r feignMissingBody) Check(f *File, repo Repo) []Finding {
	if !isJava(f) || !fileHasAnnotation(f, "feignclient") {
		return nil
	}
	var out []Finding
	for _, m := range methods(f.Lines()) {
		if !hasAnnotation(m.Annotations, "postmapping") && !hasAnnotation(m.Annotations, "putmapping") &&
			!hasAnnotation(m.Annotations, "patchmapping") {
			continue
		}
		if len(m.Params) == 0 || !f.Touches(m.Line, m.Line) {
			continue
		}
		annotated := false
		complex := false
		for _, p := range m.Params {
			if p.Annotated() {
				annotated = true
				break
			}
			if !p.IsSimpleType() {
				complex = true
			}
		}
		if annotated || !complex {
			continue
		}
		sev := SeverityMedium
		if len(m.Params) > 1 {
			sev = SeverityHigh
		}
		fd := finding(f, r.ID(), sev, VerdictUnverified, m.Line,
			"Feign 方法 "+m.Name+" 的 "+itoa(len(m.Params))+" 个参数都没有绑定注解（@RequestBody / @RequestParam），调用时请求体可能为空",
			"给请求体参数加 @RequestBody；若确实是查询参数，加 @RequestParam 消除歧义")
		// A nil repo means there is no cross-project index to consult.
		if repo == nil {
			out = append(out, fd)
			continue
		}
		// Reverse-look up the provider in the other collected repositories.
		var peerLines, names []string
		seen := map[string]bool{}
		for _, path := range declPaths(f.Lines(), m.Line) {
			labels, decls, sources := crossRepoDecls(f, repo, path)
			for _, label := range labels {
				// Only a provider that binds the body with @RequestBody is a mismatch.
				if seen[label] || !strings.Contains(strings.ToLower(decls[label]), "@requestbody") {
					continue
				}
				seen[label] = true
				peerLines = append(peerLines, label+"\n"+sources[label])
				names = append(names, peerRepo(label))
			}
		}
		if len(peerLines) == 0 {
			// D3 does not guess: say the provider was not found locally.
			fd.Message = oneLine(fd.Message + " 未在本机收藏仓找到 provider 声明，需人工确认")
			out = append(out, fd)
			continue
		}
		sort.Strings(peerLines)
		if len(peerLines) > 3 {
			peerLines = peerLines[:3]
		}
		fd.Evidence = clip(fd.Evidence+"\n"+strings.Join(peerLines, "\n"), 800)
		fd.Message = oneLine(fd.Message + " 对端 " + strings.Join(uniqNames(names), "、") + " 的 provider 声明带了 @RequestBody，契约不一致")
		out = append(out, fd)
	}
	return out
}

// feignGetBody: @RequestBody on a GET/DELETE method. No proxy reliably
// forwards a body on those verbs, so this is deterministic — no model review
// needed, verdict confirmed.
type feignGetBody struct{}

func (feignGetBody) ID() string { return "feign_get_body" }

func (r feignGetBody) Check(f *File, _ Repo) []Finding {
	if !isJava(f) || !fileHasAnnotation(f, "feignclient") {
		return nil
	}
	var out []Finding
	for _, m := range methods(f.Lines()) {
		if !hasAnnotation(m.Annotations, "getmapping") && !hasAnnotation(m.Annotations, "deletemapping") {
			continue
		}
		if !f.Touches(m.Line, m.Line) {
			continue
		}
		for _, p := range m.Params {
			if !hasAnnotation(p.Annotations, "requestbody") {
				continue
			}
			out = append(out, finding(f, r.ID(), SeverityHigh, VerdictConfirmed, m.Line,
				"GET/DELETE 方法 "+m.Name+" 的参数用了 @RequestBody，网关与代理会丢弃请求体",
				"改成 @RequestParam / @PathVariable，或把该方法改为 POST"))
			break
		}
	}
	return out
}

// routeRenamed: the commit changed a mapping path, and other Java files still
// reference the OLD route. This is the cross-repository half of the Feign
// story: the consumer file was not touched, so a diff-only review sees
// nothing.
//
// Known false-positive boundary: the old path may appear only in a comment or
// a doc string. References are therefore counted per file, not per line, and
// the message lists the files so a human can judge in one glance.
//
// rule_rev 2026-11-09 — two shapes the first version conflated, found in the
// labelling run (one commit produced 30 of these, all false):
//
//  1. `"checkModify"` → `"/checkModify"`. Spring's `PatternParser` treats a
//     leading slash as optional, so this is the SAME route renamed for
//     style. Comparing path strings literally called it a rename; comparing
//     canonicalPath("checkModify") == canonicalPath("/checkModify") does
//     not. The commit was a 30-line cosmetic sweep across one controller.
//  2. The reference test was `GrepJava(old)` — a substring search over file
//     contents. `checkModify` is a substring of `"/checkModify"`, so every
//     consumer that had already been updated still "referenced the old
//     path". The test now reads each candidate file's own mapping paths and
//     compares routes, not text.
type routeRenamed struct{}

func (routeRenamed) ID() string { return "route_renamed" }

// canonicalPath reduces a mapping literal to the route it matches.
// `"/x"`, `"x"` and `"/x/"` are one route in Spring; `""` and `"/"` mean
// "the class-level path", which is not a rename target.
func canonicalPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimSuffix(p, "/")
	return p
}

func (r routeRenamed) Check(f *File, repo Repo) []Finding {
	if !isJava(f) || repo == nil {
		return nil
	}
	oldPaths := mappingPaths(f.Removed)
	if len(oldPaths) == 0 {
		return nil
	}
	newPaths := map[string]bool{}
	for _, p := range mappingPaths(addedTexts(f)) {
		newPaths[canonicalPath(p)] = true
	}
	var out []Finding
	for _, old := range oldPaths {
		want := canonicalPath(old)
		if want == "" || newPaths[want] {
			continue
		}
		// Which OTHER files still declare this route? Read their mapping
		// annotations instead of grepping for the text: a Feign consumer
		// re-declares the route with its own @PostMapping, and that is the
		// reference worth naming. A comment that merely contains the word
		// is not (D3's boundary), and neither is an updated `"/x"`.
		var others []string
		for _, h := range repo.GrepJava(old) {
			if h == f.Path || h == f.Key {
				continue
			}
			content, ok := repo.Read(h)
			if !ok {
				continue
			}
			for _, p := range mappingPaths(strings.Split(content, "\n")) {
				if canonicalPath(p) == want {
					// Encoded keys are rendered readable so no NUL byte reaches the report.
					others = append(others, humanKey(h))
					break
				}
			}
		}
		if len(others) == 0 {
			continue
		}
		sort.Strings(others)
		line := firstAddedLine(f)
		// unverified, not confirmed: D3 lists this rule's false-positive
		// boundary and rule.go reserves `confirmed` for rules that have
		// none. Whether the referring file is a live consumer or a dead
		// duplicate is a judgement the model pass is for.
		out = append(out, finding(f, r.ID(), SeverityHigh, VerdictUnverified, line,
			"路由字面量 "+old+" 被改动，但其它 Java 文件仍在声明它："+strings.Join(others, ", "),
			"同步修改这些调用方，或保留旧路径一段时间做兼容"))
	}
	return out
}

// feignArity: a Feign method signature changed shape. The provider side is
// the contract's other half and usually lives in a different repository, so
// the finding is unverified by construction — it says "check the provider",
// not "this is broken".
type feignArity struct{}

func (feignArity) ID() string { return "feign_arity" }

func (r feignArity) Check(f *File, repo Repo) []Finding {
	if !isJava(f) || !fileHasAnnotation(f, "feignclient") {
		return nil
	}
	oldSigs := map[string]int{} // name -> param count
	for _, line := range f.Removed {
		if name, params, ok := parseSignature(strings.TrimSpace(stripLiteral(line))); ok {
			oldSigs[name] = len(params)
		}
	}
	if len(oldSigs) == 0 {
		return nil
	}
	var out []Finding
	for _, m := range methods(f.Lines()) {
		before, seen := oldSigs[m.Name]
		if !seen || before == len(m.Params) || !f.Touches(m.Line, m.Line) {
			continue
		}
		fd := finding(f, r.ID(), SeverityMedium, VerdictUnverified, m.Line,
			"Feign 方法 "+m.Name+" 的参数个数从 "+itoa(before)+" 变成 "+itoa(len(m.Params))+"，provider 侧方法签名需同步",
			"核对 provider 的同 path 方法；provider 不在本机时请人工确认后再发布")
		// Without an index keep the original unverified behaviour.
		if repo == nil {
			out = append(out, fd)
			continue
		}
		// Compare the parameter count against the provider in other repositories.
		var peerLines, names []string
		found := false
		seenLabel := map[string]bool{}
		for _, path := range declPaths(f.Lines(), m.Line) {
			labels, decls, sources := crossRepoDecls(f, repo, path)
			for _, label := range labels {
				if seenLabel[label] {
					continue
				}
				seenLabel[label] = true
				_, params, ok := parseSignature(strings.TrimSpace(stripLiteral(decls[label])))
				if !ok {
					continue
				}
				found = true
				// Matching counts mean the provider already follows the contract.
				if len(params) == len(m.Params) {
					continue
				}
				peerLines = append(peerLines, label+" 参数个数="+itoa(len(params))+"\n"+sources[label])
				names = append(names, peerRepo(label))
			}
		}
		// No provider found: keep the original unverified finding and wording.
		if !found {
			out = append(out, fd)
			continue
		}
		// Every provider matches: drop the false positive.
		if len(peerLines) == 0 {
			continue
		}
		sort.Strings(peerLines)
		if len(peerLines) > 3 {
			peerLines = peerLines[:3]
		}
		fd.Evidence = clip(fd.Evidence+"\n"+strings.Join(peerLines, "\n"), 800)
		fd.Message = oneLine(fd.Message + " 对端 " + strings.Join(uniqNames(names), "、") + " 的 provider 参数个数不匹配")
		out = append(out, fd)
	}
	return out
}

// --------------------------------------------------------------- 异常处理 ----

// swallowException: a catch block that does nothing, only prints the stack
// trace, or logs and then lets the method return normally. The last shape is
// the dangerous one — the caller sees success and the data is gone.
//
// Known false-positive boundary: idempotent cleanup code legitimately ignores
// failures, and that is exactly what the model review pass is for.
type swallowException struct{}

func (swallowException) ID() string { return "swallow_exception" }

func (r swallowException) Check(f *File, _ Repo) []Finding {
	if !isJava(f) {
		return nil
	}
	lines := f.Lines()
	var out []Finding
	for _, n := range addedMatching(f, func(l string) bool {
		s := stripLiteral(l)
		return strings.Contains(s, "catch") && strings.Contains(s, "(")
	}) {
		body := catchBody(lines, n)
		if body == nil {
			continue
		}
		stripped := make([]string, 0, len(body))
		for _, b := range body {
			if t := strings.TrimSpace(stripLiteral(b)); t != "" && t != "{" && t != "}" {
				stripped = append(stripped, t)
			}
		}
		switch {
		case len(stripped) == 0:
			out = append(out, finding(f, r.ID(), SeverityMedium, VerdictUnverified, n,
				"catch 块为空，异常被完全吞掉", "至少记一条日志，或重新抛出"))
		case len(stripped) == 1 && strings.Contains(stripped[0], "printStackTrace"):
			out = append(out, finding(f, r.ID(), SeverityMedium, VerdictUnverified, n,
				"catch 块只调用 printStackTrace()，生产环境没有日志也没有告警", "改用日志框架记录 error 级别"))
		case hasLogCall(stripped) && !hasRethrow(stripped) && !escalates(stripped) && !returnsFailure(stripped):
			out = append(out, finding(f, r.ID(), SeverityMedium, VerdictUnverified, n,
				"catch 块记了日志，但异常既没上抛也没外发告警，调用方无从得知失败",
				"确认失败必须上抛；确实可忽略时补一句注释说明原因"))
		}
	}
	return out
}

// catchBody returns the lines inside a catch block starting at line n, or nil
// when the block cannot be delimited. A one-line `catch (X e) { }` yields an
// EMPTY slice, not nil: "the body is empty" is exactly what the rule reports.
func catchBody(lines []string, n int) []string {
	end := matchBrace(lines, n-1)
	if end < 0 {
		return nil
	}
	start := n // 0-based index of the line after the catch line
	if end > len(lines) {
		end = len(lines)
	}
	if end < start {
		end = start
	}
	return lines[start:end]
}

func hasLogCall(lines []string) bool {
	for _, l := range lines {
		lower := strings.ToLower(l)
		if strings.Contains(lower, "log.") || strings.Contains(lower, "logger.") {
			return true
		}
	}
	return false
}

func hasRethrow(lines []string) bool {
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "throw ") {
			return true
		}
	}
	return false
}

// escalates reports whether the catch block pushes the failure to an
// out-of-band alarm channel instead of the call stack. rule_rev 2026-11-09:
// the shape this assumes is "an identifier containing Alarm, used as either
// the receiver or the method" (PlatformClientForAlarm.sendAlarmMessage,
// MonitorAlarm.error, sendWarnAlarmMessage) — a deliberate job pattern, not
// a swallow. Before this check existed the labelling run counted 11
// candidates whose bodies did exactly that: log, alarm, continue.
func escalates(lines []string) bool {
	for _, l := range lines {
		lower := strings.ToLower(stripLiteral(l))
		// Identifiers only: the word "alarm" inside a message string must
		// not count, which is why stripLiteral runs first.
		for _, tok := range strings.FieldsFunc(lower, func(r rune) bool {
			// Non-ASCII is always a separator: byte(r) on a multi-byte rune
			// would alias into the ASCII ident range and split a Chinese log
			// message into fake identifiers.
			return r > 127 || !isIdentChar(byte(r))
		}) {
			if strings.Contains(tok, "alarm") {
				return true
			}
		}
	}
	return false
}

// returnsFailure reports whether the catch block hands the caller an
// explicit failure value, which is the opposite of "the caller believes the
// operation succeeded". rule_rev 2026-11-09: `return EasyMsResponseEntity
// .ofFail(...)`. A bare `return ""` deliberately does not match — falling
// back to an empty value silently is exactly the shape worth reporting.
func returnsFailure(lines []string) bool {
	for _, l := range lines {
		s := stripLiteral(l)
		lower := strings.ToLower(s)
		if !strings.Contains(lower, "return") {
			continue
		}
		if strings.Contains(lower, "offail") || strings.Contains(lower, "fail(") ||
			strings.Contains(lower, "errorresult") {
			return true
		}
	}
	return false
}

// --------------------------------------------------------------- 事务 ----

// txSelfCall: `this.foo()` on a @Transactional method of the same class. The
// Spring proxy only wraps calls that arrive from outside the bean, so the
// annotation silently does nothing. Deterministic — confirmed.
type txSelfCall struct{}

func (txSelfCall) ID() string { return "tx_self_call" }

func (r txSelfCall) Check(f *File, _ Repo) []Finding {
	if !isJava(f) {
		return nil
	}
	transactional := map[string]bool{}
	for _, m := range methods(f.Lines()) {
		if m.HasAnnotation("transactional") {
			transactional[m.Name] = true
		}
	}
	if len(transactional) == 0 {
		return nil
	}
	var out []Finding
	for _, n := range addedMatching(f, func(l string) bool {
		s := stripLiteral(l)
		for name := range transactional {
			if strings.Contains(s, "this."+name+"(") {
				return true
			}
		}
		return false
	}) {
		out = append(out, finding(f, r.ID(), SeverityHigh, VerdictConfirmed, n,
			"同类内 this 调用 @Transactional 方法，Spring 代理不拦截，事务不会生效",
			"把方法拆到另一个 bean，或改为通过注入自身代理调用"))
	}
	return out
}

// --------------------------------------------------------------- SQL ----

// sqlInjection: SQL built by string concatenation, or a MyBatis `${}` splice.
// Concatenation is reported high; `${}` is reported medium because dynamic
// table names and sort columns are a legitimate (if unwise) use.
type sqlInjection struct{}

func (sqlInjection) ID() string { return "sql_injection" }

func (r sqlInjection) Check(f *File, _ Repo) []Finding {
	if !isJava(f) {
		return nil
	}
	var out []Finding
	for _, n := range f.AddedLines() {
		line := f.Added[n]
		if strings.Contains(line, "${") && hasSqlLiteral(line) {
			out = append(out, finding(f, r.ID(), SeverityMedium, VerdictUnverified, n,
				"SQL 里出现 MyBatis ${} 直接拼接", "改为 #{}；表名/排序字段必须动态时用白名单校验"))
			continue
		}
		// Concatenation inside the SQL string, or a '+' joining a variable
		// into a query constant. rule_rev 2026-11-09: the SQL keyword must
		// live **inside a string literal that starts like SQL**, not anywhere
		// on the line. Both of the labelling run's sql_injection candidates
		// were notification copy — `"please update the documents at your
		// earliest convenience." + relevant.getFullName() + ...` — where
		// "update" is prose and the `+` is ordinary message assembly.
		//
		// The verdict also changed from confirmed to unverified: this rule
		// has a known false-positive boundary in ADR-0053 D3 (a whitelisted
		// dynamic table name), and rule.go reserves `confirmed` for rules
		// without one. A boundary-having rule that ships `confirmed` gets
		// past the notify threshold and into the rail dot, which is exactly
		// the three-false-positives-and-it's-off failure the ADR measures.
		if !hasSqlLiteral(line) {
			continue
		}
		if hasSqlConcat(line) {
			out = append(out, finding(f, r.ID(), SeverityHigh, VerdictUnverified, n,
				"SQL 通过字符串拼接构造，参数可注入", "改用预编译参数占位符（? / #{}）传值"))
		}
	}
	return out
}

// hasSqlConcat reports whether a SQL-bearing line joins a literal with a
// NON-literal operand. rule_rev 2026-11-09: both sql_injection candidates
// the labelling run produced were `@Select("select r.user_id from t r " +
// "where not exists (select 1 from u)")` — the `+` concatenates two string
// CONSTANTS, which javac folds into one literal at compile time. That is not
// injectable by definition; flagging it is the kind of false positive that
// gets the feature switched off. Only a `+` with a variable, a call result,
// or a `%s`/format argument on one side is a real splice.
func hasSqlConcat(line string) bool {
	inStr := false
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inStr {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			continue
		}
		if c == '/' && i+1 < len(line) && (line[i+1] == '/' || line[i+1] == '*') {
			break
		}
		if c != '+' {
			continue
		}
		left := strings.TrimRight(line[:i], " \t")
		right := strings.TrimLeft(line[i+1:], " \t")
		if left == "" || right == "" {
			continue
		}
		// Literal on one side, not on the other: the non-literal side is the
		// injected value.
		litLeft := strings.HasSuffix(left, "\"") || strings.HasSuffix(left, "'")
		litRight := strings.HasPrefix(right, "\"") || strings.HasPrefix(right, "'")
		if litLeft != litRight {
			return true
		}
		// Both sides are expressions (no quotes adjacent): `a + b` inside a
		// SQL-building statement only matters when one side is a literal
		// somewhere else on the line, which the loop reaches on that '+'.
	}
	return false
}

// hasSqlLiteral reports whether the line carries a string literal that opens
// like SQL. It reads the RAW line because stripLiteral makes literal
// contents invisible on purpose; here the content is the whole question.
func hasSqlLiteral(line string) bool {
	inStr := false
	start := 0
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '\\' && inStr {
			i++
			continue
		}
		if c != '"' {
			continue
		}
		if !inStr {
			inStr = true
			start = i + 1
			continue
		}
		s := strings.ToLower(strings.TrimSpace(line[start:i]))
		for _, kw := range []string{"select ", "insert ", "update ", "delete ",
			"where ", "from ", "order by ", "values ", "set "} {
			if strings.HasPrefix(s, kw) {
				return true
			}
		}
		inStr = false
	}
	return false
}

// --------------------------------------------------------------- 日志 ----

// sensitiveLog: logging an identity or credential field. Opt-in: plenty of
// codebases deliberately log a masked phone number, and a rule that cannot
// tell "phone" from "maskedPhone" would cry wolf every day.
type sensitiveLog struct{}

func (sensitiveLog) ID() string  { return "sensitive_log" }
func (sensitiveLog) OptIn() bool { return true }

func (r sensitiveLog) Check(f *File, _ Repo) []Finding {
	if !isJava(f) {
		return nil
	}
	var out []Finding
	for _, n := range addedMatching(f, func(l string) bool {
		lower := strings.ToLower(stripLiteral(l))
		if !strings.Contains(lower, "log.") && !strings.Contains(lower, "logger.") {
			return false
		}
		for _, word := range []string{"idcard", "phone", "mobile", "cardno", "password", "token"} {
			if strings.Contains(lower, word) {
				return true
			}
		}
		return false
	}) {
		out = append(out, finding(f, r.ID(), SeverityMedium, VerdictUnverified, n,
			"日志语句中出现身份证 / 手机号 / 卡号 / 口令类字段，明文落盘", "脱敏后再打印，或只打印长度与哈希"))
	}
	return out
}

// --------------------------------------------------------------- 并发 ----

// sharedDateFormat: `static SimpleDateFormat`. The class is mutable and not
// thread-safe, so two concurrent requests can corrupt the parsed date or throw
// — the classic heisenbug that never reproduces locally.
type sharedDateFormat struct{}

func (sharedDateFormat) ID() string { return "shared_sdf" }

func (r sharedDateFormat) Check(f *File, _ Repo) []Finding {
	if !isJava(f) {
		return nil
	}
	var out []Finding
	for _, n := range addedMatching(f, func(l string) bool {
		s := stripLiteral(l)
		return strings.Contains(s, "static") && strings.Contains(s, "SimpleDateFormat")
	}) {
		out = append(out, finding(f, r.ID(), SeverityMedium, VerdictUnverified, n,
			"static SimpleDateFormat 被多线程共享，格式化结果可能错乱或抛异常",
			"改用 DateTimeFormatter（不可变），或用 ThreadLocal 包一层"))
	}
	return out
}

// remoteInLoop: a remote call inside a loop body — the N+1 shape. It is only
// "possibly N+1" because the collection may have three elements; the message
// says so instead of asserting.
//
// rule_rev 2026-11-09: the call must mention the loop's iteration variable.
// The labelling run found the batch-pagination shape — `while (true) {
// feign.fix(req(list, batchSize)); if (rsp.count < batchSize) break; }`, with
// a `//todo 这里不需要循环` comment above it acknowledging the design —
// reported as N+1. Sending the whole list once per batch is the opposite of
// N+1. Requiring the per-item variable is the cheapest line between them:
// `stockClient.query(it.getId())` fires, `feign.fix(identiferNoList,
// batchSize)` does not.
type remoteInLoop struct{}

func (remoteInLoop) ID() string { return "remote_in_loop" }

func (r remoteInLoop) Check(f *File, _ Repo) []Finding {
	if !isJava(f) {
		return nil
	}
	stacks := blockStack(f.Lines())
	vars := loopVarStack(f.Lines())
	lines := f.Lines()
	seen := map[[2]int]bool{}
	var out []Finding
	for _, n := range f.AddedLines() {
		if !InLoop(stacks, n) {
			continue
		}
		// The statement, not the line: a builder call names its callee on
		// one line and passes the loop variable two lines down.
		from, to, stmt := statementWindow(lines, n)
		if stmt == "" || !looksRemoteCall(stmt) {
			continue
		}
		looped := false
		for _, name := range vars[n-1] {
			if touchesVar(stmt, name) {
				looped = true
				break
			}
		}
		if !looped {
			continue
		}
		// One finding per statement, at its first line: five added lines of
		// the same builder call are one N+1, not five.
		key := [2]int{from, to}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, finding(f, r.ID(), SeverityMedium, VerdictUnverified, from+1,
			"循环体内对每个元素发起远程调用，可能是 N+1",
			"改为批量接口，或在循环外聚合一次调用"))
	}
	return out
}

// looksRemoteCall reports whether a line calls something whose name marks it
// as a remote interface. The suffix list is deliberately short: matching
// `Service` would fire on every local bean call and bury the real hits.
func looksRemoteCall(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isIdentChar(s[i]) {
			continue
		}
		j := i
		for j < len(s) && isIdentChar(s[j]) {
			j++
		}
		ident := s[i:j]
		if j < len(s) && s[j] == '.' && (strings.HasSuffix(ident, "Client") ||
			strings.HasSuffix(ident, "Api") || strings.HasSuffix(ident, "Feign") ||
			strings.HasSuffix(ident, "Rpc")) {
			return true
		}
		i = j
	}
	return false
}

// --------------------------------------------------------------- helpers ----

// mappingPaths extracts the quoted path literals from mapping annotations on
// the given lines.
func mappingPaths(lines []string) []string {
	var out []string
	for _, line := range lines {
		if !hasAnyAnnotation(line, mappingAnnotations) {
			continue
		}
		out = append(out, quotedStrings(line)...)
	}
	return out
}

func hasAnyAnnotation(line string, names []string) bool {
	found := annotationNames(line)
	for _, n := range names {
		if hasAnnotation(found, n) {
			return true
		}
	}
	return false
}

// quotedStrings returns the contents of double-quoted literals on a line.
func quotedStrings(line string) []string {
	var out []string
	inStr := false
	start := 0
	for i := 0; i < len(line); i++ {
		switch {
		case inStr && line[i] == '\\':
			i++
		case line[i] == '"':
			if inStr {
				out = append(out, line[start:i])
			} else {
				start = i + 1
			}
			inStr = !inStr
		}
	}
	return out
}

func addedTexts(f *File) []string {
	out := make([]string, 0, len(f.Added))
	for _, n := range f.AddedLines() {
		out = append(out, f.Added[n])
	}
	return out
}

func firstAddedLine(f *File) int {
	ls := f.AddedLines()
	if len(ls) == 0 {
		return 0
	}
	return ls[0]
}

// itoa is strconv.Itoa for the small positive numbers in messages, kept local
// to avoid an import that only formatting needs.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// declPaths returns the mapping paths on the annotations above a method declaration.
func declPaths(lines []string, line int) []string {
	from := line - 4
	if from < 1 {
		from = 1
	}
	to := line - 1
	if to > len(lines) {
		to = len(lines)
	}
	if from > to {
		return nil
	}
	return mappingPaths(lines[from-1 : to])
}

// methodDeclaresPath reports whether a method declares the canonical route want.
func methodDeclaresPath(lines []string, m method, want string) bool {
	for _, p := range declPaths(lines, m.Line) {
		if canonicalPath(p) == want {
			return true
		}
	}
	return false
}

// crossRepoDecls finds provider declarations in the other loaded files that re-declare path.
// It returns sorted labels plus the raw declaration text and a short excerpt per label.
func crossRepoDecls(f *File, repo Repo, path string) (labels []string, decls, sources map[string]string) {
	decls = map[string]string{}
	sources = map[string]string{}
	if repo == nil {
		return
	}
	want := canonicalPath(path)
	if want == "" {
		return
	}
	seen := map[string]bool{}
	for _, h := range repo.GrepJava(path) {
		// The changed file is not its own provider.
		if h == f.Path || h == f.Key {
			continue
		}
		content, ok := repo.Read(h)
		if !ok {
			continue
		}
		lines := strings.Split(content, "\n")
		for _, pm := range methods(lines) {
			if !methodDeclaresPath(lines, pm, want) {
				continue
			}
			label := humanKey(h) + ":" + itoa(pm.Line)
			if seen[label] {
				continue
			}
			seen[label] = true
			decls[label] = declText(lines, pm.Line)
			sources[label] = peerSource(lines, pm.Line)
			labels = append(labels, label)
		}
	}
	sort.Strings(labels)
	return
}

// declText joins a declaration with continuation lines until its parens balance.
func declText(lines []string, line int) string {
	i := line - 1
	if i < 0 || i >= len(lines) {
		return ""
	}
	joined := lines[i]
	for parensOpen(stripLiteral(joined)) && i+1 < len(lines) {
		i++
		joined += " " + strings.TrimSpace(lines[i])
	}
	return strings.TrimSpace(joined)
}

// peerSource returns up to three trimmed source lines around a declaration.
func peerSource(lines []string, line int) string {
	from := line - 1
	if from < 1 {
		from = 1
	}
	to := line + 1
	if to > len(lines) {
		to = len(lines)
	}
	var parts []string
	for n := from; n <= to; n++ {
		if t := strings.TrimSpace(lines[n-1]); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
}

// humanKey turns an encoded Repo key into a readable root:rel label without NUL bytes.
func humanKey(key string) string {
	root, rel := SplitKey(key)
	if root != "" {
		return root + ":" + rel
	}
	return rel
}

// peerRepo returns the repository part of a humanKey label for messages.
func peerRepo(label string) string {
	if i := strings.LastIndexByte(label, ':'); i > 0 {
		label = label[:i]
	}
	if i := strings.IndexByte(label, ':'); i > 0 {
		return label[:i]
	}
	return label
}

// uniqNames sorts and de-duplicates repository names for a message.
func uniqNames(names []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
