package eval

import (
	"math"
	"reflect"
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

func TestRecallCountsARepeatedIDOnce(t *testing.T) {
	// Two chunks of one document collapse to the same id; counted twice this
	// was recall 2/2 for a query that found one of two.
	if got := RecallAtK([]string{"a", "a"}, []string{"a", "b"}, 2); !approx(got, 0.5) {
		t.Errorf("RecallAtK = %v, want 0.5", got)
	}
	if got := PrecisionAtK([]string{"a", "a"}, []string{"a", "b"}, 2); !approx(got, 0.5) {
		t.Errorf("PrecisionAtK = %v, want 0.5", got)
	}
}

func TestNDCGIsPerfectWhenEveryRankWithinKIsRelevant(t *testing.T) {
	// Three relevant documents and room for one: a relevant document first is
	// the best ranking possible at k=1, so the ideal must be capped at k.
	if got := NDCGAtK([]string{"a"}, []string{"a", "b", "c"}, 1); !approx(got, 1) {
		t.Errorf("NDCG@1 = %v, want 1", got)
	}
}

func TestNDCGNeverExceedsOneWhenAnIDRepeats(t *testing.T) {
	if got := NDCGAtK([]string{"a", "a", "a"}, []string{"a"}, 3); !approx(got, 1) {
		t.Errorf("NDCG = %v, want 1", got)
	}
}

func TestHitAndAllAtKSeparateAnyEvidenceFromAllEvidence(t *testing.T) {
	retrieved := []string{"x", "a", "y", "b"}
	relevant := []string{"a", "b"}
	cases := []struct {
		k        int
		hit, all float64
	}{{1, 0, 0}, {2, 1, 0}, {3, 1, 0}, {4, 1, 1}}
	for _, c := range cases {
		if got := HitAtK(retrieved, relevant, c.k); got != c.hit {
			t.Errorf("HitAtK(k=%d) = %v, want %v", c.k, got, c.hit)
		}
		if got := AllAtK(retrieved, relevant, c.k); got != c.all {
			t.Errorf("AllAtK(k=%d) = %v, want %v", c.k, got, c.all)
		}
	}
	if HitAtK(retrieved, nil, 4) != 0 || AllAtK(retrieved, nil, 4) != 0 {
		t.Error("a query with nothing relevant scored a hit")
	}
}

func TestPercentileIsNearestRank(t *testing.T) {
	xs := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for p, want := range map[float64]float64{50: 5, 95: 10, 90: 9, 1: 1, 100: 10} {
		if got := Percentile(xs, p); got != want {
			t.Errorf("p%v = %v, want %v", p, got, want)
		}
	}
	if Percentile(nil, 50) != 0 {
		t.Error("percentile of nothing is not 0")
	}
}

func TestCollapseIDsKeepsEachGroupAtItsBestRank(t *testing.T) {
	group := map[string]string{"t1": "s1", "t2": "s2", "t3": "s1", "t4": "s3"}
	got := CollapseIDs([]string{"t2", "t1", "t3", "unknown", "t4"}, func(id string) string { return group[id] })
	if want := []string{"s2", "s1", "s3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("CollapseIDs = %v, want %v", got, want)
	}
}
