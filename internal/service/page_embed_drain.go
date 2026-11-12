package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"repotory/internal/db"
)

// Page-embedding rebuild + incremental drain (ADR-0018 lane 2, second half,
// schema v23). The page twin of search_semantic.go / embed_drain.go.
//
// It shares the NOTE embedding provider (endpoint, model, dim) rather than
// introducing a second one: the provider is axis A (hybrid.Embedder), independent
// of where vectors live (axis B), and a page's text is embedded exactly like a
// note's. What is deliberately NOT shared is the vector STORE — pages write to
// page_embeddings via the dedicated db.Page* helpers, never through
// vectordb.Store. That is the ADR's hard constraint: vectordb.Store's methods are
// keyed to note rowids (PutNoteEmbedding / KnnNoteIDs → note_embeddings), so
// routing pages through it would fuse page ids with note ids in one RRF space —
// the exact collision trap the evidence interleaver's comment calls out. A remote
// vector store for pages is deferred; the local sqlite-vec index is what lane 2
// ships, and a future remote page store is a separate decision (ADR-0013's seam).
//
// The whole path stays behind semantic_search: that key is the user's explicit
// consent to send text to the configured endpoint, so page embedding inherits it
// rather than introducing a second switch whose "off" would leave a stale index.
// Only APPROVED pages are embedded (ListPageEmbeddingInputs filters status), so a
// pending/rejected page never reaches the endpoint — ADR-0015's boundary.

// RebuildPageEmbeddings fully (re)embeds every approved page into page_embeddings.
// The page twin of RebuildEmbeddings. It learns the dimension from the note index
// (or embedding_dim config) rather than re-probing, because pages and notes share
// one provider: a rebuild that ran for notes already fixed the width, and probing
// again would spend an extra request. Returns the number of pages embedded.
//
// A page-less project (or a database with no approved pages) embeds nothing and
// still leaves a correctly-sized empty index behind, so the incremental drainer
// has somewhere to write. Only a completed rebuild clears the queue.
func (s *Service) RebuildPageEmbeddings() (int, error) {
	if !s.semanticEnabled() {
		return 0, fmt.Errorf("semantic search: enable semantic_search to embed pages")
	}
	emb, ok := s.noteEmbedder()
	if !ok {
		return 0, fmt.Errorf("semantic search: embedding endpoint not configured")
	}
	// Resolve the dimension the note index was built for. Falling back to the
	// page index's own meta, then erroring, keeps this from guessing a width —
	// guessing means calling Ensure with the wrong dim, which DROPS AND REBUILDS.
	dim := emb.Dim
	if dim <= 0 {
		if d, ok := s.storedEmbeddingDim(); ok {
			dim = d
		} else if d, ok := db.StoredPageVectorDim(s.db); ok {
			dim = d
		}
	}
	if dim <= 0 {
		return 0, fmt.Errorf("semantic search: unknown embedding dim; rebuild notes first (or set embedding_dim)")
	}
	if err := db.EnsurePageVectorIndex(s.db, dim); err != nil {
		return 0, err
	}
	// Full re-embed starts from an empty index, mirroring RebuildEmbeddings'
	// store.Clear: the evaluation tools rebuild over a copy of a live database
	// whose index already holds vectors, and rows for pages that left the
	// approved set since the copy was taken would otherwise survive.
	if err := db.ClearPageVectorIndex(s.db); err != nil {
		return 0, err
	}
	pages, err := db.ListPageEmbeddingInputs(s.db)
	if err != nil {
		return 0, err
	}
	embedded, bailed := 0, false
	for start := 0; start < len(pages); start += embedBatch {
		end := start + embedBatch
		if end > len(pages) {
			end = len(pages)
		}
		texts := make([]string, 0, end-start)
		ids := make([]int64, 0, end-start)
		for _, p := range pages[start:end] {
			texts = append(texts, p.Text)
			ids = append(ids, p.ID)
		}
		vecs, err := emb.Embed(texts)
		if err != nil {
			log.Printf("page semantic rebuild: embed batch %d: %v", start, err)
			bailed = true
			break
		}
		for i, vec := range vecs {
			if i >= len(ids) {
				break
			}
			if len(vec) != dim {
				log.Printf("page semantic rebuild: dim mismatch for page %d (want %d got %d), skipping", ids[i], dim, len(vec))
				continue
			}
			if err := db.PutPageEmbedding(s.db, ids[i], vec); err != nil {
				return embedded, err
			}
			embedded++
		}
	}
	// Only a completed rebuild may clear the queue: everything still queued after
	// a bailed run provably has NOT been embedded.
	if !bailed {
		if err := db.TruncateDirtyPageIDs(s.db); err != nil {
			log.Printf("page semantic rebuild: could not clear the incremental queue: %v", err)
		}
	} else {
		// A bailed rebuild may still have left stale vectors for pages that are no
		// longer approved; prune so un-approved pages do not linger in recall.
		if _, err := db.PrunePageEmbeddings(s.db); err != nil {
			log.Printf("page semantic rebuild: prune after bail: %v", err)
		}
	}
	log.Printf("page semantic rebuild: %d page(s) embedded at dim %d", embedded, dim)
	return embedded, nil
}

