package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"
)

func feedBackends(t *testing.T) []backend {
	t.Helper()
	out := backends(t)
	for _, b := range out {
		if err := b.store.EnsureChangeFeed(context.Background()); err != nil {
			t.Fatalf("%s: EnsureChangeFeed: %v", b.name, err)
		}
	}
	return out
}

func allChanges(t *testing.T, g *GraphStore, after int64) []ChangeEvent {
	t.Helper()
	var out []ChangeEvent
	for {
		page, err := g.Changes(context.Background(), after, 1000)
		if err != nil {
			t.Fatalf("Changes(%d): %v", after, err)
		}
		out = append(out, page...)
		if len(page) < 1000 {
			return out
		}
		after = page[len(page)-1].Seq
	}
}

func exTriple(s, p, o string) *RDFTriple {
	return &RDFTriple{Subject: NewIRI("http://ex.test/" + s), Predicate: NewIRI("http://ex.test/" + p), Object: NewIRI("http://ex.test/" + o)}
}

func summarize(events []ChangeEvent) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = ev.Kind + " " + ev.Op + " " + ev.ID
	}
	return out
}

func TestTheChangeFeedRecordsEachCommittedGraphWriteOnce(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			start, err := g.ChangesHead(ctx)
			if err != nil {
				t.Fatal(err)
			}
			vec := []float32{0.1, 0.2, 0.3, 0.4}
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(g.UpsertNode(ctx, &GraphNode{ID: "alice", Vector: vec, NodeType: "Person", Properties: map[string]any{"name": "Alice"}}))
			must(g.UpsertNode(ctx, &GraphNode{ID: "bob", Vector: vec, NodeType: "Person"}))
			// Identical content: not an event.
			must(g.UpsertNode(ctx, &GraphNode{ID: "bob", Vector: vec, NodeType: "Person"}))
			must(g.UpsertEdge(ctx, &GraphEdge{ID: "e1", FromNodeID: "alice", ToNodeID: "bob", EdgeType: "knows", Weight: 1}))
			must(g.UpsertNode(ctx, &GraphNode{ID: "bob", Vector: vec, NodeType: "Person", Properties: map[string]any{"name": "Bob"}}))
			tr := exTriple("s", "p", "o")
			must(g.UpsertTriple(ctx, tr))
			must(g.UpsertTriple(ctx, exTriple("s", "p", "o")))
			must(g.DeleteTriple(ctx, *exTriple("s", "p", "o")))
			must(g.DeleteNode(ctx, "bob"))

			events := allChanges(t, g, start)
			want := []string{
				"node insert alice",
				"node insert bob",
				"edge insert e1",
				"node supersede bob",
				"triple insert " + tr.ID,
				"triple delete " + tr.ID,
				"edge delete e1",
				"node delete bob",
			}
			got := summarize(events)
			// The edge cascades with the node; which of the two the database
			// reports first is its business, so compare the tail as a set.
			if len(got) != len(want) {
				t.Fatalf("events:\n%v\nwant:\n%v", got, want)
			}
			sort.Strings(got[6:])
			sort.Strings(want[6:])
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("event %d = %q, want %q\nall: %v", i, got[i], want[i], got)
				}
			}
			for i := 1; i < len(events); i++ {
				if events[i].Seq <= events[i-1].Seq {
					t.Fatalf("seq not increasing: %d then %d", events[i-1].Seq, events[i].Seq)
				}
			}
			var after struct {
				Subject   string `json:"subject"`
				Predicate string `json:"predicate"`
			}
			if err := json.Unmarshal(events[4].After, &after); err != nil || after.Predicate != "http://ex.test/p" {
				t.Fatalf("triple summary %s (%v)", events[4].After, err)
			}
			var node struct {
				Properties map[string]any `json:"properties"`
			}
			if err := json.Unmarshal(events[3].After, &node); err != nil || node.Properties["name"] != "Bob" {
				t.Fatalf("node summary should embed properties as JSON: %s (%v)", events[3].After, err)
			}
			if events[3].Reason != ReasonSuperseded || events[3].Producer != ProducerGraphUpsert {
				t.Fatalf("supersede reason/producer = %q/%q", events[3].Reason, events[3].Producer)
			}
		})
	}
}

