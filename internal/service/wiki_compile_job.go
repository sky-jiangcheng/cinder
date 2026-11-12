package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"repotory/internal/db"
	"repotory/internal/service/wikigen"
)

// Async compilation (ADR-0016).
//
// ADR-0016's first acceptance criterion is that the compile core is reused with
// zero changes, so this file contains no compilation logic at all: it enqueues
// work, and the worker calls the existing per-note entry point. If a second
// implementation of compilation appeared here, the boundary would be wrong.
//
// The reason this exists at all is a measurement, not a theory: one note took 173
// seconds on a local 27B model. Submission therefore returns an id immediately and
// the caller polls, so no UI has to hang, progress is visible, and navigating
// away no longer loses the result.

// compileJobPollInterval is how often the worker looks for queued work. It is a
// timer rather than a channel because jobs are also submitted by other transports
// (/api/rpc, a future CLI) against the same database, and a row is the one place
// all of them agree.
const compileJobPollInterval = 2 * time.Second

// StartCompileJob enqueues compiling up to maxNotes of a project's notes and
// returns the job id without doing any work itself. The caller can leave.
func (s *Service) StartCompileJob(projectID int64, maxNotes int) (int64, error) {
	if projectID <= 0 {
		return 0, fmt.Errorf("a project id is required to compile notes")
	}
	if maxNotes <= 0 || maxNotes > compileMaxNotes {
		maxNotes = compileMaxNotes
	}
	if !s.wikiCompileEnabled() {
		return 0, fmt.Errorf("compilation is off: set %s=1 and configure an AI endpoint first", wikiCompileKey)
	}
	if _, err := s.aiChatConfig(); err != nil {
		return 0, fmt.Errorf("no AI endpoint configured: %w", err)
	}
	return db.CreateCompileJob(s.db, projectID, maxNotes)
}

// StartLintJob enqueues the model-backed lint pass and returns the job id
// without calling the model (ADR-0016 待决 ①).
//
// It refuses when the model half is switched off, rather than enqueueing a job
// that would do nothing: wiki_lint_llm defaults to off precisely because the
// pass sends page content to a model, and a queued job that silently no-ops is
// harder to reason about than a refused one.
func (s *Service) StartLintJob(projectID int64) (int64, error) {
	if projectID <= 0 {
		// A queued lint is per-project. The all-pages pass runs on the scheduled
		// synchronous path; see db.CreateLintJob for why a job cannot span projects.
		return 0, fmt.Errorf("a project id is required to start a lint job")
	}
	if !s.wikiLintLLMEnabled() {
		return 0, fmt.Errorf("the model-backed checks are off: set %s=1 to let lint read your pages", wikiLintLLMKey)
	}
	if _, err := s.aiChatConfig(); err != nil {
		return 0, fmt.Errorf("no AI endpoint configured: %w", err)
	}
	pages, err := db.ListWikiPages(s.db, "", projectID)
	if err != nil {
		return 0, err
	}
	if len(pages) == 0 {
		return 0, fmt.Errorf("there are no pages to lint yet")
	}
	return db.CreateLintJob(s.db, projectID, len(pages))
}

// GetCompileJob and ListCompileJobs are the polling surface.
func (s *Service) GetCompileJob(jobID int64) (*db.CompileJob, error) {
	j, err := db.GetCompileJob(s.db, jobID)
	if err != nil {
		return nil, fmt.Errorf("compile job %d not found: %w", jobID, err)
	}
	return j, nil
}

// ListCompileJobs returns recent jobs, newest first. kind == "" lists both kinds;
// the review panel filters so a compile row's page counters and a lint row's
// finding count are never rendered under one set of labels.
func (s *Service) ListCompileJobs(projectID int64, kind string, limit int) ([]db.CompileJob, error) {
	if kind != "" && kind != db.JobKindCompile && kind != db.JobKindLint {
		return nil, fmt.Errorf("unknown job kind %q", kind)
	}
	return db.ListCompileJobs(s.db, projectID, kind, limit)
}

