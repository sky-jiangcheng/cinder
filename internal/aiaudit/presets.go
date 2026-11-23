package aiaudit

import (
	"database/sql"
	"regexp"

	"repotory/internal/db"
)

// Built-in secrets/PII presets (ADR-0068, absorbing Hindsight's Memory
// Defense in the ADR-0030 shape).
//
// ADR-0030 shipped no built-in blocklist, and that judgment stands for
// KEYWORDS: a word list is a weak control that blocks legitimate work.
// Structured credentials are a different class — an AWS key id or a private
// key block has a fixed shape, so a pattern for it is high-precision, and
// "your transcript just wrote a live token into the memory layer" is a real
// leak path the auto-capture feature opened. These presets are that narrow
// class, and they stay OPT-IN exactly like every other audit gate:
//
//   - ai_review_presets=1 appends them to the exit-side review rules;
//   - memory_redact=1 masks matches on the entry side, before a note /
//     handoff / imported document is stored.
//
// Every pattern is compiled (?i) and written to match BOTH the original text
// (Redact replaces real spans) and its lowercase form (MatchRule lowercases
// before testing user rules, and presets ride the same path).

// Preset is one named built-in pattern. The name is what a redaction marker
// shows, so it must read as a diagnosis, not a regex.
type Preset struct {
	Name string
	Re   *regexp.Regexp
}

// Presets is the factory library, in application order. More specific shapes
// lead their generic cousins (the full private-key block before its bare
// header, the 18-digit national id before the bank-card pattern) so the
// marker names the most precise diagnosis.
var Presets = []Preset{
	{"private-key", regexp.MustCompile(`(?i)-----BEGIN [a-z0-9 ]*PRIVATE KEY-----[\s\S]*?-----END [a-z0-9 ]*PRIVATE KEY-----`)},
	{"private-key-header", regexp.MustCompile(`(?i)-----BEGIN [a-z0-9 ]*PRIVATE KEY-----`)},
	{"aws-access-key-id", regexp.MustCompile(`(?i)\bAKIA[0-9A-Z]{16}\b`)},
	{"github-token", regexp.MustCompile(`(?i)\b(?:gh[pousr]_[0-9a-z]{36,}|github_pat_[0-9a-z_]{50,})\b`)},
	{"gitlab-token", regexp.MustCompile(`(?i)\bglpat-[0-9a-z_-]{20,}\b`)},
	{"slack-token", regexp.MustCompile(`(?i)\bxox[baprs]-[0-9a-z-]{10,}\b`)},
	{"anthropic-api-key", regexp.MustCompile(`(?i)\bsk-ant-[0-9a-z_-]{20,}\b`)},
	{"stripe-secret-key", regexp.MustCompile(`(?i)\b[sr]k_live_[0-9a-z]{20,}\b`)},
	{"sk-api-key", regexp.MustCompile(`(?i)\bsk-[0-9a-z_-]{20,}\b`)},
	{"google-api-key", regexp.MustCompile(`(?i)\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"npm-token", regexp.MustCompile(`(?i)\bnpm_[0-9a-z]{36,}\b`)},
	{"huggingface-token", regexp.MustCompile(`(?i)\bhf_[0-9a-z]{30,}\b`)},
	{"sendgrid-api-key", regexp.MustCompile(`(?i)\bSG\.[0-9a-z_-]{20,}\.[0-9a-z_-]{20,}\b`)},
	{"twilio-api-key", regexp.MustCompile(`(?i)\bSK[0-9a-f]{32}\b`)},
	{"jwt", regexp.MustCompile(`(?i)\beyj[0-9a-z_-]{8,}\.[0-9a-z_-]{8,}\.[0-9a-z_-]{4,}`)},
	{"db-connection-string", regexp.MustCompile(`(?i)\b(?:postgres(?:ql)?|mysql|mongodb(?:\+srv)?|rediss?|amqps?)://[^\s:@/]+:[^\s@]+@[^\s]+`)},
	{"secret-assignment", regexp.MustCompile(`(?i)\b(?:password|passwd|pwd|client_secret|secret|access_token|token|api[_-]?key|apikey|access[_-]?key|secret[_-]?key|private[_-]?key)\s*[:=]\s*["']?[0-9a-z/+_.\-]{8,}["']?`)},
	{"cn-id-number", regexp.MustCompile(`(?i)\b\d{17}[\dx]\b`)},
	{"cn-mobile-number", regexp.MustCompile(`(?i)\b1[3-9]\d{9}\b`)},
	{"bank-card-number", regexp.MustCompile(`(?i)\b(?:4\d{12}(?:\d{3})?|5[1-5]\d{14}|3[47]\d{13}|6(?:011|5\d{2})\d{12})\b`)},
}

// Deliberately absent: email addresses and person names. A development
// memory layer is full of legitimate emails (git authors, issue references),
// and masking them would destroy the attribution the handoff exists to
// carry. Precision first; a user who wants them can still write their own
// ai_review_rules regex.

// PresetRules returns the library as plain regexes, for appending to the
// exit-side review rule set.
func PresetRules() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(Presets))
	for i, p := range Presets {
		out[i] = p.Re
	}
	return out
}

// Redact masks every preset match with a named marker and reports how many
// spans were masked. The marker is the user-visible signal that something
// was removed — a silent deletion would be indistinguishable from a model
// that never wrote the secret. Markers match no preset, so a second pass is
// a no-op (idempotent).
func Redact(text string) (string, int) {
	count := 0
	for _, p := range Presets {
		text = p.Re.ReplaceAllStringFunc(text, func(string) string {
			count++
			return "[redacted:" + p.Name + "]"
		})
	}
	return text, count
}

// RedactIfEnabled is the entry-side gate shared by every storage writer
// (service note creation and the plugin import runtime): it reads
// memory_redact and masks only when the user opted in. Default-off means
// the stored bytes are exactly what they were before ADR-0068.
func RedactIfEnabled(database *sql.DB, text string) (string, int) {
	v, err := db.GetConfig(database, "memory_redact")
	if err != nil || v != "1" {
		return text, 0
	}
	return Redact(text)
}