func TestAMergeIsReportedAsAMerge(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			vec := []float32{0.1, 0.2, 0.3, 0.4}
			for _, id := range []string{"canon", "alias", "other"} {
				if err := g.UpsertNode(ctx, &GraphNode{ID: id, Vector: vec, NodeType: "Thing"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := g.UpsertEdge(ctx, &GraphEdge{ID: "ae", FromNodeID: "alias", ToNodeID: "other", EdgeType: "rel", Weight: 1}); err != nil {
				t.Fatal(err)
			}
			head, _ := g.ChangesHead(ctx)
			if err := g.MergeEntities(ctx, "canon", []string{"alias"}); err != nil {
				t.Fatal(err)
			}
			got := summarize(allChanges(t, g, head))
			want := []string{"edge merge ae", "node merge alias"}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("merge events %v, want %v", got, want)
			}
		})
	}
}

func TestARolledBackTransactionLeavesNoEvent(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			head, err := g.ChangesHead(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 20; i++ {
				tx, err := g.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := g.txExec(ctx, tx, `INSERT INTO kg_triples (id, subject_kind, subject_value, predicate_value, object_kind, object_value)
					VALUES (?, 'iri', 'http://ex.test/a', 'http://ex.test/p', 'iri', 'http://ex.test/b')`, fmt.Sprintf("rolled-%d", i)); err != nil {
					t.Fatal(err)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			if err := g.UpsertTriple(ctx, exTriple("kept", "p", "o")); err != nil {
				t.Fatal(err)
			}
			events := allChanges(t, g, head)
			if len(events) != 1 || events[0].Kind != ChangeKindTriple {
				t.Fatalf("after 20 rollbacks and one commit: %v", summarize(events))
			}
		})
	}
}