// pageDrainInterval mirrors embedDrainInterval: the page queue is durable, so a
// missed round is recovered by the next, and app exits never lose pending work.
const pageDrainInterval = 5 * time.Second

// startPageEmbedDrainer launches the page drain loop as tracked background work,
// the page twin of startEmbedDrainer. Reusing the same interval and batch keeps
// the two queues on one cadence and one cost profile.
func (s *Service) startPageEmbedDrainer() {
	s.bgGo("page-embed-drainer", func(ctx context.Context) {
		failures, escalated := 0, false
		for {
			wait := pageDrainInterval
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
			if w, e := db.NextDirtyPageEmbedWork(s.db, 1); e == nil {
				pending = len(w.ToEmbed) > 0 || len(w.Gone) > 0
			}
			n, err := s.drainDirtyPages(embedDrainBatch)
			switch {
			case drainRoundFailed(err, pending, n):
				failures++
				if err != nil {
					log.Printf("page semantic incremental [deferred]: %v", err)
				}
				if !escalated && failures >= embedDrainEscalateAfter {
					log.Printf("WARN page semantic incremental: %d consecutive rounds made no progress; backing off", failures)
					escalated = true
				}
			case n > 0:
				failures, escalated = 0, false
				log.Printf("page semantic incremental: %d page(s) indexed", n)
			default:
				failures, escalated = 0, false
			}
		}
	})
}

// drainDirtyPages embeds and un-indexes what the page triggers queued, up to
// limit, and returns how many ids were resolved. Every gate is a no-op decision:
// this must never block, fail, or surprise a page write. Twin of drainDirtyNotes,
// but writing to the separate page_embeddings index.
func (s *Service) drainDirtyPages(limit int) (int, error) {
	if limit <= 0 {
		limit = embedDrainBatch
	}
	// Queue first: short-circuit on an empty queue keeps the steady state cheap,
	// and the config/embedder reads below only happen when there is real work.
	work, err := db.NextDirtyPageEmbedWork(s.db, limit)
	if err != nil {
		return 0, err
	}
	if len(work.ToEmbed) == 0 && len(work.Gone) == 0 {
		return 0, nil
	}
	if !s.semanticEnabled() {
		return 0, nil // inherited gate: no consent, no endpoint spend
	}
	emb, ok := s.noteEmbedder()
	if !ok {
		return 0, nil
	}
	dim := emb.Dim
	if dim <= 0 {
		if d, ok := s.storedEmbeddingDim(); ok {
			dim = d
		} else if d, ok := db.StoredPageVectorDim(s.db); ok {
			dim = d
		}
	}
	if dim <= 0 || !db.PageVectorIndexReady(s.db) {
		// Index not built yet (or dim unknown): leave the queue alone. The first
		// RebuildPageEmbeddings covers these pages and truncates the queue.
		return 0, nil
	}

	resolved := make([]int64, 0, len(work.ToEmbed)+len(work.Gone))

	// Deletions / un-approvals first: they cost no embedding request, and a stale
	// vector for a since-pending page is an active correctness bug (recall keeps
	// surfacing content that left the approved set).
	for _, id := range work.Gone {
		if err := db.DeletePageEmbedding(s.db, id); err != nil {
			return 0, err
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
			// Endpoint-side failure: do NOT consume the queue — these pages retry
			// next tick, exactly as the note drainer leaves failed work queued.
			log.Printf("page semantic incremental [embed]: %v (queued work retained)", err)
			break
		}
		for i, vec := range vecs {
			if i >= len(chunk) {
				break
			}
			if len(vec) != dim {
				log.Printf("page semantic incremental [dim]: page %d width %d != %d — rebuild pages after changing the model", chunk[i].ID, len(vec), dim)
				continue
			}
			if err := db.PutPageEmbedding(s.db, chunk[i].ID, vec); err != nil {
				return 0, err
			}
			resolved = append(resolved, chunk[i].ID)
		}
	}

	if len(resolved) == 0 {
		return 0, nil
	}
	if err := db.ClearDirtyPageIDs(s.db, resolved); err != nil {
		return 0, err
	}
	return len(resolved), nil
}
