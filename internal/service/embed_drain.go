package service

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"repotory/internal/db"
)

// Incremental embedding (ADR-0012's "增删改即时生效" half). ADR-0012 shipped the
// vector index and the query-side fusion but left indexing all-or-nothing: after
// turning semantic search on, every new or edited note stayed invisible to
// vector recall until someone pressed 重建索引 — an O(all notes) endpoint walk
// that a desktop user is not going to run after each edit.
//
// The queue is maintained by SQLite triggers (internal/db/embed_dirty.go)
// precisely because note writes do not all come through this package: the five
// agent-memory importers upsert through the plugin runtime straight into
// internal/db. A service-layer hook would have covered the UI and missed the
// bulk of the writes.

const (
	// embedDrainInterval is a latency/cost trade, not a correctness knob: the
	// queue is durable, so a missed round is recovered by the next one, and app
	// exits never lose pending work. Notes appear in vector recall within about
	// one interval of being written — good enough for a personal knowledge base,
	// and it lets a burst of writes (an import, or paste-then-save-then-save)
	// coalesce into one request per note instead of one per click.
	embedDrainInterval = 5 * time.Second
	// embedDrainBatch caps the notes per round, mirroring embedBatch so one round
	// is at most one request per batch. Without a cap, importing a 5k-note memory
	// directory would hand the endpoint everything at once.
	embedDrainBatch = 64
	// embedDrainMaxBackoff caps the retry delay for a persistently dead endpoint.
	embedDrainMaxBackoff = 5 * time.Minute
	// embedDrainEscalateAfter is the consecutive-failure count that earns one WARN.
	embedDrainEscalateAfter = 6
)

// drainerBackoff doubles the wait per consecutive failed round, capped at
// embedDrainMaxBackoff, so a dead endpoint is not hit every 5 seconds forever.
func drainerBackoff(failures int) time.Duration {
	if failures <= 0 {
		return embedDrainInterval
	}
	d := embedDrainInterval
	for i := 0; i < failures; i++ {
		if d >= embedDrainMaxBackoff {
			return embedDrainMaxBackoff
		}
		d *= 2
	}
	if d > embedDrainMaxBackoff {
		return embedDrainMaxBackoff
	}
	return d
}

// drainRoundFailed reports a round that made no progress while work was still
// queued — the endpoint/store-down case, which keeps its queue untouched.
func drainRoundFailed(err error, pending bool, resolved int) bool {
	return err != nil || (pending && resolved == 0)
}

// startEmbedDrainer launches the drain loop as tracked background work, so
// Shutdown waits for it and never leaves it writing into a closed database.
func (s *Service) startEmbedDrainer() {
	s.bgGo("embed-drainer", func(ctx context.Context) {
		failures, escalated := 0, false
		for {
			wait := embedDrainInterval
			if failures > 0 {
				wait = drainerBackoff(failures)
			}
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			pending := false
			if w, e := db.NextDirtyEmbedWork(s.db, 1); e == nil {
				pending = len(w.ToEmbed) > 0 || len(w.Gone) > 0
			}
			n, err := s.drainDirtyNotes(embedDrainBatch)
			switch {
			case drainRoundFailed(err, pending, n):
				failures++
				if err != nil {
					s.logEmbedDrain("deferred", err)
				}
				if !escalated && failures >= embedDrainEscalateAfter {
					log.Printf("WARN semantic incremental: %d consecutive rounds made no progress; backing off", failures)
					escalated = true
				}
			case n > 0:
				failures, escalated = 0, false
				log.Printf("semantic incremental: %d note(s) indexed", n)
			default:
				failures, escalated = 0, false
			}
		}
	})
}

// readinessChecker is an optional capability: the local sqlite-vec index can say
// whether it exists yet, and writing into a never-created index would just error
// on every tick. Remote stores do not implement it and are driven blind — their
// first failure invalidates the memo and the next round re-resolves (ADR-0013's
// fallback ladder).
type readinessChecker interface{ Ready() bool }

