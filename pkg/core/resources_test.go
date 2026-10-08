package core

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
)

func openResourceStore(t *testing.T, path string, mutate func(*Config)) *SQLiteStore {
	t.Helper()
	config := DefaultConfig()
	config.Path = path
	config.HNSW.Enabled = true
	config.AutoSave.Enabled = false
	if mutate != nil {
		mutate(&config)
	}
	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

func seedVectors(t *testing.T, store *SQLiteStore, n, dim int) [][]float32 {
	t.Helper()
	r := rand.New(rand.NewSource(7))
	vecs := make([][]float32, n)
	embs := make([]*Embedding, n)
	for i := range vecs {
		v := make([]float32, dim)
		for j := range v {
			v[j] = float32(r.NormFloat64())
		}
		vecs[i] = v
		embs[i] = &Embedding{ID: fmt.Sprintf("v%03d", i), Vector: v, Content: fmt.Sprintf("doc %d", i), Metadata: map[string]string{"parity": fmt.Sprint(i % 2)}}
	}
	if err := store.UpsertBatch(context.Background(), embs); err != nil {
		t.Fatal(err)
	}
	return vecs
}

func snapshotRows(t *testing.T, store *SQLiteStore) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM index_snapshots WHERE type = 'HNSW' OR (type >= 'HNSW/' AND type < 'HNSW0')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A snapshot is the whole index in one blob. Rewriting it when nothing
// changed is a full copy in memory and a full write to disk — on an SD card,
// wear — so a store that only read closes without writing one.
func TestSnapshotIsWrittenOnlyWhenTheIndexChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	store := openResourceStore(t, path, nil)
	seedVectors(t, store, 40, 16)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store = openResourceStore(t, path, nil)
	if snapshotRows(t, store) == 0 {
		t.Fatal("the first close should have written a snapshot")
	}
	if store.snapshotChanges() != 0 {
		t.Fatalf("a freshly loaded snapshot is current, got %d changes", store.snapshotChanges())
	}
	if _, err := store.db.Exec(`DELETE FROM index_snapshots WHERE type >= 'HNSW/' AND type < 'HNSW0'`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store = openResourceStore(t, path, nil)
	defer func() { _ = store.Close() }()
	if n := snapshotRows(t, store); n != 0 {
		t.Fatalf("closing an unchanged index rewrote its snapshot (%d rows)", n)
	}
	// The index was rebuilt from rows on this open, so this close owes one.
	if store.snapshotChanges() == 0 {
		t.Fatal("an index rebuilt from rows should count as changed")
	}
}

// A rebuild from rows must make one connected graph: a vector searched for by
// its own coordinates is its own nearest neighbour.
func TestRebuildFindsEveryVector(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	seedStore := openResourceStore(t, path, func(c *Config) { c.HNSW.Enabled = false; c.IndexType = IndexTypeFlat })
	vecs := seedVectors(t, seedStore, 600, 24)
	if err := seedStore.Close(); err != nil {
		t.Fatal(err)
	}

	store := openResourceStore(t, path, nil)
	defer func() { _ = store.Close() }()
	if got := store.hnswIndex.Size(); got != len(vecs) {
		t.Fatalf("rebuild indexed %d of %d vectors", got, len(vecs))
	}
	missed := 0
	for i, v := range vecs {
		res, err := store.Search(context.Background(), v, SearchOptions{TopK: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(res) == 0 || res[0].ID != fmt.Sprintf("v%03d", i) {
			missed++
		}
	}
	if missed > len(vecs)/100 {
		t.Fatalf("a vector searched for by itself was not its own nearest neighbour %d times in %d", missed, len(vecs))
	}
}

// Reopening a quantized store whose dimension is left to auto-detection used
// to build the index from float32 vectors: the quantizer was only made once
// the dimension was known, and on open it was not yet.
func TestQuantizedReopenHoldsCodesNotFloats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	seedStore := openResourceStore(t, path, func(c *Config) { c.HNSW.Enabled = false; c.IndexType = IndexTypeFlat })
	seedVectors(t, seedStore, 200, 32)
	if err := seedStore.Close(); err != nil {
		t.Fatal(err)
	}

	quantized := func(c *Config) {
		c.VectorDim = 0
		c.Quantization = QuantizationConfig{Enabled: true, Type: "scalar", NBits: 8}
	}
	check := func(store *SQLiteStore, when string) {
		t.Helper()
		if store.quantizer == nil {
			t.Fatalf("%s: no quantizer", when)
		}
		for id, node := range store.hnswIndex.Nodes {
			if node.Vector != nil || node.Quantized == nil {
				t.Fatalf("%s: node %s holds float32 vectors", when, id)
			}
		}
	}
	store := openResourceStore(t, path, quantized)
	check(store, "rebuilt")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openResourceStore(t, path, quantized)
	defer func() { _ = store.Close() }()
	check(store, "from snapshot")
}

// A snapshot written without quantization holds float32 vectors; loading it
// under a quantized configuration would keep them.
func TestFloatSnapshotIsRebuiltWhenQuantizationIsOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	store := openResourceStore(t, path, nil)
	seedVectors(t, store, 120, 32)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openResourceStore(t, path, func(c *Config) {
		c.Quantization = QuantizationConfig{Enabled: true, Type: "scalar", NBits: 8}
	})
	defer func() { _ = store.Close() }()
	for id, node := range store.hnswIndex.Nodes {
		if node.Vector != nil {
			t.Fatalf("node %s kept the float32 vector of the old snapshot", id)
		}
	}
}

// The scan that keeps only the best TopK must return what collecting every
// row and sorting returned, filters and threshold included.
func TestLinearTopKMatchesFullSort(t *testing.T) {
	store := openResourceStore(t, filepath.Join(t.TempDir(), "s.db"), func(c *Config) {
		c.HNSW.Enabled = false
		c.IndexType = IndexTypeFlat
	})
	defer func() { _ = store.Close() }()
	seedVectors(t, store, 300, 16)
	ctx := context.Background()
	r := rand.New(rand.NewSource(3))
	for _, opts := range []SearchOptions{
		{TopK: 10},
		{TopK: 1},
		{TopK: 500},
		{TopK: 7, Filter: map[string]string{"parity": "1"}},
		{TopK: 10, Threshold: 0.2},
	} {
		q := make([]float32, 16)
		for j := range q {
			q[j] = float32(r.NormFloat64())
		}
		got, err := store.searchLinearTopK(ctx, q, opts)
		if err != nil {
			t.Fatal(err)
		}
		all, err := store.fetchCandidates(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		want := store.scoreCandidates(q, all, opts)
		if len(got) != len(want) {
			t.Fatalf("%+v: %d results, want %d", opts, len(got), len(want))
		}
		for i := range want {
			if got[i].ID != want[i].ID || got[i].Score != want[i].Score {
				t.Fatalf("%+v: result %d is %s (%.4f), want %s (%.4f)", opts, i, got[i].ID, got[i].Score, want[i].ID, want[i].Score)
			}
		}
	}
}

func TestSQLiteDSNFollowsResources(t *testing.T) {
	def := sqliteDSN("x.db", ResourceConfig{})
	for _, want := range []string{"cache_size(-2000)", "temp_store(MEMORY)"} {
		if !strings.Contains(def, want) {
			t.Errorf("default DSN lacks %s: %s", want, def)
		}
	}
	if strings.Contains(def, "mmap_size") {
		t.Errorf("default DSN maps the file: %s", def)
	}
	small := sqliteDSN("x.db", ResourceConfig{CacheSizeKiB: 512, MmapSizeMiB: 64, TempStoreFile: true})
	for _, want := range []string{"cache_size(-512)", "temp_store(FILE)", fmt.Sprintf("mmap_size(%d)", 64<<20)} {
		if !strings.Contains(small, want) {
			t.Errorf("DSN lacks %s: %s", want, small)
		}
	}
}

// A snapshot larger than one chunk is written as several rows and read back
// as one stream.
func TestChunkedSnapshotRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	store := openResourceStore(t, path, nil)
	vecs := seedVectors(t, store, 2500, 768) // ~7.7 MB of float32: two chunks
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openResourceStore(t, path, nil)
	defer func() { _ = store.Close() }()
	if n := snapshotRows(t, store); n < 2 {
		t.Fatalf("a %d-vector snapshot took %d chunk(s)", len(vecs), n)
	}
	if store.snapshotChanges() != 0 {
		t.Fatal("the index should have come from the snapshot, not a rebuild")
	}
	if got := store.hnswIndex.Size(); got != len(vecs) {
		t.Fatalf("snapshot held %d of %d vectors", got, len(vecs))
	}
	res, err := store.Search(context.Background(), vecs[1234], SearchOptions{TopK: 1})
	if err != nil || len(res) == 0 || res[0].ID != "v1234" {
		t.Fatalf("search after loading chunks: %v %v", res, err)
	}
}

// A snapshot written before chunking is one row typed "HNSW"; it still loads.
func TestLegacySnapshotStillLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	store := openResourceStore(t, path, nil)
	vecs := seedVectors(t, store, 80, 16)
	var blob bytes.Buffer
	if err := store.hnswIndex.Save(&blob); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openResourceStore(t, path, nil)
	if _, err := store.db.Exec(`DELETE FROM index_snapshots WHERE type >= 'HNSW/' AND type < 'HNSW0'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO index_snapshots (type, data) VALUES ('HNSW', ?)`, blob.Bytes()); err != nil {
		t.Fatal(err)
	}
	_ = store.db.Close()
	store.closed = true

	store = openResourceStore(t, path, nil)
	defer func() { _ = store.Close() }()
	if store.snapshotChanges() != 0 || store.hnswIndex.Size() != len(vecs) {
		t.Fatalf("legacy snapshot not loaded: %d changes, %d nodes", store.snapshotChanges(), store.hnswIndex.Size())
	}
}
