package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// AI call audit log (ADR-0030, schema v28).
//
// Cinder is the local-first memory layer for AI coding agents, and the LLM
// exit is unusually well converged: every outbound model call in the codebase
// goes through one of three HTTP primitives in internal/service/ai.go (aiGet,
// aiPostJSON, aiPostJSONStream), and all eight business paths funnel into
// them. That makes "what did this machine send out" a question with exactly
// one place to answer it.
//
// This table is that answer. It records the EVIDENCE of a call — when, to
// which endpoint, with which model, from which surface, how large, and what
// the review verdicts were — and deliberately does NOT record the prompt or
// the reply by default. Writing prompt text into the knowledge base would
// pollute it and pull API keys into the same SQLite file the notes live in,
// which runs directly against the "one local file, nothing leaves the
// machine" promise this product is built on (ADR-0002). The default record
// carries a SHA-256 of the prompt instead: it answers "was this the same
// prompt as last time" without answering "what was in it".
//
// Four properties, each for a reason:
//
//  1. Append-only is a constraint, not a policy. BEFORE UPDATE / BEFORE DELETE
//     both RAISE(ABORT), exactly as page_versions does (ADR-0021 决策 3). An
//     audit row that can be edited after the fact is not evidence.
//  2. NO foreign key, for the same reason page_versions has none: SQLite fires
//     row triggers for foreign-key CASCADE actions, so a cascading FK on an
//     append-only table would make deleting the audited thing fail outright.
//     Audit rows are expected to outlive whatever they describe.
//  3. The writer is the service layer, NOT a trigger — and that is a real
//     difference from page_versions. The trigger point for AI auditing is the
//     HTTP primitives in internal/service/ai.go, which is not in the db layer
//     where a trigger could be hung. The single-writer property comes from the
//     code structure instead: there is exactly one place where a model call
//     happens, so there is exactly one place that writes this table.
//  4. There is deliberately NO retention policy, same as page_versions
//     (ADR-0021 待决 3): the point of an audit trail is that it cannot be
//     quietly pruned, and a user who wants it gone can delete the database.
//
// The table is created by migration v28 and re-asserted on every InitDB, so
// an existing database gains it on upgrade without a rebuild.

// aiCallLogColumns is the projection shared by every read of ai_call_log.
const aiCallLogColumns = "id, created_at, COALESCE(surface, 'unknown'), COALESCE(endpoint, ''), " +
	"COALESCE(model, ''), COALESCE(prompt_hash, ''), COALESCE(prompt_chars, 0), " +
	"COALESCE(response_chars, 0), COALESCE(input_verdict, ''), COALESCE(output_verdict, '')"

// AICallLog is one audited AI call.
//
// PromptHash is a hex SHA-256 of the prompt text (empty when auditing is
// off). PromptChars / ResponseChars are character counts, not byte counts:
// they answer "how much went out" without answering "what went out".
// InputVerdict / OutputVerdict carry the review outcome — "allow",
// "blocked", "flagged" or empty when the corresponding review is disabled.
type AICallLog struct {
	ID            int64  `json:"id"`
	CreatedAt     string `json:"created_at"`
	Surface       string `json:"surface"`
	Endpoint      string `json:"endpoint"`
	Model         string `json:"model"`
	PromptHash    string `json:"prompt_hash"`
	PromptChars   int    `json:"prompt_chars"`
	ResponseChars int    `json:"response_chars"`
	InputVerdict  string `json:"input_verdict"`
	OutputVerdict string `json:"output_verdict"`
}

// aiCallLogObjects are the objects this file owns, in drop order (triggers
// before their table).
var aiCallLogObjects = []struct{ kind, name string }{
	{"TRIGGER", "ai_call_log_no_update"},
	{"TRIGGER", "ai_call_log_no_delete"},
	{"INDEX", "idx_ai_call_log_created"},
	{"TABLE", "ai_call_log"},
}

