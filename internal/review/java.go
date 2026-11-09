package review

import (
	"strings"
)

// This file is a deliberately shallow Java reader. It is NOT a parser: the
// rules only need to answer three questions about a file — "which annotations
// sit on this declaration", "what are this method's parameters", and "is this
// line inside a loop" — and a real parser would be a dependency, a build
// surface and a new failure mode for answers we can get with braces and
// commas. Everything here therefore degrades toward "not recognised", which
// silently produces fewer candidates rather than wrong ones.

// controlKeywords are the words that can precede '(' without being a method
// name. Without this list `if (x) {` would parse as a method named "if".
var controlKeywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true,
	"synchronized": true, "return": true, "new": true, "try": true, "else": true,
	"do": true, "throw": true, "assert": true, "case": true,
	"super": true, "this": true, "yield": true, "instanceof": true,
}

// stripLiteral removes string/char literals and comments from a line so that
// structural scanning is not fooled by braces or parens inside them. It is not
// a lexer — an unterminated literal simply eats the rest of the line, which is
// the same thing javac would complain about.
func stripLiteral(line string) string {
	var b strings.Builder
	b.Grow(len(line))
	inStr, inChar := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inStr:
			if c == '\\' {
				i++
			} else if c == '"' {
				inStr = false
				b.WriteByte(' ')
			}
		case inChar:
			if c == '\\' {
				i++
			} else if c == '\'' {
				inChar = false
				b.WriteByte(' ')
			}
		case c == '"':
			inStr = true
			b.WriteByte(' ')
		case c == '\'':
			inChar = true
			b.WriteByte(' ')
		case c == '/' && i+1 < len(line) && line[i+1] == '/':
			return b.String()
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// annotationNames extracts lowercased annotation names from a line, e.g.
// `@PostMapping("/x") @ResponseBody` -> ["postmapping", "responsebody"].
// Arguments are dropped, which is why callers that need the path read the raw
// line instead.
func annotationNames(line string) []string {
	s := stripLiteral(line)
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '@' {
			continue
		}
		j := i + 1
		for j < len(s) && (isIdentChar(s[j]) || s[j] == '.') {
			j++
		}
		name := s[i+1 : j]
		if name == "" || name == "interface" { // `@interface` is a declaration
			i = j
			continue
		}
		out = append(out, strings.ToLower(name))
		i = j
	}
	return out
}

