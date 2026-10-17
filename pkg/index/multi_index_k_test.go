package index

import "testing"

// kStubIndex answers every search with the same two results.
type kStubIndex struct {
	ids   []string
	dists []float32
}

func (kStubIndex) Insert(string, []float32) error { return nil }
func (kStubIndex) Delete(string) error            { return nil }
func (s kStubIndex) Size() int                    { return len(s.ids) }
func (s kStubIndex) Search(_ []float32, k int) ([]string, []float32) {
	if k > len(s.ids) {
		k = len(s.ids)
	}
	return s.ids[:k], s.dists[:k]
}

// A zero or negative k must come back empty, never panic, for every
// MultiIndex strategy and for HybridIndex.
func TestMultiIndexAndHybridNonPositiveK(t *testing.T) {
	strategies := []CombineStrategy{StrategyPrimaryOnly, StrategyMergeAll, StrategyRerank, StrategyVoting}
	for _, strategy := range strategies {
		m := NewMultiIndex(MultiIndexConfig{PrimaryIndex: IndexTypeFlat, CombineStrategy: strategy})
		m.AddIndex(IndexTypeFlat, kStubIndex{ids: []string{"a", "b"}, dists: []float32{0.1, 0.2}})
		for _, k := range []int{0, -1} {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%s with k=%d panicked: %v", strategy, k, r)
					}
				}()
				if ids, _ := m.Search(nil, k); len(ids) != 0 {
					t.Errorf("%s with k=%d returned %v, want none", strategy, k, ids)
				}
			}()
		}
	}

	h := NewHybridIndex(2, 8, 1)
	if err := h.Train([][]float32{{0, 0}}); err != nil {
		t.Fatal(err)
	}
	if err := h.Insert("a", []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []int{0, -1} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("hybrid with k=%d panicked: %v", k, r)
				}
			}()
			if ids, _ := h.Search([]float32{1, 0}, k); len(ids) != 0 {
				t.Errorf("hybrid with k=%d returned %v, want none", k, ids)
			}
		}()
	}
}
