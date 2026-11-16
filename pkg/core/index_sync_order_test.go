package core

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
)

// indexedStore is a store with an HNSW index and nothing in it.
func indexedStore(t *testing.T) *SQLiteStore {
	t.Helper()
	config := DefaultConfig()
	config.Path = filepath.Join(t.TempDir(), "store.db")
	config.VectorDim = 4
	config.HNSW.Enabled = true
	store, err := NewWithConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// indexAgainstTable lists what the HNSW index and the table disagree on:
// an id only one of them holds, or a vector that is not the committed one.
func indexAgainstTable(t *testing.T, store *SQLiteStore) []string {
	t.Helper()
	table := map[string][]float32{}
	rows, err := store.db.Query(`SELECT id, vector FROM embeddings`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			t.Fatal(err)
		}
		vector, err := encoding.DecodeVector(raw)
		if err != nil {
			t.Fatal(err)
		}
		table[id] = vector
	}
	var drift []string
	live := store.hnswIndex.LiveNodeDims()
	for id := range live {
		if _, ok := table[id]; !ok {
			drift = append(drift, "index only: "+id)
		}
	}
	for id, want := range table {
		if _, ok := live[id]; !ok {
			drift = append(drift, "table only: "+id)
			continue
		}
		if got := store.hnswIndex.Nodes[id].Vector; fmt.Sprint(got) != fmt.Sprint(want) {
			drift = append(drift, fmt.Sprintf("stale vector: %s is %v in the index, %v in the table", id, got, want))
		}
	}
	sort.Strings(drift)
	return drift
}

// write is one transaction: it deletes ids, upserts vectors, reserves its
// place, commits and syncs in its place, as a saved or deleted document does.
func write(t *testing.T, store *SQLiteStore, deleted []string, upserts []*Embedding, synced []*Embedding) error {
	t.Helper()
	ctx := context.Background()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range deleted {
		if _, err := tx.ExecContext(ctx, `DELETE FROM embeddings WHERE id = ?`, id); err != nil {
			return err
		}
	}
	if err := store.UpsertBatchTx(ctx, tx, upserts); err != nil {
		return err
	}
	syncInOrder := store.ReserveIndexSync()
	defer syncInOrder(nil)
	if err := tx.Commit(); err != nil {
		return err
	}
	syncInOrder(func() {
		store.SyncDeletedEmbeddingIDs(ctx, deleted)
		store.SyncUpsertedEmbeddings(ctx, synced)
	})
	return nil
}

// committed counts the rows of the table that have this id.
func committed(t *testing.T, store *SQLiteStore, id string) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM embeddings WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// heldSync starts a transaction whose sync stops in the store, on a vector
// the index refuses, and returns once it is there. The store holds a.
func heldSync(t *testing.T, store *SQLiteStore, synced ...*Embedding) (held *heldLogger, done chan error) {
	t.Helper()
	a := &Embedding{ID: "a", Vector: []float32{1, 0, 0, 0}, Content: "a"}
	if err := write(t, store, nil, []*Embedding{a}, []*Embedding{a}); err != nil {
		t.Fatal(err)
	}
	held = &heldLogger{Logger: NopLogger(), entered: make(chan struct{}), release: make(chan struct{})}
	store.SetLogger(held)
	done = make(chan error, 1)
	var upserts []*Embedding
	upserts = append(upserts, synced...)
	go func() {
		done <- write(t, store, nil, upserts, append([]*Embedding{{ID: "refused", Vector: []float32{1, 0, 0}}}, synced...))
	}()
	select {
	case <-held.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the sync did not report the vector the index refuses")
	}
	return held, done
}

