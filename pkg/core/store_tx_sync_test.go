package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// heldLogger stops the store inside a warning until released, and says when
// it got there: a way to stand inside SyncUpsertedEmbeddings while it works.
type heldLogger struct {
	Logger
	entered chan struct{}
	release chan struct{}
}

func (l *heldLogger) Warn(string, ...any) {
	l.entered <- struct{}{}
	<-l.release
}

func (l *heldLogger) With(...any) Logger { return l }

// syncStoppedHalfWay starts a sync of committed vectors and returns once it
// is stopped inside the store: a vector of another size is refused by the
// index, which the store reports with a warning, and the logger holds it
// there. finish lets it go and waits for it.
func syncStoppedHalfWay(t *testing.T) (store *SQLiteStore, finish func()) {
	t.Helper()
	return syncStoppedIn(t, false)
}

// syncStoppedIn is syncStoppedHalfWay for a store of either index: with ivf,
// the index that refuses the vector is the trained IVF one, which the sync
// fills after the HNSW one.
func syncStoppedIn(t *testing.T, ivf bool) (store *SQLiteStore, finish func()) {
	t.Helper()
	config := DefaultConfig()
	config.Path = filepath.Join(t.TempDir(), "store.db")
	config.VectorDim = 4
	config.HNSW.Enabled = !ivf
	if ivf {
		config.IndexType = IndexTypeIVF
	}
	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertBatch(ctx, []*Embedding{
		{ID: "a", Vector: []float32{1, 0, 0, 0}, Content: "a"},
		{ID: "b", Vector: []float32{0, 1, 0, 0}, Content: "b"},
	}); err != nil {
		t.Fatal(err)
	}
	if ivf {
		if err := store.TrainIndex(ctx, 1); err != nil {
			t.Fatal(err)
		}
	}

	held := &heldLogger{Logger: NopLogger(), entered: make(chan struct{}), release: make(chan struct{})}
	store.SetLogger(held)
	synced := make(chan struct{})
	go func() {
		defer close(synced)
		store.SyncUpsertedEmbeddings(ctx, []*Embedding{{ID: "c", Vector: []float32{1, 0, 0}}})
	}()
	select {
	case <-held.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the sync did not report the vector the index refuses")
	}
	return store, func() {
		close(held.release)
		<-synced
	}
}

// searchReturns runs one search and reports whether it came back with a.
func searchReturns(t *testing.T, store *SQLiteStore) bool {
	t.Helper()
	found := make(chan []ScoredEmbedding, 1)
	go func() {
		hits, err := store.Search(context.Background(), []float32{1, 0, 0, 0}, SearchOptions{TopK: 1})
		if err != nil {
			t.Error(err)
		}
		found <- hits
	}()
	select {
	case hits := <-found:
		if len(hits) != 1 || hits[0].ID != "a" {
			t.Errorf("expected a, got %+v", hits)
		}
		return true
	case <-time.After(3 * time.Second):
		return false
	}
}

// A document's vectors are committed first and put into the in-memory index
// after: that second step can take as long as the document is large. Searches
// must go on while it runs. It used to take the store's exclusive lock, which
// every search waits for.
func TestSearchGoesOnWhileCommittedVectorsAreIndexed(t *testing.T) {
	store, finish := syncStoppedHalfWay(t)
	defer finish()
	if !searchReturns(t, store) {
		t.Error("the search waited for the index sync to end")
	}
}

// The same while the vectors enter the IVF index, the second the sync fills.
func TestSearchGoesOnWhileCommittedVectorsEnterTheIVFIndex(t *testing.T) {
	store, finish := syncStoppedIn(t, true)
	defer finish()
	if !searchReturns(t, store) {
		t.Error("the search waited for the index sync to end")
	}
	if store.mu.TryLock() {
		store.mu.Unlock()
		t.Error("the store's lock could be taken exclusively during the index sync")
	}
}

// The sync still holds the store's lock, shared: nothing can swap an index
// under it, which is what the lock is for.
func TestNoIndexIsSwappedWhileCommittedVectorsAreIndexed(t *testing.T) {
	store, finish := syncStoppedHalfWay(t)
	defer finish()
	if store.mu.TryLock() {
		store.mu.Unlock()
		t.Error("the store's lock could be taken exclusively during the index sync")
	}
}

// The delete sync holds the store's lock too: while an index is being
// swapped, which takes the lock exclusively, it waits.
func TestDeleteSyncWaitsWhileAnIndexIsSwapped(t *testing.T) {
	store, finish := syncStoppedHalfWay(t)
	finish()
	store.mu.Lock()
	forgotten := make(chan struct{})
	go func() {
		defer close(forgotten)
		store.SyncDeletedEmbeddingIDs(context.Background(), []string{"b"})
	}()
	select {
	case <-forgotten:
		store.mu.Unlock()
		t.Fatal("the delete sync ran while the store's lock was held exclusively")
	case <-time.After(200 * time.Millisecond):
	}
	store.mu.Unlock()
	<-forgotten
}

// A re-saved document syncs its deleted ids, then its new vectors. The
// delete sync used to ask for the store's lock exclusively: while it waited
// for another document's sync to end, every new search waited behind it.
func TestSearchGoesOnWhileADeleteSyncMeetsAnIndexSync(t *testing.T) {
	store, finish := syncStoppedHalfWay(t)
	defer finish()
	forgotten := make(chan struct{})
	go func() {
		defer close(forgotten)
		store.SyncDeletedEmbeddingIDs(context.Background(), []string{"b"})
	}()
	select {
	case <-forgotten:
	case <-time.After(3 * time.Second):
		t.Fatal("the delete sync waited for the index sync to end")
	}
	if !searchReturns(t, store) {
		t.Error("the search waited for the index sync to end")
	}
	if hits, err := store.Search(context.Background(), []float32{0, 1, 0, 0}, SearchOptions{TopK: 2}); err != nil || len(hits) != 1 || hits[0].ID != "a" {
		t.Errorf("after b was forgotten a search returns %+v, %v, want a alone", hits, err)
	}
}
