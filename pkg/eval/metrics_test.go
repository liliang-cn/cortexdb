package eval

import (
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestRecallAtK(t *testing.T) {
	retrieved := []string{"a", "x", "b", "y", "c"}
	relevant := []string{"a", "b", "c"}
	cases := map[int]float64{1: 1.0 / 3, 3: 2.0 / 3, 5: 1.0, 10: 1.0}
	for k, want := range cases {
		if got := RecallAtK(retrieved, relevant, k); !approx(got, want) {
			t.Errorf("RecallAtK(k=%d)=%v want %v", k, got, want)
		}
	}
	if got := RecallAtK(retrieved, nil, 5); got != 0 {
		t.Errorf("RecallAtK with no relevant = %v, want 0", got)
	}
}

func TestPrecisionAtK(t *testing.T) {
	retrieved := []string{"a", "x", "b"}
	relevant := []string{"a", "b", "c"}
	if got := PrecisionAtK(retrieved, relevant, 3); !approx(got, 2.0/3) {
		t.Errorf("PrecisionAtK=%v want %v", got, 2.0/3)
	}
	if got := PrecisionAtK(retrieved, relevant, 0); got != 0 {
		t.Errorf("PrecisionAtK(0)=%v want 0", got)
	}
}

func TestReciprocalRank(t *testing.T) {
	if got := ReciprocalRank([]string{"x", "a", "b"}, []string{"a"}); !approx(got, 0.5) {
		t.Errorf("RR=%v want 0.5", got)
	}
	if got := ReciprocalRank([]string{"x", "y"}, []string{"a"}); got != 0 {
		t.Errorf("RR (miss)=%v want 0", got)
	}
	if got := ReciprocalRank([]string{"a"}, []string{"a"}); !approx(got, 1.0) {
		t.Errorf("RR (first)=%v want 1", got)
	}
}

func TestNDCGAtK(t *testing.T) {
	// Perfect ranking -> nDCG 1.0.
	if got := NDCGAtK([]string{"a", "b"}, []string{"a", "b"}, 2); !approx(got, 1.0) {
		t.Errorf("NDCG perfect=%v want 1", got)
	}
	// One relevant at rank 2 out of 1 relevant total:
	// DCG = 1/log2(3); IDCG = 1/log2(2)=1 -> nDCG = 1/log2(3).
	want := 1.0 / math.Log2(3)
	if got := NDCGAtK([]string{"x", "a"}, []string{"a"}, 5); !approx(got, want) {
		t.Errorf("NDCG=%v want %v", got, want)
	}
	if got := NDCGAtK([]string{"x"}, nil, 5); got != 0 {
		t.Errorf("NDCG no relevant=%v want 0", got)
	}
}