// drainDirtyNotes embeds and un-indexes what the triggers queued, up to limit,
// and returns how many ids were resolved. Every gate below is a no-op decision:
// this must never block, fail, or surprise a note write.
//
// The whole path stays behind semantic_search: that key is the user's explicit
// consent to send note text to the configured endpoint (ADR-0012 风险项), so
// incremental indexing inherits it rather than introducing a second switch whose
// "off" state would silently leave the index stale.
func (s *Service) drainDirtyNotes(limit int) (int, error) {
	if limit <= 0 {
		limit = embedDrainBatch
	}
	// Queue first. This tick fires every 5s whether or not anything changed, and
	// the config reads below cost five-plus queries — short-circuiting on an empty
	// queue keeps the steady state at ONE cheap SELECT per tick instead of undoing
	// exactly the kind of wasted per-query work the vectorStore memo just removed.
	work, err := db.NextDirtyEmbedWork(s.db, limit)
	if err != nil {
		return 0, err
	}
	if len(work.ToEmbed) == 0 && len(work.Gone) == 0 {
		return 0, nil
	}
	if !s.semanticEnabled() {
		return 0, nil // work stays queued; nothing may leave the app while off
	}
	emb, ok := s.noteEmbedder()
	if !ok {
		return 0, nil // endpoint not configured yet; nothing to do
	}
	// A known dimension is required, not optional. The drainer must never call
	// Ensure() speculatively: Ensure with a different dim DROPS AND REBUILDS the
	// whole index (vecindex.go), so guessing a dim from a single response could
	// wipe every vector in the store. RebuildEmbeddings persists the dim it
	// learns, which is what unlocks incremental work.
	if emb.Dim <= 0 {
		if stored, ok := s.storedEmbeddingDim(); ok {
			emb.Dim = stored
		} else {
			return 0, nil
		}
	}

	store := s.vectorStore()
	if rc, ok := store.(readinessChecker); ok && !rc.Ready() {
		// Index not built yet: leave the queue alone. The first 重建索引 covers
		// these notes (it truncates the queue on success), so nothing is lost.
		return 0, nil
	}

	resolved := make([]int64, 0, len(work.ToEmbed)+len(work.Gone))

	// Deletions first: they cost no embedding request, and a stale vector for a
	// deleted note is an active correctness bug (search keeps recalling text the
	// user removed).
	for _, id := range work.Gone {
		if err := store.Delete(id); err != nil {
			s.invalidateVectorStore()
			s.logEmbedDrain("delete", err)
			break // the rest would fail the same way; retry next tick
		}
		resolved = append(resolved, id)
	}

	for start := 0; start < len(work.ToEmbed); start += embedBatch {
		end := start + embedBatch
		if end > len(work.ToEmbed) {
			end = len(work.ToEmbed)
		}
		chunk := work.ToEmbed[start:end]
		texts := make([]string, len(chunk))
		for i, w := range chunk {
			texts[i] = w.Text
		}
		vecs, err := emb.Embed(texts)
		if err != nil {
			// Endpoint-side failure: do NOT drop the store memo (the store is
			// fine) and do NOT consume the queue — these notes are retried.
			s.logEmbedDrain("embed", err)
			break
		}
		for i, vec := range vecs {
			if i >= len(chunk) {
				break
			}
			if len(vec) != emb.Dim {
				// The endpoint changed models under us. Writing a wrong-width
				// vector would either fail or, worse, be quietly mis-ranked by
				// KNN; a full rebuild is the honest repair, so we skip and say so
				// once per round rather than retry it every 5s.
				s.logEmbedDrain("dim", &embedDimMismatch{want: emb.Dim, got: len(vec)})
				continue
			}
			if err := store.Upsert(chunk[i].ID, vec); err != nil {
				s.invalidateVectorStore()
				s.logEmbedDrain("upsert", err)
				break
			}
			resolved = append(resolved, chunk[i].ID)
		}
	}

	if len(resolved) == 0 {
		return 0, nil
	}
	if err := db.ClearDirtyNoteIDs(s.db, resolved); err != nil {
		return 0, err
	}
	return len(resolved), nil
}

// embedDimMismatch names a skip condition that is not a transient failure, so it
// reads differently in the log than a 5xx would.
type embedDimMismatch struct{ want, got int }

func (e *embedDimMismatch) Error() string {
	return "embedding dim mismatch: configured " + strconv.Itoa(e.want) + ", endpoint returned " + strconv.Itoa(e.got)
}

// logEmbedDrain keeps one prefix so a user grepping the log can find every
// deferred item, and rate-limits the dim mismatch (it would otherwise repeat on
// every tick until the user rebuilds).
func (s *Service) logEmbedDrain(stage string, err error) {
	if _, ok := err.(*embedDimMismatch); ok {
		if s.embedDimWarned.Swap(true) {
			return
		}
		log.Printf("semantic incremental [%s]: %v — run 重建索引 after changing the embedding model", stage, err)
		return
	}
	log.Printf("semantic incremental [%s]: %v (queued work retained)", stage, err)
}

// storedEmbeddingDim resolves the dimension the index is actually built for:
// `embedding_dim` if the user (or a rebuild) set it, otherwise the dimension
// recorded in the local index's own meta table.
func (s *Service) storedEmbeddingDim() (int, bool) {
	if v, err := db.GetConfig(s.db, "embedding_dim"); err == nil {
		if dim, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && dim > 0 {
			return dim, true
		}
	}
	return db.StoredVectorDim(s.db)
}