// The property the cursor exists for: a reader tailing the log while many
// writers commit concurrently sees every committed event exactly once, in the
// same order a reader arriving afterwards sees, with nothing appearing behind
// its cursor after it moved past.
func TestConcurrentWritersAreSeenExactlyOnceInCommitOrderByATailingReader(t *testing.T) {
	writes := 10_000
	if testing.Short() {
		writes = 1500
	}
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			start, err := g.ChangesHead(ctx)
			if err != nil {
				t.Fatal(err)
			}
			const writers = 8
			perWriter := writes / writers

			stop := make(chan struct{})
			var tailed []ChangeEvent
			tailErr := make(chan error, 1)
			go func() {
				cursor := start
				for {
					page, err := g.Changes(ctx, cursor, 300)
					if err != nil {
						tailErr <- err
						return
					}
					tailed = append(tailed, page...)
					if len(page) > 0 {
						cursor = page[len(page)-1].Seq
						continue
					}
					select {
					case <-stop:
						// One last read after every writer finished.
						rest, err := g.Changes(ctx, cursor, 1_000_000)
						tailed = append(tailed, rest...)
						tailErr <- err
						return
					default:
						time.Sleep(time.Millisecond)
					}
				}
			}()

			// And a subscription, the in-process consumer, tailing the same
			// writes concurrently.
			var subMu sync.Mutex
			var subscribed []ChangeEvent
			subCtx, stopSub := context.WithCancel(ctx)
			defer stopSub()
			subDone := make(chan error, 1)
			go func() {
				subDone <- g.SubscribeChanges(subCtx, start, SubscribeOptions{BatchSize: 300, PollInterval: time.Millisecond},
					func(batch []ChangeEvent) error {
						subMu.Lock()
						subscribed = append(subscribed, batch...)
						subMu.Unlock()
						return nil
					})
			}()

			var wg sync.WaitGroup
			errs := make(chan error, writers)
			for w := 0; w < writers; w++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					for i := 0; i < perWriter; i++ {
						// Every eighth write is a transaction that rolls back.
						if i%8 == 7 {
							tx, err := g.db.BeginTx(ctx, nil)
							if err != nil {
								errs <- err
								return
							}
							_, err = g.txExec(ctx, tx, `INSERT INTO kg_triples (id, subject_kind, subject_value, predicate_value, object_kind, object_value)
								VALUES (?, 'iri', 'x', 'y', 'iri', 'z')`, fmt.Sprintf("rb-%d-%d", w, i))
							_ = tx.Rollback()
							if err != nil {
								errs <- err
								return
							}
							continue
						}
						tx, err := g.db.BeginTx(ctx, nil)
						if err != nil {
							errs <- err
							return
						}
						if _, err := g.txExec(ctx, tx, `INSERT INTO kg_triples (id, subject_kind, subject_value, predicate_value, object_kind, object_value)
							VALUES (?, 'iri', ?, 'http://ex.test/seq', 'iri', ?)`,
							fmt.Sprintf("w%d-%05d", w, i), fmt.Sprintf("w%d", w), fmt.Sprintf("%05d", i)); err != nil {
							_ = tx.Rollback()
							errs <- err
							return
						}
						if err := tx.Commit(); err != nil {
							errs <- err
							return
						}
					}
				}(w)
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Fatalf("writer: %v", err)
			}
			close(stop)
			if err := <-tailErr; err != nil {
				t.Fatalf("tail: %v", err)
			}

			final := allChanges(t, g, start)
			committed := 0
			for w := 0; w < writers; w++ {
				for i := 0; i < perWriter; i++ {
					if i%8 != 7 {
						committed++
					}
				}
			}
			if len(final) != committed {
				t.Fatalf("log holds %d events for %d committed writes", len(final), committed)
			}
			deadline := time.Now().Add(20 * time.Second)
			for {
				subMu.Lock()
				n := len(subscribed)
				subMu.Unlock()
				if n >= len(final) || time.Now().After(deadline) {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			stopSub()
			<-subDone
			if len(subscribed) != len(final) {
				t.Fatalf("subscription delivered %d events, the log holds %d", len(subscribed), len(final))
			}
			for i := range final {
				if subscribed[i].Seq != final[i].Seq {
					t.Fatalf("subscription event %d is seq %d, log has %d", i, subscribed[i].Seq, final[i].Seq)
				}
			}
			if len(tailed) != len(final) {
				t.Fatalf("tailing reader saw %d events, the log holds %d — an event committed behind the cursor", len(tailed), len(final))
			}
			seen := make(map[string]bool, len(final))
			lastPerWriter := make(map[string]string)
			for i := range final {
				if tailed[i].Seq != final[i].Seq || tailed[i].ID != final[i].ID {
					t.Fatalf("event %d: tail saw %d/%s, log holds %d/%s", i, tailed[i].Seq, tailed[i].ID, final[i].Seq, final[i].ID)
				}
				if i > 0 && final[i].Seq <= final[i-1].Seq {
					t.Fatalf("seq not increasing at %d", i)
				}
				id := final[i].ID
				if seen[id] {
					t.Fatalf("event for %s delivered twice", id)
				}
				seen[id] = true
				// A writer's own commits are ordered, so its events must be.
				writer := id[:len(id)-6]
				if prev := lastPerWriter[writer]; prev != "" && prev > id {
					t.Fatalf("writer %s: %s delivered after %s", writer, id, prev)
				}
				lastPerWriter[writer] = id
			}
		})
	}
}

// On PostgreSQL two transactions can commit in the opposite order to the one
// they wrote in. The seq must follow the commit, or a reader that moved past
// the later writer's seq never sees the earlier one.
func TestASlowTransactionIsNotSkippedByAReaderThatMovedPastAFasterOne(t *testing.T) {
	for _, b := range feedBackends(t) {
		if b.name != "postgres" {
			continue // SQLite's single writer cannot interleave two commits.
		}
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			head, _ := g.ChangesHead(ctx)
			insert := `INSERT INTO kg_triples (id, subject_kind, subject_value, predicate_value, object_kind, object_value)
				VALUES (?, 'iri', 'a', 'b', 'iri', 'c')`
			slow, err := g.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = slow.Rollback() }()
			if _, err := g.txExec(ctx, slow, insert, "slow"); err != nil {
				t.Fatal(err)
			}
			fast, err := g.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := g.txExec(ctx, fast, insert, "fast"); err != nil {
				t.Fatal(err)
			}
			if err := fast.Commit(); err != nil {
				t.Fatal(err)
			}
			first := allChanges(t, g, head)
			if len(first) != 1 || first[0].ID != "fast" {
				t.Fatalf("before the slow commit: %v", summarize(first))
			}
			if err := slow.Commit(); err != nil {
				t.Fatal(err)
			}
			second := allChanges(t, g, first[0].Seq)
			if len(second) != 1 || second[0].ID != "slow" {
				t.Fatalf("after the slow commit, resuming from %d: %v — the slow writer's event was skipped", first[0].Seq, summarize(second))
			}
		})
	}
}

