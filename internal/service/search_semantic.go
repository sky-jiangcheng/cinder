package service

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"

	"repotory/internal/db"
	"repotory/internal/domain"
	"repotory/internal/search/hybrid"
	"repotory/internal/search/vectordb"
)

// M3-A semantic search wiring (ADR-0012). Default OFF: nothing here runs unless
// `semantic_search=="1"`. Even when enabled, every failure (endpoint not set,
// embed call error, vector index absent) degrades gracefully to the existing
// lexical result — so enabling it can never make search return less than before.

const (
	semanticConfigKey = "semantic_search"
	// 16 texts per request: remote endpoints have a large fixed per-request
	// overhead (~15s observed) plus per-token cost, and a 64-text batch of long
	// notes blew a 60s client timeout in practice. Smaller batches trade a few
	// more requests for staying inside the timeout on slow endpoints.
	embedBatch   = 16
	embedRecallK = 30 // vector ids fused with the lexical hits
	// noteRRFK and vectorFuseWeight are the ADR-0071 fusion tuning, measured on
	// the 70-case gate (split-half validated): a sharper k=10 plus a 1.5x vector
	// weight lifts ndcg@10 ~0.81->0.87 with zero recall regression. k=60 (the
	// generic DefaultRRFK) left the vector arm under-weighted; embedRecallK=30
	// (not 20) is what keeps the higher vector weight from dropping a lexical
	// hit out of the top-10. The temporal arm keeps weight 1.0 and the same k.
	noteRRFK         = 10
	vectorFuseWeight = 1.5
)

// semanticEnabled reports whether the (opt-in) semantic search is on.
func (s *Service) semanticEnabled() bool {
	v, err := db.GetConfig(s.db, semanticConfigKey)
	return err == nil && v == "1"
}

// noteEmbedder builds a RemoteEmbedder from config; ok=false when base_url or
// model is unset. dim may be 0 ("learn it from the first embedding").
func (s *Service) noteEmbedder() (*hybrid.RemoteEmbedder, bool) {
	base, _ := db.GetConfig(s.db, "embedding_base_url")
	model, _ := db.GetConfig(s.db, "embedding_model")
	if strings.TrimSpace(base) == "" || strings.TrimSpace(model) == "" {
		return nil, false
	}
	key, _ := db.GetConfig(s.db, "embedding_api_key")
	dimStr, _ := db.GetConfig(s.db, "embedding_dim")
	dim, _ := strconv.Atoi(dimStr)
	return &hybrid.RemoteEmbedder{BaseURL: base, Model: model, APIKey: key, Dim: dim}, true
}

// vectorStoreCache memoises the resolved vector store (axis B). It lives here
// rather than as loose fields on Service so this file keeps ownership of the
// vector-store concern (and service.go needs no vectordb import).
type vectorStoreCache struct {
	mu    sync.Mutex
	store vectordb.Store
}

// vectorStore resolves the configured vector STORE (axis B, ADR-0013), falling
// back to local sqlite-vec when remote is unset/unreachable. Reading the real
// (unmasked) api keys here is intentional: GetConfig masks them for the UI, but
// the store needs them to connect.
//
// The result is memoised. Resolving it re-reads four config rows and — for a
// remote backend — runs vectordb.Open, whose factory issues an HTTP
// reachability probe. On the search path that was an extra network round trip
// per query, which is what made semantic search slower than lexical whenever a
// remote store was configured.
//
// The memo is dropped by invalidateVectorStore on two triggers: a
// `vector_store*` config write (otherwise a reconfigured endpoint would keep
// being ignored until restart) and any store error (so a remote that dies
// mid-session is re-resolved — and falls back to local — instead of being
// hammered with the same dead handle).
func (s *Service) vectorStore() vectordb.Store {
	s.vecStore.mu.Lock()
	defer s.vecStore.mu.Unlock()
	if s.vecStore.store != nil {
		return s.vecStore.store
	}
	kind, _ := db.GetConfig(s.db, "vector_store")
	url, _ := db.GetConfig(s.db, "vector_store_url")
	key, _ := db.GetConfig(s.db, "vector_store_api_key")
	coll, _ := db.GetConfig(s.db, "vector_store_collection")
	s.vecStore.store = vectordb.Open(s.db, kind, url, key, coll)
	return s.vecStore.store
}

// invalidateVectorStore drops the memoised store. Resolution itself never fails
// (Open falls back to local), so this is safe to call from error paths.
func (s *Service) invalidateVectorStore() {
	s.vecStore.mu.Lock()
	s.vecStore.store = nil
	s.vecStore.mu.Unlock()
}

