package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

type namedBrain struct {
	name string
	db   *DB
}

// changeBrains opens the same test on SQLite and, when configured, PostgreSQL.
func changeBrains(t *testing.T, opts ...Option) []namedBrain {
	t.Helper()
	path := filepath.Join(t.TempDir(), fmt.Sprintf("changes_%d.db", testname.Nano()))
	sqlite, err := Open(DefaultConfig(path), opts...)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	out := []namedBrain{{name: "sqlite", db: sqlite}}
	if os.Getenv("CORTEXDB_TEST_POSTGRES") == "" {
		t.Log("CORTEXDB_TEST_POSTGRES unset — PostgreSQL is NOT covered by this run")
		return out
	}
	out = append(out, namedBrain{name: "postgres", db: openPostgresBrainWith(t, 8, opts...)})
	return out
}

// openPostgresBrainWith is openPostgresBrain with options: a schema of its
// own on the test database, dropped afterwards.
func openPostgresBrainWith(t *testing.T, dims int, opts ...Option) *DB {
	t.Helper()
	dsn := os.Getenv("CORTEXDB_TEST_POSTGRES")
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		_ = admin.Close()
		t.Fatalf("extension: %v", err)
	}
	schema := fmt.Sprintf("changes_test_%d", testname.Nano())
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		_ = admin.Close()
		t.Fatalf("schema: %v", err)
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	cfg := DefaultConfig(dsn + sep + "search_path=" + schema + ",public")
	cfg.Dimensions = dims
	db, err := Open(cfg, opts...)
	if err != nil {
		t.Fatalf("Open on a postgres DSN: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		_ = admin.Close()
	})
	return db
}

func callChangesSince(t *testing.T, db *DB, req ChangesSinceRequest) ChangesSinceResponse {
	t.Helper()
	raw, err := db.GraphRAGTools().Call(context.Background(), "changes_since", mustJSONChanges(t, req))
	if err != nil {
		t.Fatalf("changes_since: %v", err)
	}
	resp, ok := raw.(*ChangesSinceResponse)
	if !ok {
		t.Fatalf("changes_since returned %T", raw)
	}
	return *resp
}

func mustJSONChanges(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestChangesSinceReportsMemoryKnowledgeAndGraphWritesInCommitOrder(t *testing.T) {
	for _, b := range changeBrains(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			db := b.db
			start := callChangesSince(t, db, ChangesSinceRequest{After: -1}).NextCursor

			if _, err := db.SaveMemory(ctx, MemorySaveRequest{MemoryID: "m1", UserID: "u1", Content: "prefers tea"}); err != nil {
				t.Fatalf("save memory: %v", err)
			}
			if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "k1", Title: "Tea", Content: "Tea is a drink."}); err != nil {
				t.Fatalf("save knowledge: %v", err)
			}
			tools := db.GraphRAGTools()
			if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{Name: "Alice", Type: "person"}, {Name: "Bob", Type: "person"}}}); err != nil {
				t.Fatal(err)
			}
			if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{{From: "Alice", To: "Bob", Type: "knows"}}}); err != nil {
				t.Fatal(err)
			}

			var events []graph.ChangeEvent
			cursor := start
			for {
				page := callChangesSince(t, db, ChangesSinceRequest{After: cursor, Limit: 3})
				events = append(events, page.Events...)
				if page.NextCursor == cursor && !page.More {
					break
				}
				cursor = page.NextCursor
				if !page.More {
					break
				}
			}
			kinds := map[string]int{}
			for i, ev := range events {
				kinds[ev.Kind]++
				if i > 0 && ev.Seq <= events[i-1].Seq {
					t.Fatalf("paged events out of order: %d after %d", ev.Seq, events[i-1].Seq)
				}
			}
			for _, kind := range []string{graph.ChangeKindMemory, graph.ChangeKindKnowledge, graph.ChangeKindNode, graph.ChangeKindEdge} {
				if kinds[kind] == 0 {
					t.Fatalf("no %s event among %v", kind, kinds)
				}
			}
			memoryAt, edgeAt := -1, -1
			for i, ev := range events {
				if ev.Kind == graph.ChangeKindMemory && memoryAt < 0 {
					memoryAt = i
				}
				if ev.Kind == graph.ChangeKindEdge {
					edgeAt = i
				}
			}
			if memoryAt > edgeAt {
				t.Fatalf("the memory was written first but reported after the relation")
			}

			only := callChangesSince(t, db, ChangesSinceRequest{After: start, Limit: 1000, Kinds: []string{graph.ChangeKindEdge}})
			for _, ev := range only.Events {
				if ev.Kind != graph.ChangeKindEdge {
					t.Fatalf("kind filter let through %s", ev.Kind)
				}
			}
			if only.NextCursor != events[len(events)-1].Seq {
				t.Fatalf("a filtered page must still advance the cursor past what it skipped: %d vs %d", only.NextCursor, events[len(events)-1].Seq)
			}
		})
	}
}

