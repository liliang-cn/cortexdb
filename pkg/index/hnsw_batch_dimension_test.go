package index

import (
	"bytes"
	"testing"
)

func TestHNSWBatchDimensionContract(t *testing.T) {
	type entry = struct {
		ID     string
		Vector []float32
	}
	for _, parallel := range []bool{false, true} {
		name := "InsertBatch"
		if parallel {
			name = "InsertBatchParallel"
		}
		t.Run(name, func(t *testing.T) {
			insert := func(h *HNSW, batch []entry) error {
				if parallel {
					return h.InsertBatchParallel(batch, 2)
				}
				return h.InsertBatch(batch)
			}
			t.Run("learns dimension and keeps valid roundtrip", func(t *testing.T) {
				h := NewHNSW(8, 32, CosineDistance)
				if err := insert(h, []entry{{"a", []float32{1, 0}}, {"b", []float32{0, 1}}}); err != nil {
					t.Fatal(err)
				}
				if h.Dim != 2 {
					t.Errorf("batch learned dimension %d, want 2", h.Dim)
				}
				if err := h.Insert("wrong", []float32{1, 0, 0}); err == nil {
					t.Error("single Insert accepted a different dimension after batch")
				}
				if len(h.Nodes) != 2 {
					t.Fatalf("graph has %d nodes, want 2", len(h.Nodes))
				}
				var saved bytes.Buffer
				if err := h.Save(&saved); err != nil {
					t.Fatal(err)
				}
				restored := NewHNSW(8, 32, CosineDistance)
				if err := restored.Load(&saved); err != nil {
					t.Fatal(err)
				}
				ids, _ := restored.Search([]float32{1, 0}, 1, 16)
				if len(ids) != 1 || ids[0] != "a" {
					t.Fatalf("roundtrip nearest IDs %v, want [a]", ids)
				}
			})
			t.Run("rejects before partial writes", func(t *testing.T) {
				h := NewHNSW(8, 32, CosineDistance)
				if err := h.Insert("original", []float32{1, 0}); err != nil {
					t.Fatal(err)
				}
				batch := []entry{{"valid", []float32{0, 1}}, {"wrong", []float32{1, 0, 0}}}
				if err := insert(h, batch); err == nil {
					t.Error("batch admitted mismatched dimensions")
				}
				if h.Dim != 2 || len(h.Nodes) != 1 {
					t.Fatalf("rejected batch changed graph: dim=%d nodes=%d", h.Dim, len(h.Nodes))
				}
				// The store's rebuild fallback retries each entry with Insert.
				for _, v := range batch {
					_ = h.Insert(v.ID, v.Vector)
				}
				if len(h.Nodes) != 2 || h.Nodes["wrong"] != nil {
					t.Fatal("fallback did not retain only compatible vectors")
				}
			})
			t.Run("rejects empty and inconsistent new graph", func(t *testing.T) {
				for _, batch := range [][]entry{{{"empty", nil}}, {{"a", []float32{1, 0}}, {"b", []float32{1}}}} {
					h := NewHNSW(8, 32, CosineDistance)
					if err := insert(h, batch); err == nil {
						t.Error("invalid new batch accepted")
					}
					if h.Dim != 0 || len(h.Nodes) != 0 || h.EntryPoint != "" {
						t.Fatalf("invalid batch left graph state: dim=%d nodes=%d entry=%q", h.Dim, len(h.Nodes), h.EntryPoint)
					}
				}
			})
		})
	}
}
