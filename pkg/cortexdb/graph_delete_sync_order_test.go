package cortexdb

import (
	"context"
	"strings"
	"testing"
	"time"
)

// nodesOfX counts the graph nodes the document "x" alone holds.
const nodesOfX = `SELECT COUNT(*) FROM graph_nodes WHERE id = 'doc:' || ?1 OR id LIKE 'chunk:' || ?1 || ':%'`

// until waits for a state another goroutine brings about.
func until(t *testing.T, what string, reached func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if reached() {
			return
		}
	}
	t.Fatalf("never reached: %s", what)
}

// The graph of a document is deleted while the save of that document, which
// committed first, still waits for its place among the index syncs. The
// delete takes its place after the save, so the graph index ends without
// the nodes. Outside the queue it removed them before the save added them.
func TestADocumentGraphDeletedAfterASaveLeavesTheIndexAfterIt(t *testing.T) {
	db, x, probe := brainWithAGraphIndex(t)
	ctx := context.Background()
	count := func() (n int) {
		_ = db.SQL().QueryRow(nodesOfX, "x").Scan(&n)
		return n
	}

	// The place ahead stands for the long sync of another document.
	ahead := db.store.ReserveIndexSync()
	released := false
	t.Cleanup(func() {
		if !released {
			ahead(nil)
		}
	})

	saved := make(chan error, 1)
	go func() {
		_, err := db.SaveKnowledge(ctx, x)
		saved <- err
	}()
	until(t, "the save of x is committed", func() bool { return count() > 0 })

	deleted := make(chan error, 1)
	go func() {
		_, err := db.GraphRAGTools().DeleteDocumentGraph(ctx, ToolDeleteDocumentGraphRequest{DocumentID: "x"})
		deleted <- err
	}()
	until(t, "the delete of the graph of x is committed", func() bool { return count() == 0 })

	released = true
	ahead(func() {})
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}

	watch := &watchedStore{db: db, probe: probe}
	if got, want := watch.graphIndex(), "graph index holds other documents"; got != want {
		t.Errorf("after a save then a delete of the graph of x: %s, want: %s", got, want)
	}
}

// DeleteDocumentGraph reserves its place before it commits, and removes the
// nodes from the graph index inside it.
func TestADocumentGraphDeleteSyncsTheIndexInItsPlace(t *testing.T) {
	db, x, probe := brainWithAGraphIndex(t)
	ctx := context.Background()
	if _, err := db.SaveKnowledge(ctx, x); err != nil {
		t.Fatal(err)
	}
	watched := &watchedStore{BrainStore: db.store, db: db, doc: "x", probe: probe, committed: nodesOfX}
	db.store = watched
	t.Cleanup(func() { db.store = watched.BrainStore })

	resp, err := db.GraphRAGTools().DeleteDocumentGraph(ctx, ToolDeleteDocumentGraphRequest{DocumentID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ChunkNodesDeleted == 0 || !resp.DocumentNodeDeleted {
		t.Fatalf("the delete removed %d chunk nodes, document node %v", resp.ChunkNodesDeleted, resp.DocumentNodeDeleted)
	}
	got := strings.Join(watched.events, "; ")
	if want := "reserve, document present; place taken, graph index holds only nodes the table lacks; place given up, graph index holds other documents"; got != want {
		t.Errorf("a delete of the graph did\n  %s\nwant\n  %s", got, want)
	}

	// A dry run commits nothing and reserves nothing.
	watched.events = nil
	if _, err := db.GraphRAGTools().DeleteDocumentGraph(ctx, ToolDeleteDocumentGraphRequest{DocumentID: "seed", DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if len(watched.events) != 0 {
		t.Errorf("a dry run did: %s", strings.Join(watched.events, "; "))
	}
}

// A delete whose commit fails gives its place up: the next sync runs.
func TestADocumentGraphDeleteThatFailsToCommitGivesItsPlaceUp(t *testing.T) {
	db, x, probe := brainWithAGraphIndex(t)
	if _, err := db.SaveKnowledge(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The context ends between the reservation and the commit.
	watched := &watchedStore{BrainStore: db.store, db: db, doc: "x", probe: probe, committed: nodesOfX, atReserve: cancel}
	db.store = watched
	t.Cleanup(func() { db.store = watched.BrainStore })

	if _, err := db.GraphRAGTools().DeleteDocumentGraph(ctx, ToolDeleteDocumentGraphRequest{DocumentID: "x"}); err == nil {
		t.Fatal("the delete committed under a cancelled context")
	}
	if got := strings.Join(watched.events, "; "); got != "reserve, document present" {
		t.Errorf("a delete that failed to commit did: %s", got)
	}

	next := make(chan struct{})
	go watched.BrainStore.ReserveIndexSync()(func() { close(next) })
	select {
	case <-next:
	case <-time.After(10 * time.Second):
		t.Fatal("the place of the failed delete holds the next sync")
	}
	if got, want := watched.graphIndex(), "graph index holds the document"; got != want {
		t.Errorf("after the failed delete: %s, want: %s", got, want)
	}
}