func TestChangesSinceSaysWhenItsCursorFellBehindRetention(t *testing.T) {
	for _, b := range changeBrains(t, WithChangeFeed(ChangeFeedOptions{Retention: graph.ChangeFeedRetention{MaxRows: 2}})) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			for i := 0; i < 5; i++ {
				if _, err := b.db.SaveMemory(ctx, MemorySaveRequest{MemoryID: fmt.Sprintf("m%d", i), UserID: "u", Content: "x"}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := b.db.PruneChanges(ctx); err != nil {
				t.Fatal(err)
			}
			resp := callChangesSince(t, b.db, ChangesSinceRequest{After: 0})
			if !resp.Pruned || resp.NextCursor != resp.PrunedThrough || resp.PrunedThrough == 0 {
				t.Fatalf("a cursor behind retention must be reported, not silently skipped: %+v", resp)
			}
		})
	}
}

// The behaviour this release exists for: a relation written through the
// ordinary tools changes what the schema infers, with no refresh call, and
// the inference goes away again when the relation does.
func TestRelationWritesKeepInferencesCurrentWithoutARefresh(t *testing.T) {
	for _, b := range changeBrains(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			db := b.db
			tools := db.GraphRAGTools()
			ents, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
				{Name: "Alice", Type: "person"}, {Name: "Bob", Type: "person"}, {Name: "Carol", Type: "person"}}})
			if err != nil {
				t.Fatal(err)
			}
			rel, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
				{From: "Alice", To: "Bob", Type: "manages"}, {From: "Bob", To: "Carol", Type: "manages"}}})
			if err != nil || rel.Written != 2 {
				t.Fatalf("relations: %+v %v", rel, err)
			}
			edges, err := db.Graph().GetEdges(ctx, ents.EntityNodeIDs[0], "out")
			if err != nil || len(edges) == 0 {
				t.Fatalf("edges: %v %v", edges, err)
			}
			manages := graph.NewIRI(graph.PropertyRelNamespace + edges[0].EdgeType)
			if _, err := db.UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest{Triples: []KnowledgeGraphTriple{{
				Subject: manages, Predicate: graph.NewIRI("http://www.w3.org/1999/02/22-rdf-syntax-ns#type"),
				Object: graph.NewIRI("http://www.w3.org/2002/07/owl#TransitiveProperty")}}}); err != nil {
				t.Fatal(err)
			}
			if err := db.WaitForInference(ctx); err != nil {
				t.Fatal(err)
			}
			alice := graph.NewIRI(graph.PropertyGraphNodeIRI(ents.EntityNodeIDs[0]))
			carol := graph.NewIRI(graph.PropertyGraphNodeIRI(ents.EntityNodeIDs[2]))
			found, err := db.Graph().FindTriples(ctx, graph.TriplePattern{Subject: &alice, Predicate: &manages, Object: &carol})
			if err != nil || len(found) != 1 || !found[0].Inferred {
				t.Fatalf("Alice should be inferred to manage Carol: %v %v", found, err)
			}
			if _, err := tools.DeleteEntities(ctx, ToolDeleteEntitiesRequest{Names: []string{"Bob"}}); err != nil {
				t.Fatal(err)
			}
			if err := db.WaitForInference(ctx); err != nil {
				t.Fatal(err)
			}
			found, err = db.Graph().FindTriples(ctx, graph.TriplePattern{Subject: &alice, Predicate: &manages, Object: &carol})
			if err != nil || len(found) != 0 {
				t.Fatalf("the inference outlived the relation it rested on: %v %v", found, err)
			}
			stats, ok := db.InferenceMaintenanceStats()
			if !ok || !stats.Holding {
				t.Fatalf("the maintainer should be running and holding: %+v %v", stats, ok)
			}
		})
	}
}

