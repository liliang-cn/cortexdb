package cortexdb

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
)

// seedAnswerGraph writes two typed nodes joined by one edge, each with a
// property graph_property_values can break down.
func seedAnswerGraph(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		t.Fatal(err)
	}
	for _, n := range []*graph.GraphNode{
		{ID: "entity:a", Content: "A", NodeType: "person", Vector: []float32{1, 0}, Properties: map[string]any{"source": "x"}},
		{ID: "entity:b", Content: "B", NodeType: "org", Vector: []float32{0, 1}, Properties: map[string]any{"source": "y"}},
		{ID: "entity:c", Content: "C", NodeType: "org", Vector: []float32{1, 1}},
	} {
		if err := db.graph.UpsertNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.graph.UpsertEdge(ctx, &graph.GraphEdge{ID: "edge:ab", FromNodeID: "entity:a", ToNodeID: "entity:b", EdgeType: "works_for", Weight: 1, Properties: map[string]any{"source": "x"}}); err != nil {
		t.Fatal(err)
	}
}

// graphAnswers is what the three cached tools say about the graph, as JSON.
type graphAnswers struct{ schema, values, stats string }

func answerJSON(t *testing.T, v any, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func servedAnswers(t *testing.T, ctx context.Context, db *DB) graphAnswers {
	t.Helper()
	schema, err := db.GraphSchema(ctx, GraphSchemaRequest{})
	s := answerJSON(t, schema, err)
	values, err := db.GraphPropertyValues(ctx, GraphPropertyValuesRequest{Key: "source"})
	v := answerJSON(t, values, err)
	stats, err := db.GraphStatistics(ctx)
	return graphAnswers{s, v, answerJSON(t, stats, err)}
}

func freshAnswers(t *testing.T, ctx context.Context, db *DB) graphAnswers {
	t.Helper()
	schema, err := db.graphSchemaFresh(ctx, GraphSchemaRequest{})
	s := answerJSON(t, schema, err)
	values, err := db.graphPropertyValuesFresh(ctx, GraphPropertyValuesRequest{Key: "source"})
	v := answerJSON(t, values, err)
	stats, err := db.graph.GetGraphStatistics(ctx)
	return graphAnswers{s, v, answerJSON(t, stats, err)}
}

// Asked again over the same graph, graph_schema, graph_property_values and
// graph_statistics are served as they were computed, without reading the
// graph again.
func TestARepeatedGraphAnswerIsServedFromTheCache(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "answers.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedAnswerGraph(t, db)

	first := servedAnswers(t, context.Background(), db)
	ctx, stages := observability.WithStages(context.Background())
	again := servedAnswers(t, ctx, db)
	if again != first {
		t.Errorf("asked again over the same graph:\nfirst %+v\nagain %+v", first, again)
	}
	if got := stageCount(stages, "graph-answer-cache"); got != 3 {
		t.Errorf("asked again over the same graph, %d of 3 answers came from the cache (stages %s)", got, stages)
	}
}

// A request that differs is a different answer: a filtered schema or a
// capped breakdown is never served for the whole one.
func TestEachGraphRequestHasItsOwnAnswer(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "requests.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedAnswerGraph(t, db)
	ctx := context.Background()

	whole, err := db.GraphSchema(ctx, GraphSchemaRequest{})
	w := answerJSON(t, whole, err)
	orgs, err := db.GraphSchema(ctx, GraphSchemaRequest{NodeTypes: []string{"org"}})
	o := answerJSON(t, orgs, err)
	if w == o {
		t.Fatal("the schema filtered to org was served as the whole schema")
	}
	fresh, err := db.graphSchemaFresh(ctx, GraphSchemaRequest{NodeTypes: []string{"org"}})
	if want := answerJSON(t, fresh, err); o != want {
		t.Errorf("filtered schema: served %s, fresh %s", o, want)
	}

	all, err := db.GraphPropertyValues(ctx, GraphPropertyValuesRequest{Key: "source"})
	a := answerJSON(t, all, err)
	one, err := db.GraphPropertyValues(ctx, GraphPropertyValuesRequest{Key: "source", Limit: 1})
	if answerJSON(t, one, err) == a {
		t.Fatal("the breakdown capped at one value was served as the whole one")
	}
}

// A caller that changes the answer it was given does not change the next
// caller's.
func TestAServedGraphAnswerIsTheCallersOwn(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "own.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedAnswerGraph(t, db)
	ctx := context.Background()

	first, err := db.GraphSchema(ctx, GraphSchemaRequest{})
	want := answerJSON(t, first, err)
	first.NodeTypes[0].PropertyKeys = nil
	first.NodeTypes = first.NodeTypes[:1]
	again, err := db.GraphSchema(ctx, GraphSchemaRequest{})
	if got := answerJSON(t, again, err); got != want {
		t.Errorf("after the first caller changed its answer, the next got %s, want %s", got, want)
	}
	stats, err := db.GraphStatistics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stats.NodeCount = -1
	if again, _ := db.GraphStatistics(ctx); again.NodeCount != 3 {
		t.Errorf("after the first caller changed its statistics, the next got %d nodes, want 3", again.NodeCount)
	}
}

// The answers served are those a fresh computation gives, after every kind
// of write that changes what they report, made through this handle or
// through another one on the same file.
func TestTheCachedGraphAnswersFollowEveryWriteToTheGraph(t *testing.T) {
	path := filepath.Join(t.TempDir(), "follow.db")
	db, err := Open(DefaultConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedAnswerGraph(t, db)
	other, err := Open(DefaultConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	ctx := context.Background()
	servedAnswers(t, ctx, db)

	exec := func(h *DB, query string) func() error {
		return func() error { _, err := h.SQL().Exec(query); return err }
	}
	writes := []struct {
		name  string
		write func() error
	}{
		{"node inserted", func() error {
			return db.graph.UpsertNode(ctx, &graph.GraphNode{ID: "entity:d", Content: "D", NodeType: "place", Vector: []float32{0, 2}, Properties: map[string]any{"source": "z"}})
		}},
		{"node type updated", exec(db, `UPDATE graph_nodes SET node_type = 'company' WHERE id = 'entity:b'`)},
		{"node property updated", exec(db, `UPDATE graph_nodes SET properties = '{"source":"w","since":"2020"}' WHERE id = 'entity:a'`)},
		{"edge inserted", func() error {
			return db.graph.UpsertEdge(ctx, &graph.GraphEdge{ID: "edge:bc", FromNodeID: "entity:b", ToNodeID: "entity:c", EdgeType: "owns", Weight: 1})
		}},
		{"edge type updated", exec(db, `UPDATE graph_edges SET edge_type = 'employs' WHERE id = 'edge:ab'`)},
		{"edge property updated", exec(db, `UPDATE graph_edges SET properties = '{"source":"v"}' WHERE id = 'edge:bc'`)},
		{"edge rewired", exec(db, `UPDATE graph_edges SET to_node_id = 'entity:d' WHERE id = 'edge:bc'`)},
		{"edge deleted", exec(db, `DELETE FROM graph_edges WHERE id = 'edge:ab'`)},
		{"node deleted then inserted at the same rowid", func() error {
			if err := exec(db, `DELETE FROM graph_nodes WHERE rowid = (SELECT MAX(rowid) FROM graph_nodes)`)(); err != nil {
				return err
			}
			return db.graph.UpsertNode(ctx, &graph.GraphNode{ID: "entity:e", Content: "E", NodeType: "event", Vector: []float32{2, 0}})
		}},
		{"node type updated by another handle", exec(other, `UPDATE graph_nodes SET node_type = 'group' WHERE id = 'entity:c'`)},
		{"node deleted by another handle", exec(other, `DELETE FROM graph_nodes WHERE id = 'entity:a'`)},
	}
	for _, w := range writes {
		before := freshAnswers(t, ctx, db)
		if err := w.write(); err != nil {
			t.Fatalf("%s: %v", w.name, err)
		}
		want := freshAnswers(t, ctx, db)
		if want == before {
			t.Fatalf("%s: the write changed no answer, so it tests nothing", w.name)
		}
		if got := servedAnswers(t, ctx, db); got != want {
			t.Errorf("%s:\nserved %+v\nfresh  %+v", w.name, got, want)
		}
	}
}

// A store that cannot hold the version counter computes every answer, as
// before, and the next call tries the counter again.
func TestAGraphWithoutTheCounterIsReadEveryTime(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "nocounter.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedAnswerGraph(t, db)
	ctx := context.Background()
	// A view holds the counter table's name: it cannot take the counter's row.
	if _, err := db.SQL().Exec(`CREATE VIEW ` + graphAnswerVersionTable + ` AS SELECT 1 AS id, 0 AS version`); err != nil {
		t.Fatal(err)
	}
	computed := 0
	compute := func() (*graph.GraphStatistics, error) {
		computed++
		return &graph.GraphStatistics{NodeCount: 1}, nil
	}
	for range 2 {
		if _, err := cachedGraphAnswer(ctx, db, "test", nil, compute); err != nil {
			t.Fatal(err)
		}
	}
	if computed != 2 {
		t.Fatalf("without the counter, 2 calls computed %d times", computed)
	}
	if _, err := db.SQL().Exec(`DROP VIEW ` + graphAnswerVersionTable); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := cachedGraphAnswer(ctx, db, "test", nil, compute); err != nil {
			t.Fatal(err)
		}
	}
	if computed != 3 {
		t.Fatalf("once the counter can be installed, 2 more calls computed %d times, want 1", computed-2)
	}
}

// An answer computed while a write changed the graph is not kept for the
// graph that write left: the next call computes again.
func TestAnAnswerComputedAcrossAWriteIsNotKept(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "across.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedAnswerGraph(t, db)
	ctx := context.Background()

	stale := func() (*graph.GraphStatistics, error) {
		// The write lands while this answer is being computed, and another
		// call sees the graph it left before this one is done.
		if _, err := db.SQL().Exec(`DELETE FROM graph_edges WHERE id = 'edge:ab'`); err != nil {
			return nil, err
		}
		if _, err := db.GraphStatistics(ctx); err != nil {
			return nil, err
		}
		return &graph.GraphStatistics{NodeCount: -1}, nil
	}
	if _, err := cachedGraphAnswer(ctx, db, "across", nil, stale); err != nil {
		t.Fatal(err)
	}
	got, err := cachedGraphAnswer(ctx, db, "across", nil, func() (*graph.GraphStatistics, error) {
		return &graph.GraphStatistics{NodeCount: 3}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeCount != 3 {
		t.Errorf("after a write during its computation, the answer was served from the cache: %+v", got)
	}
}

// The cache holds at most graphAnswerCacheEntries answers.
func TestTheGraphAnswerCacheIsBounded(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "bounded.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedAnswerGraph(t, db)
	ctx := context.Background()
	for i := range graphAnswerCacheEntries + 10 {
		if _, err := db.GraphPropertyValues(ctx, GraphPropertyValuesRequest{Key: "source", Limit: i + 1}); err != nil {
			t.Fatal(err)
		}
		db.graphAnswers.mu.Lock()
		n := len(db.graphAnswers.answers)
		db.graphAnswers.mu.Unlock()
		if n > graphAnswerCacheEntries {
			t.Fatalf("after %d distinct requests the cache holds %d answers, bound %d", i+1, n, graphAnswerCacheEntries)
		}
	}
}

// A counter dropped after it was installed leaves every answer computed, and
// a computation that fails returns its error, kept nowhere.
func TestAGraphAnswerWithoutItsCounterOrItsComputationIsNotServed(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "dropped.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedAnswerGraph(t, db)
	ctx := context.Background()

	failed := errors.New("graph unreadable")
	if _, err := cachedGraphAnswer(ctx, db, "failing", nil, func() (*graph.GraphStatistics, error) { return nil, failed }); !errors.Is(err, failed) {
		t.Fatalf("a failed computation returned %v, want its error", err)
	}
	got, err := cachedGraphAnswer(ctx, db, "failing", nil, func() (*graph.GraphStatistics, error) { return &graph.GraphStatistics{NodeCount: 3}, nil })
	if err != nil || got.NodeCount != 3 {
		t.Fatalf("after a failed computation, the next call got %+v, %v", got, err)
	}

	if _, err := db.GraphStatistics(ctx); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`DROP TRIGGER graph_answer_version_nodes_ad`, `DROP TRIGGER graph_answer_version_nodes_au`,
		`DROP TRIGGER graph_answer_version_edges_ad`, `DROP TRIGGER graph_answer_version_edges_au`,
		`DROP TABLE ` + graphAnswerVersionTable,
	} {
		if _, err := db.SQL().Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.GraphStatistics(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`DELETE FROM graph_edges`); err != nil {
		t.Fatal(err)
	}
	stats, err := db.GraphStatistics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.EdgeCount != 0 {
		t.Errorf("with its counter dropped, the statistics were served from the cache: %d edges, want 0", stats.EdgeCount)
	}
}
