package service

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"repotory/internal/db"
	"repotory/internal/grouper"
	"repotory/internal/platform"
	"repotory/internal/scanner"
)

// ScanResult holds the result of a scan operation.
type ScanResult struct {
	Success    bool `json:"success"`
	ReposFound int  `json:"repos_found"`
	Projects   int  `json:"projects"`
	// SyncErrors counts project groups whose DB sync failed while others
	// succeeded. A partial sync used to report plain success, leaving the
	// agent to believe the knowledge base was complete.
	SyncErrors int `json:"sync_errors,omitempty"`
	// AgentInstructionsMissing counts collected projects whose directory has
	// no AGENTS.md/CLAUDE.md carrying the managed repotory block. These are
	// exactly the projects whose agent sessions start without the memory-layer
	// instructions — the condition that let the handoff protocol idle for two
	// weeks unnoticed. The desktop offers a one-click top-up; an agent reading
	// this after repotory_scan can tell the user which projects to fix.
	AgentInstructionsMissing int    `json:"agent_instructions_missing,omitempty"`
	TaskID                   string `json:"task_id,omitempty"`
}

// ScanStatus holds the current scanning progress.
type ScanStatus struct {
	Running     bool   `json:"running"`
	Backfilling bool   `json:"backfilling"`
	Message     string `json:"message"`
	Progress    int    `json:"progress"`
	Total       int    `json:"total"`
}

// EnsureDefaultScanRoots seeds the platform default scan roots on first run,
// tracked by the scan_roots_seeded config flag so a user who deliberately
// removes every root is not re-seeded on the next launch.
//
// Shared by the desktop app and the MCP server: a headless (MCP-only) install
// must be able to discover repositories without ever opening the GUI, which is
// what turns the cold start from install→scan→star→refresh into install→scan.
func (s *Service) EnsureDefaultScanRoots() {
	if seeded, _ := db.GetConfig(s.db, "scan_roots_seeded"); seeded != "" {
		return
	}
	// Never clobber roots that are already configured: seeding is a first-run
	// convenience, not a reset. A database with roots but no flag (set by an
	// older build, or by configuration) keeps what it has.
	if existing, _ := db.GetScanRoots(s.db); len(existing) > 0 {
		_ = db.SetConfig(s.db, "scan_roots_seeded", "1")
		return
	}
	// Defaults go through the same normalisation as user input. On Windows the
	// platform default is every drive letter, so absent or unmounted drives are
	// expected here; storing those verbatim would leave dead roots in the config
	// that the user cannot remove later, because every write re-validates.
	if defaults := platform.DefaultScanRoots(); len(defaults) > 0 {
		roots, rejected := normalizeScanRoots(defaults)
		if err := db.ReplaceScanRoots(s.db, roots); err != nil {
			log.Printf("seed default scan roots error: %v", err)
		} else {
			log.Printf("seeded %d default scan root(s)", len(roots))
			for _, r := range rejected {
				log.Printf("skipped default scan root %s: %s", r.Path, r.Reason)
			}
		}
	}
	_ = db.SetConfig(s.db, "scan_roots_seeded", "1")
}

// ScanNow runs the full scan pipeline synchronously and reports how much it
// found. Unlike TriggerScan (fire-and-forget, for the desktop UI's background
// job) this blocks until discovery and stats refresh finish, so an AI agent
// gets a deterministic answer from a single tool call.
//
// Concurrency is guarded by the same flag TriggerScan uses: a headless scan
// must not race an in-flight desktop scan. ctx is the caller's (MCP request)
// context, so a client disconnect cancels the scan instead of burning CPU on
// work nobody is waiting for.
func (s *Service) ScanNow(ctx context.Context) (*ScanResult, error) {
	s.scanMu.Lock()
	if s.scanning {
		s.scanMu.Unlock()
		return nil, fmt.Errorf("scan already in progress")
	}
	s.scanning = true
	s.scanMu.Unlock()

	defer func() {
		s.scanMu.Lock()
		s.scanning = false
		s.scanProgress = 0
		s.scanTotal = 0
		s.scanMu.Unlock()
	}()

	res, err := s.runCollectedScan(ctx)
	if err != nil {
		return nil, err
	}
	res.Success = true
	return &res, nil
}

