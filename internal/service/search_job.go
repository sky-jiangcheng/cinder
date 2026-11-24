package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"repotory/internal/domain"
)

// Progressive search (ADR-0077).
//
// Search is two stages with two very different costs. The local FTS pass is
// milliseconds; the semantic arm embeds the QUERY over the network, which
// against a remote endpoint is 4–12s on a real library. Doing both inside one
// request meant the UI showed nothing at all for those seconds — on a 300ms
// debounce that reads as "the search box is dead", and retyping merely started
// another multi-second wait.
//
// So the UI-facing path is a job: the first answer (local hits) comes back with
// the submission itself, and the refined answer is collected by polling. The
// synchronous SearchAll binding stays as it is — agents and the CLI call it and
// want one complete answer, not a handle.
const (
	// SearchJobRefining means the local hits are in hand and the semantic arm
	// is still running. SearchJobDone means hits are final.
	SearchJobRefining = "refining"
	SearchJobDone     = "done"

	// searchJobTTL bounds how long a finished job stays pollable. The frontend
	// polls every 400ms and stops on the first done, so this only has to cover
	// a slow client, not a slow search.
	searchJobTTL = 2 * time.Minute
	// searchJobCap bounds concurrent+retained jobs. A user typing through a
	// debounce can start a handful; anything older than the newest few is
	// certainly abandoned, and every retained job pins its hit slice.
	searchJobCap = 32
	// searchJobRefineTimeout bounds the remote embed. Without it a hung
	// endpoint would leave the job "refining" until the TTL evicted it — and
	// the UI would keep polling a job that can never finish.
	searchJobRefineTimeout = 60 * time.Second
)

// SearchJob is the polling handle for one progressive search.
//
// Hits is always populated when StartSearchJob returns — that is the point of
// the job. A caller that never polls still gets a usable answer, just without
// the semantic arm.
type SearchJob struct {
	ID    string             `json:"id"`
	Query string             `json:"query"`
	State string             `json:"state"`
	Hits  []domain.SearchHit `json:"hits"`
}

type searchJobEntry struct {
	job     SearchJob
	created time.Time
}

// searchJobStore is the in-memory registry. It is deliberately not a table:
// these jobs are sub-minute, worthless after a restart (the poller is gone),
// and writing every query a user types into the knowledge database would leak
// their search history into a file that outlives the session.
type searchJobStore struct {
	mu    sync.Mutex
	seq   uint64
	jobs  map[string]*searchJobEntry
	order []string
}

// StartSearchJob runs the local stage and returns at once; the semantic arm
// continues in the background when there is one to run.
//
// With semantic search off (the default) there is no remote stage at all, so
// the job comes back already done and the caller never polls — the async
// machinery costs nothing until the user opts into a remote embedder.
func (s *Service) StartSearchJob(ctx context.Context, query string) (*SearchJob, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return &SearchJob{State: SearchJobDone, Hits: []domain.SearchHit{}}, nil
	}

	notes, todos, err := s.searchLexical(ctx, q)
	if err != nil {
		return nil, err
	}
	if !s.semanticArmed() {
		// Local-only config: the temporal arm is pure computation and the
		// fusion is a no-op otherwise, so the complete answer is already here.
		return &SearchJob{State: SearchJobDone, Hits: s.fuseHits(ctx, q, notes, todos)}, nil
	}

	// Reuse a still-running job for the same query. Without this, every
	// debounce tick during the 4–12s wait would start another remote embed of
	// the same text — a keystroke storm turns into a bill.
	if existing := s.searchJobs.findRefining(q); existing != nil {
		return existing, nil
	}

	local := append(append([]domain.SearchHit{}, notes...), todos...)
	job := &SearchJob{ID: s.searchJobs.nextID(), Query: q, State: SearchJobRefining, Hits: local}
	s.searchJobs.put(job)

	// bgGo (not a bare `go`) so a panic in the fusion cannot take the process
	// down and Shutdown waits for it before closing the database.
	s.bgGo("search job refine", func(ctx context.Context) {
		refineCtx, cancel := context.WithTimeout(ctx, searchJobRefineTimeout)
		defer cancel()
		s.searchJobs.finish(job.ID, s.fuseHits(refineCtx, q, notes, todos))
	})
	return job, nil
}

// GetSearchJob returns the job's current state. An unknown or expired id is an
// error rather than a "done, no hits" job: the poller must be able to tell
// "the answer is empty" from "this handle is gone" (ADR-0055 D2, the same rule
// that made a storage fault stop masquerading as zero hits).
func (s *Service) GetSearchJob(id string) (*SearchJob, error) {
	job, ok := s.searchJobs.get(id)
	if !ok {
		return nil, fmt.Errorf("search job %q not found or expired", id)
	}
	return job, nil
}

// semanticArmed reports whether stage two will actually do remote work. It
// mirrors what fuseSemantic checks; keeping the test here means StartSearchJob
// can decide "no polling needed" without running the arm to find out.
func (s *Service) semanticArmed() bool {
	if !s.semanticEnabled() {
		return false
	}
	_, ok := s.noteEmbedder()
	return ok
}

// --- store ------------------------------------------------------------------

func (st *searchJobStore) nextID() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.seq++
	return fmt.Sprintf("sj-%d", st.seq)
}

func (st *searchJobStore) put(job *SearchJob) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.jobs == nil {
		st.jobs = make(map[string]*searchJobEntry)
	}
	st.pruneLocked(time.Now())
	st.jobs[job.ID] = &searchJobEntry{job: *job, created: time.Now()}
	st.order = append(st.order, job.ID)
	// Evict oldest beyond the cap. The slice holds ids in insertion order, so
	// dropping the front is enough; entries are removed from both structures.
	for len(st.order) > searchJobCap {
		oldest := st.order[0]
		st.order = st.order[1:]
		delete(st.jobs, oldest)
	}
}

// finish publishes the refined hits. The job may already have been evicted
// (slow embed, busy user), in which case the result is simply dropped.
func (st *searchJobStore) finish(id string, hits []domain.SearchHit) {
	st.mu.Lock()
	defer st.mu.Unlock()
	entry, ok := st.jobs[id]
	if !ok {
		return
	}
	entry.job.Hits = hits
	entry.job.State = SearchJobDone
}

func (st *searchJobStore) get(id string) (*SearchJob, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	entry, ok := st.jobs[id]
	if !ok {
		return nil, false
	}
	if time.Since(entry.created) > searchJobTTL {
		delete(st.jobs, id)
		return nil, false
	}
	// Copy the struct so the poller never shares a slice header with the
	// goroutine that is about to replace it.
	snapshot := entry.job
	return &snapshot, true
}

// findRefining returns a copy of a running job for the same query, if any.
func (st *searchJobStore) findRefining(query string) *SearchJob {
	st.mu.Lock()
	defer st.mu.Unlock()
	for id := len(st.order) - 1; id >= 0; id-- {
		entry, ok := st.jobs[st.order[id]]
		if !ok {
			continue
		}
		if entry.job.State == SearchJobRefining && entry.job.Query == query {
			snapshot := entry.job
			return &snapshot
		}
	}
	return nil
}

// pruneLocked drops entries past their TTL. Called on insert, which is the
// only moment the map grows.
func (st *searchJobStore) pruneLocked(now time.Time) {
	if len(st.order) == 0 {
		return
	}
	kept := st.order[:0]
	for _, id := range st.order {
		entry, ok := st.jobs[id]
		if !ok {
			continue
		}
		if now.Sub(entry.created) > searchJobTTL {
			delete(st.jobs, id)
			continue
		}
		kept = append(kept, id)
	}
	st.order = kept
}
