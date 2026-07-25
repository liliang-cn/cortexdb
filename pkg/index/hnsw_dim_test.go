package index

import (
	"math"
	"strings"
	"testing"
)

// A store can hold several vector dimensionalities at once — an embedding model change
// leaves the old vectors behind, and collections declare their own sizes. Comparing
// across them used to panic inside an HNSW insert ("index out of range [768] with
// length 768"), which killed the process in the middle of a write.

func TestDistanceFunctionsRejectMismatchedDimensions(t *testing.T) {
	long := make([]float32, 4096)
	short := make([]float32, 768)
	for i := range long {
		long[i] = 0.5
	}
	for i := range short {
		short[i] = 0.5
	}

	// The panic came from iterating the longer vector while indexing the shorter one,
	// so check both orders.
	if got := CosineDistance(long, short); got != 1.0 {
		t.Errorf("CosineDistance(4096, 768) = %v, want 1.0 (incomparable)", got)
	}
	if got := CosineDistance(short, long); got != 1.0 {
		t.Errorf("CosineDistance(768, 4096) = %v, want 1.0 (incomparable)", got)
	}
	if got := EuclideanDistance(long, short); !math.IsInf(float64(got), 1) {
		t.Errorf("EuclideanDistance(4096, 768) = %v, want +Inf", got)
	}
	if got := DotProductDistance(short, long); !math.IsInf(float64(got), 1) {
		t.Errorf("DotProductDistance(768, 4096) = %v, want +Inf", got)
	}
	// Empty vectors are incomparable too, and must not divide by zero.
	if got := CosineDistance(nil, nil); got != 1.0 {
		t.Errorf("CosineDistance(nil, nil) = %v, want 1.0", got)
	}

	// Equal dimensions still compute normally.
	if got := CosineDistance(short, short); got > 1e-6 {
		t.Errorf("CosineDistance of identical vectors = %v, want ~0", got)
	}
}

func TestInsertRejectsMismatchedDimensionInsteadOfCorruptingTheGraph(t *testing.T) {
	idx := NewHNSW(16, 200, CosineDistance)

	first := make([]float32, 768)
	first[0] = 1
	if err := idx.Insert("a", first); err != nil {
		t.Fatalf("first insert failed: %v", err)
	}
	if idx.Dim != 768 {
		t.Errorf("index learned dimension %d, want 768", idx.Dim)
	}

	second := make([]float32, 768)
	second[1] = 1
	if err := idx.Insert("b", second); err != nil {
		t.Fatalf("same-dimension insert failed: %v", err)
	}

	// The insert that used to panic deep inside neighbour selection.
	wrong := make([]float32, 4096)
	wrong[0] = 1
	err := idx.Insert("c", wrong)
	if err == nil {
		t.Fatal("insert of a 4096-dim vector into a 768-dim index was accepted")
	}
	if !strings.Contains(err.Error(), "dimensions") {
		t.Errorf("error %q does not explain the dimension mismatch", err)
	}
	if _, exists := idx.Nodes["c"]; exists {
		t.Error("rejected vector was still added to the graph")
	}
	if len(idx.Nodes) != 2 {
		t.Errorf("graph holds %d nodes, want 2", len(idx.Nodes))
	}

	// The index still works after the rejection.
	results, _ := idx.Search(first, 2, 50)
	if len(results) == 0 {
		t.Error("search returned nothing after a rejected insert")
	}

	if err := idx.Insert("d", nil); err == nil {
		t.Error("empty vector was accepted")
	}
}