func isIdentChar(b byte) bool {
	return b == '_' || b == '$' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// hasAnnotation reports whether the declaration carries the named annotation
// (case-insensitive, unqualified — `org.springframework...@RequestBody` is
// still "requestbody" once the import alias is resolved by the compiler).
func hasAnnotation(names []string, want string) bool {
	for _, n := range names {
		if n == want || strings.HasSuffix(n, "."+want) {
			return true
		}
	}
	return false
}

// param is one method parameter split into the parts the rules read.
type param struct {
	// Annotations are lowercased names without arguments.
	Annotations []string
	// Type is the declared type with annotations and the name removed. It is
	// only compared against a small "simple type" set, so qualification is
	// left in place (IsSimpleType takes the last dotted segment).
	Type string
	// Name is the parameter identifier, "" when it could not be read.
	Name string
	// Raw is the parameter as written, used in messages.
	Raw string
}

// Annotated reports whether the parameter carries any annotation at all.
// `feign_missing_body` keys off "no parameter has any binder annotation",
// which is a stronger and much safer signal than "does not have
// @RequestBody" — the latter fires on every @RequestParam method.
func (p param) Annotated() bool { return len(p.Annotations) > 0 }

// IsSimpleType reports whether the parameter type is a value-ish type: a
// missing @RequestBody on `String id` is harmless (Spring binds it as a query
// parameter), while on a POJO it silently sends nothing.
func (p param) IsSimpleType() bool {
	t := p.Type
	if i := strings.LastIndexByte(t, '.'); i >= 0 {
		t = t[i+1:]
	}
	switch t {
	case "String", "int", "long", "short", "byte", "char", "float", "double",
		"boolean", "Integer", "Long", "Short", "Byte", "Character", "Float",
		"Double", "Boolean", "BigDecimal", "BigInteger", "Date", "LocalDate",
		"LocalDateTime", "LocalTime", "Instant", "UUID", "Object":
		return true
	}
	return false
}

// method is one method declaration found by scanning.
type method struct {
	Name string
	// Line is the 1-based line where the declaration starts; its annotations
	// are on the lines above it.
	Line int
	// EndLine is the closing brace line, or Line for an abstract/interface
	// method. Used to bound "is this added line inside the method".
	EndLine int
	// Annotations on the declaration, lowercased.
	Annotations []string
	Params      []param
	// Abstract is true when the declaration ends with ';' — an interface
	// method or an abstract method, which is exactly the Feign shape.
	Abstract bool
}

// HasAnnotation is a convenience wrapper for readability at call sites.
func (m method) HasAnnotation(name string) bool { return hasAnnotation(m.Annotations, name) }

// methods scans a file's lines and returns the method declarations it can
// recognise, in source order. Method bodies are skipped wholesale: the rules
// never want a nested block parsed as a declaration, and skipping keeps a
// 3000-line file linear.
//
// Multi-line parameter lists are joined before parsing; multi-line annotations
// are not (they are rare, and the failure mode is a missed candidate).
func methods(lines []string) []method {
	var out []method
	var pending []string // annotations waiting for their declaration

	for i := 0; i < len(lines); i++ {
		s := strings.TrimSpace(stripLiteral(lines[i]))
		if s == "" || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "*") {
			continue
		}
		if strings.HasPrefix(s, "@") {
			pending = append(pending, annotationNames(s)...)
			s = strings.TrimSpace(removeAnnotations(s))
			if s == "" {
				continue
			}
		}
		name, params, ok := parseSignature(s)
		if !ok {
			// A field or statement ends the annotation run: leaving `pending`
			// populated would attach `@Autowired` to the next method.
			if strings.HasSuffix(s, ";") || strings.HasSuffix(s, "}") {
				pending = nil
			}
			continue
		}
		m := method{Name: name, Line: i + 1, Annotations: pending, Params: params}
		pending = nil

		// Join continuation lines until the parentheses balance, so a
		// parameter list split across lines still parses.
		joined := s
		for parensOpen(joined) && i+1 < len(lines) {
			i++
			joined += " " + strings.TrimSpace(stripLiteral(lines[i]))
		}
		if _, p, ok := parseSignature(joined); ok {
			m.Params = p
		}
		if strings.HasSuffix(strings.TrimSpace(joined), ";") {
			m.Abstract = true
			m.EndLine = m.Line
			out = append(out, m)
			continue
		}
		end := matchBrace(lines, i)
		if end < 0 {
			m.EndLine = m.Line
			out = append(out, m)
			continue
		}
		m.EndLine = end + 1
		out = append(out, m)
		i = end // skip the body
	}
	return out
}

// parensOpen reports a net-unclosed '(' count.
func parensOpen(s string) bool {
	return strings.Count(s, "(") > strings.Count(s, ")")
}

// parseSignature recognises `[modifiers] [generics] ReturnType name(params)`.
// It returns the name and the parameters. Control-flow lines are rejected.
func parseSignature(s string) (string, []param, bool) {
	p := strings.IndexByte(s, '(')
	if p <= 0 {
		return "", nil, false
	}
	end := p
	start := end
	for start > 0 && isIdentChar(s[start-1]) {
		start--
	}
	name := s[start:end]
	if name == "" || controlKeywords[strings.ToLower(name)] {
		return "", nil, false
	}
	// A call is not a declaration: `foo(bar);` has no type before the name.
	prefix := strings.TrimSpace(s[:start])
	if prefix == "" || strings.HasPrefix(prefix, ".") || strings.HasPrefix(prefix, "=") ||
		strings.HasSuffix(prefix, ".") {
		return "", nil, false
	}
	closeIdx := matchParenIn(s, p)
	if closeIdx < 0 {
		closeIdx = len(s)
	}
	params := parseParams(s[p+1 : minInt(closeIdx, len(s))])
	return name, params, true
}