// CancelCompileJob stops a job from processing further notes. An LLM request
// already in flight is allowed to finish: aborting it would save nothing (the
// tokens are spent) while costing the whole call chain cancellation semantics.
func (s *Service) CancelCompileJob(jobID int64) (*db.CompileJob, error) {
	return db.RequestCancelCompileJob(s.db, jobID)
}

// startCompileJobWorker runs the queue: one job at a time, one note at a time.
// Single worker is deliberate — SQLite here is a single-writer embedded database,
// and compiling two jobs concurrently would only add lock contention and make the
// "how long will this take" question unanswerable.
func (s *Service) startCompileJobWorker() {
	s.bgGo("compile-jobs", func(ctx context.Context) {
		t := time.NewTicker(compileJobPollInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.runNextCompileJob(ctx)
			}
		}
	})
}

// runNextCompileJob claims and drains one job. It is a method rather than inline
// so a test can drive the queue deterministically without waiting on the ticker.
//
// The claimed row's kind picks the kernel. Both kernels are the existing
// synchronous entry points, unchanged — the queue is transport, not logic.
func (s *Service) runNextCompileJob(ctx context.Context) bool {
	job, err := db.ClaimCompileJob(s.db)
	if err != nil {
		log.Printf("compile jobs: claim failed: %v", err)
		return false
	}
	if job == nil {
		return false
	}
	if job.Kind == db.JobKindLint {
		s.runLintJob(ctx, job)
		return true
	}
	return s.runCompileJob(ctx, job)
}

// runLintJob drains one queued lint pass. The structural half (orphan pages,
// missing cross-references, data gaps) is pure SQL and stays on the synchronous
// path — it is milliseconds, and making the user wait on a job row for it would
// be theatre. Only the model-backed half is queued, which is the half that can
// take minutes.
func (s *Service) runLintJob(ctx context.Context, job *db.CompileJob) {
	if err := ctx.Err(); err != nil {
		s.finishJob(job.ID, db.CompileJobCanceled, "", db.CompileJobErrShutdown)
		return
	}
	if canceled, _ := db.CompileJobIsCanceled(s.db, job.ID); canceled {
		s.finishJob(job.ID, db.CompileJobCanceled, "", db.CompileJobErrCanceledEarly)
		return
	}
	rep, err := s.RunWikiLint(job.ProjectID, true)
	if err != nil {
		s.failJob(job.ID, fmt.Sprintf("wiki lint: %v", err))
		return
	}
	row := &db.CompileJob{
		NotesTotal: rep.Pages,
		NotesDone:  rep.Pages,
		Findings:   len(rep.Findings),
		Note:       rep.LLMNote,
		Stopped:    lintStopNote(rep),
	}
	if err := db.UpdateCompileJobProgress(s.db, job.ID, row); err != nil {
		log.Printf("lint job %d: progress write failed: %v", job.ID, err)
	}
	// A canceled job must not be flipped back to succeeded by this late write —
	// FinishCompileJob is unconditional, so the flag is re-read here.
	if canceled, _ := db.CompileJobIsCanceled(s.db, job.ID); canceled {
		return
	}
	note := row.Stopped
	if note == "" {
		note = "lint finished"
	}
	s.finishJob(job.ID, db.CompileJobDone, note, "")
}

// lintStopNote says what the pass actually did, in the job row's own terms. An
// LLM pass that was switched off is not a pass that found nothing, and the two
// must not read the same in a list.
func lintStopNote(rep *WikiLintReport) string {
	switch {
	case !rep.LLMRan && rep.LLMNote != "":
		return rep.LLMNote
	case len(rep.Findings) == 0:
		return "no findings"
	default:
		return fmt.Sprintf("%d finding(s), %d todo(s) filed", len(rep.Findings), rep.TodosCreated)
	}
}

