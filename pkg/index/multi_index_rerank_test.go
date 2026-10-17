package index

import "testing"

// fixedIndex answers every search with the same ranked list.
type fixedIndex struct {
	ids   []string
	dists []float32
}

func (f fixedIndex) Insert(string, []float32) error { return nil }
func (f fixedIndex) Delete(string) error            { return nil }
func (f fixedIndex) Size() int                      { return len(f.ids) }
func (f fixedIndex) Search(_ []float32, k int) ([]string, []float32) {
	if k > len(f.ids) {
		k = len(f.ids)
	}
	return f.ids[:k], f.dists[:k]
}

func rerankIndex(secondary VectorIndex) *MultiIndex {
	cfg := MultiIndexConfig{
		PrimaryIndex:    IndexTypeHNSW,
		CombineStrategy: StrategyRerank,
	}
	m := NewMultiIndex(cfg)
	m.AddIndex(IndexTypeHNSW, fixedIndex{
		ids:   []string{"a", "b", "c"},
		dists: []float32{0.1, 0.2, 0.3},
	})
	if secondary != nil {
		m.config.SecondaryIndices = []IndexType{IndexTypeFlat}
		m.AddIndex(IndexTypeFlat, secondary)
	}
	return m
}

// Without secondary indices the rerank strategy must keep the primary order
// and distances rather than scoring every candidate zero.
func TestMultiIndexRerankWithoutSecondariesKeepsPrimaryOrder(t *testing.T) {
	m := rerankIndex(nil)
	for run := 0; run < 20; run++ {
		ids, dists := m.Search(nil, 3)
		want := []string{"a", "b", "c"}
		for i := range want {
			if ids[i] != want[i] || dists[i] == 0 {
				t.Fatalf("run %d: got %v %v, want %v with primary distances", run, ids, dists, want)
			}
		}
	}
}

// A candidate the secondary index does not return must not outrank candidates
// it confirmed with a real distance.
func TestMultiIndexRerankUnconfirmedCandidateNotRankedFirst(t *testing.T) {
	m := rerankIndex(fixedIndex{
		ids:   []string{"a", "b"},
		dists: []float32{0.1, 0.2},
	})
	ids, dists := m.Search(nil, 3)
	want := []string{"a", "b", "c"}
	if len(ids) != 3 {
		t.Fatalf("got %v %v, want 3 results", ids, dists)
	}
	for i := range want {
		if ids[i] != want[i] || dists[i] <= 0 {
			t.Fatalf("got %v %v, want %v with real distances", ids, dists, want)
		}
	}
}
