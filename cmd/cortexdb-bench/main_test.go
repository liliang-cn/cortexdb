package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// fakeOllama serves /api/embed with a deterministic 4-dimensional vector per
// text, counts how many texts it was asked to embed and remembers them.
func fakeOllama(t *testing.T) (*httptest.Server, *atomic.Int64) {
	srv, embedded, _ := recordingOllama(t)
	return srv, embedded
}

func recordingOllama(t *testing.T) (*httptest.Server, *atomic.Int64, func() []string) {
	t.Helper()
	var embedded atomic.Int64
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		embedded.Add(int64(len(req.Input)))
		mu.Lock()
		seen = append(seen, req.Input...)
		mu.Unlock()
		out := make([][]float32, len(req.Input))
		for i, s := range req.Input {
			out[i] = []float32{float32(len(s)), 1, float32(strings.Count(s, "a")), 0.5}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	}))
	t.Cleanup(srv.Close)
	return srv, &embedded, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

func TestCachedEmbedderEmbedsADocumentOnceAcrossRuns(t *testing.T) {
	srv, embedded := fakeOllama(t)
	ctx := context.Background()
	inner, err := newOllamaEmbedder(ctx, srv.URL, "fake")
	if err != nil {
		t.Fatal(err)
	}
	embedded.Store(0) // the dimension probe is not a document
	path := filepath.Join(t.TempDir(), "embeddings-fake.bin")

	first, err := openCachedEmbedder(inner, path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := first.EmbedBatch(ctx, []string{"alpha", "beta", "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if got := embedded.Load(); got != 3 {
		t.Fatalf("first run embedded %d texts, want 3", got)
	}

	// A run killed mid-write leaves part of a record behind; the next run must
	// keep every whole record and carry on.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte{1, 2, 3})
	f.Close()

	second, err := openCachedEmbedder(inner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	got, err := second.EmbedBatch(ctx, []string{"beta", "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, [][]float32{want[1], want[0]}) {
		t.Errorf("cached vectors = %v, want %v", got, [][]float32{want[1], want[0]})
	}
	if n := embedded.Load(); n != 3 {
		t.Errorf("second run embedded %d more texts, want none", n-3)
	}
	if hits, misses := second.Stats(); hits != 2 || misses != 0 {
		t.Errorf("stats = %d hits, %d misses; want 2, 0", hits, misses)
	}

	// Queries go to the model every time, so measured latency includes them.
	if _, err := second.Embed(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if n := embedded.Load(); n != 4 {
		t.Errorf("a query embedding was served from the cache")
	}
}

// Warming predicts the facade's chunking. If SaveKnowledge ever chunks a short
// passage differently, warming would embed texts nobody asks for and every
// ingest would quietly pay twice; this catches that drift.
func TestWarmedChunksAreTheOnesSaveKnowledgeEmbeds(t *testing.T) {
	srv, _, texts := recordingOllama(t)
	ctx := context.Background()
	inner, err := newOllamaEmbedder(ctx, srv.URL, "fake")
	if err != nil {
		t.Fatal(err)
	}
	cache, err := openCachedEmbedder(inner, filepath.Join(t.TempDir(), "embeddings-fake.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	contents := []string{
		"Lionel Messi\nMessi joined the youth academy of  FC Barcelona.",
		"[1:56 pm on 8 May, 2023] Caroline: I went to a support group yesterday.",
		"Teutberga was a queen of Lotharingia.\tShe married Lothair II.",
	}
	if err := cache.Warm(ctx, contents); err != nil {
		t.Fatal(err)
	}
	before := len(texts())

	db, cleanup, err := openTempDB(cache)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for i, c := range contents {
		if _, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{KnowledgeID: fmt.Sprint(i), Content: c}); err != nil {
			t.Fatal(err)
		}
	}
	// SaveKnowledge also embeds the entity names it extracts; those are not
	// predicted. No passage may be embedded a second time.
	for _, text := range texts()[before:] {
		for _, c := range contents {
			if want, _ := predictedChunk(c); text == want {
				t.Errorf("SaveKnowledge embedded %q again: warming predicted a different chunk", text)
			}
		}
		if len(strings.Fields(text)) > 4 {
			t.Errorf("SaveKnowledge embedded %q, which warming had not predicted", text)
		}
	}
	if _, ok := predictedChunk(strings.Repeat("word ", facadeChunkWords+1)); ok {
		t.Error("predicted a single chunk for a passage longer than one")
	}
}

func TestRetrieveReturnsDistinctDocumentsUpToTheDepth(t *testing.T) {
	db, cleanup, err := openTempDB(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	ctx := context.Background()
	// Long documents, so each has several chunks that could each match, and
	// enough of them that the facade's default 2,400-character context budget
	// would cut the list short.
	for i := 0; i < 12; i++ {
		var b strings.Builder
		for j := 0; j < 40; j++ {
			fmt.Fprintf(&b, "Harbour report %d notes the lighthouse keeper logged storm number %d near the pier. ", i, j)
		}
		if _, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
			KnowledgeID: fmt.Sprintf("doc-%02d", i),
			Content:     b.String(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := retrieve(ctx, db, modeLexical, "lighthouse keeper storm", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 10 {
		t.Fatalf("got %d ids, want 10: the context budget or chunk duplicates shortened the ranking", len(ids))
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Errorf("document %s ranked twice", id)
		}
		seen[id] = true
	}
}

func TestFlagsRefuseAModeWithoutTheEmbedderItNeeds(t *testing.T) {
	cases := map[string][]string{
		"vector without embedder":   {"--dataset", "locomo", "--mode", "vector"},
		"hybrid without embedder":   {"--dataset", "locomo", "--mode", "hybrid"},
		"lexical with an embedder":  {"--dataset", "locomo", "--mode", "lexical", "--embedder", "x"},
		"unknown mode":              {"--dataset", "locomo", "--mode", "semantic"},
		"no dataset":                {"--mode", "lexical"},
		"merge without output path": {"--merge", "a.json"},
	}
	for name, argv := range cases {
		if _, _, err := parseFlags(argv); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, _, err := parseFlags([]string{"--dataset", "locomo", "--mode", "graph"}); err != nil {
		t.Errorf("graph without an embedder is the lexical graph path and must be accepted: %v", err)
	}
}

func TestMergeKeepsNumbersAndDropsPerQueryDetail(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, v any) string {
		data, _ := json.Marshal(v)
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a := write("a.json", map[string]any{"benchmark": "musique", "mode": "hybrid", "limit": 0,
		"metrics": map[string]any{"document": map[string]any{"mrr": 0.5, "per_query": []any{"q1"}}}})
	b := write("b.json", map[string]any{"benchmark": "musique", "mode": "lexical", "limit": 0,
		"metrics": map[string]any{"document": map[string]any{"mrr": 0.4}}})
	out := filepath.Join(dir, "merged.json")
	if err := runMerge(out, []string{a, b}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "per_query") {
		t.Error("merged baseline kept per-query detail")
	}
	var merged []map[string]any
	if err := json.Unmarshal(data, &merged); err != nil {
		t.Fatal(err)
	}
	if len(merged) != 2 || merged[0]["mode"] != "lexical" || merged[1]["mode"] != "hybrid" {
		t.Errorf("merged order = %v, want lexical before hybrid", merged)
	}
}

func TestRecordedCommandOmitsMachineLocalPaths(t *testing.T) {
	got := commandLine([]string{"--dataset", "musique", "--out", "/tmp/x.json", "--data-dir=/home/a/bench", "--mode", "vector", "--embedder", "embeddinggemma"})
	if want := "go run ./cmd/cortexdb-bench --dataset musique --mode vector --embedder embeddinggemma"; got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}