func TestAConsumerResumingFromItsCursorAfterARestartLosesNothing(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			start, _ := g.ChangesHead(ctx)
			for i := 0; i < 50; i++ {
				if err := g.UpsertTriple(ctx, exTriple(fmt.Sprintf("s%02d", i), "p", "o")); err != nil {
					t.Fatal(err)
				}
			}
			firstHalf, err := g.Changes(ctx, start, 20)
			if err != nil {
				t.Fatal(err)
			}
			cursor := firstHalf[len(firstHalf)-1].Seq

			// A new store over the same database: everything in memory is gone,
			// only the cursor the consumer kept survives.
			restarted := NewGraphStoreOn(g.db, g.dialect, g.store)
			for i := 50; i < 80; i++ {
				if err := restarted.UpsertTriple(ctx, exTriple(fmt.Sprintf("s%02d", i), "p", "o")); err != nil {
					t.Fatal(err)
				}
			}
			rest := allChanges(t, restarted, cursor)
			all := append(append([]ChangeEvent(nil), firstHalf...), rest...)
			if len(all) != 80 {
				t.Fatalf("resumed consumer saw %d events, want 80", len(all))
			}
			seen := map[string]bool{}
			for _, ev := range all {
				if seen[ev.ID] {
					t.Fatalf("%s seen twice", ev.ID)
				}
				seen[ev.ID] = true
			}
		})
	}
}

func TestASubscriptionDeliversCommittedWritesInOrderAsTheyHappen(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			g := b.store
			head, _ := g.ChangesHead(ctx)
			got := make(chan ChangeEvent, 100)
			subErr := make(chan error, 1)
			subCtx, stop := context.WithCancel(ctx)
			go func() {
				subErr <- g.SubscribeChanges(subCtx, head, SubscribeOptions{BatchSize: 3}, func(batch []ChangeEvent) error {
					for _, ev := range batch {
						got <- ev
					}
					return nil
				})
			}()
			for i := 0; i < 10; i++ {
				if err := g.UpsertTriple(ctx, exTriple(fmt.Sprintf("sub%d", i), "p", "o")); err != nil {
					t.Fatal(err)
				}
				g.NotifyChanges()
			}
			var last int64
			for i := 0; i < 10; i++ {
				select {
				case ev := <-got:
					if ev.Seq <= last {
						t.Fatalf("out of order: %d after %d", ev.Seq, last)
					}
					last = ev.Seq
				case <-ctx.Done():
					t.Fatalf("only %d of 10 events delivered", i)
				}
			}
			stop()
			if err := <-subErr; !errors.Is(err, context.Canceled) {
				t.Fatalf("subscription ended with %v", err)
			}
		})
	}
}

func TestPruningKeepsTheNewestRowsAndRefusesACursorItOutran(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			start, _ := g.ChangesHead(ctx)
			for i := 0; i < 30; i++ {
				if err := g.UpsertTriple(ctx, exTriple(fmt.Sprintf("pr%02d", i), "p", "o")); err != nil {
					t.Fatal(err)
				}
			}
			head, _ := g.ChangesHead(ctx)
			report, err := g.PruneChanges(ctx, ChangeFeedRetention{MaxRows: 10})
			if err != nil {
				t.Fatal(err)
			}
			if report.PrunedThrough != head-10 {
				t.Fatalf("pruned through %d, want %d", report.PrunedThrough, head-10)
			}
			if _, err := g.Changes(ctx, start, 100); !errors.Is(err, ErrChangesPruned) {
				t.Fatalf("a cursor behind the prune must be refused, got %v", err)
			}
			kept := allChanges(t, g, report.PrunedThrough)
			if len(kept) != 10 {
				t.Fatalf("kept %d events, want 10", len(kept))
			}
			if again, _ := g.ChangesHead(ctx); again != head {
				t.Fatalf("head moved from %d to %d by pruning", head, again)
			}
			// Age: nothing is older than an hour yet.
			if r, err := g.PruneChanges(ctx, ChangeFeedRetention{MaxAge: time.Hour}); err != nil || r.Removed != 0 {
				t.Fatalf("age prune removed %v (%v)", r, err)
			}
		})
	}
}

