// Package aiaudit is the AI call audit and content review plugin (ADR-0030):
// the switches, the user's rule set, the review hooks and the audit rows.
//
// It owns exactly two pieces of state - the declared surface and the optional
// forwarder - plus the store handle. Everything else is a pure function over
// config, which is what lets the policy be resolved once per call and passed
// around as a value.
package aiaudit

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"regexp"
	"strings"

	"repotory/internal/aiclient"
	"repotory/internal/db"
)

// Recorder holds the per-process audit state: the surface this process serves,
// the optional single-writer forwarder, and the store.
type Recorder struct {
	db        *sql.DB
	surface   string
	forwarder func(db.AICallLog) error
}

// New returns a recorder for db. Defaults: surface unknown, no forwarder, and
// every gate off (the config rows decide).
func New(db *sql.DB) *Recorder {
	return &Recorder{db: db}
}

// Policy is the resolved audit/review policy for one call.
type Policy struct {
	Enabled        bool
	ReviewInput    bool
	ReviewOutput   bool
	PresetsEnabled bool
	Rules          []*regexp.Regexp
}

// AI call audit and content review (ADR-0030).
//
// This file is the "security plugin" half of the ADR-0030 decision: the
// memory layer stays the protagonist, and this is a switchable extra that
// sits between knowledge and model. Everything here is OFF by default — with
// the gates closed, behaviour is byte-for-byte what it was before, and an
// existing user notices nothing.
//
// The shape of the design follows from one fact: the LLM exit converges.
// Every outbound model call in the codebase goes through one of three HTTP
// primitives in ai.go, and all eight business paths funnel into them. So the
// review hook belongs at that convergence point, not scattered across the
// eight callers.
//
// Why the review lives here and not inside the HTTP primitives: aiclient.PostJSON
// and friends are package-level functions with no access to the database, so
// they cannot read the policy or write the audit row. The Service wrappers
// immediately above them (chatWithAudit / streamChatWithAudit) can, and
// every business path routes through one of those two.
//
// Four gates, all default-off:
//   - ai_audit_enabled     → write an audit row per model call
//   - ai_review_input      → screen the prompt before it leaves
//   - ai_review_output     → screen the reply as it arrives
//   - ai_review_rules      → the patterns to look for
//
// The fourth deserves a note: the rules are the user's, not the product's.
// A built-in KEYWORD blocklist would be a false promise — keyword matching
// is trivially bypassed and blocks legitimate work at the same time. What
// Cinder provides is the MECHANISM; what it screens for is the user's
// decision, expressed as regexes they write for their own organisation.
//
// ADR-0068 carves out one exception: STRUCTURED credentials (API key shapes,
// private key blocks, connection strings with passwords, ID/card numbers)
// are not keywords — their format is the signal, so a shipped preset library
// can be precise where a keyword list cannot. It stays opt-in
// (ai_review_presets), appended to the user's rules, never replacing them.

// Review verdicts stored in ai_call_log. An empty verdict means the
// corresponding review was disabled for that call.
const (
	// VerdictAllow means the content passed the review.
	VerdictAllow = "allow"
	// VerdictBlocked means a rule matched and the content was withheld.
	VerdictBlocked = "blocked"
	// VerdictFlagged means a rule matched but the content was still sent.
	// Reserved for the output side, where the chunk has already been
	// delivered to the client and cannot be taken back.
	VerdictFlagged = "flagged"
)

// Call surfaces. The surface answers "who called", which is the question an
// audit is asked most often.
//
// It is declared once per PROCESS rather than per call, because each binary
// IS one surface: cmd/mcp is the MCP server, cmd/server is the HTTP server,
// the desktop app is the desktop app. There is no moment at which one
// process serves two of them, so a per-call argument would be noise that
// every caller has to get right. A surface that was not declared reads as
// "unknown" rather than a guess.
const (
	// SurfaceUnknown is the default when a process did not declare itself.
	SurfaceUnknown = "unknown"
	// SurfaceMCP is the MCP server (cmd/mcp).
	SurfaceMCP = "mcp"
	// SurfaceHTTP is the headless server's HTTP / JSON-RPC surface.
	SurfaceHTTP = "http"
	// SurfaceDesktop is the Wails desktop app.
	SurfaceDesktop = "desktop"
)

