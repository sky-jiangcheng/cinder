package wikilint

import "testing"

// The reply parser moved here from internal/service/wiki_lint_test.go with the
// code it pins (ADR-0060 step four): it needs no database and no service, so it
// belongs where the parser does. The table is unchanged.
func TestParseLintReply(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		ok    bool
		count int
	}{
		{"plain array", `[{"type":"stale","page_id":3,"detail":"x"}]`, true, 1},
		{"fenced", "```json\n[{\"type\":\"stale\",\"page_id\":3}]\n```", true, 1},
		{"prose before", "我看了这些页面：[{\"type\":\"contradiction\",\"page_id\":1}]", true, 1},
		{"empty array", `[]`, true, 0},
		{"prose only", "抱歉，没有发现冲突。", false, 0},
		{"broken json", `[{"type":`, false, 0},
	}
	for _, tc := range cases {
		items, ok := parseLintReply(tc.raw)
		if ok != tc.ok {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.ok)
			continue
		}
		if ok && len(items) != tc.count {
			t.Errorf("%s: %d items, want %d", tc.name, len(items), tc.count)
		}
	}
}
