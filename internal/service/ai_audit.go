package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"regexp"
	"strings"

	"repotory/internal/db"
)

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
// Why the review lives here and not inside the HTTP primitives: aiPostJSON
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
// Shipping a built-in blocklist would be a false promise — keyword matching
// is trivially bypassed and blocks legitimate work at the same time. What
// Cinder provides is the MECHANISM; what it screens for is the user's
// decision, expressed as regexes they write for their own organisation.

// Review verdicts stored in ai_call_log. An empty verdict means the
// corresponding review was disabled for that call.
const (
	// AIReviewAllow means the content passed the review.
	AIReviewAllow = "allow"
	// AIReviewBlocked means a rule matched and the content was withheld.
	AIReviewBlocked = "blocked"
	// AIReviewFlagged means a rule matched but the content was still sent.
	// Reserved for the output side, where the chunk has already been
	// delivered to the client and cannot be taken back.
	AIReviewFlagged = "flagged"
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
	// AISurfaceUnknown is the default when a process did not declare itself.
	AISurfaceUnknown = "unknown"
	// AISurfaceMCP is the MCP server (cmd/mcp).
	AISurfaceMCP = "mcp"
	// AISurfaceHTTP is the headless server's HTTP / JSON-RPC surface.
	AISurfaceHTTP = "http"
	// AISurfaceDesktop is the Wails desktop app.
	AISurfaceDesktop = "desktop"
)

// SetAuditSurface declares which surface this process serves. It is called
// once at startup by each entry point and never changed afterwards, so the
// value is read without synchronisation.
func (s *Service) SetAuditSurface(surface string) {
	if surface == "" {
		surface = AISurfaceUnknown
	}
	s.auditSurface = surface
}

// auditSurfaceOf reports the declared surface, defaulting to unknown.
func (s *Service) auditSurfaceOf() string {
	if s.auditSurface == "" {
		return AISurfaceUnknown
	}
	return s.auditSurface
}

// aiAuditConfig is the resolved audit/review policy for one call.
type aiAuditConfig struct {
	auditEnabled bool
	reviewInput  bool
	reviewOutput bool
	rules        []*regexp.Regexp
}

// aiAuditEnabled reports whether audit rows should be written.
func (s *Service) aiAuditEnabled() bool {
	v, err := db.GetConfig(s.db, "ai_audit_enabled")
	return err == nil && v == "1"
}

// aiReviewInputEnabled reports whether prompts are screened.
func (s *Service) aiReviewInputEnabled() bool {
	v, err := db.GetConfig(s.db, "ai_review_input")
	return err == nil && v == "1"
}

// aiReviewOutputEnabled reports whether replies are screened.
func (s *Service) aiReviewOutputEnabled() bool {
	v, err := db.GetConfig(s.db, "ai_review_output")
	return err == nil && v == "1"
}

