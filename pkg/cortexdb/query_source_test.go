package cortexdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
)

// fakeSource stands in for Meilisearch, Weaviate, or anything else that can
// name candidates. Tests assert on the seam, not on somebody's HTTP API.
type fakeSource struct {
	name string
	hits []QuerySourceHit
	err  error
	saw  QuerySourceRequest
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) Search(_ context.Context, req QuerySourceRequest) ([]QuerySourceHit, error) {
	f.saw = req
	return f.hits, f.err
}

func openSourceTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := fmt.Sprintf("test_query_source_%d.db", testname.Nano())
	t.Cleanup(func() { _ = os.Remove(dbPath) })
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	for _, row := range []struct {
		id, content string
		vec         []float32
	}{
		{"alpha", "apollo launch checklist", []float32{1, 0, 0}},
		{"beta", "apollo launch telemetry", []float32{0.95, 0.05, 0}},
		{"gamma", "garden planning notes", []float32{0, 1, 0}},
	} {
		if err := db.InsertTextWithVector(ctx, row.id, row.content, row.vec, nil); err != nil {
			t.Fatalf("insert %s: %v", row.id, err)
		}
	}
	return db
}

func TestAnExternalSourceCanBeARetrievalLane(t *testing.T) {
	src := &fakeSource{name: "meili", hits: []QuerySourceHit{{ID: "gamma", Score: 9}, {ID: "alpha", Score: 3}}}
	db := openSourceTestDB(t)
	WithQuerySource(src)(db)

	resp, err := db.Query(context.Background(), QueryRequest{
		Query:      "anything",
		Prefetch:   []QueryPrefetch{{Kind: QueryPrefetchSource, Source: "meili"}},
		IncludeRaw: true,
		Limit:      5,
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(resp.Results))
	}
	// The source named ids; the content comes from the brain, which is the
	// system of record. A source cannot inject text CortexDB never stored.
	if resp.Results[0].ID != "gamma" || resp.Results[0].Content != "garden planning notes" {
		t.Fatalf("first result = %+v, want gamma hydrated from the store", resp.Results[0])
	}
	if got := resp.Results[0].SourceScores["meili"]; got != 9 {
		t.Fatalf("source score = %v, want 9", got)
	}
	if len(resp.Prefetches) != 1 || resp.Prefetches[0] != "meili" {
		t.Fatalf("prefetches = %v, want [meili]", resp.Prefetches)
	}
	// The lane is told what to search for, not left to guess.
	if src.saw.Query != "anything" || src.saw.Limit <= 0 {
		t.Fatalf("source saw %+v, want the query and a limit", src.saw)
	}
}

func TestAnUnknownSourceIsNamedRatherThanIgnored(t *testing.T) {
	db := openSourceTestDB(t)
	WithQuerySource(&fakeSource{name: "meili"})(db)

	_, err := db.Query(context.Background(), QueryRequest{
		Query:    "apollo",
		Prefetch: []QueryPrefetch{{Kind: QueryPrefetchSource, Source: "weaviate"}},
	})
	if err == nil {
		t.Fatal("a lane naming an unregistered source should fail, not quietly return nothing")
	}
	if !strings.Contains(err.Error(), "weaviate") || !strings.Contains(err.Error(), "meili") {
		t.Fatalf("error %q should name both the missing source and the registered ones", err)
	}
}

func TestASourceLaneWithoutANameIsRefused(t *testing.T) {
	db := openSourceTestDB(t)
	WithQuerySource(&fakeSource{name: "meili"})(db)

	if _, err := db.Query(context.Background(), QueryRequest{
		Query:    "apollo",
		Prefetch: []QueryPrefetch{{Kind: QueryPrefetchSource}},
	}); err == nil {
		t.Fatal("a source lane that names no source should fail")
	}
}

func TestCandidatesTheBrainDoesNotHaveAreDropped(t *testing.T) {
	// An external index goes stale: it still names a row that was deleted here.
	src := &fakeSource{name: "meili", hits: []QuerySourceHit{
		{ID: "alpha", Score: 5},
		{ID: "deleted-last-week", Score: 99},
	}}
	db := openSourceTestDB(t)
	WithQuerySource(src)(db)

	resp, err := db.Query(context.Background(), QueryRequest{
		Query:    "apollo",
		Prefetch: []QueryPrefetch{{Kind: QueryPrefetchSource, Source: "meili"}},
		Limit:    5,
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].ID != "alpha" {
		t.Fatalf("results = %+v, want only alpha — a stale id must not become a result", resp.Results)
	}
}

func TestASourceFailureIsNotSwallowed(t *testing.T) {
	boom := errors.New("meilisearch: connection refused")
	db := openSourceTestDB(t)
	WithQuerySource(&fakeSource{name: "meili", err: boom})(db)

	_, err := db.Query(context.Background(), QueryRequest{
		Query:    "apollo",
		Prefetch: []QueryPrefetch{{Kind: QueryPrefetchSource, Source: "meili"}},
	})
	if err == nil {
		t.Fatal("a lane whose backend is down should fail the query, not silently shrink the result set")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("error %v should wrap the source's own error", err)
	}
}

func TestAnExternalLaneFusesWithTheBuiltInOnes(t *testing.T) {
	// gamma is lexically and vectorially wrong for this query; the external
	// lane is the only thing voting for it, so it must appear and must rank
	// below the rows two lanes agree on.
	src := &fakeSource{name: "meili", hits: []QuerySourceHit{{ID: "gamma", Score: 1}}}
	db := openSourceTestDB(t)
	WithQuerySource(src)(db)

	resp, err := db.Query(context.Background(), QueryRequest{
		Query: "apollo launch",
		Prefetch: []QueryPrefetch{
			{Name: "vector", Kind: QueryPrefetchVector, QueryVector: []float32{1, 0, 0}},
			{Name: "lexical", Kind: QueryPrefetchLexical, Query: "apollo launch"},
			{Kind: QueryPrefetchSource, Source: "meili"},
		},
		IncludeRaw: true,
		Limit:      5,
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	var seenGamma bool
	for i, r := range resp.Results {
		if r.ID == "gamma" {
			seenGamma = true
			if i == 0 {
				t.Fatal("one lane's lone vote should not outrank rows the other lanes agree on")
			}
			if _, ok := r.SourceScores["meili"]; !ok {
				t.Fatalf("gamma should carry the lane that found it: %+v", r.SourceScores)
			}
		}
	}
	if !seenGamma {
		t.Fatal("the external lane's candidate never reached the fused result")
	}
	if len(resp.Prefetches) != 3 {
		t.Fatalf("prefetches = %v, want all three lanes", resp.Prefetches)
	}
}

func TestRegisteredQuerySourcesAreListed(t *testing.T) {
	db := openSourceTestDB(t)
	if got := db.QuerySources(); len(got) != 0 {
		t.Fatalf("a fresh DB should have no sources, got %v", got)
	}
	WithQuerySource(&fakeSource{name: "meili"})(db)
	WithQuerySource(&fakeSource{name: "weaviate"})(db)
	got := db.QuerySources()
	if len(got) != 2 || got[0] != "meili" || got[1] != "weaviate" {
		t.Fatalf("QuerySources() = %v, want [meili weaviate] sorted", got)
	}
}
