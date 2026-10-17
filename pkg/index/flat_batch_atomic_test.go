package index

import "testing"

// A batch with a wrong-sized vector must be rejected whole: the vectors
// before it must not be left in the index.
func TestFlatIndexBatchInsertRejectsWholeBatch(t *testing.T) {
	f := NewFlatIndex(2, nil)
	if err := f.Insert("existing", []float32{1, 2}); err != nil {
		t.Fatal(err)
	}

	err := f.BatchInsert(
		[]string{"a", "existing", "bad"},
		[][]float32{{1, 1}, {9, 9}, {1}},
	)
	if err == nil {
		t.Fatal("expected a dimension mismatch error")
	}
	if got := f.Size(); got != 1 {
		t.Fatalf("size after rejected batch = %d, want 1", got)
	}
	if _, ok := f.GetVector("a"); ok {
		t.Error("vector before the bad one was inserted")
	}
	if v, _ := f.GetVector("existing"); v[0] != 1 || v[1] != 2 {
		t.Errorf("existing vector overwritten by rejected batch: %v", v)
	}
}
