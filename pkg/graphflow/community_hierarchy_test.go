package graphflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// countingLLM is a deterministic generator for every step global search
// takes: leaf reports, parent reports, map and reduce. Each answer is derived
// from the prompt, so a test can tell which inputs reached which step.
type countingLLM struct {
	mu                                     sync.Mutex
	leaf, parent, mapCalls, reduce, failed int
	failLeaf                               bool
}

func (f *countingLLM) GenerateJSON(_ context.Context, system, user string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.Contains(system, "built from the reports of the smaller communities"):
		f.parent++
		return []byte(`{"title":"Theme ` + fmt.Sprint(f.parent) + `","summary":"A theme uniting its sub-communities.","findings":["merged"]}`), nil
	case strings.Contains(system, "community of related entities"):
		f.leaf++
		if f.failLeaf {
			f.failed++
			return nil, fmt.Errorf("model down")
		}
		first := strings.SplitN(strings.TrimPrefix(user, "Entities:\n"), ",", 2)[0]
		return []byte(`{"title":"Group of ` + first + `","summary":"Entities around ` + first + `.","findings":[]}`), nil
	case strings.Contains(system, "extract, from community reports"):
		f.mapCalls++
		return []byte(`{"points":[{"point":"theme point ` + fmt.Sprint(f.mapCalls) + `","score":80}]}`), nil
	case strings.Contains(system, "answer the user's question"):
		f.reduce++
		return []byte(`{"answer":"Synthesised from ` + fmt.Sprint(strings.Count(user, "\n- ")) + ` points."}`), nil
	}
	return []byte(`{}`), nil
}

// plantedEntityGraph writes groups × cliques × size entities: complete
// cliques, cliques in one group densely joined, groups joined by one edge.
func plantedEntityGraph(t testing.TB, db *cortexdb.DB, groups, cliques, size int) {
	t.Helper()
	ctx := context.Background()
	name := func(g, c, i int) string { return fmt.Sprintf("G%dC%dN%d", g, c, i) }
	var ents []cortexdb.ToolEntityInput
	for g := 0; g < groups; g++ {
		for c := 0; c < cliques; c++ {
			for i := 0; i < size; i++ {
				ents = append(ents, cortexdb.ToolEntityInput{Name: name(g, c, i), Type: "concept"})
			}
		}
	}
	tools := db.GraphRAGTools()
	if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: ents}); err != nil {
		t.Fatalf("upsert entities: %v", err)
	}
	var rels []cortexdb.ToolRelationInput
	for g := 0; g < groups; g++ {
		for c := 0; c < cliques; c++ {
			for i := 0; i < size; i++ {
				for j := i + 1; j < size; j++ {
					rels = append(rels, cortexdb.ToolRelationInput{From: name(g, c, i), To: name(g, c, j), Type: "related_to"})
				}
			}
			for d := c + 1; d < cliques; d++ {
				for k := 0; k < size; k++ {
					rels = append(rels, cortexdb.ToolRelationInput{From: name(g, c, k), To: name(g, d, (k+1)%size), Type: "links"})
				}
			}
		}
		if g+1 < groups {
			rels = append(rels, cortexdb.ToolRelationInput{From: name(g, 0, 0), To: name(g+1, 0, 0), Type: "bridges"})
		}
	}
	if _, err := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: rels}); err != nil {
		t.Fatalf("upsert relations: %v", err)
	}
}

func openHierarchyDB(t testing.TB) *cortexdb.DB {
	t.Helper()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "h.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestBuildCommunityHierarchyBottomUpWithModel(t *testing.T) {
	db := openHierarchyDB(t)
	plantedEntityGraph(t, db, 3, 4, 5)
	llm := &countingLLM{}
	rep, err := BuildCommunityHierarchy(context.Background(), db, HierarchyOptions{LLM: llm})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Levels) < 2 {
		t.Fatalf("expected at least two levels, got %+v", rep.Levels)
	}
	if rep.Levels[0].Communities != 12 || rep.Levels[1].Communities != 3 {
		t.Fatalf("expected 12 cliques then 3 groups, got %+v", rep.Levels)
	}
	if llm.leaf != 12 || llm.parent != 3 {
		t.Fatalf("expected 12 leaf and 3 parent model calls, got leaf=%d parent=%d", llm.leaf, llm.parent)
	}
	for _, c := range rep.Communities {
		if c.Level == 1 && (len(c.Children) != 4 || c.Generated != "model" || !strings.HasPrefix(c.Title, "Theme")) {
			t.Fatalf("level-1 report should come from its 4 children via the model: %+v", c)
		}
		if c.Level == 0 && c.Parent < 0 {
			t.Fatalf("level-0 report without parent: %+v", c)
		}
	}
	levels := loadHierarchyLevels(context.Background(), db)
	if len(levels[0]) != 12 || len(levels[1]) != 3 {
		t.Fatalf("persisted levels: %d / %d", len(levels[0]), len(levels[1]))
	}

	// A rebuild replaces, never accumulates.
	if _, err := BuildCommunityHierarchy(context.Background(), db, HierarchyOptions{LLM: llm, MaxLevels: 1}); err != nil {
		t.Fatal(err)
	}
	levels = loadHierarchyLevels(context.Background(), db)
	if len(levels) != 1 || len(levels[0]) != 12 {
		t.Fatalf("rebuild with one level left %d levels", len(levels))
	}
}

