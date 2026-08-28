package cortexdb

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
)

func findNodesTestDB(t *testing.T) (*DB, *GraphRAGToolbox, context.Context) {
	t.Helper()
	dbPath := fmt.Sprintf("test_find_nodes_%d.db", testname.Nano())
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	})
	ctx := context.Background()
	tools := db.GraphRAGTools()
	// A mixed-language shelf, which is the normal case for study material and
	// the case that motivated the tool.
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{
			{Name: "Limit", Type: "Concept"},
			{Name: "Infinite Limit", Type: "Concept"},
			{Name: "Left-Hand Limit", Type: "Concept"},
			{Name: "含绝对值的极限", Type: "Concept"},
			{Name: "Two-Phase Commit", Type: "Concept"},
			{Name: "Chapter 4: Storage", Type: "Chapter"},
		},
	}); err != nil {
		t.Fatalf("seed entities: %v", err)
	}
	return db, tools, ctx
}

// TestFindNodesResolvesANameToAnID covers the gap the tool exists for: the graph
// could previously only be entered by ID, so a caller holding a name had to
// derive the ID its writer would have produced, and got an empty result — which
// is what the graph also returns for something it has never heard of — whenever
// the derivation was wrong.
func TestFindNodesResolvesANameToAnID(t *testing.T) {
	_, tools, ctx := findNodesTestDB(t)

	resp, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"Limit"}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(resp.Matches) != 1 || len(resp.Matches[0].Nodes) == 0 {
		t.Fatalf("expected a match for Limit, got %+v", resp.Matches)
	}
	if got := resp.Matches[0].Nodes[0].Content; got != "Limit" {
		t.Fatalf("expected the exact node first, got %q", got)
	}
	if resp.Matches[0].Match != "exact" {
		t.Fatalf("expected an exact match to say so, got %q", resp.Matches[0].Match)
	}
}

// TestFindNodesPrefersTheExactNameOverOneContainingIt is the ranking that keeps
// the tool useful. "Limit" also appears inside "Infinite Limit" and "Left-Hand
// Limit", which are different concepts; answering with one of those sends a
// caller down a chain belonging to something the reader did not ask about.
func TestFindNodesPrefersTheExactNameOverOneContainingIt(t *testing.T) {
	_, tools, ctx := findNodesTestDB(t)

	resp, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"Limit"}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	nodes := resp.Matches[0].Nodes
	if nodes[0].Content != "Limit" {
		t.Fatalf("exact match must rank first, got %q", nodes[0].Content)
	}
	// And the near misses are still offered, so a caller can see them.
	if len(nodes) < 2 {
		t.Fatalf("expected the containing names to be offered too, got %d", len(nodes))
	}
}

// TestFindNodesIgnoresCaseAndPunctuation covers the same concept written two
// ways by two extractions of one book. Neither spelling is more correct.
func TestFindNodesIgnoresCaseAndPunctuation(t *testing.T) {
	_, tools, ctx := findNodesTestDB(t)

	resp, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"two phase commit"}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(resp.Matches[0].Nodes) == 0 {
		t.Fatal("expected 'two phase commit' to reach 'Two-Phase Commit'")
	}
	if got := resp.Matches[0].Nodes[0].Content; got != "Two-Phase Commit" {
		t.Fatalf("got %q", got)
	}
	// Reported as weaker than exact, because it is: a caller acting on it should
	// be able to decide not to.
	if resp.Matches[0].Match != "fold" {
		t.Fatalf("expected the match kind to be reported as fold, got %q", resp.Matches[0].Match)
	}
}

// TestFindNodesWorksOnCJK guards the fold: stripping non-letters must not strip
// CJK, or every Chinese concept in a mixed graph becomes unreachable — which is
// worse than not folding at all, since it fails silently.
func TestFindNodesWorksOnCJK(t *testing.T) {
	_, tools, ctx := findNodesTestDB(t)

	resp, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"含绝对值的极限"}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(resp.Matches[0].Nodes) == 0 || resp.Matches[0].Nodes[0].Content != "含绝对值的极限" {
		t.Fatalf("expected the CJK concept, got %+v", resp.Matches[0])
	}
}

// TestFindNodesFiltersByType keeps places in a book out of an answer about
// things to learn: "Chapter 4" is where a concept is, not a concept.
func TestFindNodesFiltersByType(t *testing.T) {
	_, tools, ctx := findNodesTestDB(t)

	resp, err := tools.FindNodes(ctx, ToolFindNodesRequest{
		Names:     []string{"Storage"},
		NodeTypes: []string{"Concept"},
	})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(resp.Matches[0].Nodes) != 0 {
		t.Fatalf("a Chapter must not answer a Concept query, got %+v", resp.Matches[0].Nodes)
	}
}

// TestFindNodesReportsANameItCannotPlace distinguishes "not in the graph" from
// "no answer", which the ID-only路径 could not: both looked like an empty
// subgraph.
func TestFindNodesReportsANameItCannotPlace(t *testing.T) {
	_, tools, ctx := findNodesTestDB(t)

	resp, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"量子色动力学", "Limit"}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(resp.Matches) != 2 {
		t.Fatalf("every requested name gets an entry, got %d", len(resp.Matches))
	}
	if resp.Matches[0].Name != "量子色动力学" || len(resp.Matches[0].Nodes) != 0 || resp.Matches[0].Match != "" {
		t.Fatalf("an unplaceable name must come back empty and say nothing about how, got %+v", resp.Matches[0])
	}
	if len(resp.Matches[1].Nodes) == 0 {
		t.Fatal("one name failing must not lose the others")
	}
}

// TestFindNodesIsRegisteredAsATool — the whole point is reachability over MCP.
// LearningPath and UpdateGraphFromText are both unreachable that way, and are
// therefore invisible to any host that speaks MCP rather than the CLI.
func TestFindNodesIsRegisteredAsATool(t *testing.T) {
	_, tools, ctx := findNodesTestDB(t)

	var found bool
	for _, def := range tools.Definitions() {
		if def.Name == "find_nodes" {
			found = true
		}
	}
	if !found {
		t.Fatal("find_nodes is not in the tool definitions")
	}

	out, err := tools.Call(ctx, "find_nodes", []byte(`{"names":["Limit"],"node_types":["Concept"]}`))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	resp, ok := out.(*ToolFindNodesResponse)
	if !ok {
		t.Fatalf("unexpected response type %T", out)
	}
	if len(resp.Matches) != 1 || len(resp.Matches[0].Nodes) == 0 {
		t.Fatalf("dispatched call returned nothing: %+v", resp)
	}
}

// TestFindNodesDoesNotMatchOnAnEmptyFold guards a collision the fold makes
// possible: a name of nothing but punctuation folds to "", and so does every
// other such name, so they would all match each other. Found by breaking the
// fold deliberately — with ASCII-only folding every CJK name folded to "" and a
// concept the graph had never heard of was confidently resolved to an unrelated
// Chinese one.
func TestFindNodesDoesNotMatchOnAnEmptyFold(t *testing.T) {
	_, tools, ctx := findNodesTestDB(t)

	resp, err := tools.FindNodes(ctx, ToolFindNodesRequest{Names: []string{"???"}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(resp.Matches[0].Nodes) != 0 {
		t.Fatalf("punctuation must not resolve to a concept, got %+v", resp.Matches[0].Nodes)
	}
}
