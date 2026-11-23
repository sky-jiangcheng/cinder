package hybrid

import (
	"reflect"
	"testing"
)

func TestFuseRRFRanksSharedDocFirst(t *testing.T) {
	a := RankList{1, 2, 3}
	b := RankList{3, 4, 1}
	got := FuseRRF(60, a, b)
	// ids 1 and 3 appear at rank 1 in one list and rank 3 in the other -> equal,
	// highest scores; tie broken by first appearance (1 before 3). ids 2 and 4
	// each appear once at rank 2 -> equal lower scores (2 before 4).
	want := []int64{1, 3, 2, 4}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FuseRRF = %v, want %v", got, want)
	}
}

func TestFuseRRFEmpty(t *testing.T) {
	if got := FuseRRF(60); len(got) != 0 {
		t.Errorf("no lists = %v, want empty", got)
	}
	if got := FuseRRF(60, RankList{}, nil); len(got) != 0 {
		t.Errorf("empty lists = %v, want empty", got)
	}
}

func TestFuseRRFPreservesSingleListOrder(t *testing.T) {
	got := FuseRRF(60, RankList{5, 6, 7})
	want := []int64{5, 6, 7}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("single list = %v, want %v", got, want)
	}
}

func TestFuseRRFZeroKUsesDefault(t *testing.T) {
	a, b := RankList{1, 2, 3}, RankList{3, 4, 1}
	def := FuseRRF(DefaultRRFK, a, b)
	zero := FuseRRF(0, a, b)
	neg := FuseRRF(-5, a, b)
	if !reflect.DeepEqual(def, zero) || !reflect.DeepEqual(def, neg) {
		t.Fatalf("k<=0 must match default: def=%v zero=%v neg=%v", def, zero, neg)
	}
}

func TestFuseRRFDeterministicAcrossCalls(t *testing.T) {
	a, b := RankList{10, 20, 30}, RankList{30, 99, 10}
	first := FuseRRF(60, a, b)
	for i := 0; i < 5; i++ {
		if !reflect.DeepEqual(FuseRRF(60, a, b), first) {
			t.Fatalf("non-deterministic fusion on call %d: %v vs %v", i, FuseRRF(60, a, b), first)
		}
	}
}

func TestFuseRRFWeightedScalesContributions(t *testing.T) {
	a := RankList{1, 2}
	b := RankList{2, 1}
	// Equal weights tie (1 and 3 symmetric) -> first appearance wins (1 first).
	equal := FuseRRFWeighted(10, WeightedRankList{IDs: a, Weight: 1}, WeightedRankList{IDs: b, Weight: 1})
	if !reflect.DeepEqual(equal, []int64{1, 2}) {
		t.Fatalf("equal weights = %v, want [1 2] (appearance tie-break)", equal)
	}
	// Doubling b's weight lifts id 2 (b's rank-1 hit) above id 1.
	weighted := FuseRRFWeighted(10, WeightedRankList{IDs: a, Weight: 1}, WeightedRankList{IDs: b, Weight: 2})
	if !reflect.DeepEqual(weighted, []int64{2, 1}) {
		t.Fatalf("weighted = %v, want [2 1] (heavier list's top hit wins)", weighted)
	}
}

func TestFuseRRFWeightedNonPositiveWeightIsOne(t *testing.T) {
	a, b := RankList{1, 2, 3}, RankList{3, 4, 1}
	want := FuseRRF(60, a, b)
	got := FuseRRFWeighted(60, WeightedRankList{IDs: a, Weight: 0}, WeightedRankList{IDs: b, Weight: -1})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("weight<=0 must equal weight 1: got %v want %v", got, want)
	}
}

func TestFuseRRFEqualsUnitWeighted(t *testing.T) {
	a, b := RankList{10, 20, 30}, RankList{30, 99, 10}
	plain := FuseRRF(60, a, b)
	weighted := FuseRRFWeighted(60, WeightedRankList{IDs: a, Weight: 1}, WeightedRankList{IDs: b, Weight: 1})
	if !reflect.DeepEqual(plain, weighted) {
		t.Fatalf("FuseRRF must equal unit-weight FuseRRFWeighted: %v vs %v", plain, weighted)
	}
}

// fakeEmbedder proves the Embedder seam is implementable with a trivial,
// deterministic strategy (NOT a production embedding).
type fakeEmbedder struct{}

func (fakeEmbedder) Dimension() int { return 2 }
func (fakeEmbedder) Embed(texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, s := range texts {
		out[i] = []float32{float32(len(s)), 0}
	}
	return out, nil
}

func TestEmbedderSeamSatisfied(t *testing.T) {
	var e Embedder = fakeEmbedder{}
	if e.Dimension() != 2 {
		t.Fatalf("dimension = %d", e.Dimension())
	}
	vecs, err := e.Embed([]string{"ab", "abcd"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 || vecs[0][0] != 2 || vecs[1][0] != 4 {
		t.Fatalf("unexpected vectors: %v", vecs)
	}
}
