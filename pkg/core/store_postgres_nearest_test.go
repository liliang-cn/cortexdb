package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"
)

// pgNearestFixture is 3,000 random vectors of dimension 16. One in twenty is
// "rare": in the collection "small", tagged rare, readable by "rare". The rest
// are "common", in "big".
type pgNearestFixture struct {
	store *PostgresStore
	index string
	vecs  map[string][]float32
	q     []float32
}

func newPGNearestFixture(t *testing.T, index IndexType) *pgNearestFixture {
	t.Helper()
	cfg := DefaultConfig()
	cfg.VectorDim = 16
	cfg.IndexType = index
	store := openPGReadStore(t, cfg)
	ctx := context.Background()
	// The index-read checks flush this connection's counts on demand, which
	// PostgreSQL offers from 15.
	var version int
	if err := store.db.QueryRowContext(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 150000 {
		t.Skipf("PostgreSQL %d has no pg_stat_force_next_flush — the PostgreSQL scan depth is NOT covered by this run", version)
	}
	for _, c := range []string{"big", "small"} {
		if _, err := store.CreateCollection(ctx, c, 16); err != nil {
			t.Fatal(err)
		}
	}
	f := &pgNearestFixture{store: store, index: "idx_embeddings_vec", vecs: map[string][]float32{}}
	if index == IndexTypeBinary {
		f.index = "idx_embeddings_vec_bq"
	}
	r := rand.New(rand.NewSource(1))
	var batch []*Embedding
	for i := 0; i < 3000; i++ {
		v := make([]float32, 16)
		for j := range v {
			v[j] = float32(r.NormFloat64())
		}
		id, coll, tag := fmt.Sprintf("e%d", i), "big", "common"
		if i%20 == 0 {
			coll, tag = "small", "rare"
		}
		f.vecs[id] = v
		batch = append(batch, &Embedding{ID: id, Vector: v, Content: "x", Collection: coll,
			Metadata: map[string]string{"tag": tag}, ACL: []string{tag}})
	}
	if err := store.UpsertBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	f.q = batch[7].Vector

	// One connection, on which the planner may not sort when an index can
	// serve the order: the answers below are the HNSW index's wherever it can
	// give them, whatever the cost model would have picked for a table this
	// small.
	store.db.SetMaxOpenConns(1)
	if _, err := store.db.ExecContext(ctx, "SET enable_sort = off"); err != nil {
		t.Fatal(err)
	}
	return f
}

// indexScans is how many scans of the store's HNSW index (the float one, or
// the bit one of a binary store) PostgreSQL has counted. The counts of this
// connection are flushed when a transaction ends, and only if asked for: the
// first statement asks, and the second reads after it.
func (f *pgNearestFixture) indexScans(t *testing.T) int64 {
	t.Helper()
	ctx := context.Background()
	if _, err := f.store.db.ExecContext(ctx, "SELECT pg_stat_force_next_flush()"); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := f.store.db.QueryRowContext(ctx,
		"SELECT idx_scan FROM pg_stat_user_indexes WHERE indexrelname = $1 AND schemaname = current_schema()", f.index).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func cosineDistance(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return 1 - dot/math.Sqrt(na*nb)
}

// nearest is the exact answer, computed here: the k closest ids (all of them
// for k 0) among those keep accepts and within maxDist.
func (f *pgNearestFixture) nearest(k int, maxDist float64, keep func(i int) bool) []string {
	type hit struct {
		id string
		d  float64
	}
	var hits []hit
	for id, v := range f.vecs {
		var i int
		fmt.Sscanf(id, "e%d", &i)
		if d := cosineDistance(f.q, v); keep(i) && d <= maxDist {
			hits = append(hits, hit{id, d})
		}
	}
	sort.Slice(hits, func(a, b int) bool { return hits[a].d < hits[b].d })
	if k > 0 && len(hits) > k {
		hits = hits[:k]
	}
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.id
	}
	return ids
}

func any3000(int) bool  { return true }
func rare(i int) bool   { return i%20 == 0 }
func common(i int) bool { return i%20 != 0 }

// Every vector search on PostgreSQL returns what it was asked for: as many
// rows as there are to return, up to top_k, and the right ones. Through the
// HNSW index, a scan stops at hnsw.ef_search rows (40 unless set), before any
// WHERE: a search for more came back with 40, and a filtered one with the few
// of those 40 that passed.
func TestAPostgresVectorSearchReturnsWhatItAsksFor(t *testing.T) {
	f := newPGNearestFixture(t, IndexTypeHNSW)
	ctx := context.Background()
	s := f.store
	search := func(o SearchOptions) func() ([]ScoredEmbedding, error) {
		return func() ([]ScoredEmbedding, error) { return s.Search(ctx, f.q, o) }
	}
	facets := func(o FacetedSearchOptions) func() ([]ScoredEmbedding, error) {
		return func() ([]ScoredEmbedding, error) {
			out, _, err := s.SearchWithFacets(ctx, f.q, o)
			return out, err
		}
	}
	rareFacet := map[string]FacetFilter{"tag": {Type: FilterTypeEquals, Values: []interface{}{"rare"}}}
	// index: the HNSW index answers (an unfiltered search of up to 1000);
	// otherwise the ranking is exact and the index is not read.
	for _, tc := range []struct {
		name  string
		run   func() ([]ScoredEmbedding, error)
		want  []string
		index bool
	}{
		{"top 10", search(SearchOptions{TopK: 10}), f.nearest(10, 2, any3000), true},
		{"default top_k", search(SearchOptions{}), f.nearest(10, 2, any3000), true},
		{"top 41", search(SearchOptions{TopK: 41}), f.nearest(41, 2, any3000), true},
		{"top 100", search(SearchOptions{TopK: 100}), f.nearest(100, 2, any3000), true},
		{"top 1000", search(SearchOptions{TopK: 1000}), f.nearest(1000, 2, any3000), true},
		{"top 1500", search(SearchOptions{TopK: 1500}), f.nearest(1500, 2, any3000), false},
		{"top 100 above 0.2", search(SearchOptions{TopK: 100, Threshold: 0.2}), f.nearest(100, 0.8, any3000), true},
		{"collection, top 5", search(SearchOptions{TopK: 5, Collection: "small"}), f.nearest(5, 2, rare), false},
		{"collection, top 100", search(SearchOptions{TopK: 100, Collection: "small"}), f.nearest(100, 2, rare), false},
		{"large collection, top 100", search(SearchOptions{TopK: 100, Collection: "big"}), f.nearest(100, 2, common), false},
		{"metadata, top 5", search(SearchOptions{TopK: 5, Filter: map[string]string{"tag": "rare"}}), f.nearest(5, 2, rare), false},
		{"acl, top 5", func() ([]ScoredEmbedding, error) {
			return s.SearchWithACL(ctx, f.q, []string{"rare"}, SearchOptions{TopK: 5})
		}, f.nearest(5, 2, rare), false},
		{"advanced, default top_k", func() ([]ScoredEmbedding, error) {
			return s.SearchWithAdvancedFilter(ctx, f.q, AdvancedSearchOptions{})
		}, f.nearest(10, 2, any3000), true},
		{"advanced, top 100", func() ([]ScoredEmbedding, error) {
			return s.SearchWithAdvancedFilter(ctx, f.q, AdvancedSearchOptions{SearchOptions: SearchOptions{TopK: 100}})
		}, f.nearest(100, 2, any3000), true},
		{"pre-filter, top 5", func() ([]ScoredEmbedding, error) {
			return s.SearchWithAdvancedFilter(ctx, f.q, AdvancedSearchOptions{SearchOptions: SearchOptions{TopK: 5},
				PreFilter: &FilterExpression{Operator: FilterEQ, Field: "tag", Value: "rare"}})
		}, f.nearest(5, 2, rare), false},
		{"post-filter, top 5", func() ([]ScoredEmbedding, error) {
			return s.SearchWithAdvancedFilter(ctx, f.q, AdvancedSearchOptions{SearchOptions: SearchOptions{TopK: 5},
				PostFilter: &FilterExpression{Operator: FilterEQ, Field: "tag", Value: "rare"}})
		}, f.nearest(5, 2, rare), false},
		{"hybrid, vector only, top 100", func() ([]ScoredEmbedding, error) {
			return s.HybridSearch(ctx, f.q, "", HybridSearchOptions{SearchOptions: SearchOptions{TopK: 100}})
		}, f.nearest(100, 2, any3000), true},
		{"range, top 100", func() ([]ScoredEmbedding, error) {
			return s.RangeSearch(ctx, f.q, 0.8, SearchOptions{TopK: 100})
		}, f.nearest(100, 0.8, any3000), false},
		{"range, all", func() ([]ScoredEmbedding, error) {
			return s.RangeSearch(ctx, f.q, 0.8, SearchOptions{})
		}, f.nearest(0, 0.8, any3000), false},
		{"faceted, top 100", facets(FacetedSearchOptions{SearchOptions: SearchOptions{TopK: 100}}), f.nearest(100, 2, any3000), true},
		{"faceted above 0.2, top 100", facets(FacetedSearchOptions{SearchOptions: SearchOptions{TopK: 100, Threshold: 0.2}}), f.nearest(100, 0.8, any3000), false},
		{"faceted, collection, top 5", facets(FacetedSearchOptions{SearchOptions: SearchOptions{TopK: 5, Collection: "small"}}), f.nearest(5, 2, rare), false},
		{"faceted, metadata, top 5", facets(FacetedSearchOptions{SearchOptions: SearchOptions{TopK: 5, Filter: map[string]string{"tag": "rare"}}}), f.nearest(5, 2, rare), false},
		{"faceted rare, top 5", facets(FacetedSearchOptions{SearchOptions: SearchOptions{TopK: 5}, Facets: rareFacet}), f.nearest(5, 2, rare), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := f.indexScans(t)
			got, err := tc.run()
			if err != nil {
				t.Fatal(err)
			}
			if used := f.indexScans(t) > before; used != tc.index {
				t.Errorf("HNSW index read: %v, want %v", used, tc.index)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("%d results, %d asked for and there to find", len(got), len(tc.want))
			}
			want := map[string]bool{}
			for _, id := range tc.want {
				want[id] = true
			}
			found := 0
			for _, e := range got {
				if want[e.ID] {
					found++
				}
			}
			// Exact ranking finds every one; the index, nine in ten.
			if (!tc.index && found < len(tc.want)) || found*10 < len(tc.want)*9 {
				t.Fatalf("%d of the %d nearest found", found, len(tc.want))
			}
		})
	}
}