// SetSurface declares which surface this process serves. It is called
// once at startup by each entry point and never changed afterwards, so the
// value is read without synchronisation.
func (r *Recorder) SetSurface(surface string) {
	if surface == "" {
		surface = SurfaceUnknown
	}
	r.surface = surface
}

// Surface reports the declared surface, defaulting to unknown.
func (r *Recorder) Surface() string {
	if r.surface == "" {
		return SurfaceUnknown
	}
	return r.surface
}

// Policy is the resolved audit/review policy for one call.

// Enabled reports whether audit rows should be written.
func (r *Recorder) Enabled() bool {
	v, err := db.GetConfig(r.db, "ai_audit_enabled")
	return err == nil && v == "1"
}

// ReviewInputEnabled reports whether prompts are screened.
func (r *Recorder) ReviewInputEnabled() bool {
	v, err := db.GetConfig(r.db, "ai_review_input")
	return err == nil && v == "1"
}

// ReviewOutputEnabled reports whether replies are screened.
func (r *Recorder) ReviewOutputEnabled() bool {
	v, err := db.GetConfig(r.db, "ai_review_output")
	return err == nil && v == "1"
}

// PresetsEnabled reports whether the built-in credential/PII preset library
// (ADR-0068) is appended to the user's rules. Default OFF, same as every
// other gate here.
func (r *Recorder) PresetsEnabled() bool {
	v, err := db.GetConfig(r.db, "ai_review_presets")
	return err == nil && v == "1"
}

// Rules parses the user's rule set.
//
// The rules live in one config key, one regex per line, because that is what
// a person editing a text field can actually maintain. Blank lines and lines
// starting with # are skipped. A line that does not compile as a regex is
// skipped too, with a log line naming it — a broken rule must not take the
// whole review down, or the user would have no way to tell "nothing matched"
// from "the gate is broken".
//
// The regexes are compiled per call rather than cached. That is a deliberate
// trade: rules change rarely and calls are infrequent, so the cost is
// negligible, and NOT caching removes any chance of a stale rule set being
// used after the user edited it.
func (r *Recorder) Rules() []*regexp.Regexp {
	raw, err := db.GetConfig(r.db, "ai_review_rules")
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil
	}
	var rules []*regexp.Regexp
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		re, cerr := regexp.Compile(line)
		if cerr != nil {
			log.Printf("ai audit: skipping invalid review rule %q: %v", line, cerr)
			continue
		}
		rules = append(rules, re)
	}
	return rules
}

// Resolve assembles the policy for one call.
func (r *Recorder) Resolve() Policy {
	cfg := Policy{
		Enabled:        r.Enabled(),
		ReviewInput:    r.ReviewInputEnabled(),
		ReviewOutput:   r.ReviewOutputEnabled(),
		PresetsEnabled: r.PresetsEnabled(),
	}
	if cfg.ReviewInput || cfg.ReviewOutput {
		cfg.Rules = r.Rules()
		if cfg.PresetsEnabled {
			cfg.Rules = append(cfg.Rules, PresetRules()...)
		}
	}
	return cfg
}