func TestBuildCommunityHierarchyWithoutModelAndWithAFailingModel(t *testing.T) {
	for _, tc := range []struct {
		name string
		llm  JSONGenerator
	}{
		{"no model", nil},
		{"failing model", &countingLLM{failLeaf: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openHierarchyDB(t)
			plantedEntityGraph(t, db, 2, 3, 4)
			rep, err := BuildCommunityHierarchy(context.Background(), db, HierarchyOptions{LLM: tc.llm})
			if err != nil {
				t.Fatal(err)
			}
			if len(rep.Communities) == 0 {
				t.Fatal("expected reports")
			}
			for _, c := range rep.Communities {
				if c.Level == 0 && (c.Generated != "deterministic" || c.Title == "" || !strings.Contains(c.Summary, "entities, most connected")) {
					t.Fatalf("level-0 report should be deterministic: %+v", c)
				}
				if tc.llm == nil && c.Level > 0 && c.Generated == "deterministic" && !strings.Contains(c.Summary, "sub-communities") {
					t.Fatalf("a deterministic parent report names its children: %+v", c)
				}
			}
		})
	}
}

func TestGlobalSearchLevelsAndNoModel(t *testing.T) {
	db := openHierarchyDB(t)
	plantedEntityGraph(t, db, 3, 4, 5)
	ctx := context.Background()

	// No reports and no build: a clear error, not an empty answer.
	no := false
	if _, err := callGlobalSearch(ctx, db, nil, GlobalSearchToolRequest{Query: "themes?", BuildIfEmpty: &no}); err == nil {
		t.Fatal("expected an error with no reports and build_if_empty=false")
	}

	res, err := callGlobalSearch(ctx, db, nil, GlobalSearchToolRequest{Query: "What is G1C2N0 part of?"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Built || res.Mode != "no_model" || res.MapBatches != 0 {
		t.Fatalf("expected a no-model build and search: %+v", res)
	}
	top := res.LevelsAvailable[len(res.LevelsAvailable)-1]
	if res.Level != top || res.CommunitiesUsed != 3 {
		t.Fatalf("default level should be the coarsest (%d, 3 reports): %+v", top, res)
	}
	if len(res.SupportingPoints) == 0 || !strings.Contains(res.SupportingPoints[0], "G1") {
		t.Fatalf("lexical ranking should put the G1 report first: %v", res.SupportingPoints)
	}

	zero := 0
	res, err = callGlobalSearch(ctx, db, nil, GlobalSearchToolRequest{Query: "G1C2N0", Level: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if res.Level != 0 || res.CommunitiesUsed != 12 || !strings.Contains(res.SupportingPoints[0], "G1C2") {
		t.Fatalf("level 0 should answer from the 12 clique reports, G1C2 first: %+v", res)
	}
	nine := 9
	if _, err := callGlobalSearch(ctx, db, nil, GlobalSearchToolRequest{Query: "x", Level: &nine}); err == nil || !strings.Contains(err.Error(), "levels with reports") {
		t.Fatalf("an absent level should name the levels that exist, got %v", err)
	}
}

// TestGlobalSearchToolRunsMapReduceOverMCP drives global_search through a real
// MCP client and server with a deterministic generator: the answer must come
// out of the reduce step, fed by the map step, over hierarchy reports.
func TestGlobalSearchToolRunsMapReduceOverMCP(t *testing.T) {
	db := openHierarchyDB(t)
	plantedEntityGraph(t, db, 3, 4, 5)
	llm := &countingLLM{}

	server, err := NewMCPServer(db, FilesystemDetector{}, HeuristicExtractor{}, MCPServerOptions{LLM: llm})
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Run(ctx, st) }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "v1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range tools.Tools {
		seen[tool.Name] = true
	}
	if !seen["global_search"] || !seen["build_community_hierarchy"] {
		t.Fatalf("tools missing from the MCP surface: %v", seen)
	}

	call := func(name string, args map[string]any, out any) {
		t.Helper()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.IsError {
			t.Fatalf("%s returned an error: %+v", name, res.Content)
		}
		raw, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s decode: %v", name, err)
		}
	}

	var built BuildHierarchyToolResponse
	call("build_community_hierarchy", map[string]any{}, &built)
	if built.Mode != "model" || built.ReportsWritten != 15 || built.ModelCalls != 15 {
		t.Fatalf("build: %+v", built)
	}

	var res GlobalSearchResult
	call("global_search", map[string]any{"query": "What are the main themes?", "level": 0}, &res)
	if res.Mode != "model" || res.Level != 0 || res.CommunitiesUsed != 12 {
		t.Fatalf("global_search: %+v", res)
	}
	if llm.mapCalls == 0 || llm.reduce != 1 || res.MapBatches != llm.mapCalls {
		t.Fatalf("map-reduce did not run: map=%d reduce=%d batches=%d", llm.mapCalls, llm.reduce, res.MapBatches)
	}
	if !strings.HasPrefix(res.Answer, "Synthesised from ") || len(res.SupportingPoints) != llm.mapCalls {
		t.Fatalf("answer must come from reduce over the mapped points: %+v", res)
	}
}
