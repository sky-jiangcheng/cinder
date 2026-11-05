// Package service contains Repotory's business logic: project aggregation,
// the repository scan pipeline, stats refresh, knowledge notes, search and
// AI-facing exports. It is the single layer that talks to the database
// (internal/db) and the git provider (internal/core/git); the Wails binding
// layer (internal/app), the CLI (cmd/repotory) and the MCP server (cmd/mcp)
// are thin adapters over it and share one implementation.
package service

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"repotory/internal/core/git"
	pluginruntime "repotory/internal/core/plugin/runtime"
	"repotory/internal/version"
)

// ImportEventPayload is the data broadcast after a knowledge-source import.
type ImportEventPayload map[string]any

// Service carries all Repotory business logic and its runtime state (scan
// progress, status-bar cache). It is safe for concurrent use.
type Service struct {
	db    *sql.DB
	git   git.Provider
	rt    *pluginruntime.Runtime
	guser string // git user.name used to split "mine" vs "all" stats

	// backupDir overrides where local snapshots land (ADR-0025 L1). Empty at
	// runtime — RunBackupNow derives it from the platform data dir — and set
	// only by tests so they never write into the real user data directory.
	backupDir string

	// liveDbPath overrides the path of the live database file, used to decide
	// whether an off-disk backup target really sits on another disk
	// (ADR-0025 L3). Empty at runtime — the platform data dir is used — and
	// set only by tests, for the same reason as backupDir.
	liveDbPath string

	// auditSurface names the surface this process serves ("mcp", "http",
	// "desktop"), written once at startup by each entry point and read when an
	// AI call is audited (ADR-0030). Each binary IS one surface, so it is
	// declared per process rather than passed per call — there is no moment at
	// which one process serves two of them. An empty value reads as "unknown".
	auditSurface string

	// remoteDialer builds the object-store client for one L2 upload (ADR-0025
	// L2). Nil at runtime — the real minio dialer runs — and overridden by
	// tests so no test ever touches a network.
	remoteDialer remoteStoreDialer

	// onImportEvent, when set, is invoked with the import.completed payload
	// after every knowledge import so the UI layer can forward it to the
	// frontend (Wails events, CLI output, ...).
	onImportEvent func(ImportEventPayload)

	// startupOnce guards Startup so double invocation (e.g. the lifecycle
	// hook being called again) never re-registers importers or re-triggers
	// the auto import.
	startupOnce sync.Once

	// Background goroutine plumbing: bgCtx is cancelled by Shutdown, and
	// bgWG tracks the goroutines so Close() never races a writer. Every
	// service-launched goroutine must go through bgGo — a panic in a bare
	// `go` kills the whole process (net/http only recovers handler
	// goroutines), and an untracked goroutine can still be writing when the
	// database handle closes underneath it.
	bgCtx    context.Context
	bgCancel context.CancelFunc
	bgWG     sync.WaitGroup

	// statsRefreshInFlight deduplicates on-demand single-day stats refreshes
	// keyed by "projectID|date": without it, every dashboard load for a date
	// with no rows yet spawns another refresh goroutine per project.
	statsRefreshInFlight sync.Map

	// Scan engine state, guarded by scanMu.
	scanMu          sync.Mutex
	scanning        bool
	scanBackfilling bool
	scanCancel      context.CancelFunc
	scanProgress    int
	scanTotal       int

	// Status bar cache to avoid repeated git log queries on every render.
	statusCacheMu   sync.Mutex
	statusCache     *StatusBarData
	statusCacheTime time.Time

	// vecStore memoises the resolved vector store (axis B). See
	// vectorStoreCache in search_semantic.go: rebuilding it costs four config
	// reads plus, for a remote backend, an HTTP reachability probe — which on
	// the search path was one extra round trip per query.
	vecStore vectorStoreCache

	// embedDimWarned keeps the "endpoint changed models" skip to one log line
	// instead of one per drain tick. Reset whenever the embedding endpoint or
	// model is reconfigured, which is exactly when the user deserves to hear
	// about it again.
	embedDimWarned atomic.Bool

	// miningInFlight tracks repoIDs currently being mined to prevent duplicate
	// goroutines when the user rapidly switches between projects.
	miningInFlight sync.Map

	// gitUserMu guards guser so the git_author config key can be applied at
	// runtime instead of only at construction time.
	gitUserMu sync.RWMutex

	// syncMu guards the periodic knowledge-sync status fields below: they are
	// written by the background ticker and read by KnowledgeSyncStatus.
	syncMu      sync.Mutex
	syncLastRun time.Time
	syncLastErr error

	// updates owns the single in-flight update download (see
	// update_download.go). It is state, not a dependency, so it lives here
	// rather than being threaded through every constructor.
	updates *updateDownloader
}

