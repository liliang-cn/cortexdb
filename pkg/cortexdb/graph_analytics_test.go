package cortexdb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// openGraphAnalyticsBrain returns an empty brain the caller fills in. The
// fixtures differ enough between these tests — one needs a hub, one needs
// nothing but ties, one needs duplicate vectors — that a single shared graph
// would have to be all three and would then prove none of them.
func openGraphAnalyticsBrain(t *testing.T) *DB {
	t.Helper()
	path := fmt.Sprintf("test_graph_analytics_%d.db", testname.Nano())
	db, err := Open(DefaultConfig(path))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(path + suffix)
		}
	})
	return db
}

func addAnalyticsNode(t *testing.T, db *DB, id, content, nodeType string, vector []float32) {
	t.Helper()
	if err := db.Graph().UpsertNode(context.Background(), &graph.GraphNode{
		ID: id, Content: content, NodeType: nodeType, Vector: vector,
	}); err != nil {
		t.Fatalf("upsert node %s: %v", id, err)
	}
}

func addAnalyticsEdge(t *testing.T, db *DB, from, to string) {
	t.Helper()
	if err := db.Graph().UpsertEdge(context.Background(), &graph.GraphEdge{
		ID: "edge:" + from + ":" + to, FromNodeID: from, ToNodeID: to,
		EdgeType: "knows", Weight: 1,
	}); err != nil {
		t.Fatalf("upsert edge %s->%s: %v", from, to, err)
	}
}

// hubBrain: bob is pointed at by three nodes, carol by two, and erin by nobody
// and points at nobody. That shape is the whole point of PageRank — bob and
// carol are not mentioned more often than anyone else, they are simply where
// the structure converges — and it also gives GetGraphStatistics two connected
// components to count.
func hubBrain(t *testing.T) *DB {
	t.Helper()
	db := openGraphAnalyticsBrain(t)
	// Content differs from the id in case, so a test asserting on the label is
	// asserting that the label came from the node and not from its id.
	for i, name := range [][2]string{
		{"alice", "Alice"}, {"bob", "Bob"}, {"carol", "Carol"},
		{"dave", "Dave"}, {"erin", "Erin"},
	} {
		vector := make([]float32, 5)
		vector[i] = 1
		addAnalyticsNode(t, db, "entity:"+name[0], name[1], "person", vector)
	}
	for _, edge := range [][2]string{
		{"alice", "bob"}, {"carol", "bob"}, {"dave", "bob"},
		{"alice", "carol"}, {"dave", "carol"},
	} {
		addAnalyticsEdge(t, db, "entity:"+edge[0], "entity:"+edge[1])
	}
	return db
}

func TestGraphPageRankRanksTheHubAndNamesIt(t *testing.T) {
	db := hubBrain(t)
	ctx := context.Background()

	result, err := db.GraphPageRank(ctx, GraphPageRankOptions{})
	if err != nil {
		t.Fatalf("pagerank: %v", err)
	}
	if result.TotalNodes != 5 {
		t.Fatalf("total nodes = %d, want 5", result.TotalNodes)
	}
	if result.Truncated {
		t.Fatalf("uncapped ranking reported as truncated")
	}
	if len(result.Nodes) != 5 {
		t.Fatalf("returned %d nodes, want 5", len(result.Nodes))
	}
	if result.Nodes[0].ID != "entity:bob" {
		t.Fatalf("top node = %s, want entity:bob", result.Nodes[0].ID)
	}
	if result.Nodes[1].ID != "entity:carol" {
		t.Fatalf("second node = %s, want entity:carol", result.Nodes[1].ID)
	}
	// The reason this facade exists rather than db.Graph().PageRank: a score
	// beside an opaque id is not an answer an agent can use.
	if result.Nodes[0].Label != "Bob" {
		t.Fatalf("top label = %q, want %q", result.Nodes[0].Label, "Bob")
	}
	if result.Nodes[0].NodeType != "person" {
		t.Fatalf("top node type = %q, want %q", result.Nodes[0].NodeType, "person")
	}
	if !(result.Nodes[0].Score > result.Nodes[1].Score) {
		t.Fatalf("scores not descending: %v then %v", result.Nodes[0].Score, result.Nodes[1].Score)
	}
}

