package core

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"path/filepath"
	"sort"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/pgtest"
)

// lowRankVectors draws unit vectors that vary mostly along a few directions,
// with a common offset — the shape real text embeddings have, scaled down so
// a test can afford it.
func lowRankVectors(n, dim, latent int, seed uint64) [][]float32 {
	rng := rand.New(rand.NewPCG(seed, 1))
	lift := make([][]float64, dim)
	for d := range lift {
		lift[d] = make([]float64, latent)
		for j := range lift[d] {
			lift[d][j] = rng.NormFloat64()
		}
	}
	centers := make([][]float64, 40)
	for c := range centers {
		centers[c] = make([]float64, latent)
		for j := range centers[c] {
			centers[c][j] = rng.NormFloat64() / math.Sqrt(float64(j+1))
		}
	}
	out := make([][]float32, n)
	for i := range out {
		c := centers[rng.IntN(len(centers))]
		v := make([]float32, dim)
		norm := 0.0
		for d := range v {
			x := 0.3 + rng.NormFloat64()*0.1
			for j, w := range lift[d] {
				x += w * (c[j] + rng.NormFloat64()*0.5/math.Sqrt(float64(j+1)))
			}
			v[d] = float32(x)
			norm += x * x
		}
		for d := range v {
			v[d] /= float32(math.Sqrt(norm))
		}
		out[i] = v
	}
	return out
}

type binaryBackend struct {
	name  string
	store interface {
		UpsertBatch(context.Context, []*Embedding) error
		Search(context.Context, []float32, SearchOptions) ([]ScoredEmbedding, error)
		Delete(context.Context, string) error
	}
	sqlite *SQLiteStore
	pg     *PostgresStore
}

func binaryBackends(t *testing.T, dim int) []binaryBackend {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Path = filepath.Join(t.TempDir(), "binary.db")
	cfg.VectorDim = dim
	cfg.IndexType = IndexTypeBinary
	s, err := NewWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	out := []binaryBackend{{name: "sqlite", store: s, sqlite: s}}

	db := pgtest.Open(t, "core_binary")
	if db == nil {
		t.Log(pgtest.EnvDSN + " unset — the PostgreSQL binary index is NOT covered by this run")
		return out
	}
	pcfg := DefaultConfig()
	pcfg.VectorDim = dim
	pcfg.IndexType = IndexTypeBinary
	pg := NewPostgresStore(db, pcfg)
	if err := pg.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ok, why := pg.Indexed(); !ok {
		t.Fatalf("PostgreSQL built no binary index: %s", why)
	}
	return append(out, binaryBackend{name: "postgres", store: pg, pg: pg})
}

func exactTop(vectors [][]float32, query []float32, k int) []string {
	type r struct {
		id string
		s  float64
	}
	all := make([]r, len(vectors))
	for i, v := range vectors {
		all[i] = r{fmt.Sprintf("e%05d", i), CosineSimilarity(query, v)}
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].s > all[b].s })
	out := make([]string, k)
	for i := range out {
		out[i] = all[i].id
	}
	return out
}

func TestBinaryIndexSearchFindsTheExactNeighboursOnBothBackends(t *testing.T) {
	const dim = 256
	all := lowRankVectors(3030, dim, 24, 1)
	vectors, queries := all[:3000], all[3000:]
	for _, b := range binaryBackends(t, dim) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			embs := make([]*Embedding, len(vectors))
			for i, v := range vectors {
				embs[i] = &Embedding{ID: fmt.Sprintf("e%05d", i), Vector: v, Content: "c"}
			}
			if err := b.store.UpsertBatch(ctx, embs); err != nil {
				t.Fatal(err)
			}
			if b.sqlite != nil {
				idx := b.sqlite.currentBinaryIndex()
				if idx == nil || idx.Size() != len(vectors) {
					t.Fatalf("binary index holds %v vectors, want %d", idx, len(vectors))
				}
				if got, want := idx.CodeBytes(), len(vectors)*dim*4/32; got != want {
					t.Fatalf("codes take %d bytes, want %d (1/32 of float32)", got, want)
				}
			}
			recall := 0.0
			for _, q := range queries {
				res, err := b.store.Search(ctx, q, SearchOptions{TopK: 10})
				if err != nil {
					t.Fatal(err)
				}
				truth := map[string]bool{}
				for _, id := range exactTop(vectors, q, 10) {
					truth[id] = true
				}
				for i, r := range res {
					if truth[r.ID] {
						recall++
					}
					// Rescored: the score is the exact similarity, in order.
					if i > 0 && r.Score > res[i-1].Score {
						t.Fatalf("results not in score order: %v", res)
					}
					if want := CosineSimilarity(q, vectors[mustIndex(t, r.ID)]); math.Abs(r.Score-want) > 1e-4 {
						t.Fatalf("%s scored %.5f, exact similarity is %.5f", r.ID, r.Score, want)
					}
				}
			}
			recall /= float64(10 * len(queries))
			if recall < 0.95 {
				t.Fatalf("recall@10 %.3f, want >= 0.95", recall)
			}
			t.Logf("recall@10 = %.3f", recall)

			// A deleted row is gone from the answers, not just from the table.
			top, _ := b.store.Search(ctx, vectors[7], SearchOptions{TopK: 1})
			if len(top) != 1 || top[0].ID != "e00007" {
				t.Fatalf("a stored vector is not its own nearest neighbour: %v", top)
			}
			if err := b.store.Delete(ctx, "e00007"); err != nil {
				t.Fatal(err)
			}
			after, _ := b.store.Search(ctx, vectors[7], SearchOptions{TopK: 5})
			for _, r := range after {
				if r.ID == "e00007" {
					t.Fatal("deleted row still returned")
				}
			}
			if len(after) != 5 {
				t.Fatalf("got %d results after a delete, want 5", len(after))
			}
		})
	}
}