// New creates a Service with production dependencies: the local git CLI
// provider and the knowledge-source runtime over the given database.
func New(database *sql.DB, gitUser string) *Service {
	return NewWithDeps(database, git.NewLocalGitProvider(), pluginruntime.New(database), gitUser)
}

// NewWithDeps constructs a Service with explicitly supplied dependencies
// (used by tests and future alternative providers).
func NewWithDeps(database *sql.DB, provider git.Provider, runtime *pluginruntime.Runtime, gitUser string) *Service {
	bgCtx, bgCancel := context.WithCancel(context.Background())
	s := &Service{db: database, git: provider, rt: runtime, guser: gitUser, bgCtx: bgCtx, bgCancel: bgCancel}
	s.updates = newUpdateDownloader()
	// Every finished download is written to disk, so a bundle the user
	// downloaded stays reachable after a restart (update_state.go). The hook
	// itself is installed when a download STARTS: it carries the state file's
	// path, which must be resolved on the caller's goroutine.
	return s
}

// bgGo runs fn on a tracked background goroutine: panics become log entries
// instead of process kills, and Shutdown waits for the goroutine to settle
// before the caller closes the database. The long-lived async scan (TriggerScan)
// keeps its own lifecycle via scanCancel — it checks ctx between work units and
// its post-commit writes are single-statement upserts, which SQLite serializes
// safely even in the close-race window.
func (s *Service) bgGo(name string, fn func(ctx context.Context)) {
	if s.bgCtx == nil {
		// Service built without NewWithDeps (tests): fall back to an
		// untracked, un-cancellable goroutine rather than panicking on a nil
		// context.
		s.bgWG.Add(1)
		go func() {
			defer s.bgWG.Done()
			s.runBgFn(name, fn, context.Background())
		}()
		return
	}
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		s.runBgFn(name, fn, s.bgCtx)
	}()
}

func (s *Service) runBgFn(name string, fn func(ctx context.Context), ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("background %s panicked: %v", name, r)
		}
	}()
	fn(ctx)
}

// gitUser returns the current "mine" author. Reads are guarded so the value
// can be changed at runtime via the git_author config key.
func (s *Service) gitUser() string {
	s.gitUserMu.RLock()
	defer s.gitUserMu.RUnlock()
	return s.guser
}

// setGitUser updates the "mine" author used to split personal vs team stats.
func (s *Service) setGitUser(v string) {
	s.gitUserMu.Lock()
	defer s.gitUserMu.Unlock()
	s.guser = v
}

// SetImportEventHandler installs the callback invoked with the
// import.completed payload after every knowledge import.
func (s *Service) SetImportEventHandler(fn func(ImportEventPayload)) {
	s.onImportEvent = fn
}