// A graph with no edges scores every node identically. The engine sorts on
// score alone with an unstable sort, so this is exactly the case where its
// order is arbitrary and the facade's tie-break has to do the work.
func TestGraphPageRankOrdersTiesByIDAndRepeats(t *testing.T) {
	db := openGraphAnalyticsBrain(t)
	ctx := context.Background()
	for _, name := range []string{"delta", "alpha", "echo", "bravo", "charlie"} {
		addAnalyticsNode(t, db, "entity:"+name, name, "person", []float32{1, 0, 0})
	}

	first, err := db.GraphPageRank(ctx, GraphPageRankOptions{})
	if err != nil {
		t.Fatalf("pagerank: %v", err)
	}
	want := []string{"entity:alpha", "entity:bravo", "entity:charlie", "entity:delta", "entity:echo"}
	got := make([]string, 0, len(first.Nodes))
	for _, node := range first.Nodes {
		got = append(got, node.ID)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tied ranking order = %v, want %v", got, want)
	}

	// Repeated because an unstable sort is not reliably wrong on the first
	// call; the property under test is that the answer does not move.
	for i := 0; i < 5; i++ {
		again, err := db.GraphPageRank(ctx, GraphPageRankOptions{})
		if err != nil {
			t.Fatalf("pagerank repeat %d: %v", i, err)
		}
		for j := range again.Nodes {
			if again.Nodes[j].ID != first.Nodes[j].ID {
				t.Fatalf("repeat %d position %d = %s, first call had %s", i, j, again.Nodes[j].ID, first.Nodes[j].ID)
			}
		}
	}
}

func TestGraphPageRankReportsWhenTheCapBit(t *testing.T) {
	db := hubBrain(t)
	ctx := context.Background()

	capped, err := db.GraphPageRank(ctx, GraphPageRankOptions{TopN: 2})
	if err != nil {
		t.Fatalf("pagerank: %v", err)
	}
	if len(capped.Nodes) != 2 {
		t.Fatalf("capped to 2 returned %d nodes", len(capped.Nodes))
	}
	if !capped.Truncated {
		t.Fatalf("cap of 2 over 5 nodes did not report truncation")
	}
	// The denominator has to survive the cut, or a caller cannot tell a head
	// of a large graph from the whole of a small one.
	if capped.TotalNodes != 5 {
		t.Fatalf("total nodes after cap = %d, want 5", capped.TotalNodes)
	}

	exact, err := db.GraphPageRank(ctx, GraphPageRankOptions{TopN: 5})
	if err != nil {
		t.Fatalf("pagerank: %v", err)
	}
	if exact.Truncated {
		t.Fatalf("cap equal to the node count reported truncation")
	}
}

func TestGraphPageRankHonoursIterationAndDampingBounds(t *testing.T) {
	db := hubBrain(t)
	ctx := context.Background()

	// One iteration cannot have converged, so it must still answer rather than
	// fail — the bound is a budget, not a precondition.
	quick, err := db.GraphPageRank(ctx, GraphPageRankOptions{Iterations: 1, DampingFactor: 0.5})
	if err != nil {
		t.Fatalf("bounded pagerank: %v", err)
	}
	if len(quick.Nodes) != 5 {
		t.Fatalf("bounded pagerank returned %d nodes, want 5", len(quick.Nodes))
	}
	if quick.Nodes[0].ID != "entity:bob" {
		t.Fatalf("bounded pagerank top node = %s, want entity:bob", quick.Nodes[0].ID)
	}
}