// RebuildEmbeddings fully (re)embeds every note into the vector index. Used when
// the user turns on semantic search or changes the endpoint/model. Returns the
// number of notes embedded. If embedding_dim is unset it is learned from the
// first embedding. The API key is read here (never returned to the UI).
func (s *Service) RebuildEmbeddings() (int, error) {
	emb, ok := s.noteEmbedder()
	if !ok {
		return 0, fmt.Errorf("semantic search: embedding endpoint not configured")
	}
	store := s.vectorStore()
	notes, err := db.ListNoteEmbeddingInputs(s.db)
	if err != nil {
		return 0, err
	}
	if emb.Dim <= 0 {
		if len(notes) == 0 {
			return 0, fmt.Errorf("semantic search: no notes to infer embedding dim from; set embedding_dim")
		}
		probe, perr := emb.Embed([]string{notes[0].Text})
		if perr != nil {
			return 0, perr
		}
		if len(probe) == 0 || len(probe[0]) == 0 {
			return 0, fmt.Errorf("semantic search: endpoint returned an empty vector")
		}
		emb.Dim = len(probe[0])
		// Persist what was just learned. The incremental drainer refuses to work
		// without a known dimension (guessing one means calling Ensure with the
		// wrong width, which DROPS AND REBUILDS the whole index), so this single
		// write is what makes "turn semantic search on, rebuild once" leave the
		// store self-maintaining afterwards instead of silently never updating.
		if err := db.SetConfig(s.db, "embedding_dim", strconv.Itoa(emb.Dim)); err != nil {
			log.Printf("semantic rebuild: could not persist learned embedding_dim: %v", err)
		}
	}
	if err := store.Ensure(emb.Dim); err != nil {
		s.invalidateVectorStore()
		return 0, err
	}
	if err := store.Clear(emb.Dim); err != nil {
		s.invalidateVectorStore()
		return 0, err
	}
	embedded := 0
	var bailErr error
	for start := 0; start < len(notes); start += embedBatch {
		end := start + embedBatch
		if end > len(notes) {
			end = len(notes)
		}
		texts := make([]string, 0, end-start)
		ids := make([]int64, 0, end-start)
		for _, n := range notes[start:end] {
			texts = append(texts, n.Text)
			ids = append(ids, n.ID)
		}
		vecs, err := emb.Embed(texts)
		if err != nil {
			log.Printf("semantic rebuild: embed batch %d: %v", start, err)
			bailErr = err
			break
		}
		for i, vec := range vecs {
			if i < len(ids) {
				if err := store.Upsert(ids[i], vec); err != nil {
					s.invalidateVectorStore()
					return embedded, err
				}
				embedded++
			}
		}
	}
	log.Printf("semantic rebuild: %s holds %d vectors", store.Name(), embedded)
	// Only a completed rebuild may clear the incremental queue: everything still
	// in it after a bailed run has provably NOT been embedded, and dropping those
	// rows would silently lose the user's pending writes.
	if bailErr == nil {
		if err := db.TruncateDirtyNoteIDs(s.db); err != nil {
			log.Printf("semantic rebuild: could not clear the incremental queue: %v", err)
		}
		return embedded, nil
	}
	// A bailed run must surface as an error: returning (partial, nil) let the UI
	// report "rebuilt N" as success while the endpoint was actually down.
	return embedded, fmt.Errorf("semantic rebuild: stopped after %d/%d notes: %w", embedded, len(notes), bailErr)
}

// fuseSemantic merges vector recall into the lexical hits via RRF. Returns the
// input unchanged when semantic search is off, unconfigured, or any embed/KNN
// step fails — never reducing results. The KNN runs against the configured
// vector STORE (local sqlite-vec by default, remote Qdrant if set and
// reachable; ADR-0013).
func (s *Service) fuseSemantic(base []domain.SearchHit, query string) []domain.SearchHit {
	if !s.semanticEnabled() {
		return base
	}
	emb, ok := s.noteEmbedder()
	if !ok {
		return base
	}
	vecs, err := emb.Embed([]string{query})
	if err != nil || len(vecs) == 0 {
		if err != nil {
			log.Printf("semantic query embed failed; using lexical only: %v", err)
		}
		return base
	}
	ids, err := s.vectorStore().Search(vecs[0], embedRecallK)
	if err != nil {
		// Drop the memo so the next query re-resolves the store (and falls back
		// to local) instead of reusing a handle that just failed. Before the
		// cache existed every query built a fresh store, so this path was
		// self-healing by accident; silence here would have been a regression.
		s.invalidateVectorStore()
		log.Printf("semantic vector search failed; using lexical only: %v", err)
		return base
	}
	if len(ids) == 0 {
		return base
	}
	return s.fuseRankList(base, ids, query, vectorFuseWeight, "semantic")
}

// fuseRankList merges a ranked id list into the base hits via weighted RRF,
// fetching any ids the base does not already hold. Shared by the vector arm
// (fuseSemantic, weight vectorFuseWeight) and the temporal arm (fuseTemporal,
// weight 1.0) so both extend the result identically; the weight is the only
// per-arm difference (ADR-0071). channel names the arm for the hit's Recall
// provenance (ADR-0072): every id the arm surfaced gets the channel appended,
// whether or not it already sat in the base list.
func (s *Service) fuseRankList(base []domain.SearchHit, ids []int64, query string, weight float64, channel string) []domain.SearchHit {
	baseIDs := make(hybrid.RankList, 0, len(base))
	byID := make(map[int64]domain.SearchHit, len(base))
	for _, h := range base {
		baseIDs = append(baseIDs, h.ID)
		byID[h.ID] = h
	}
	merged := hybrid.FuseRRFWeighted(noteRRFK,
		hybrid.WeightedRankList{IDs: baseIDs, Weight: 1.0},
		hybrid.WeightedRankList{IDs: hybrid.RankList(ids), Weight: weight})

	// Fetch any ids we don't already hold.
	var missing []int64
	for _, id := range merged {
		if _, have := byID[id]; !have {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		extra, err := db.NoteHitsByIDs(s.db, missing, query)
		if err == nil {
			for _, h := range extra {
				byID[h.ID] = h
			}
		}
	}
	for _, id := range ids {
		if h, have := byID[id]; have {
			byID[id] = appendRecallChannel(h, channel)
		}
	}
	out := make([]domain.SearchHit, 0, len(merged))
	for _, id := range merged {
		if h, have := byID[id]; have {
			out = append(out, h)
		}
	}
	return out
}