// The index is brought up to date after the commit, outside the transaction.
// Two transactions must do it in the order they committed: when the delete
// of a row committed after its write, the delete reaches the index after
// the write, however long the write's sync takes. Out of order, the index
// keeps a row the table no longer holds.
func TestADeleteCommittedAfterAWriteReachesTheIndexAfterIt(t *testing.T) {
	store := indexedStore(t)
	// The write of x: its sync stops before it reaches x.
	held, written := heldSync(t, store, &Embedding{ID: "x", Vector: []float32{0, 1, 0, 0}, Content: "x"})

	// x is committed. Its delete commits at once, and reaches the index
	// only when the write's sync is over.
	deleted := make(chan error, 1)
	go func() { deleted <- write(t, store, []string{"x"}, nil, nil) }()
	deadline := time.Now().Add(10 * time.Second)
	for committed(t, store, "x") != 0 {
		if time.Now().After(deadline) {
			close(held.release)
			t.Fatal("the delete did not commit while the write before it was being indexed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case err := <-deleted:
		close(held.release)
		t.Fatalf("the delete was synced (%v) while the write before it was still being indexed", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(held.release)
	for _, done := range []chan error{written, deleted} {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if drift := indexAgainstTable(t, store); len(drift) != 0 {
		t.Errorf("the index and the table disagree: %v", drift)
	}
}

// Nothing is held across a commit: while one transaction is being indexed,
// another commits, a write of SQL beside them is done, and a
// search is served. Only the sync of the second transaction waits.
func TestWritersAndSearchesGoOnWhileATransactionIsIndexed(t *testing.T) {
	store := indexedStore(t)
	held, first := heldSync(t, store)
	b := &Embedding{ID: "b", Vector: []float32{0, 1, 0, 0}, Content: "b"}
	second := make(chan error, 1)
	go func() { second <- write(t, store, nil, []*Embedding{b}, []*Embedding{b}) }()
	deadline := time.Now().Add(10 * time.Second)
	for committed(t, store, "b") != 1 {
		if time.Now().After(deadline) {
			close(held.release)
			t.Fatal("the second transaction did not commit while the first was being indexed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	other := make(chan error, 1)
	go func() {
		_, err := store.db.Exec(`UPDATE embeddings SET content = 'a, written again' WHERE id = 'a'`)
		other <- err
	}()
	select {
	case err := <-other:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(5 * time.Second):
		t.Error("a write outside the two transactions waited for the index sync")
	}
	if !searchReturns(t, store) {
		t.Error("the search waited for the transactions to be indexed")
	}
	if store.mu.TryLock() {
		store.mu.Unlock()
		t.Error("the store's lock could be taken exclusively during the index sync")
	}
	close(held.release)
	for _, done := range []chan error{first, second} {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if drift := indexAgainstTable(t, store); len(drift) != 0 {
		t.Errorf("the index and the table disagree: %v", drift)
	}
}

// A place is used once, and one that is given up without a sync, as after a
// commit that failed, does not hold the next: it is handed on when the place
// ahead of it is done, without making its owner wait.
func TestAPlaceGivenUpDoesNotHoldTheNext(t *testing.T) {
	store := indexedStore(t)
	held, first := heldSync(t, store)

	// A second place, given up while the first is still being indexed.
	givenUp := make(chan struct{})
	second := store.ReserveIndexSync()
	go func() {
		second(nil)
		second(func() { t.Error("a place given up was used after") })
		close(givenUp)
	}()
	select {
	case <-givenUp:
	case <-time.After(5 * time.Second):
		t.Fatal("giving a place up waited for the sync ahead of it")
	}

	// A third place waits for the first through the second.
	third := store.ReserveIndexSync()
	ran := make(chan int, 2)
	go func() {
		third(func() { ran <- 1 })
		third(func() { ran <- 2 })
		close(ran)
	}()
	select {
	case <-ran:
		close(held.release)
		t.Fatal("a place was used before the one two ahead of it was done")
	case <-time.After(300 * time.Millisecond):
	}
	close(held.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	var runs []int
	for n := range ran {
		runs = append(runs, n)
	}
	if len(runs) != 1 || runs[0] != 1 {
		t.Errorf("the third place ran %v, want its first function once", runs)
	}
}

// A sync that panics still gives its place up.
func TestAPlaceIsGivenUpWhenItsSyncPanics(t *testing.T) {
	store := indexedStore(t)
	func() {
		defer func() { _ = recover() }()
		store.ReserveIndexSync()(func() { panic("sync") })
	}()
	next := make(chan struct{})
	go func() { store.ReserveIndexSync()(func() { close(next) }) }()
	select {
	case <-next:
	case <-time.After(5 * time.Second):
		t.Fatal("the place after a sync that panicked was never given")
	}
}

// PostgreSQL keeps its index in the transaction: a place there runs what it
// is given at once, and takes nil.
func TestAPostgresPlaceRunsItsSyncAtOnce(t *testing.T) {
	place := (&PostgresStore{}).ReserveIndexSync()
	place(nil)
	ran := false
	place(func() { ran = true })
	if !ran {
		t.Error("the sync given to a PostgreSQL place did not run")
	}
}

// Writers whose transactions delete and write the same rows, with searches
// beside them: when they are done, the index holds the rows of the table
// and their committed vectors.
func TestTheIndexHoldsWhatTheTableHoldsAfterCrossingTransactions(t *testing.T) {
	store := indexedStore(t)
	const writers, rounds, ids = 6, 10, 60
	stop := make(chan struct{})
	var searchers sync.WaitGroup
	for range 2 {
		searchers.Add(1)
		go func() {
			defer searchers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = store.Search(context.Background(), []float32{1, 0, 0, 0}, SearchOptions{TopK: 5})
				}
			}
		}()
	}
	var wg sync.WaitGroup
	errs := make(chan error, writers*rounds)
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range rounds {
				// Each transaction deletes a run of ids and writes another
				// that overlaps it and those of the other writers.
				var deleted []string
				var upserts []*Embedding
				for i := range 12 {
					deleted = append(deleted, fmt.Sprintf("id-%02d", (w*7+r*5+i)%ids))
				}
				for i := range 24 {
					id := fmt.Sprintf("id-%02d", (w*11+r*3+i+6)%ids)
					upserts = append(upserts, &Embedding{ID: id, Content: id, Vector: []float32{float32(w + 1), float32(r + 1), float32(i + 1), 1}})
				}
				if err := write(t, store, deleted, upserts, upserts); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	searchers.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if drift := indexAgainstTable(t, store); len(drift) != 0 {
		t.Errorf("the index and the table disagree on %d rows: %v", len(drift), drift)
	}
}