// The binary search shortlists top_k × oversample rows through its bit index,
// and pgvector refuses an ef_search past 1000: from top_k 126 at the default
// oversample, every search failed. The shortlist is what the rescoring has to
// choose from, so its length shows in the answer: 16 bits tell little apart,
// and a shortlist of top_k rows finds under half of the nearest.
func TestAPostgresBinarySearchTakesALongShortlist(t *testing.T) {
	f := newPGNearestFixture(t, IndexTypeBinary)
	for _, k := range []int{10, 125, 126, 500} {
		before := f.indexScans(t)
		got, err := f.store.Search(context.Background(), f.q, SearchOptions{TopK: k})
		if err != nil {
			t.Fatalf("top %d: %v", k, err)
		}
		// The bit index serves shortlists of up to 1000 (top 125 at the
		// default oversample of 8); 126 and 500 are ranked exactly.
		if used := f.indexScans(t) > before; used != (k*8 <= 1000) {
			t.Errorf("top %d: bit index read: %v", k, used)
		}
		if len(got) != k {
			t.Fatalf("top %d: %d results", k, len(got))
		}
		if k < 125 {
			continue
		}
		want := map[string]bool{}
		for _, id := range f.nearest(k, 2, any3000) {
			want[id] = true
		}
		found := 0
		for _, e := range got {
			if want[e.ID] {
				found++
			}
		}
		if found*4 < k*3 {
			t.Fatalf("top %d: %d of the nearest found", k, found)
		}
	}
}