// runCompileJob drains one queued compile, a note at a time.
func (s *Service) runCompileJob(ctx context.Context, job *db.CompileJob) bool {
	notes, err := db.ListNotes(s.db, job.ProjectID)
	if err != nil {
		s.failJob(job.ID, fmt.Sprintf("list notes: %v", err))
		return true
	}
	// Newest first: the freshest knowledge is what a compile should reach for
	// first, and the list from db is oldest-first. Shared with the synchronous
	// path (wikigen.CompileNoteTargets) so the two cannot disagree about "which
	// notes".
	targets := wikigen.CompileNoteTargets(notes, job.Requested)
	agg := &WikiCompileReport{ProjectID: job.ProjectID}
	agg.NotesScanned = len(targets)
	agg.NotesRequested = len(targets)
	failedNotes := 0

	for i, n := range targets {
		if err := ctx.Err(); err != nil {
			s.finishJob(job.ID, db.CompileJobCanceled, "", db.CompileJobErrShutdown)
			return true
		}
		if canceled, _ := db.CompileJobIsCanceled(s.db, job.ID); canceled {
			s.finishJob(job.ID, db.CompileJobCanceled,
				fmt.Sprintf("stopped after %d of %d notes", i, len(targets)), "")
			return true
		}
		rep, err := s.CompileNote(n.ID)
		if err != nil {
			// One note failing does not condemn the job: record, keep going, and
			// let the note count tell the story. A whole-job failure is reserved
			// for the run being unable to continue at all.
			log.Printf("compile job %d: note %d failed: %v", job.ID, n.ID, err)
			agg.LLMNote = fmt.Sprintf("note %d failed: %v", n.ID, err)
			agg.RejectedOps++
			failedNotes++
		} else {
			agg.Absorb(rep)
		}
		agg.NotesDone = i + 1
		agg.NotesRequested = len(targets)
		if updErr := db.UpdateCompileJobProgress(s.db, job.ID, agg.ToJobRow()); updErr != nil {
			log.Printf("compile job %d: progress write failed: %v", job.ID, updErr)
		}
	}

	note := agg.Stopped
	if note == "" {
		note = fmt.Sprintf("%d note(s) processed", agg.NotesDone)
	}
	// The status enum has only done/failed/canceled, so a mixed run is still
	// done — but its note payload says partial:true and carries the failure
	// count, so a poller can tell it from a clean success (no schema change).
	note = compileJobNote(note, failedNotes)
	s.finishJob(job.ID, db.CompileJobDone, note, "")
	return true
}

// compileJobNote renders the finishing note; a partially failed run gets a
// machine-readable payload instead of a plain "processed" string.
func compileJobNote(base string, failedNotes int) string {
	if failedNotes <= 0 {
		return base
	}
	payload, _ := json.Marshal(map[string]any{
		"partial": true,
		"failed":  failedNotes,
		"message": base,
	})
	return string(payload)
}

// compileNoteTargets (which notes a run covers) moved to wikigen with the
// compile kernel — it is the kernel's budget decision, and the job path above
// calls wikigen.CompileNoteTargets directly.

// recoverInterruptedCompileJobs is the startup path ADR-0016 asks for: work left
// 'running' by a dead process is marked failed rather than resumed, because the
// stopping point is not recorded anywhere.
func (s *Service) recoverInterruptedCompileJobs() {
	n, err := db.FailInterruptedCompileJobs(s.db)
	if err != nil {
		log.Printf("compile jobs: startup recovery failed: %v", err)
		return
	}
	if n > 0 {
		log.Printf("compile jobs: marked %d interrupted job(s) as failed (not resumed)", n)
	}
}

func (s *Service) failJob(id int64, msg string) {
	if err := db.FinishCompileJob(s.db, id, db.CompileJobFailed, "", msg); err != nil {
		log.Printf("compile job %d: %v", id, err)
	}
}

func (s *Service) finishJob(id int64, status, note, errMsg string) {
	if err := db.FinishCompileJob(s.db, id, status, note, errMsg); err != nil {
		log.Printf("compile job %d: %v", id, err)
	}
}
