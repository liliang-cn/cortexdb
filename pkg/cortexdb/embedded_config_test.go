package cortexdb

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// EmbeddedConfig reaches the store: the index is quantized, and a reopened
// store finds what it saved.
func TestEmbeddedConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "edge.db")
	ctx := context.Background()
	r := rand.New(rand.NewSource(5))
	vecs := make([][]float32, 300)
	embs := make([]*core.Embedding, len(vecs))
	for i := range vecs {
		v := make([]float32, 64)
		for j := range v {
			v[j] = float32(r.NormFloat64())
		}
		vecs[i] = v
		embs[i] = &core.Embedding{ID: fmt.Sprintf("e%03d", i), Vector: v, Content: fmt.Sprint(i)}
	}

	db, err := Open(EmbeddedConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Vector().UpsertBatch(ctx, embs); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(EmbeddedConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	info := db.Info()
	if !info.Quantization.Enabled || info.IndexType != "HNSW" {
		t.Fatalf("embedded store reports %+v", info)
	}
	cfg := db.store.Config()
	if cfg.Resources.MaxOpenConns != 4 || !cfg.Resources.TempStoreFile || !cfg.AutoSave.Enabled {
		t.Fatalf("resource limits did not reach the store: %+v autosave=%+v", cfg.Resources, cfg.AutoSave)
	}
	hits := 0
	for i := 0; i < 50; i++ {
		res, err := db.Vector().Search(ctx, vecs[i], core.SearchOptions{TopK: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(res) > 0 && res[0].ID == fmt.Sprintf("e%03d", i) {
			hits++
		}
	}
	if hits < 45 {
		t.Fatalf("a stored vector found itself %d times in 50", hits)
	}
}