// A vector search opens its own transaction, and a search whose context is
// done stops there with the context's error. No database is needed: database/sql
// gives up before asking the pool for a connection.
func TestAPostgresVectorSearchStopsWithItsContext(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://nobody@127.0.0.1:1/none")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := DefaultConfig()
	cfg.VectorDim = 4
	s := NewPostgresStore(db, cfg)
	binary := NewPostgresStore(db, cfg)
	binary.binary = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	q := []float32{1, 0, 0, 0}
	for name, run := range map[string]func() error{
		"search": func() error { _, err := s.Search(ctx, q, SearchOptions{TopK: 5}); return err },
		"binary": func() error { _, err := binary.Search(ctx, q, SearchOptions{TopK: 5}); return err },
		"acl":    func() error { _, err := s.SearchWithACL(ctx, q, nil, SearchOptions{TopK: 5}); return err },
		"range":  func() error { _, err := s.RangeSearch(ctx, q, 0.5, SearchOptions{}); return err },
		"faceted": func() error {
			_, _, err := s.SearchWithFacets(ctx, q, FacetedSearchOptions{SearchOptions: SearchOptions{TopK: 5}})
			return err
		},
	} {
		if err := run(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: %v, want the context's error", name, err)
		}
	}
}

// A wider hnsw.ef_search set by the operator (database, role or session) is
// kept: the search widens the scan to its limit, it never narrows it.
func TestAPostgresVectorSearchKeepsAWiderEfSearch(t *testing.T) {
	f := newPGNearestFixture(t, IndexTypeHNSW)
	ctx := context.Background()
	ef := func(limit int) string {
		t.Helper()
		tx, err := f.store.beginVectorScan(ctx, limit, false)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback() //nolint:errcheck // read-only
		var v string
		if err := tx.QueryRowContext(ctx, "SHOW hnsw.ef_search").Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tc := range []struct {
		session string
		limit   int
		want    string
	}{
		{"40", 10, "40"},
		{"40", 100, "100"},
		{"400", 10, "400"},
		{"400", 1000, "1000"},
	} {
		if _, err := f.store.db.ExecContext(ctx, "SET hnsw.ef_search = "+tc.session); err != nil {
			t.Fatal(err)
		}
		if got := ef(tc.limit); got != tc.want {
			t.Errorf("session ef_search %s, limit %d: scan at %s, want %s", tc.session, tc.limit, got, tc.want)
		}
	}
}