// matchParenIn returns the index of the ')' matching the '(' at open, or -1.
func matchParenIn(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// parseParams splits a parameter list on top-level commas and parses each.
func parseParams(s string) []param {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var parts []string
	depth := 0
	last := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '<', '[', '{':
			depth++
		case ')', '>', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[last:i])
				last = i + 1
			}
		}
	}
	parts = append(parts, s[last:])

	out := make([]param, 0, len(parts))
	for _, raw := range parts {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		p := param{Raw: raw, Annotations: annotationNames(raw)}
		rest := strings.TrimSpace(removeAnnotations(raw))
		rest = strings.TrimSpace(strings.ReplaceAll(rest, "final ", ""))
		fields := strings.Fields(rest)
		switch {
		case len(fields) == 0:
		case len(fields) == 1:
			p.Name = fields[0]
		default:
			p.Name = fields[len(fields)-1]
			p.Type = strings.Join(fields[:len(fields)-1], " ")
		}
		out = append(out, p)
	}
	return out
}

// removeAnnotations strips `@Foo` and `@Foo(...)` from s.
func removeAnnotations(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '@' {
			b.WriteByte(s[i])
			continue
		}
		j := i + 1
		for j < len(s) && (isIdentChar(s[j]) || s[j] == '.') {
			j++
		}
		if j < len(s) && s[j] == '(' {
			depth := 0
			for ; j < len(s); j++ {
				if s[j] == '(' {
					depth++
				} else if s[j] == ')' {
					depth--
					if depth == 0 {
						j++
						break
					}
				}
			}
		}
		b.WriteByte(' ')
		i = j - 1
	}
	return b.String()
}

// matchBrace finds the line index of the '}' matching the first '{' at or
// after line i. Returns -1 when the body never closes (truncated file).
func matchBrace(lines []string, i int) int {
	depth := 0
	started := false
	for j := i; j < len(lines); j++ {
		s := stripLiteral(lines[j])
		if !started {
			// The body starts at this line's first `{`. Anything before it is
			// the end of something else, and counting those braces here is
			// what broke `} catch (Exception e) {`: the `}` that closes the
			// try and the `{` that opens the catch cancel out, the matcher
			// returns the header line as its own end, and swallow_exception
			// reads an empty body. The 2026-11-09 labelling run caught 20
			// candidates of which 17 were this one line.
			first := strings.Index(s, "{")
			if first < 0 {
				continue
			}
			s = s[first:]
			started = true
		}
		depth += strings.Count(s, "{") - strings.Count(s, "}")
		if depth <= 0 {
			return j
		}
	}
	return -1
}

// blockKind classifies a block opener so `remote_in_loop` can ask "is this
// line inside a loop" instead of guessing from indentation.
type blockKind int

const (
	blockOther blockKind = iota
	blockLoop
	blockMethod
	blockClass
)