// The cost the feed adds to a write: the same mix of writes against a store
// without the triggers and one with them, alternating in rounds so that drift
// in the machine's load lands on both. Opt-in, since it is a measurement.
func TestChangeFeedWriteOverhead(t *testing.T) {
	if os.Getenv("CORTEXDB_FEED_BENCH") == "" {
		t.Skip("set CORTEXDB_FEED_BENCH=1 to measure")
	}
	ctx := context.Background()
	stores := map[bool]*GraphStore{}
	for _, on := range []bool{false, true} {
		_, g, cleanup := setupTestGraph(t)
		t.Cleanup(cleanup)
		if on {
			if err := g.EnsureChangeFeed(ctx); err != nil {
				t.Fatal(err)
			}
		}
		stores[on] = g
	}
	vec := []float32{0.1, 0.2, 0.3}
	samples := map[bool][]time.Duration{}
	byKind := map[string][]time.Duration{}
	n := 0
	for round := 0; round < 10; round++ {
		for _, on := range []bool{round%2 == 0, round%2 != 0} {
			g := stores[on]
			for i := 0; i < 200; i++ {
				n++
				id := fmt.Sprintf("n%d", n)
				start := time.Now()
				var err error
				switch i % 4 {
				case 0:
					err = g.UpsertNode(ctx, &GraphNode{ID: id, Vector: vec, NodeType: "Thing", Properties: map[string]any{"name": id}})
				case 1:
					err = g.UpsertEdge(ctx, &GraphEdge{ID: "e" + id, FromNodeID: fmt.Sprintf("n%d", n-1), ToNodeID: fmt.Sprintf("n%d", n-1), EdgeType: "self", Weight: 1})
				case 2:
					err = g.UpsertTriple(ctx, exTriple(id, "p", "o"))
				default:
					err = g.DeleteTriple(ctx, *exTriple(fmt.Sprintf("n%d", n-1), "p", "o"))
				}
				if err != nil {
					t.Fatal(err)
				}
				samples[on] = append(samples[on], time.Since(start))
				byKind[fmt.Sprintf("%v/%d", on, i%4)] = append(byKind[fmt.Sprintf("%v/%d", on, i%4)], time.Since(start))
			}
		}
	}
	p := func(d []time.Duration, q float64) time.Duration {
		sorted := append([]time.Duration(nil), d...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		return sorted[int(float64(len(sorted)-1)*q)]
	}
	off, on := samples[false], samples[true]
	for k := 0; k < 4; k++ {
		a, b := byKind[fmt.Sprintf("false/%d", k)], byKind[fmt.Sprintf("true/%d", k)]
		t.Logf("op %d: off p50=%v on p50=%v", k, p(a, .5), p(b, .5))
	}
	t.Logf("feed off p50=%v p95=%v | feed on p50=%v p95=%v | p50 overhead %.1f%%",
		p(off, .5), p(off, .95), p(on, .5), p(on, .95), 100*(float64(p(on, .5))/float64(p(off, .5))-1))
}

// On SQLite the cursor is the rowid, and a table emptied by pruning would hand
// out 1 again. Pruning must therefore keep the newest event however old it is.
func TestPruningEverythingStillKeepsTheCursorMovingForward(t *testing.T) {
	for _, b := range feedBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			g := b.store
			for i := 0; i < 5; i++ {
				if err := g.UpsertTriple(ctx, exTriple(fmt.Sprintf("old%d", i), "p", "o")); err != nil {
					t.Fatal(err)
				}
			}
			head, _ := g.ChangesHead(ctx)
			if _, err := g.PruneChanges(ctx, ChangeFeedRetention{MaxAge: time.Nanosecond}); err != nil {
				t.Fatal(err)
			}
			if err := g.UpsertTriple(ctx, exTriple("new", "p", "o")); err != nil {
				t.Fatal(err)
			}
			events := allChanges(t, g, head)
			if len(events) != 1 || events[0].Seq <= head {
				t.Fatalf("after pruning by age, the next event must still come after %d: %v", head, summarize(events))
			}
		})
	}
}