func TestGraphPageRankLabelsLongContentReadably(t *testing.T) {
	db := openGraphAnalyticsBrain(t)
	ctx := context.Background()
	long := strings.Repeat("这是一段很长的中文内容。", 60)
	addAnalyticsNode(t, db, "chunk:doc:000", long, "chunk", []float32{1, 0, 0})
	addAnalyticsNode(t, db, "entity:blank", "", "entity", []float32{0, 1, 0})

	result, err := db.GraphPageRank(ctx, GraphPageRankOptions{})
	if err != nil {
		t.Fatalf("pagerank: %v", err)
	}
	labels := map[string]string{}
	for _, node := range result.Nodes {
		labels[node.ID] = node.Label
	}
	chunkLabel := labels["chunk:doc:000"]
	if runes := []rune(chunkLabel); len(runes) > maxGraphNodeLabelRunes+1 {
		t.Fatalf("chunk label is %d runes, want at most %d plus the ellipsis", len(runes), maxGraphNodeLabelRunes)
	}
	if !strings.HasSuffix(chunkLabel, "…") {
		t.Fatalf("trimmed label %q does not say it was trimmed", chunkLabel)
	}
	// Cut on runes, not bytes: a byte cut through a CJK character leaves a
	// replacement glyph, which is how this would fail silently in production.
	if strings.ContainsRune(chunkLabel, '�') {
		t.Fatalf("label was cut mid-character: %q", chunkLabel)
	}
	// A node with no content still has to be identifiable.
	if labels["entity:blank"] != "entity:blank" {
		t.Fatalf("contentless node label = %q, want its id", labels["entity:blank"])
	}
}

func TestGraphStatisticsCountsShapeAndIslands(t *testing.T) {
	db := hubBrain(t)
	ctx := context.Background()

	stats, err := db.GraphStatistics(ctx)
	if err != nil {
		t.Fatalf("statistics: %v", err)
	}
	if stats.NodeCount != 5 {
		t.Fatalf("node count = %d, want 5", stats.NodeCount)
	}
	if stats.EdgeCount != 5 {
		t.Fatalf("edge count = %d, want 5", stats.EdgeCount)
	}
	if stats.AverageDegree != 2 {
		t.Fatalf("average degree = %v, want 2", stats.AverageDegree)
	}
	if stats.Density != 0.25 {
		t.Fatalf("density = %v, want 0.25", stats.Density)
	}
	// erin is joined to nothing, so the graph is two islands. This is the
	// number that tells an operator an ingest wrote entities without linking
	// them.
	if stats.ConnectedComponents != 2 {
		t.Fatalf("connected components = %d, want 2", stats.ConnectedComponents)
	}
}

// An empty brain is a legitimate state — a database opened and not yet written
// to — and every one of these has to answer it rather than fail.
func TestGraphAnalyticsOnAnEmptyGraphAnswerEmpty(t *testing.T) {
	db := openGraphAnalyticsBrain(t)
	ctx := context.Background()

	ranked, err := db.GraphPageRank(ctx, GraphPageRankOptions{})
	if err != nil {
		t.Fatalf("pagerank on empty graph: %v", err)
	}
	if len(ranked.Nodes) != 0 || ranked.TotalNodes != 0 || ranked.Truncated {
		t.Fatalf("empty graph ranking = %+v, want an empty untruncated answer", ranked)
	}

	stats, err := db.GraphStatistics(ctx)
	if err != nil {
		t.Fatalf("statistics on empty graph: %v", err)
	}
	if stats.NodeCount != 0 || stats.EdgeCount != 0 || stats.ConnectedComponents != 0 {
		t.Fatalf("empty graph statistics = %+v, want zeros", stats)
	}

	// A node with no neighbours to suggest is an empty answer too.
	addAnalyticsNode(t, db, "entity:lonely", "Lonely", "person", []float32{1, 0, 0})
	predicted, err := db.GraphPredictEdges(ctx, GraphPredictEdgesOptions{NodeID: "entity:lonely"})
	if err != nil {
		t.Fatalf("predict edges on a one-node graph: %v", err)
	}
	if len(predicted.Edges) != 0 || predicted.Truncated {
		t.Fatalf("one-node prediction = %+v, want empty", predicted)
	}
}

// duplicateBrain: three nodes that are near-identical in vector space and
// unconnected. This is the finding link prediction is actually good at on this
// engine — the same entity stored under different names.
func duplicateBrain(t *testing.T) *DB {
	t.Helper()
	db := openGraphAnalyticsBrain(t)
	for _, name := range []string{"acme", "acme-corp", "acme-inc", "acme-ltd"} {
		addAnalyticsNode(t, db, "entity:"+name, name, "org", []float32{1, 0, 0})
	}
	addAnalyticsNode(t, db, "entity:unrelated", "coffee", "thing", []float32{0, 1, 0})
	return db
}

