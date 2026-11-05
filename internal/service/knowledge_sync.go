package service

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"repotory/internal/db"
	"repotory/internal/importers/claude"
)

// Periodic knowledge sync: a background ticker that re-runs the enabled
// knowledge importers on a schedule, alongside the manual per-source triggers
// and the one-shot startup auto-import. It exists because an agent's memory
// files accumulate while Cinder is closed — without it, notes silently age
// until the user clicks each source again.
//
// The design keeps three promises:
//
//   - Never on launch. The first tick lands a full interval after startup
//     (NewTicker's first fire), so opening the app fires no import burst —
//     startup auto-import already covers an initial pass for sources the user
//     opted in to. Scheduling is a maintenance cadence, not a startup task:
//     the same rationale as wiki lint's first-pass-one-interval-away rule.
//
//   - Opt-in per source. Every source syncs only when its sync_source_<name>
//     key says "1". Curated claude memory is the one exception that defaults
//     ON (it is the curated $CLAUDE_CONFIG_DIR memory the user keeps, and the
//     same source the startup auto-import already treats as AUTO); every
//     transcript-based source defaults OFF until the user enables it
//     (ADR-0011 决策 4's opt-in gate, applied to scheduling rather than to a
//     one-shot trigger).
//
//   - Serialized with manual imports. Each round goes through
//     TriggerKnowledgeImport, so the runtime's importMu serializes it against
//     a click on the settings page — a sync round can never double-import
//     with a manual trigger, and the startup auto-import pass.
const (
	// knowledgeSyncDefaultIntervalMinutes is the cadence used when
	// knowledge_sync_interval_minutes is unset or invalid. 240 minutes = 4
	// hours: frequent enough that fresh agent memories land within a working
	// day, rare enough to be invisible in the import log.
	knowledgeSyncDefaultIntervalMinutes = 240
)

// knowledgeSyncInterval resolves the configured sync cadence. Invalid or
// unset values fall back to the 240-minute default; the interval is re-read
// before every round so a settings change takes effect without a restart.
func (s *Service) knowledgeSyncInterval() time.Duration {
	v, err := db.GetConfig(s.db, "knowledge_sync_interval_minutes")
	if err != nil || strings.TrimSpace(v) == "" {
		return knowledgeSyncDefaultIntervalMinutes * time.Minute
	}
	mins, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || mins <= 0 {
		return knowledgeSyncDefaultIntervalMinutes * time.Minute
	}
	return time.Duration(mins) * time.Minute
}

// syncSourceEnabled reports whether name should be included in periodic sync
// rounds. An absent config row means "the user never touched this switch":
// curated claude memory defaults ON (it is the AUTO source of the startup
// pass too), every other source defaults OFF — transcript imports stay an
// explicit opt-in even when scheduled.
func (s *Service) syncSourceEnabled(name string) bool {
	v, err := db.GetConfig(s.db, "sync_source_"+name)
	if err != nil {
		return false
	}
	if v == "" {
		return name == claude.SourceName
	}
	return v == "1"
}

// startKnowledgeSyncTicker launches the periodic sync loop as tracked
// background work, so Shutdown waits for a mid-round import and never closes
// the database underneath it. The ticker is re-created every round so the
// configured interval is re-read live; the first tick lands one full interval
// after startup by construction, never at launch.
func (s *Service) startKnowledgeSyncTicker() {
	s.bgGo("knowledge-sync", func(ctx context.Context) {
		for {
			t := time.NewTicker(s.knowledgeSyncInterval())
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			t.Stop()
			s.runKnowledgeSync(ctx)
		}
	})
}

// runKnowledgeSync runs every enabled, detected source once and records the
// round's status. A failure in one source does not stop the others — exactly
// like the startup auto-import pass — and the round's aggregate error is what
// KnowledgeSyncStatus reports, so the settings page can show "last sync had a
// problem" without naming which transient hiccup.
func (s *Service) runKnowledgeSync(ctx context.Context) {
	if s.rt == nil {
		return
	}
	statuses := s.rt.SourceStatuses()

	s.syncMu.Lock()
	s.syncLastRun = time.Now()
	s.syncLastErr = nil
	s.syncMu.Unlock()

	var firstErr error
	for _, st := range statuses {
		if ctx.Err() != nil {
			return
		}
		if !s.syncSourceEnabled(st.Name) {
			continue
		}
		if !st.Detected {
			// No agent data on this machine right now: an import would read
			// nothing, so skip rather than log a zero-doc round.
			continue
		}
		if _, err := s.TriggerKnowledgeImport(st.Name); err != nil && firstErr == nil {
			log.Printf("knowledge sync %q failed: %v", st.Name, err)
			firstErr = err
		}
	}
	s.syncMu.Lock()
	s.syncLastErr = firstErr
	s.syncMu.Unlock()
}

// KnowledgeSyncStatus reports the periodic sync's last round for the settings
// page: when it last ran, and whether any source in that round failed. Both
// fields are empty until the first scheduled round (the very first run is
// deliberately one interval away, never at launch).
type KnowledgeSyncStatus struct {
	LastRun   string `json:"last_run"`   // RFC3339; empty when never run
	LastError string `json:"last_error"` // empty when the last round was clean
}

// KnowledgeSyncStatus returns the current periodic-sync status.
func (s *Service) KnowledgeSyncStatus() KnowledgeSyncStatus {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	st := KnowledgeSyncStatus{}
	if !s.syncLastRun.IsZero() {
		st.LastRun = s.syncLastRun.Format(time.RFC3339)
	}
	if s.syncLastErr != nil {
		st.LastError = s.syncLastErr.Error()
	}
	return st
}