// PromptText flattens the chat messages into the text that would actually
// be sent. It is what the input rules are matched against and what the audit
// row's hash covers.
func PromptText(messages []aiclient.Message) string {
	var b strings.Builder
	for _, m := range messages {
		if m.Role != "" {
			b.WriteString(m.Role)
			b.WriteString(": ")
		}
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

// PromptHash is the SHA-256 of the prompt, hex encoded.
//
// The audit row stores THIS rather than the prompt text: it answers "was this
// the same prompt as last time" without answering "what was in it". Storing
// the text would put API keys and customer data into the same SQLite file the
// notes live in, which is exactly what ADR-0030's "locally private" promise
// forbids. An empty prompt yields an empty hash rather than the hash of the
// empty string, so "no prompt" and "an empty prompt" stay distinguishable.
func PromptHash(text string) string {
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// MatchRule returns the first rule that matches the text, or "".
//
// The match is case-insensitive: the rules are written by people thinking in
// terms of content ("客户身份证", "password"), not in terms of regex flags.
// A rule that wants case sensitivity can still use an inline flag.
func MatchRule(text string, rules []*regexp.Regexp) string {
	if text == "" || len(rules) == 0 {
		return ""
	}
	lower := strings.ToLower(text)
	for _, re := range rules {
		if re.MatchString(lower) {
			return re.String()
		}
	}
	return ""
}

// SetForwarder routes every audit row this process would write through
// fn instead of straight into the table. The agent processes (cmd/mcp,
// cmd/repotory-capture) install the single-writer channel (ADR-0058) as the
// forwarder: an audit INSERT is a write like any other, and a non-writer
// inserting directly would hit exactly the lock contention the contract removes
// — or, if it failed silently as today, leave the trail missing precisely the
// agent calls it exists to cover. The row's Surface is whatever the emitting
// process declared, so a row executed by the desktop writer still reads `mcp`.
//
// Declared once per process at startup, same rule as SetSurface.
func (r *Recorder) SetForwarder(fn func(db.AICallLog) error) {
	r.forwarder = fn
}

// RecordRow writes one audit row directly. Exported for the writer
// catalogue (writerapi), which reaches it only in the process that holds the
// write role. The append-only guard (table missing = auditing off) applies here
// exactly as in RecordCall.
func (r *Recorder) RecordRow(entry db.AICallLog) error {
	if !db.AICallLogTableExists(r.db) {
		return nil
	}
	return db.InsertAICallLog(r.db, entry)
}

// RecordCall writes one audit row.
//
// An audit failure is logged and swallowed, never propagated: losing the
// record is bad, but failing the user's model call because the audit table
// misbehaved is worse — the call itself is the thing the user asked for. The
// table is append-only with triggers, so a write here either lands or is
// rejected outright; there is no partial state to clean up.
func (r *Recorder) RecordCall(entry db.AICallLog) {
	if r.forwarder != nil {
		// The forwarder's own failure is also swallowed by the same rule: no
		// audit path, direct or forwarded, may fail the model call.
		if err := r.forwarder(entry); err != nil {
			log.Printf("ai audit: could not forward call record: %v", err)
		}
		return
	}
	if !db.AICallLogTableExists(r.db) {
		return
	}
	if err := db.InsertAICallLog(r.db, entry); err != nil {
		log.Printf("ai audit: could not record call: %v", err)
	}
}

// Entry assembles the audit row for one call.
func Entry(surface, endpoint, model, promptText, inputVerdict, outputVerdict string) db.AICallLog {
	return db.AICallLog{
		Surface:       surface,
		Endpoint:      endpoint,
		Model:         model,
		PromptHash:    PromptHash(promptText),
		PromptChars:   len([]rune(promptText)),
		InputVerdict:  inputVerdict,
		OutputVerdict: outputVerdict,
	}
}

// aiBlockedError is the error returned when the input review withholds a
// prompt. It names the rule that matched so the user can see WHY the call
// did not happen — a silent block would be indistinguishable from a broken
// endpoint.
type BlockedError struct {
	Rule string
}

func (e BlockedError) Error() string {
	return fmt.Sprintf("AI 调用被本地审查规则拦截（命中规则：%s）", e.Rule)
}

// ReviewInput runs the input review and returns the verdict.
//
// When the review is off, or no rules are configured, the verdict is empty
// (meaning "not screened") rather than "allow" — the audit row distinguishes
// "screened and clean" from "not screened at all", and collapsing them would
// make the record claim a check happened when it did not.
func (r *Recorder) ReviewInput(cfg Policy, promptText string) (verdict string, blockedRule string) {
	if !cfg.ReviewInput || len(cfg.Rules) == 0 {
		return "", ""
	}
	if rule := MatchRule(promptText, cfg.Rules); rule != "" {
		return VerdictBlocked, rule
	}
	return VerdictAllow, ""
}
