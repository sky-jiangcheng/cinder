// Package hybrid holds the vendor-neutral core of M3 semantic search
// (ADR-0012): the Embedder seam and Reciprocal-Rank-Fusion merging. It is
// deliberately free of any embedding implementation and of SQLite/storage, so
// the search machinery can be built and unit-tested while the open product
// decision — how vectors are produced (local model vs optional remote API) —
// is still pending (ADR-0012 决策 2). Nothing here is wired into the app yet;
// semantic search ships only after the A/B evaluation gate clears, off by
// default.
package hybrid

// Embedder converts text into fixed-dimension vectors. Implementations must be
// deterministic for a given input so that re-embedding a stored note yields the
// same vector and index rebuilds are idempotent.
type Embedder interface {
	// Dimension is the length of each returned vector.
	Dimension() int
	// Embed returns one vector per input text, in the same order.
	Embed(texts []string) ([][]float32, error)
}

// RankList is one retrieval system's ordered result set, most relevant first.
// Elements are note (or document) ids.
type RankList []int64

// WeightedRankList is a ranked list carrying a fusion weight. A weight scales
// every contribution the list makes to a document's RRF score, letting one arm
// (e.g. vector recall) speak louder than another without changing its internal
// order. A weight <= 0 is treated as 1.0.
type WeightedRankList struct {
	IDs    RankList
	Weight float64
}

// DefaultRRFK is the conventional Reciprocal Rank Fusion constant; 60 damps the
// influence of any single list's top hit and is robust across list lengths.
const DefaultRRFK = 60

// entry accumulates one document's fused score and its first-appearance order.
type entry struct {
	id    int64
	score float64
	seen  int
}

// FuseRRF merges any number of ranked lists into one via Reciprocal Rank Fusion.
// A document's score is sum_i 1 / (k + rank_i) over the lists it appears in
// (rank is 1-based). k <= 0 uses DefaultRRFK. Ordering is by score descending,
// ties broken by earliest appearance so the result is deterministic.
//
// It is the equal-weight special case of FuseRRFWeighted; callers that need one
// arm to outweigh another use that directly.
func FuseRRF(k int, lists ...RankList) []int64 {
	weighted := make([]WeightedRankList, len(lists))
	for i, l := range lists {
		weighted[i] = WeightedRankList{IDs: l, Weight: 1.0}
	}
	return FuseRRFWeighted(k, weighted...)
}

// FuseRRFWeighted is FuseRRF with per-list weights: a document's score is
// sum_i weight_i / (k + rank_i). k <= 0 uses DefaultRRFK; a weight <= 0 is
// treated as 1.0. Ordering and tie-breaking are identical to FuseRRF.
func FuseRRFWeighted(k int, lists ...WeightedRankList) []int64 {
	if k <= 0 {
		k = DefaultRRFK
	}
	index := make(map[int64]int) // id -> position in entries
	entries := make([]entry, 0)
	for _, list := range lists {
		w := list.Weight
		if w <= 0 {
			w = 1.0
		}
		for rank, id := range list.IDs {
			if pos, ok := index[id]; ok {
				entries[pos].score += w / float64(k+rank+1)
				continue
			}
			index[id] = len(entries)
			entries = append(entries, entry{
				id:    id,
				score: w / float64(k+rank+1),
				seen:  len(entries),
			})
		}
	}
	sortEntries(entries)
	out := make([]int64, len(entries))
	for i, e := range entries {
		out[i] = e.id
	}
	return out
}

// sortEntries orders by score desc, then by first appearance (seen) for a
// stable, deterministic result without importing "sort".
func sortEntries(entries []entry) {
	for i := 1; i < len(entries); i++ {
		cur := entries[i]
		j := i - 1
		// Shift larger elements right while cur should rank before them.
		for j >= 0 && lessEntry(cur, entries[j]) {
			entries[j+1] = entries[j]
			j--
		}
		entries[j+1] = cur
	}
}

// lessEntry reports whether a should sort before b: higher score first, then
// lower "seen" first (stable tie-break by first appearance).
func lessEntry(a, b entry) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	return a.seen < b.seen
}
