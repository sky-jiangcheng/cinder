package aiaudit

import (
	"path/filepath"
	"strings"
	"testing"

	"repotory/internal/db"
)

// Each preset must catch its real shape. The samples are synthetic but
// structurally exact — length and alphabet are what the patterns key on.
func TestPresetsMatchTheirTargets(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"private-key", "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA1234\n-----END RSA PRIVATE KEY-----"},
		{"private-key-header", "-----BEGIN OPENSSH PRIVATE KEY----- (truncated)"},
		{"aws-access-key-id", "key id AKIAIOSFODNN7EXAMPLE in the env"},
		{"github-token", "ghp_" + strings.Repeat("a1B2", 10)},
		{"github-token", "github_pat_" + strings.Repeat("a1B2c3D4e5", 6)},
		{"gitlab-token", "glpat-" + strings.Repeat("x9Y8", 6)},
		{"slack-token", "xoxb-123456789012-abcdefABCDEF"},
		{"anthropic-api-key", "sk-ant-" + strings.Repeat("aB3d", 12)},
		{"stripe-secret-key", "sk_live_" + strings.Repeat("a1b2c3", 5)},
		{"sk-api-key", "sk-" + strings.Repeat("a1b2c3d4", 6)},
		{"google-api-key", "AIza" + strings.Repeat("a1B2", 9)[:35]},
		{"npm-token", "npm_" + strings.Repeat("a1b2", 10)},
		{"huggingface-token", "hf_" + strings.Repeat("a1b2c3", 6)},
		{"sendgrid-api-key", "SG." + strings.Repeat("a1", 12) + "." + strings.Repeat("b2", 12)},
		{"twilio-api-key", "SK" + strings.Repeat("a1b2c3d4", 4)},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn9.dozjgNryP4J3jVmNHl0w5N"},
		{"db-connection-string", "postgres://admin:s3cr3tpass@db.internal:5432/prod"},
		{"db-connection-string", "mongodb+srv://user:hunter2password@cluster0.example.net/db"},
		{"secret-assignment", `password = "SuperSecret123"`},
		{"secret-assignment", "api_key: abcd1234efgh5678"},
		{"cn-id-number", "身份证号 11010519491231002X 请保密"},
		{"cn-mobile-number", "联系电话 13812345678"},
		{"bank-card-number", "卡号 4111111111111111 过期"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hit string
			for _, p := range Presets {
				if p.Re.MatchString(tc.text) {
					hit = p.Name
					break
				}
			}
			if hit == "" {
				t.Errorf("no preset matched %q", tc.text)
			}
		})
	}
}

// The library is precision-first: ordinary development prose, git SHAs,
// UUIDs, timestamps and version strings must pass through untouched. A
// preset that eats normal handoff content is worse than no preset — it
// destroys the memory it exists to protect.
func TestPresetsLeaveOrdinaryContentAlone(t *testing.T) {
	ordinary := []string{
		"fixed the login bug in commit 3f2a1b9c8d7e6f5a4b3c2d1e0f9a8b7c6d5e4f3a",
		"session id 550e8400-e29b-41d4-a716-446655440000 expired",
		"build finished at 1700000000000 (epoch ms), version v2.41.1",
		"the password field validates length before hashing",
		"see docs/adr/0030-ai-call-audit.md for the decision",
		"order 20261123000123 shipped; invoice 99887766554433221100",
		"Gotcha: GOTOOLCHAIN=local is required because GOSUMDB=off",
		"email the maintainer at dev@example.com about the release",
	}
	for _, text := range ordinary {
		if out, n := Redact(text); n != 0 {
			t.Errorf("ordinary content was redacted (%d spans): %q -> %q", n, text, out)
		}
	}
}

