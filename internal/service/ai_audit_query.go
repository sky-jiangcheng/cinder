package service

import (
	"fmt"
	"repotory/internal/aiaudit"

	"repotory/internal/db"
)

// AI audit queries (ADR-0030).
//
// These are the read side of the audit plugin: "what did this machine send
// out, when, and did anything trip a rule". They are deliberately the ONLY
// way to reach the audit table from outside the service layer, so the
// decision about what an audit row may contain stays in one place.

// AIAuditStatus is the current state of the audit/review plugin.
//
// It exists so a UI (or an agent) can tell the difference between "auditing
// is on and has nothing to report" and "auditing is off, so there is
// nothing to report" — a distinction that otherwise looks identical from
// the outside, and which is exactly the difference between "we checked and
// found nothing" and "we never checked".
type AIAuditStatus struct {
	AuditEnabled bool   `json:"audit_enabled"`
	ReviewInput  bool   `json:"review_input"`
	ReviewOutput bool   `json:"review_output"`
	RuleCount    int    `json:"rule_count"`
	CallCount    int    `json:"call_count"`
	BlockedCount int    `json:"blocked_count"`
	FlaggedCount int    `json:"flagged_count"`
	Surface      string `json:"surface"`
	TableExists  bool   `json:"table_exists"`
	// Retention names the retention POLICY as a machine-readable token, not as
	// a sentence. There is no retention policy on purpose (ADR-0030): an
	// audit trail that can be quietly pruned is not one. The token lets the UI
	// say so out loud in the reader's own language; a hardcoded Chinese
	// sentence here would leak into the English UI and into the MCP tool
	// output alike.
	Retention string `json:"retention"`
}

// AIRetentionAppendOnly is the only retention policy the audit trail has:
// rows are written and never removed by the application.
const AIRetentionAppendOnly = "append_only"

// GetAIAuditStatus reports the plugin's current state and a summary of the
// recorded calls.
func (s *Service) GetAIAuditStatus() (*AIAuditStatus, error) {
	cfg := s.Audit.Resolve()
	status := &AIAuditStatus{
		AuditEnabled: cfg.Enabled,
		ReviewInput:  cfg.ReviewInput,
		ReviewOutput: cfg.ReviewOutput,
		RuleCount:    len(cfg.Rules),
		Surface:      s.Audit.Surface(),
		TableExists:  db.AICallLogTableExists(s.db),
		Retention:    AIRetentionAppendOnly,
	}
	if !status.TableExists {
		return status, nil
	}
	rows, err := db.ListAICallLog(s.db, 500)
	if err != nil {
		return nil, err
	}
	status.CallCount = len(rows)
	for _, r := range rows {
		switch {
		case r.InputVerdict == aiaudit.VerdictBlocked:
			status.BlockedCount++
		case r.OutputVerdict == aiaudit.VerdictFlagged:
			status.FlaggedCount++
		}
	}
	return status, nil
}

// ListAICallAudit returns the most recent audited calls, newest first.
//
// The limit is clamped inside the db layer, so a caller asking for the whole
// table gets the ceiling instead.
func (s *Service) ListAICallAudit(limit int) ([]db.AICallLog, error) {
	if !db.AICallLogTableExists(s.db) {
		return []db.AICallLog{}, nil
	}
	rows, err := db.ListAICallLog(s.db, limit)
	if err != nil {
		return nil, fmt.Errorf("list ai call audit: %w", err)
	}
	return rows, nil
}
