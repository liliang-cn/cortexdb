package graphflow

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

var textCommunityDocs = map[string]struct {
	text  string
	names [3]string
}{
	"lovelace": {"Ada Lovelace wrote to Charles Babbage that the Analytical Engine might compose elaborate pieces of music.", [3]string{"Ada Lovelace", "Charles Babbage", "Analytical Engine"}},
	"hopper":   {"Grace Hopper built the first COBOL compiler with a Navy team that doubted machines could read English.", [3]string{"Grace Hopper", "COBOL", "Navy"}},
	"turing":   {"Alan Turing argued at Bletchley Park that the Bombe would break Enigma within hours, not weeks.", [3]string{"Alan Turing", "Bletchley Park", "Enigma"}},
}

// fixtureEmbedder is a constant vector: the reports read the graph and the
// chunks, not vector rankings, and InsertGraphDocument needs one to store
// chunks.
type fixtureEmbedder struct{}

func (fixtureEmbedder) Embed(context.Context, string) ([]float32, error) { return []float32{1, 0}, nil }
func (fixtureEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0}
	}
	return out, nil
}
func (fixtureEmbedder) Dim() int { return 2 }

// sentenceExtractor gives a fixture sentence its three entities, linked in a
// triangle, so each sentence's entities form one community; the ingest links
// the chunk to them by mentions edges, as an extractor's entities are.
type sentenceExtractor struct{}

func (sentenceExtractor) Extract(_ context.Context, text string) (*cortexdb.GraphExtraction, error) {
	out := &cortexdb.GraphExtraction{}
	for _, d := range textCommunityDocs {
		if text != d.text && !strings.Contains(text, d.names[0]) {
			continue
		}
		n := d.names
		for _, name := range n {
			out.Entities = append(out.Entities, cortexdb.GraphEntity{Name: name, Type: "concept"})
		}
		out.Relationships = append(out.Relationships,
			cortexdb.GraphRelationship{From: n[0], To: n[1], Type: "knows"},
			cortexdb.GraphRelationship{From: n[1], To: n[2], Type: "built"},
			cortexdb.GraphRelationship{From: n[0], To: n[2], Type: "described"})
	}
	return out, nil
}

func openTextCommunityDB(t *testing.T) *cortexdb.DB {
	t.Helper()
	cfg := cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "text.db"))
	cfg.Dimensions = 2
	db, err := cortexdb.Open(cfg, cortexdb.WithEmbedder(fixtureEmbedder{}))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for id, d := range textCommunityDocs {
		if _, err := db.InsertGraphDocument(context.Background(), cortexdb.GraphRAGDocument{ID: id, Title: id, Content: d.text},
			cortexdb.GraphRAGIngestOptions{Extractor: sentenceExtractor{}}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	return db
}

// promptRecorder answers every report and keeps each leaf prompt; it also
// counts how many calls were in flight at once.
type promptRecorder struct {
	mu               sync.Mutex
	leafPrompts      []string
	inFlight, maxFly int
	delay            time.Duration
}

func (p *promptRecorder) GenerateJSON(_ context.Context, system, user string) ([]byte, error) {
	p.mu.Lock()
	p.inFlight++
	p.maxFly = max(p.maxFly, p.inFlight)
	if !strings.Contains(system, "built from the reports") {
		p.leafPrompts = append(p.leafPrompts, user)
	}
	p.mu.Unlock()
	time.Sleep(p.delay)
	p.mu.Lock()
	p.inFlight--
	p.mu.Unlock()
	return []byte(`{"title":"t","summary":"s","findings":["f"]}`), nil
}

// A leaf community's report is written from the text its members were found
// in, not from their names and relations alone (#245).
func TestLeafReportsAreWrittenFromTheSourceText(t *testing.T) {
	db := openTextCommunityDB(t)
	llm := &promptRecorder{}
	if _, err := BuildCommunityHierarchy(context.Background(), db, HierarchyOptions{LLM: llm}); err != nil {
		t.Fatalf("build: %v", err)
	}
	found := false
	for _, p := range llm.leafPrompts {
		if strings.Contains(p, "compose elaborate pieces of music") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no leaf report prompt carried the source passage; prompts: %q", llm.leafPrompts)
	}
}

// Without a model the report still carries what the passages said, and
// global_search returns it (#245).
func TestNoModelReportsCarryExcerptsGlobalSearchReturns(t *testing.T) {
	db := openTextCommunityDB(t)
	if _, err := BuildCommunityHierarchy(context.Background(), db, HierarchyOptions{}); err != nil {
		t.Fatalf("build: %v", err)
	}
	level := 0
	res, err := GlobalSearch(context.Background(), db, "what did they say about music", GlobalSearchOptions{Level: &level})
	if err != nil {
		t.Fatalf("global search: %v", err)
	}
	if !strings.Contains(strings.Join(res.SupportingPoints, "\n"), "compose elaborate pieces of music") {
		t.Fatalf("no-model global_search returned no text of the passages: %q", res.SupportingPoints)
	}
}

// The reports of one level are written side by side, as many at once as
// Concurrency allows (#245).
func TestReportsOfALevelAreWrittenConcurrently(t *testing.T) {
	for _, tc := range []struct{ concurrency, wantMax int }{{1, 1}, {4, 3}} {
		t.Run(fmt.Sprint(tc.concurrency), func(t *testing.T) {
			db := openTextCommunityDB(t)
			llm := &promptRecorder{delay: 50 * time.Millisecond}
			rep, err := BuildCommunityHierarchy(context.Background(), db, HierarchyOptions{LLM: llm, Concurrency: tc.concurrency})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if llm.maxFly != tc.wantMax {
				t.Fatalf("%d calls in flight at most, want %d (model calls %d)", llm.maxFly, tc.wantMax, rep.ModelCalls)
			}
		})
	}
}