// Redact replaces the span with a named marker, keeps everything around it,
// and is idempotent — the marker itself must match no preset.
func TestRedactMasksSpansAndIsIdempotent(t *testing.T) {
	in := "token ghp_" + strings.Repeat("a1B2", 10) + " and password = \"SuperSecret123\" end"
	out, n := Redact(in)
	if n != 2 {
		t.Fatalf("masked %d spans, want 2: %q", n, out)
	}
	if !strings.Contains(out, "[redacted:github-token]") || !strings.Contains(out, "[redacted:secret-assignment]") {
		t.Errorf("markers missing: %q", out)
	}
	if strings.Contains(out, "ghp_") || strings.Contains(out, "SuperSecret123") {
		t.Errorf("secret survived redaction: %q", out)
	}
	if !strings.HasPrefix(out, "token ") || !strings.HasSuffix(out, " end") {
		t.Errorf("surrounding content was damaged: %q", out)
	}
	again, n2 := Redact(out)
	if n2 != 0 || again != out {
		t.Errorf("second pass changed the text (%d spans): %q", n2, again)
	}
}

// Presets ride MatchRule, which lowercases the text before testing. Every
// pattern must still fire on the lowercase form, or the exit-side review
// would silently miss everything the entry side catches.
func TestPresetsMatchThroughLowercasingMatchRule(t *testing.T) {
	samples := []string{
		"AKIAIOSFODNN7EXAMPLE",
		"ghp_" + strings.Repeat("A1B2", 10),
		"-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn9.dozjgNryP4J3jVmNHl0w5N",
		"postgres://admin:S3CR3TPASS@db.internal:5432/prod",
		`PASSWORD = "SuperSecret123"`,
		"11010519491231002X",
	}
	rules := PresetRules()
	for _, s := range samples {
		if MatchRule(s, rules) == "" {
			t.Errorf("MatchRule missed %q after lowercasing", s)
		}
	}
}

// The entry-side gate is default-off: an unset config stores the exact
// bytes, and only memory_redact=1 turns masking on.
func TestRedactIfEnabledDefaultOff(t *testing.T) {
	database, err := db.InitDB(filepath.Join(t.TempDir(), "presets.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	secret := "aws key AKIAIOSFODNN7EXAMPLE"
	if out, n := RedactIfEnabled(database, secret); n != 0 || out != secret {
		t.Errorf("default state redacted content: %q (%d)", out, n)
	}
	if err := db.SetConfig(database, "memory_redact", "1"); err != nil {
		t.Fatal(err)
	}
	out, n := RedactIfEnabled(database, secret)
	if n != 1 || !strings.Contains(out, "[redacted:aws-access-key-id]") {
		t.Errorf("enabled state did not redact: %q (%d)", out, n)
	}
}

// Resolve appends the presets to the user's rules — never replaces them —
// and only when a review gate is open. With everything off the policy is
// exactly what it was before ADR-0068.
func TestResolveAppendsPresetsToUserRules(t *testing.T) {
	database, err := db.InitDB(filepath.Join(t.TempDir(), "resolve.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	r := New(database)

	if cfg := r.Resolve(); cfg.PresetsEnabled || len(cfg.Rules) != 0 {
		t.Errorf("default policy not empty: %+v", cfg)
	}

	for k, v := range map[string]string{
		"ai_review_input":   "1",
		"ai_review_rules":   "内网专用代号",
		"ai_review_presets": "1",
	} {
		if err := db.SetConfig(database, k, v); err != nil {
			t.Fatal(err)
		}
	}
	cfg := r.Resolve()
	if !cfg.PresetsEnabled {
		t.Fatal("PresetsEnabled not resolved")
	}
	if len(cfg.Rules) != 1+len(Presets) {
		t.Errorf("rules = %d, want user 1 + presets %d", len(cfg.Rules), len(Presets))
	}
	if MatchRule("AKIAIOSFODNN7EXAMPLE", cfg.Rules) == "" {
		t.Error("preset did not fire through resolved rules")
	}
	if MatchRule("内网专用代号 leaked", cfg.Rules) == "" {
		t.Error("user rule lost after presets were appended")
	}
}