// blockStack returns, per line index, the kinds of the blocks enclosing it
// (outermost first). A line's own braces are not part of its stack, so a
// `for (...) {` line is not itself "inside" the loop.
func blockStack(lines []string) [][]blockKind {
	out := make([][]blockKind, len(lines))
	var stack []blockKind
	for i, raw := range lines {
		out[i] = append([]blockKind(nil), stack...)
		s := stripLiteral(raw)
		kind := classifyOpener(s)
		for k := 0; k < strings.Count(s, "{"); k++ {
			stack = append(stack, kind)
		}
		for k := 0; k < strings.Count(s, "}"); k++ {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return out
}

// InLoop reports whether the line (1-based) sits inside a loop body.
func InLoop(stacks [][]blockKind, line int) bool {
	if line < 1 || line > len(stacks) {
		return false
	}
	for _, k := range stacks[line-1] {
		if k == blockLoop {
			return true
		}
	}
	return false
}

// classifyOpener decides what kind of block a line opens.
func classifyOpener(s string) blockKind {
	t := strings.TrimSpace(s)
	if t == "" {
		return blockOther
	}
	lower := strings.ToLower(t)
	if strings.Contains(lower, ".foreach(") || strings.HasPrefix(lower, "do ") || strings.HasPrefix(lower, "do{") {
		return blockLoop
	}
	for _, kw := range []string{"for", "while"} {
		if idx := strings.Index(lower, kw); idx >= 0 {
			if strings.HasPrefix(strings.TrimSpace(lower[idx+len(kw):]), "(") {
				return blockLoop
			}
		}
	}
	if strings.Contains(lower, " class ") || strings.HasPrefix(lower, "class ") ||
		strings.Contains(lower, " interface ") || strings.HasPrefix(lower, "interface ") ||
		strings.HasPrefix(lower, "enum ") {
		return blockClass
	}
	if _, _, ok := parseSignature(t); ok {
		return blockMethod
	}
	return blockOther
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// loopVarStack returns, per line index, the loop-variable names in scope at
// that line (innermost last). It walks the same brace stack as blockStack;
// the difference is that it remembers the declared variable of each loop
// header it pushes.
//
// A for-each `for (Item it : items)` contributes "it"; a C-style
// `for (int i = 0; ...)` contributes "i". A `while` header contributes
// nothing — which is the point: the 2026-11-09 labelling run found the
// classic batch-pagination shape (`while (true) { feign.fix(buildReq(list,
// batchSize)); if (lastPage) break; }`) reported as N+1. The call passes the
// whole list each round; there is no per-item variable because there is no
// per-item loop. Requiring the call to touch a loop variable separates the
// two shapes with no new mechanism.
func loopVarStack(lines []string) [][]string {
	out := make([][]string, len(lines))
	var stack []string
	for i, raw := range lines {
		out[i] = append([]string(nil), stack...)
		s := stripLiteral(raw)
		kind := classifyOpener(s)
		for k := 0; k < strings.Count(s, "{"); k++ {
			if k == 0 && kind == blockLoop {
				stack = append(stack, loopVar(s))
			} else {
				stack = append(stack, "")
			}
		}
		for k := 0; k < strings.Count(s, "}"); k++ {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return out
}

// loopVar extracts the iteration variable from a loop header, or "" when the
// header has none (while, do). Cheap by construction: the first token of the
// parenthesised part, minus its type prefix.
//
// Three shapes, all from real code in the 2026-11-09 labelling run:
//   - `for (Item it : items)`  -> "it"
//   - `for (int i = 0; ...)`   -> "i"
//   - `items.forEach(item -> {` -> "item" — the lambda parameter. The third
//     one is not decoration: a true N+1 written as a forEach was the only
//     candidate that would have been LOST by the fix, and a rule whose
//     precision improves because it stopped seeing real bugs has just
//     become quieter, not better.
func loopVar(header string) string {
	open := strings.Index(header, "(")
	if open < 0 {
		return ""
	}
	inner := ""
	if close := matchParenIn(header, open); close >= 0 {
		inner = strings.TrimSpace(header[open+1 : close])
	} else {
		// Unclosed on this line: a lambda opener, `list.forEach(item -> {`.
		// The parameter lives inside the parenthesis that does not close
		// until the statement does, so read everything after it.
		inner = strings.TrimSpace(header[open+1:])
	}
	if inner == "" {
		return ""
	}
	if colon := strings.Index(inner, ":"); colon >= 0 && !strings.Contains(inner[:colon], ";") {
		// for-each: "Type name"
		fields := strings.Fields(inner[:colon])
		if len(fields) >= 2 {
			return fields[len(fields)-1]
		}
		return ""
	}
	// C-style: "int i = 0; ..." — the declared name is token 2.
	if idx := strings.Index(inner, ";"); idx >= 0 {
		fields := strings.Fields(inner[:idx])
		if len(fields) >= 2 {
			return fields[1]
		}
	}
	// forEach / stream lambda: the arrow parameter. Either one name or a
	// parenthesised parameter list; the first is what real call sites look
	// like, so the list is handled but never required.
	if arrow := strings.Index(inner, "->"); arrow > 0 {
		lhs := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(inner[:arrow]), "("), ")")
		fields := strings.Fields(lhs)
		if len(fields) == 1 && fields[0] != "" {
			return fields[0]
		}
	}
	return ""
}

// touchesVar reports whether the (literal-stripped) line mentions the
// identifier as a word.
func touchesVar(line, name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(line); i++ {
		if !isIdentChar(line[i]) {
			continue
		}
		j := i
		for j < len(line) && isIdentChar(line[j]) {
			j++
		}
		if line[i:j] == name {
			return true
		}
		i = j
	}
	return false
}

// statementWindow expands a 1-based line to the call statement it belongs to,
// returning the 0-based index range [from, to] and the joined,
// literal-stripped text.
//
// The reason this exists is that the builder pattern splits one call across
// six lines, and the loop variable lives on a continuation line, never on the
// line that names the callee:
//
//	boolean ok = cstModifyFeign.judge(CstModifyJudgeReq.builder()   // <- callee
//	        .reqParam(CstVerifyUtil.buildTriggerVerifyBO(item))     // <- loop var
//	        .build());
//
// A rule that only reads the callee's line cannot see `item`, so it must
// either report every call in a loop (the batch-pagination false positive) or
// lose this one. Reading the statement is the only shape that keeps both
// correct; losing a true positive to buy precision would just be a quieter
// tool, not a better one.
//
// The scan is paren-based, and that is the content of rule_rev 2026-11-09.
// The first version walked forward to the next `;` at brace depth 0, so a
// multi-line `if (...) {` header swallowed its whole body and reported the
// block as one statement — five remote_in_loop candidates from a file that
// had one. Walking backward only through lines that leave a parenthesis open,
// and forward until the parens balance **and** the line terminates, keeps an
// `if` header inside its own window so the body is never read as a
// continuation.
func statementWindow(lines []string, line int) (int, int, string) {
	const cap = 12
	if line < 1 || line > len(lines) {
		return 0, 0, ""
	}
	i := line - 1

	// Backward: the candidate start is the highest k such that lines
	// [k..i-1] leave open more parentheses than they close. Each open
	// paren there must be closed at or after line i, so those lines are
	// continuations of the statement i belongs to.
	from := i
	for k := i - 1; k >= 0 && i-k <= cap; k-- {
		net := parenNetRange(lines, k, i-1)
		if net <= 0 {
			break
		}
		pt := strings.TrimSpace(stripLiteral(lines[k]))
		if pt == "" || strings.HasPrefix(pt, "//") || strings.HasPrefix(pt, "*") {
			break
		}
		from = k
	}

	// Forward: from the statement's start, the statement ends at the first
	// line that brings the accumulated parens back to zero AND terminates
	// with `;` (or a block boundary). depth > 0 means a closer is still
	// owed, so the body of an `if (...) {` header is never swallowed: the
	// header itself is the terminating line there.
	to := from
	depth := 0
	for k := from; k < len(lines) && k-from <= cap; k++ {
		s := stripLiteral(lines[k])
		depth += parenNet(s)
		t := strings.TrimSpace(s)
		to = k
		if depth <= 0 && (strings.HasSuffix(t, ";") || strings.HasSuffix(t, "{") ||
			strings.HasSuffix(t, "}") || t == "") {
			break
		}
	}

	parts := make([]string, 0, to-from+1)
	for k := from; k <= to; k++ {
		if t := strings.TrimSpace(stripLiteral(lines[k])); t != "" {
			parts = append(parts, t)
		}
	}
	return from, to, strings.Join(parts, " ")
}

// parenNetRange sums the parenthesis balance of lines [from, to] inclusive.
func parenNetRange(lines []string, from, to int) int {
	net := 0
	for k := from; k <= to && k < len(lines); k++ {
		net += parenNet(stripLiteral(lines[k]))
	}
	return net
}

// parenNet is the parenthesis balance of a line whose literals are already
// stripped.
func parenNet(s string) int {
	return strings.Count(s, "(") - strings.Count(s, ")")
}