// aiReviewRules parses the user's rule set.
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
func (s *Service) aiReviewRules() []*regexp.Regexp {
	raw, err := db.GetConfig(s.db, "ai_review_rules")
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

// resolveAuditConfig assembles the policy for one call.
func (s *Service) resolveAuditConfig() aiAuditConfig {
	cfg := aiAuditConfig{
		auditEnabled: s.aiAuditEnabled(),
		reviewInput:  s.aiReviewInputEnabled(),
		reviewOutput: s.aiReviewOutputEnabled(),
	}
	if cfg.reviewInput || cfg.reviewOutput {
		cfg.rules = s.aiReviewRules()
	}
	return cfg
}

// aiPromptText flattens the chat messages into the text that would actually
// be sent. It is what the input rules are matched against and what the audit
// row's hash covers.
func aiPromptText(messages []chatMessage) string {
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

// aiPromptHash is the SHA-256 of the prompt, hex encoded.
//
// The audit row stores THIS rather than the prompt text: it answers "was this
// the same prompt as last time" without answering "what was in it". Storing
// the text would put API keys and customer data into the same SQLite file the
// notes live in, which is exactly what ADR-0030's "locally private" promise
// forbids. An empty prompt yields an empty hash rather than the hash of the
// empty string, so "no prompt" and "an empty prompt" stay distinguishable.
func aiPromptHash(text string) string {
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// aiMatchRule returns the first rule that matches the text, or "".
//
// The match is case-insensitive: the rules are written by people thinking in
// terms of content ("客户身份证", "password"), not in terms of regex flags.
// A rule that wants case sensitivity can still use an inline flag.
func aiMatchRule(text string, rules []*regexp.Regexp) string {
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

// SetAuditForwarder routes every audit row this process would write through
// fn instead of straight into the table. The agent processes (cmd/mcp,
// cmd/repotory-capture) install the single-writer channel (ADR-0058) as the
// forwarder: an audit INSERT is a write like any other, and a non-writer
// inserting directly would hit exactly the lock contention the contract removes
// — or, if it failed silently as today, leave the trail missing precisely the
// agent calls it exists to cover. The row's Surface is whatever the emitting
// process declared, so a row executed by the desktop writer still reads `mcp`.
//
// Declared once per process at startup, same rule as SetAuditSurface.
func (s *Service) SetAuditForwarder(fn func(db.AICallLog) error) {
	s.auditForwarder = fn
}

// RecordAuditRow writes one audit row directly. Exported for the writer
// catalogue (writerapi), which reaches it only in the process that holds the
// write role. The append-only guard (table missing = auditing off) applies here
// exactly as in recordAICall.
func (s *Service) RecordAuditRow(entry db.AICallLog) error {
	if !db.AICallLogTableExists(s.db) {
		return nil
	}
	return db.InsertAICallLog(s.db, entry)
}

// recordAICall writes one audit row.
//
// An audit failure is logged and swallowed, never propagated: losing the
// record is bad, but failing the user's model call because the audit table
// misbehaved is worse — the call itself is the thing the user asked for. The
// table is append-only with triggers, so a write here either lands or is
// rejected outright; there is no partial state to clean up.
func (s *Service) recordAICall(entry db.AICallLog) {
	if s.auditForwarder != nil {
		// The forwarder's own failure is also swallowed by the same rule: no
		// audit path, direct or forwarded, may fail the model call.
		if err := s.auditForwarder(entry); err != nil {
			log.Printf("ai audit: could not forward call record: %v", err)
		}
		return
	}
	if !db.AICallLogTableExists(s.db) {
		return
	}
	if err := db.InsertAICallLog(s.db, entry); err != nil {
		log.Printf("ai audit: could not record call: %v", err)
	}
}

// aiAuditEntry assembles the audit row for one call.
func aiAuditEntry(surface, endpoint, model, promptText, inputVerdict, outputVerdict string) db.AICallLog {
	return db.AICallLog{
		Surface:       surface,
		Endpoint:      endpoint,
		Model:         model,
		PromptHash:    aiPromptHash(promptText),
		PromptChars:   len([]rune(promptText)),
		InputVerdict:  inputVerdict,
		OutputVerdict: outputVerdict,
	}
}

// aiBlockedError is the error returned when the input review withholds a
// prompt. It names the rule that matched so the user can see WHY the call
// did not happen — a silent block would be indistinguishable from a broken
// endpoint.
type aiBlockedError struct {
	rule string
}

func (e aiBlockedError) Error() string {
	return fmt.Sprintf("AI 调用被本地审查规则拦截（命中规则：%s）", e.rule)
}

// aiReviewInputText runs the input review and returns the verdict.
//
// When the review is off, or no rules are configured, the verdict is empty
// (meaning "not screened") rather than "allow" — the audit row distinguishes
// "screened and clean" from "not screened at all", and collapsing them would
// make the record claim a check happened when it did not.
func (s *Service) aiReviewInputText(cfg aiAuditConfig, promptText string) (verdict string, blockedRule string) {
	if !cfg.reviewInput || len(cfg.rules) == 0 {
		return "", ""
	}
	if rule := aiMatchRule(promptText, cfg.rules); rule != "" {
		return AIReviewBlocked, rule
	}
	return AIReviewAllow, ""
}