func TestGraphPredictEdgesNamesBothEndpoints(t *testing.T) {
	db := duplicateBrain(t)
	ctx := context.Background()

	result, err := db.GraphPredictEdges(ctx, GraphPredictEdgesOptions{NodeID: "entity:acme"})
	if err != nil {
		t.Fatalf("predict edges: %v", err)
	}
	if len(result.Edges) == 0 {
		t.Fatalf("no predictions for a node with three near-duplicates")
	}
	for _, edge := range result.Edges {
		if edge.FromID != "entity:acme" {
			t.Fatalf("prediction from %s, want entity:acme", edge.FromID)
		}
		// Both ends named, not an id pair: a reader has to be able to tell a
		// duplicate from a missing fact, and only the labels say which it is.
		if edge.FromLabel != "acme" {
			t.Fatalf("from label = %q, want %q", edge.FromLabel, "acme")
		}
		if edge.ToLabel == "" || edge.ToLabel == edge.ToID {
			t.Fatalf("to endpoint %s came back unnamed", edge.ToID)
		}
		if edge.ToNodeType != "org" {
			t.Fatalf("to node type = %q, want %q", edge.ToNodeType, "org")
		}
		if edge.Method == "" {
			t.Fatalf("prediction %s->%s did not say how it was scored", edge.FromID, edge.ToID)
		}
		if edge.ToID == "entity:unrelated" {
			t.Fatalf("orthogonal node predicted as a connection")
		}
	}
	// Deterministic: descending score, then id.
	for i := 1; i < len(result.Edges); i++ {
		prev, cur := result.Edges[i-1], result.Edges[i]
		if prev.Score < cur.Score {
			t.Fatalf("predictions not ordered by score: %v then %v", prev.Score, cur.Score)
		}
		if prev.Score == cur.Score && prev.ToID > cur.ToID {
			t.Fatalf("tied predictions not ordered by id: %s then %s", prev.ToID, cur.ToID)
		}
	}
}

func TestGraphPredictEdgesReportsWhenTheCapBit(t *testing.T) {
	db := duplicateBrain(t)
	ctx := context.Background()

	capped, err := db.GraphPredictEdges(ctx, GraphPredictEdgesOptions{NodeID: "entity:acme", MaxResults: 1})
	if err != nil {
		t.Fatalf("predict edges: %v", err)
	}
	if len(capped.Edges) != 1 {
		t.Fatalf("cap of 1 returned %d edges", len(capped.Edges))
	}
	if !capped.Truncated {
		t.Fatalf("cap of 1 over three candidates did not report truncation")
	}

	whole, err := db.GraphPredictEdges(ctx, GraphPredictEdgesOptions{NodeID: "entity:acme", MaxResults: 50})
	if err != nil {
		t.Fatalf("predict edges: %v", err)
	}
	if whole.Truncated {
		t.Fatalf("cap above the candidate count reported truncation")
	}
}

func TestGraphPredictEdgesRejectsAMissingNodeID(t *testing.T) {
	db := duplicateBrain(t)
	ctx := context.Background()

	if _, err := db.GraphPredictEdges(ctx, GraphPredictEdgesOptions{NodeID: "  "}); err == nil {
		t.Fatalf("blank node id was accepted")
	}
	// A node that is not there is a mistake, not a finding, so it must not come
	// back as "no suggestions".
	if _, err := db.GraphPredictEdges(ctx, GraphPredictEdgesOptions{NodeID: "entity:nobody"}); err == nil {
		t.Fatalf("unknown node id was accepted")
	}
}