func TestDisablingAutoInferenceLeavesInferredTriplesToTheRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manual.db")
	db, err := Open(DefaultConfig(path), WithAutoInference(false))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	sub := graph.NewIRI("http://www.w3.org/2000/01/rdf-schema#subClassOf")
	if _, err := db.UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest{Triples: []KnowledgeGraphTriple{
		{Subject: graph.NewIRI("http://ex.test/A"), Predicate: sub, Object: graph.NewIRI("http://ex.test/B")},
		{Subject: graph.NewIRI("http://ex.test/B"), Predicate: sub, Object: graph.NewIRI("http://ex.test/C")},
	}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	inferred := true
	got, err := db.Graph().FindTriples(ctx, graph.TriplePattern{Inferred: &inferred})
	if err != nil || len(got) != 0 {
		t.Fatalf("with maintenance off nothing should be inferred before a refresh: %d %v", len(got), err)
	}
	if _, ok := db.InferenceMaintenanceStats(); ok {
		t.Fatal("no maintainer should run")
	}
}

// Per-write cost of the change feed and of dormant inference maintenance on a
// copy of the live brain, which declares no schema. Opt-in: it needs the
// snapshot and it is a measurement.
func TestChangeFeedAndDormantMaintenanceCostOnTheBrainSnapshot(t *testing.T) {
	snapshot := os.Getenv("CORTEXDB_BRAIN_SNAPSHOT")
	if snapshot == "" {
		t.Skip("set CORTEXDB_BRAIN_SNAPSHOT to a brain file to measure")
	}
	copyFile := func(name string) string {
		dst := filepath.Join(t.TempDir(), name)
		in, err := os.Open(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		defer in.Close()
		out, err := os.Create(dst)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(out, in); err != nil {
			t.Fatal(err)
		}
		_ = out.Close()
		return dst
	}
	measure := func(db *DB, label string, wait bool) []time.Duration {
		ctx := context.Background()
		tools := db.GraphRAGTools()
		out := make([]time.Duration, 0, 300)
		for i := 0; i < 300; i++ {
			start := time.Now()
			switch i % 3 {
			case 0:
				_, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{{Name: fmt.Sprintf("%s-e%d", label, i), Type: "probe"}}})
				if err != nil {
					t.Fatal(err)
				}
			case 1:
				_, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{{
					From: fmt.Sprintf("%s-e%d", label, i-1), To: fmt.Sprintf("%s-e%d", label, i-1), Type: "probe_rel"}}})
				if err != nil {
					t.Fatal(err)
				}
			default:
				_, err := db.UpsertKnowledgeGraph(ctx, KnowledgeGraphUpsertRequest{Triples: []KnowledgeGraphTriple{{
					Subject: graph.NewIRI("http://probe.test/" + label), Predicate: graph.NewIRI("http://probe.test/p"),
					Object: graph.NewLiteral(fmt.Sprint(i))}}})
				if err != nil {
					t.Fatal(err)
				}
			}
			if wait {
				if err := db.WaitForInference(ctx); err != nil {
					t.Fatal(err)
				}
			}
			out = append(out, time.Since(start))
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
	p := func(d []time.Duration, q float64) time.Duration { return d[int(float64(len(d)-1)*q)] }

	plainDB, err := Open(DefaultConfig(copyFile("plain.db")), WithChangeFeed(ChangeFeedOptions{Disabled: true}))
	if err != nil {
		t.Fatal(err)
	}
	plain := measure(plainDB, "plain", false)
	_ = plainDB.Close()

	feedOnlyDB, err := Open(DefaultConfig(copyFile("feedonly.db")), WithAutoInference(false))
	if err != nil {
		t.Fatal(err)
	}
	feedOnly := measure(feedOnlyDB, "feedonly", false)
	_ = feedOnlyDB.Close()

	fedDB, err := Open(DefaultConfig(copyFile("fed.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer fedDB.Close()
	fed := measure(fedDB, "fed", false)
	waited := measure(fedDB, "waited", true)
	stats, _ := fedDB.InferenceMaintenanceStats()
	t.Logf("no feed: p50=%v p95=%v | feed only: p50=%v p95=%v | feed + dormant maintainer: p50=%v p95=%v | + WaitForInference: p50=%v p95=%v | maintainer %+v",
		p(plain, .5), p(plain, .95), p(feedOnly, .5), p(feedOnly, .95), p(fed, .5), p(fed, .95), p(waited, .5), p(waited, .95), stats)
}