// aiCallLogSchemaStatements is the DDL, defined once so the v28 migration and
// the InitDB re-assert cannot disagree (same trick as
// pageVersionSchemaStatements in wiki_versions.go).
var aiCallLogSchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS ai_call_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		surface TEXT NOT NULL DEFAULT 'unknown',
		endpoint TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		prompt_hash TEXT NOT NULL DEFAULT '',
		prompt_chars INTEGER NOT NULL DEFAULT 0,
		response_chars INTEGER NOT NULL DEFAULT 0,
		input_verdict TEXT NOT NULL DEFAULT '',
		output_verdict TEXT NOT NULL DEFAULT ''
	)`,
	// Newest first: an audit list is only ever read as "what happened lately".
	`CREATE INDEX IF NOT EXISTS idx_ai_call_log_created ON ai_call_log(created_at DESC, id DESC)`,
	// Append-only as a structural fact, same as page_versions.
	`CREATE TRIGGER IF NOT EXISTS ai_call_log_no_update BEFORE UPDATE ON ai_call_log BEGIN ` +
		`SELECT RAISE(ABORT, 'ai_call_log is append-only: audit rows cannot be modified'); END`,
	`CREATE TRIGGER IF NOT EXISTS ai_call_log_no_delete BEFORE DELETE ON ai_call_log BEGIN ` +
		`SELECT RAISE(ABORT, 'ai_call_log is append-only: audit rows cannot be deleted'); END`,
}

// EnsureAICallLog is migration v28. Idempotent, and exported so the InitDB
// re-assert and tests share one definition.
func EnsureAICallLog(db *sql.DB) error {
	for _, stmt := range aiCallLogSchemaStatements {
		if _, err := db.Exec(stmt); err != nil {
			if isAlreadyExistsErr(err) {
				continue
			}
			return fmt.Errorf("db: ensure ai call log: %w", err)
		}
	}
	return nil
}

// InsertAICallLog appends one audit row.
//
// The caller has already decided the verdicts; this function records them.
// Errors are returned rather than swallowed — but the CALLER decides whether a
// failed audit write should fail the call (see internal/service/ai_audit.go:
// an audit failure never blocks a model call, because losing the call would
// be worse than losing the record).
func InsertAICallLog(db *sql.DB, entry AICallLog) error {
	_, err := db.Exec(
		`INSERT INTO ai_call_log(surface, endpoint, model, prompt_hash, prompt_chars,
			response_chars, input_verdict, output_verdict)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.Surface, entry.Endpoint, entry.Model, entry.PromptHash,
		entry.PromptChars, entry.ResponseChars, entry.InputVerdict, entry.OutputVerdict)
	if err != nil {
		return fmt.Errorf("db: insert ai call log: %w", err)
	}
	return nil
}

// ListAICallLog returns the most recent audit rows, newest first. limit is
// clamped to a sane range so a caller cannot ask for the whole table.
func ListAICallLog(db *sql.DB, limit int) ([]AICallLog, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := db.Query(
		"SELECT "+aiCallLogColumns+" FROM ai_call_log ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, fmt.Errorf("db: list ai call log: %w", err)
	}
	defer rows.Close()
	var out []AICallLog
	for rows.Next() {
		var e AICallLog
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.Surface, &e.Endpoint, &e.Model,
			&e.PromptHash, &e.PromptChars, &e.ResponseChars,
			&e.InputVerdict, &e.OutputVerdict); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountAICallLog returns how many audit rows exist. It is the growth signal
// ADR-0030 mentions: there is no retention policy on purpose, so the number
// has to be readable before a limit gets argued for.
func CountAICallLog(db *sql.DB) (int, error) {
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM ai_call_log").Scan(&n); err != nil {
		return 0, fmt.Errorf("db: count ai call log: %w", err)
	}
	return n, nil
}

// DropAICallLog removes this file's objects. Used by the test harness that
// asserts the table and its triggers exist as declared; not part of any
// user-facing flow.
func DropAICallLog(db *sql.DB) error {
	for _, o := range aiCallLogObjects {
		if _, err := db.Exec("DROP " + o.kind + " IF EXISTS " + o.name); err != nil {
			return fmt.Errorf("db: drop ai_call_log %s %s: %w", o.kind, o.name, err)
		}
	}
	return nil
}

// AICallLogTableExists reports whether the audit table is present. A database
// from before v28 that has not been migrated yet answers false, and the
// service layer treats "no table" as "auditing is off" rather than failing.
func AICallLogTableExists(db *sql.DB) bool {
	var name string
	err := db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'ai_call_log'").Scan(&name)
	return err == nil && strings.EqualFold(name, "ai_call_log")
}