// Shared structure alone must be able to produce a prediction.
//
// alice and dave both point at bob and at carol and at nothing else — the
// strongest structural signal this graph can make — and their vectors are
// orthogonal, so there is no similarity to lean on. The engine used to average
// the structural score with the similarity, and since the structural term is
// commonNeighbours/(degree+1) and so strictly below 1, halving it put it
// permanently below the 0.5 a prediction has to clear: this pair could not be
// returned at all. It is returned now, and the pinned-limitation test that
// stood here has become this one.
func TestGraphPredictEdgesSeesStructureAlone(t *testing.T) {
	db := hubBrain(t)
	ctx := context.Background()

	result, err := db.GraphPredictEdges(ctx, GraphPredictEdgesOptions{NodeID: "entity:alice"})
	if err != nil {
		t.Fatalf("predict edges: %v", err)
	}
	for _, edge := range result.Edges {
		if edge.ToID == "entity:dave" {
			if edge.Method != "combined" {
				t.Fatalf("method = %q, want combined — this prediction rests on shared neighbours", edge.Method)
			}
			if edge.FromLabel == "" || edge.ToLabel == "" {
				t.Fatalf("both endpoints must come back named, got %+v", edge)
			}
			return
		}
	}
	t.Fatalf("alice and dave share every neighbour and have nothing else in common, "+
		"and the prediction is missing: %+v", result.Edges)
}

func TestToolRankGraphNodesCapsAndCounts(t *testing.T) {
	db := hubBrain(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	res, err := tools.RankGraphNodes(ctx, ToolRankGraphNodesRequest{TopN: 2})
	if err != nil {
		t.Fatalf("rank graph nodes: %v", err)
	}
	if res.Count != 2 || len(res.Nodes) != 2 {
		t.Fatalf("count = %d with %d nodes, want 2 and 2", res.Count, len(res.Nodes))
	}
	if !res.Truncated || res.TotalNodes != 5 {
		t.Fatalf("truncated = %v, total = %d, want true and 5", res.Truncated, res.TotalNodes)
	}
	if res.Nodes[0].ID != "entity:bob" || res.Nodes[0].Label != "Bob" {
		t.Fatalf("top node = %+v, want entity:bob/Bob", res.Nodes[0])
	}

	// An absurd request is clamped rather than honoured: a tool answer lands in
	// a context window.
	huge, err := tools.RankGraphNodes(ctx, ToolRankGraphNodesRequest{TopN: 100000})
	if err != nil {
		t.Fatalf("rank graph nodes: %v", err)
	}
	if huge.Count != 5 || huge.Truncated {
		t.Fatalf("clamped request = %d nodes truncated=%v, want all 5 untruncated", huge.Count, huge.Truncated)
	}
}

func TestToolPredictGraphEdgesNamesAndCounts(t *testing.T) {
	db := duplicateBrain(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	res, err := tools.PredictGraphEdges(ctx, ToolPredictGraphEdgesRequest{NodeID: "entity:acme", MaxResults: 2})
	if err != nil {
		t.Fatalf("predict graph edges: %v", err)
	}
	if res.Count != len(res.Edges) {
		t.Fatalf("count %d disagrees with %d edges", res.Count, len(res.Edges))
	}
	if res.Count != 2 || !res.Truncated {
		t.Fatalf("cap of 2 over three candidates gave %d edges truncated=%v", res.Count, res.Truncated)
	}
	for _, edge := range res.Edges {
		if edge.FromLabel == "" || edge.ToLabel == "" {
			t.Fatalf("tool returned an unnamed endpoint: %+v", edge)
		}
	}

	if _, err := tools.PredictGraphEdges(ctx, ToolPredictGraphEdgesRequest{}); err == nil {
		t.Fatalf("tool accepted a request with no node id")
	}
}

func TestToolGraphStatisticsReportsTheGraph(t *testing.T) {
	db := hubBrain(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	res, err := tools.GraphStatistics(ctx, ToolGraphStatisticsRequest{})
	if err != nil {
		t.Fatalf("graph statistics: %v", err)
	}
	if res.NodeCount != 5 || res.EdgeCount != 5 || res.ConnectedComponents != 2 {
		t.Fatalf("statistics = %+v, want 5 nodes, 5 edges, 2 components", res)
	}
	if res.AverageDegree != 2 || res.Density != 0.25 {
		t.Fatalf("statistics = %+v, want degree 2 and density 0.25", res)
	}
}
