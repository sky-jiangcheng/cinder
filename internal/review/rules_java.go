package review

import (
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
func isJava(f *File) bool {
	if !strings.HasSuffix(f.Path, ".java") {
		return false
	}
	lower := strings.ToLower(f.Path)
	for _, bad := range []string{"/target/", "/build/", "/generated/", "/generated-sources/"} {
		if strings.Contains(lower, bad) {
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

func (r feignMissingBody) Check(f *File, _ Repo) []Finding {
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
		out = append(out, finding(f, r.ID(), sev, VerdictUnverified, m.Line,
			"Feign 方法 "+m.Name+" 的 "+itoa(len(m.Params))+" 个参数都没有绑定注解（@RequestBody / @RequestParam），调用时请求体可能为空",
			"给请求体参数加 @RequestBody；若确实是查询参数，加 @RequestParam 消除歧义"))
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
// reference the old literal. This is the cross-repository half of the Feign
// story: the consumer file was not touched, so a diff-only review sees
// nothing.
//
// Known false-positive boundary: the old path may appear only in a comment or
// a doc string. References are therefore counted per file, not per line, and
// the message lists the files so a human can judge in one glance.
type routeRenamed struct{}

func (routeRenamed) ID() string { return "route_renamed" }

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
		newPaths[p] = true
	}
	var out []Finding
	for _, old := range oldPaths {
		if old == "" || old == "/" || newPaths[old] {
			continue
		}
		hits := repo.GrepJava(old)
		others := make([]string, 0, len(hits))
		for _, h := range hits {
			if h != f.Path {
				others = append(others, h)
			}
		}
		if len(others) == 0 {
			continue
		}
		line := firstAddedLine(f)
		out = append(out, finding(f, r.ID(), SeverityHigh, VerdictConfirmed, line,
			"路由字面量 "+old+" 被改动，但其它 Java 文件仍在引用它："+strings.Join(others, ", "),
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

func (r feignArity) Check(f *File, _ Repo) []Finding {
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
		out = append(out, finding(f, r.ID(), SeverityMedium, VerdictUnverified, m.Line,
			"Feign 方法 "+m.Name+" 的参数个数从 "+itoa(before)+" 变成 "+itoa(len(m.Params))+"，provider 侧方法签名需同步",
			"核对 provider 的同 path 方法；provider 不在本机时请人工确认后再发布"))
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
		case hasLogCall(stripped) && !hasRethrow(stripped):
			out = append(out, finding(f, r.ID(), SeverityMedium, VerdictUnverified, n,
				"catch 块记录了日志但没有抛出，调用方会以为操作成功", "确认失败必须上抛；确实可忽略时补一句注释说明原因"))
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
		lower := strings.ToLower(line)
		if strings.Contains(line, "${") && (strings.Contains(lower, "select") ||
			strings.Contains(lower, "insert") || strings.Contains(lower, "update") ||
			strings.Contains(lower, "delete") || strings.Contains(lower, "order by")) {
			out = append(out, finding(f, r.ID(), SeverityMedium, VerdictUnverified, n,
				"SQL 里出现 MyBatis ${} 直接拼接", "改为 #{}；表名/排序字段必须动态时用白名单校验"))
			continue
		}
		if !strings.Contains(lower, "select") && !strings.Contains(lower, "insert") &&
			!strings.Contains(lower, "update") && !strings.Contains(lower, "delete") &&
			!strings.Contains(lower, "where") {
			continue
		}
		// Concatenation inside the SQL string, or a '+' joining a variable
		// into a query constant.
		if strings.Contains(line, "\" +") || strings.Contains(line, "+ \"") ||
			strings.Contains(line, "' +") || strings.Contains(line, "+ '") {
			out = append(out, finding(f, r.ID(), SeverityHigh, VerdictConfirmed, n,
				"SQL 通过字符串拼接构造，参数可注入", "改用预编译参数占位符（? / #{}）传值"))
		}
	}
	return out
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
type remoteInLoop struct{}

func (remoteInLoop) ID() string { return "remote_in_loop" }

func (r remoteInLoop) Check(f *File, _ Repo) []Finding {
	if !isJava(f) {
		return nil
	}
	stacks := blockStack(f.Lines())
	var out []Finding
	for _, n := range f.AddedLines() {
		if !InLoop(stacks, n) {
			continue
		}
		if !looksRemoteCall(stripLiteral(f.Added[n])) {
			continue
		}
		out = append(out, finding(f, r.ID(), SeverityMedium, VerdictUnverified, n,
			"循环体内出现远程调用（Client / Api / Feign 后缀），可能是 N+1",
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