// TriggerScan starts an async full repository scan and returns immediately.
// Stats are refreshed for every collected project, including the ones this
// scan marks as collected.
func (s *Service) TriggerScan() (*ScanResult, error) {
	s.scanMu.Lock()
	if s.scanning {
		s.scanMu.Unlock()
		return nil, fmt.Errorf("scan already in progress")
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.scanCancel = cancel
	s.scanning = true
	s.scanMu.Unlock()

	taskID := fmt.Sprintf("%d", time.Now().UnixNano())

	go func() {
		if _, err := s.runCollectedScan(ctx); err != nil {
			// Cancellation via CancelScan is the expected path: the desktop
			// user pressed "stop". Anything else is a real scan failure that
			// used to vanish into runCollectedScan's log-only error handling.
			if ctx.Err() == nil {
				log.Printf("async scan failed: %v", err)
			}
		}
		s.scanMu.Lock()
		s.scanning = false
		s.scanProgress = 0
		s.scanTotal = 0
		s.scanCancel = nil
		s.scanMu.Unlock()
	}()

	return &ScanResult{Success: true, TaskID: taskID}, nil
}

// GetScanStatus returns the current scan progress.
func (s *Service) GetScanStatus() *ScanStatus {
	s.scanMu.Lock()
	running := s.scanning
	backfilling := s.scanBackfilling
	progress := s.scanProgress
	total := s.scanTotal
	s.scanMu.Unlock()
	msg := ""
	if running {
		if total > 0 {
			msg = fmt.Sprintf("Scanning %d/%d...", progress, total)
		} else {
			msg = "Scanning..."
		}
	}
	return &ScanStatus{Running: running, Backfilling: backfilling, Message: msg, Progress: progress, Total: total}
}

// runCollectedScan is the single scan pipeline: filesystem scan → grouping →
// transactional sync of projects/repos → stale cleanup → stats refresh for
// collected projects. Collected ids are re-read after the transaction commits
// so projects discovered by this scan are included. It returns how many
// repositories were found and how many projects they grouped into, so a
// synchronous caller (ScanNow) can report the outcome.
//
// Hard failures (filesystem scan, transaction lifecycle) are returned as
// errors instead of being logged and swallowed: ScanNow must be able to tell
// an agent "the scan failed" rather than reporting success with 0 repos.
// Per-group sync errors stay non-fatal (one bad path must not abort the whole
// scan) but are counted into ScanResult.SyncErrors, so a partial sync never
// masquerades as a clean one; a panic anywhere in the pipeline is converted
// into an error too, because a recovered panic that reports success would be
// a lie.
func (s *Service) runCollectedScan(ctx context.Context) (out ScanResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in collected scan: %v", r)
			out = ScanResult{}
			err = fmt.Errorf("scan panicked: %v", r)
		}
	}()

	depthStr, _ := db.GetConfig(s.db, "scan_depth")
	maxDepth, _ := strconv.Atoi(depthStr)
	if maxDepth <= 0 || maxDepth > 2 {
		maxDepth = 2
	}

	// A read error must not be mistaken for "no roots configured" — it would
	// turn a transient DB problem into a scan refusal. Fail loudly instead.
	roots, err := db.GetScanRoots(s.db)
	if err != nil {
		return ScanResult{}, fmt.Errorf("read scan roots: %w", err)
	}
	// With no roots configured, ScanRepositories returns zero repos and the
	// stale-data cleanup below treats "scanned nothing" as "everything is
	// gone" — wiping every repository, stat and orphaned project in the
	// knowledge base. A scan that can only ever return nothing must not be
	// allowed to reach the destructive path: the user has not configured
	// anything yet, or deliberately cleared the list, and neither means
	// "delete my data".
	if len(roots) == 0 {
		return ScanResult{}, fmt.Errorf("no scan roots configured: add at least one directory in Settings before scanning")
	}
	repos, err := scanner.ScanRepositories(ctx, roots, maxDepth)
	if err != nil {
		// Cancellation or a genuine walk failure — never present a partial
		// filesystem result as a complete scan.
		return ScanResult{ReposFound: len(repos)}, fmt.Errorf("scan repositories: %w", err)
	}

	// Group discovered repositories into projects. Repositories returned by
	// the scanner are filesystem paths without a DB id, so they are all
	// grouped and then synced.
	groups := grouper.GroupRepositories(repos)

	s.scanMu.Lock()
	s.scanTotal = len(groups)
	s.scanProgress = 0
	s.scanMu.Unlock()

	scannedPaths := make([]string, 0, len(repos))
	for _, r := range repos {
		scannedPaths = append(scannedPaths, r.Path)
	}

	// A scan that walked the configured roots and came back with nothing, while
	// the knowledge base already holds repositories, means the walk failed —
	// the root is unreadable, a symlink target vanished, an external volume is
	// unmounted. It does NOT mean the user deleted every repo. Reconciling
	// against that empty result would delete all of them, and stats computed
	// from git history cannot be rebuilt without re-walking every project.
	// Abort instead; the next successful scan reconciles normally.
	if len(scannedPaths) == 0 {
		existing, err := db.GetCollectedProjectIDs(ctx, s.db)
		if err != nil {
			log.Printf("count collected projects before cleanup: %v", err)
		}
		if len(existing) > 0 {
			return ScanResult{}, fmt.Errorf(
				"scan found 0 repositories under %d configured root(s) but %d project(s) are on record; "+
					"refusing to delete them — check that the roots exist and are readable",
				len(roots), len(existing))
		}
		// Genuinely empty knowledge base with genuinely empty scan: nothing to
		// lose, so fall through. CleanupStaleDataTx still refuses the empty
		// path set, which is the right outcome — there is nothing to clean.
		return ScanResult{}, fmt.Errorf("scan found 0 repositories under %d configured root(s): %w",
			len(roots), db.ErrNoScannedPaths)
	}

	// BeginTx binds the whole transaction to the caller's context: cancelling a
	// scan rolls it back instead of leaving the single DB connection held until
	// the loop happens to notice at its next iteration.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScanResult{ReposFound: len(repos)}, fmt.Errorf("begin scan transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	syncErrs := 0
	for i, group := range groups {
		select {
		case <-ctx.Done():
			return ScanResult{ReposFound: len(repos)}, ctx.Err()
		default:
		}
		s.scanMu.Lock()
		s.scanProgress = i + 1
		s.scanMu.Unlock()

		projectID, err := db.SyncProjectTx(tx, group.Name, group.RootPath, 0, group.IsAutoGrouped)
		if err != nil {
			log.Printf("sync project error: %v", err)
			syncErrs++
			continue
		}
		// Participating in a controlled scan makes the project "collected":
		// it becomes eligible for the history backfill after the scan.
		if err := db.MarkProjectCollectedTx(tx, projectID); err != nil {
			log.Printf("mark project collected error: %v", err)
		}
		for _, repo := range group.Repos {
			if err := db.UpsertRepositoryTx(tx, repo.Path, projectID); err != nil {
				log.Printf("upsert repo error: %v", err)
			}
		}
	}
	// Every group failing means the sync did nothing useful; reporting
	// "found N projects" would tell the agent the knowledge base is ready
	// when it is empty.
	if syncErrs > 0 && syncErrs == len(groups) {
		return ScanResult{}, fmt.Errorf("scan sync failed for all %d discovered project group(s)", len(groups))
	}

	if err := db.CleanupStaleDataTxContext(ctx, tx, scannedPaths); err != nil {
		return ScanResult{ReposFound: len(repos)}, fmt.Errorf("cleanup stale data: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return ScanResult{ReposFound: len(repos)}, fmt.Errorf("commit scan transaction: %w", err)
	}

	// The committed cleanup may have cascade-deleted notes with their
	// projects. If the derived vector index exists, prune the orphans it
	// left behind so KNN's recall budget is not spent on dead ids.
	if db.VectorIndexReady(s.db) {
		if n, err := db.PruneNoteEmbeddings(s.db); err != nil {
			log.Printf("prune orphaned embeddings: %v", err)
		} else if n > 0 {
			log.Printf("pruned %d orphaned embedding(s) after scan cleanup", n)
		}
	}

	// Re-read after commit: projects found by this scan were just marked
	// collected, so they now show up and get their history backfilled in the
	// same run instead of waiting for a second scan.
	collectedIDs, err := db.GetCollectedProjectIDs(ctx, s.db)
	if err != nil {
		// The scan itself is committed at this point; only the stats refresh
		// is skipped, so report the real counts with a warning-level log
		// instead of failing the whole call.
		log.Printf("load collected projects error: %v", err)
		return ScanResult{ReposFound: len(repos), Projects: len(groups), SyncErrors: syncErrs}, nil
	}
	s.refreshCollectedStats(ctx, collectedIDs)
	_ = db.SetConfig(s.db, "last_stats_backfill", s.git.GetTodayDate())
	// The instruction checklist rides the scan result (ADR-0054 follow-up): a
	// collection just happened, which is the moment the user can act on "these
	// N projects have no AGENTS.md/CLAUDE.md block, so their agent sessions
	// start blind". The count is a handful of stat() calls; agents reading
	// repotory_scan get it for free, the desktop overlays live status on top.
	missing := s.MissingAgentInstructionsCount()
	log.Printf("scan complete: %d repos, %d projects (%d sync errors, %d missing agent instructions)", len(repos), len(groups), syncErrs, missing)
	return ScanResult{ReposFound: len(repos), Projects: len(groups), SyncErrors: syncErrs, AgentInstructionsMissing: missing}, nil
}