// Startup performs one-time initialisation: registers the built-in knowledge
// importers, loads script plugins and kicks off the (optional) auto import.
// Safe to call multiple times; only the first call has an effect.
func (s *Service) Startup() {
	s.startupOnce.Do(func() {
		// The last update download is restored before anything else: a bundle
		// the user already downloaded stays openable ("打开安装包" / "在文件
		// 管理器中显示") across a restart, instead of the app forgetting it.
		s.restorePersistedDownload()

		// Incremental embedding (ADR-0012's 增删改即时生效) needs only the queue
		// and a configured endpoint — never the plugin runtime — so it starts
		// ahead of the rt guard below rather than being skipped by it.
		s.startEmbedDrainer()

		// Page-embedding drain (ADR-0018 lane 2): the page twin of the note
		// drainer above, sharing the same provider and semantic_search gate but
		// writing to the separate page_embeddings index. Also ahead of the rt
		// guard: it needs only the queue and the endpoint.
		s.startPageEmbedDrainer()

		// Wiki lint (ADR-0014 决策 5): the scheduled half of the two trigger paths.
		// Its first pass is one interval away, so opening the app files no todos.
		s.startWikiLintTicker()

		// Async compile jobs (ADR-0016). Recovery runs before the worker, so a job
		// left 'running' by a dead process is marked failed and never picked up as
		// if it knew where it stopped.
		s.recoverInterruptedCompileJobs()
		s.startCompileJobWorker()

		if s.rt == nil {
			return
		}
		s.registerBuiltinImporters()
		log.Printf("knowledge-source runtime ready: %d source(s)", len(s.rt.SourceStatuses()))

		// Periodic knowledge sync: re-runs the sources whose sync_source_<name>
		// switch is on, on a scheduler cadence. Started after the importer
		// registration (it needs the runtime) and before auto-import (both go
		// through TriggerImport, so the runtime's importMu serializes them).
		// The first round is one full interval away, never at launch.
		s.startKnowledgeSyncTicker()

		// Local database snapshots (ADR-0025 L1): one VACUUM INTO snapshot per
		// day, produced at first open and retried hourly (no-ops once today's
		// file exists). Purely local — no outbound traffic, no privacy gate.
		s.startBackupTicker()

		// Auto-import knowledge sources on startup (issue #36) — but only on an
		// explicit opt-in. Tracked via bgGo so Shutdown waits for it instead of
		// closing the database underneath a mid-import upsert.
		if s.autoImportEnabled() {
			s.bgGo("auto-import", func(ctx context.Context) {
				for _, r := range s.TriggerAllKnowledgeImports() {
					if ctx.Err() != nil {
						return
					}
					if r.Err != "" {
						log.Printf("auto-import %q failed: %s", r.Name, r.Err)
					} else {
						log.Printf("auto-import %q: +%d ~%d -%d",
							r.Name, r.Run.Created, r.Run.Updated, r.Run.Skipped)
					}
				}
			})
		}
	})
}

// Shutdown cancels the running scan, signals every background goroutine, and
// waits (bounded) for them to settle. Callers may Close() the database only
// after this returns; without the wait, an in-flight auto-import or stats
// refresh would be writing into a closed handle. The timeout is a backstop —
// a goroutine stuck on a slow git subprocess must not hang the exit path.
func (s *Service) Shutdown() {
	s.scanMu.Lock()
	if s.scanCancel != nil {
		s.scanCancel()
	}
	s.scanMu.Unlock()
	if s.bgCancel != nil {
		s.bgCancel()
	}
	done := make(chan struct{})
	go func() {
		s.bgWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		log.Printf("shutdown: background work did not settle in 3s; continuing")
	}
}

// Health returns a health-check payload.
func (s *Service) Health() map[string]any {
	if err := s.db.Ping(); err != nil {
		return map[string]any{"status": "error", "message": "database unavailable"}
	}
	// "version" stays the release version: the updater and the install-info
	// path compare it against the latest published release. "core_version" is
	// the memory-layer contract version, which is what a client programs
	// against.
	return map[string]any{
		"status":       "ok",
		"version":      version.Version,
		"core_version": version.CoreVersion,
	}
}

// Close releases the underlying database handle.
func (s *Service) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
