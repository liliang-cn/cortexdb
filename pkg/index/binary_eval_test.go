package index

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The recall/QPS curve behind DefaultBinaryOversample, kept runnable:
//
//	CORTEXDB_BINARY_EVAL=1 go test ./pkg/index -run TestBinaryQuantizationEval -v -timeout 30m
//
// With CORTEXDB_BINARY_EVAL_DIR pointing at a directory holding real.f32
// and/or corpus.f32 (raw little-endian float32 rows of 768) those sets are
// measured too. Skipped unless asked for: it is a measurement, not a check.

func readF32(t *testing.T, path string, dim int) [][]float32 {
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	n := len(raw) / (4 * dim)
	out := make([][]float32, n)
	for i := range out {
		v := make([]float32, dim)
		for d := range v {
			v[d] = math.Float32frombits(binary.LittleEndian.Uint32(raw[(i*dim+d)*4:]))
		}
		out[i] = v
	}
	return out
}

func splitQueries(all [][]float32, queries int, seed uint64) ([][]float32, [][]float32) {
	rng := rand.New(rand.NewPCG(seed, 9))
	perm := rng.Perm(len(all))
	var base, qs [][]float32
	for i, p := range perm {
		if i < queries {
			qs = append(qs, all[p])
		} else {
			base = append(base, all[p])
		}
	}
	return base, qs
}

func TestBinaryQuantizationEval(t *testing.T) {
	if os.Getenv("CORTEXDB_BINARY_EVAL") == "" {
		t.Skip("set CORTEXDB_BINARY_EVAL=1 to run the binary quantization measurement")
	}
	type dataset struct {
		name          string
		base, queries [][]float32
	}
	var sets []dataset
	if dir := os.Getenv("CORTEXDB_BINARY_EVAL_DIR"); dir != "" {
		for _, f := range []string{"real.f32", "corpus.f32", "sparse.f32"} {
			p := filepath.Join(dir, f)
			if _, err := os.Stat(p); err != nil {
				continue
			}
			base, qs := splitQueries(readF32(t, p, 768), 200, 1)
			sets = append(sets, dataset{f, base, qs})
		}
		if len(sets) >= 2 && sets[0].name == "real.f32" && sets[1].name == "corpus.f32" {
			base := append(append([][]float32(nil), sets[0].base...), sets[1].base...)
			qs := append(append([][]float32(nil), sets[0].queries...), sets[1].queries...)
			sets = append(sets, dataset{"real+corpus", base, qs})
		}
	}
	if os.Getenv("CORTEXDB_BINARY_EVAL_SYNTHETIC") != "0" {
		emb := embeddingLikeVectors(100200, 768, 1000, 128, 7)
		sets = append(sets, dataset{"synthetic-embedding-like-100k", emb[:100000], emb[100000:]})
		iso := clusteredVectors(100200, 768, 1000, 1.0, 1.0, 0.5, 7)
		sets = append(sets, dataset{"synthetic-isotropic-100k (stress)", iso[:100000], iso[100000:]})
	}

	t.Log("| dataset | N | centered | oversample | recall@10 | binary QPS | exact flat QPS |")
	for _, ds := range sets {
		ids := make([]string, len(ds.base))
		for i := range ids {
			ids[i] = fmt.Sprintf("v%06d", i)
		}
		truth := make([][]string, len(ds.queries))
		flat := NewFlatIndex(768, CosineDistance)
		for i, v := range ds.base {
			_ = flat.Insert(ids[i], v)
		}
		start := time.Now()
		for qi, q := range ds.queries {
			truth[qi], _ = flat.Search(q, 10)
		}
		exactQPS := float64(len(ds.queries)) / time.Since(start).Seconds()

		for _, center := range []bool{false, true} {
			idx := NewBinaryIndex(768, BinaryIndexOptions{Center: center, KeepVectors: true})
			if err := idx.Rebuild(ids, ds.base); err != nil {
				t.Fatal(err)
			}
			codesOnly := NewBinaryIndex(768, BinaryIndexOptions{Center: center})
			_ = codesOnly.Rebuild(ids, ds.base)
			r := 0.0
			for qi, q := range ds.queries {
				got, _ := codesOnly.SearchOversampled(q, 10, 1)
				r += recallAt(got, truth[qi])
			}
			t.Logf("| %s | %d | %v | Hamming only, no rescoring | %.3f | | |", ds.name, len(ds.base), center, r/float64(len(ds.queries)))
			for _, over := range []int{1, 2, 4, 8, 16} {
				recall := 0.0
				start := time.Now()
				reps := 0
				for time.Since(start) < 300*time.Millisecond || reps == 0 {
					for qi, q := range ds.queries {
						got, _ := idx.SearchOversampled(q, 10, over)
						if reps == 0 {
							recall += recallAt(got, truth[qi])
						}
					}
					reps++
				}
				qps := float64(reps*len(ds.queries)) / time.Since(start).Seconds()
				t.Logf("| %s | %d | %v | %d | %.3f | %.0f | %.0f |", ds.name, len(ds.base), center, over, recall/float64(len(ds.queries)), qps, exactQPS)
			}
		}
	}
}