func mustIndex(t *testing.T, id string) int {
	var i int
	if _, err := fmt.Sscanf(id, "e%05d", &i); err != nil {
		t.Fatalf("id %q: %v", id, err)
	}
	return i
}

func TestBinaryIndexIsRebuiltFromTheTableAndRecentersAsTheStoreGrows(t *testing.T) {
	const dim = 64
	vectors := lowRankVectors(600, dim, 12, 2)
	ctx := context.Background()
	cfg := DefaultConfig()
	cfg.Path = filepath.Join(t.TempDir(), "binary.db")
	cfg.IndexType = IndexTypeBinary // dimension auto-detected
	s, err := NewWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	for i, v := range vectors[:100] {
		if err := s.Upsert(ctx, &Embedding{ID: fmt.Sprintf("e%05d", i), Vector: v}); err != nil {
			t.Fatal(err)
		}
	}
	idx := s.currentBinaryIndex()
	if idx == nil || idx.Size() != 100 {
		t.Fatalf("the first inserts did not create and fill the index: %v", idx)
	}
	if idx.NeedsRetrain() {
		t.Fatal("the center was not retrained as the store grew from empty")
	}
	embs := make([]*Embedding, 0, 500)
	for i, v := range vectors[100:] {
		embs = append(embs, &Embedding{ID: fmt.Sprintf("e%05d", 100+i), Vector: v})
	}
	if err := s.UpsertBatch(ctx, embs); err != nil {
		t.Fatal(err)
	}
	if idx.Size() != 600 || idx.NeedsRetrain() {
		t.Fatalf("after growing to 600: size %d, needs retrain %v", idx.Size(), idx.NeedsRetrain())
	}
	_ = s.Close()

	// Reopened, the index is rebuilt from the table.
	cfg.VectorDim = dim
	s2, err := NewWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if got := s2.currentBinaryIndex().Size(); got != 600 {
		t.Fatalf("reopened index holds %d vectors, want 600", got)
	}
	res, err := s2.Search(ctx, vectors[42], SearchOptions{TopK: 3})
	if err != nil || len(res) != 3 || res[0].ID != "e00042" {
		t.Fatalf("search after reopen: %v %v", res, err)
	}
}

func TestBinarySearchScopedToACollectionStillFillsTopK(t *testing.T) {
	const dim = 64
	vectors := lowRankVectors(1000, dim, 12, 3)
	ctx := context.Background()
	cfg := DefaultConfig()
	cfg.Path = filepath.Join(t.TempDir(), "binary.db")
	cfg.VectorDim = dim
	cfg.IndexType = IndexTypeBinary
	s, err := NewWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCollection(ctx, "small", dim); err != nil {
		t.Fatal(err)
	}
	var embs []*Embedding
	for i, v := range vectors {
		e := &Embedding{ID: fmt.Sprintf("e%05d", i), Vector: v}
		if i%100 == 0 { // ten rows, scattered
			e.Collection = "small"
		}
		embs = append(embs, e)
	}
	if err := s.UpsertBatch(ctx, embs); err != nil {
		t.Fatal(err)
	}
	res, err := s.Search(ctx, vectors[555], SearchOptions{TopK: 10, Collection: "small"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 10 {
		t.Fatalf("collection-scoped search returned %d of the collection's 10 rows", len(res))
	}
}
