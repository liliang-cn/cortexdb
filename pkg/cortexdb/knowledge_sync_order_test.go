package cortexdb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// watchedStore writes down when a knowledge write reserves its place among
// the index syncs, and what it does inside that place.
type watchedStore struct {
	core.BrainStore
	db    *DB
	doc   string
	probe []float32
	// committed counts the rows of doc a reader sees; empty, it counts the
	// document itself.
	committed string
	// atReserve, when set, runs as the place is reserved.
	atReserve func()
	events    []string
}

// graphIndex says what the graph's HNSW index gives nearest to the vector
// of the watched document. The search reads each node it finds from the
// table and passes over one that is not there.
func (w *watchedStore) graphIndex() string {
	hits, _ := w.db.graph.HNSWSearch(context.Background(), w.probe, 1, -2)
	switch {
	case len(hits) == 0:
		return "graph index holds only nodes the table lacks"
	case hits[0].VectorScore > 0.999:
		return "graph index holds the document"
	}
	return "graph index holds other documents"
}

func (w *watchedStore) ReserveIndexSync() func(apply func()) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	query := w.committed
	if query == "" {
		query = `SELECT COUNT(*) FROM documents WHERE id = ?`
	}
	var committed int
	if err := w.db.SQL().QueryRowContext(ctx, query, w.doc).Scan(&committed); err != nil {
		w.events = append(w.events, "reserve: "+err.Error())
	} else if committed == 0 {
		w.events = append(w.events, "reserve, document absent")
	} else {
		w.events = append(w.events, "reserve, document present")
	}
	if w.atReserve != nil {
		w.atReserve()
	}
	place := w.BrainStore.ReserveIndexSync()
	return func(apply func()) {
		if apply == nil {
			place(nil)
			return
		}
		place(func() {
			w.events = append(w.events, "place taken, "+w.graphIndex())
			apply()
			w.events = append(w.events, "place given up, "+w.graphIndex())
		})
	}
}

func (w *watchedStore) SyncDeletedEmbeddingIDs(ctx context.Context, ids []string) {
	w.events = append(w.events, "store deletes")
	w.BrainStore.SyncDeletedEmbeddingIDs(ctx, ids)
}

func (w *watchedStore) SyncUpsertedEmbeddings(ctx context.Context, embs []*core.Embedding) {
	w.events = append(w.events, "store upserts")
	w.BrainStore.SyncUpsertedEmbeddings(ctx, embs)
}

// brainWithAGraphIndex opens a brain that holds the document "seed" and has
// held the document "x", with the graph's HNSW index on. It gives the save
// request of x and the vector the nodes of x take.
func brainWithAGraphIndex(t *testing.T) (*DB, KnowledgeSaveRequest, []float32) {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "brain.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	seed := KnowledgeSaveRequest{KnowledgeID: "seed", Content: "Cheese is made from milk, salt and time."}
	x := KnowledgeSaveRequest{KnowledgeID: "x", Content: "The indexes follow the order of the commits."}
	if _, err := db.SaveKnowledge(ctx, seed); err != nil {
		t.Fatal(err)
	}
	// The vector the nodes of x take: x is saved once, read, and deleted.
	if _, err := db.SaveKnowledge(ctx, x); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := db.SQL().QueryRow(`SELECT vector FROM graph_nodes WHERE length(vector) > 0 AND ` +
		db.Dialect().JSONTextGuarded("properties", "document_id") + ` = 'x' LIMIT 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	probe, err := encoding.DecodeVector(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := db.graph.EnableHNSWIndex(len(probe)); err != nil {
		t.Fatal(err)
	}
	return db, x, probe
}

// A saved document and a deleted one reserve their place before they commit
// and bring the indexes, of the store and of the graph, up to date inside
// it. Outside the place, two documents written at once reach the indexes in
// an order that is not the order of their commits.
func TestAKnowledgeWriteSyncsTheIndexesInItsPlace(t *testing.T) {
	db, x, probe := brainWithAGraphIndex(t)
	ctx := context.Background()
	watched := &watchedStore{BrainStore: db.store, db: db, doc: "x", probe: probe}
	db.store = watched
	t.Cleanup(func() { db.store = watched.BrainStore })

	if _, err := db.SaveKnowledge(ctx, x); err != nil {
		t.Fatal(err)
	}
	saved := strings.Join(watched.events, "; ")
	if want := "reserve, document absent; place taken, graph index holds other documents; store deletes; store upserts; place given up, graph index holds the document"; saved != want {
		t.Errorf("a save did\n  %s\nwant\n  %s", saved, want)
	}

	watched.events = nil
	if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "x"}); err != nil {
		t.Fatal(err)
	}
	deleted := strings.Join(watched.events, "; ")
	if want := "reserve, document present; place taken, graph index holds only nodes the table lacks; store deletes; place given up, graph index holds other documents"; deleted != want {
		t.Errorf("a delete did\n  %s\nwant\n  %s", deleted, want)
	}
}
