package cortexdb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// The facade half of point-in-time reads, on both backends.
//
// temporalBrains returns the brains a test should run against: always SQLite,
// and PostgreSQL too when CORTEXDB_TEST_POSTGRES is set. Skipped loudly rather
// than silently, so a green run cannot be mistaken for coverage it does not
// have.
func temporalBrains(t *testing.T) map[string]*DB {
	t.Helper()
	out := map[string]*DB{}

	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "temporal.db")))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	out["sqlite"] = db

	if os.Getenv("CORTEXDB_TEST_POSTGRES") == "" {
		t.Log("CORTEXDB_TEST_POSTGRES is unset — PostgreSQL is NOT covered by this run")
		return out
	}
	out["postgres"] = openPostgresBrain(t, 4)
	return out
}

// A retracted fact must still be able to say where it came from.
//
// This is the interaction that makes retraction worth more than deletion. An
// audit asks "we told somebody this and then withdrew it — what was it based
// on?", and a hard delete has no answer at all. FactProvenanceFor reads through
// the same as-of source as everything else, so the answer is one context away.
func TestProvenanceOfARetractedFactStillResolvesAsOf(t *testing.T) {
	for name, db := range temporalBrains(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			tools := db.GraphRAGTools()

			if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
				{Name: "sds-meta", Type: "Host"},
				{Name: "sds-a", Type: "Host"},
			}}); err != nil {
				t.Fatalf("UpsertEntities: %v", err)
			}
			rel, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
				DocumentID: "runbook",
				Relations: []ToolRelationInput{{
					From: "sds-meta", To: "sds-a", Type: "fails_over_to",
					ChunkIDs: []string{"runbook#1"},
				}},
			})
			if err != nil || len(rel.EdgeIDs) != 1 {
				t.Fatalf("UpsertRelations: %v (%d edges)", err, len(rel.EdgeIDs))
			}
			edgeID := rel.EdgeIDs[0]

			before := db.Graph().Now()
			if err := db.Graph().DeleteEdge(ctx, edgeID); err != nil {
				t.Fatalf("DeleteEdge: %v", err)
			}

			// Now: the fact is gone, and so is its account of itself.
			if _, err := db.FactProvenanceFor(ctx, edgeID, false); err == nil {
				t.Error("a retracted fact still resolves in the present")
			}

			// Then: both are there.
			prov, err := db.FactProvenanceFor(AsOf(ctx, before), edgeID, false)
			if err != nil {
				t.Fatalf("FactProvenanceFor as-of a retracted edge: %v", err)
			}
			if prov.DocumentID != "runbook" {
				t.Errorf("document = %q, want runbook", prov.DocumentID)
			}
			if !prov.Cited() {
				t.Errorf("the retracted fact lost its citation: %+v", prov)
			}
		})
	}
}

func TestGraphSnapshotCountsThePastAndTheDiffAgreesWithIt(t *testing.T) {
	for name, db := range temporalBrains(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			g := db.Graph()
			if err := g.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			add := func(id, content string) {
				t.Helper()
				if err := g.UpsertNode(ctx, &graph.GraphNode{
					ID: id, Vector: []float32{1, 0, 0, 0}, Content: content, NodeType: "thing",
				}); err != nil {
					t.Fatalf("UpsertNode %s: %v", id, err)
				}
			}
			add("t:1", "one")
			add("t:2", "two")
			t1 := g.Now()
			add("t:3", "three")
			if err := g.DeleteNode(ctx, "t:1"); err != nil {
				t.Fatalf("DeleteNode: %v", err)
			}
			t2 := g.Now()

			was, err := db.GraphSnapshotAt(ctx, t1, SnapshotOptions{Sample: 10})
			if err != nil {
				t.Fatalf("GraphSnapshotAt: %v", err)
			}
			if was.Nodes != 2 {
				t.Errorf("snapshot at t1 counted %d nodes, want 2", was.Nodes)
			}
			if len(was.NodeSample) != 2 {
				t.Errorf("sample listed %d nodes, want 2", len(was.NodeSample))
			}

			is, err := db.GraphSnapshotAt(ctx, t2, SnapshotOptions{})
			if err != nil {
				t.Fatalf("GraphSnapshotAt t2: %v", err)
			}
			if is.Nodes != 2 {
				t.Errorf("snapshot at t2 counted %d nodes, want 2 (one added, one retracted)", is.Nodes)
			}

			// The diff has to be consistent with the two snapshots it sits
			// between: one added and one retracted moves the count by zero.
			diff, err := db.GraphDiff(ctx, t1, t2, graph.DiffOptions{})
			if err != nil {
				t.Fatalf("GraphDiff: %v", err)
			}
			var added, retracted int
			for _, c := range diff.Nodes {
				switch c.Kind {
				case graph.DiffAdded:
					added++
				case graph.DiffRetracted:
					retracted++
				}
			}
			if added != 1 || retracted != 1 {
				t.Errorf("diff = %d added, %d retracted; want 1 and 1", added, retracted)
			}
			if was.Nodes+added-retracted != is.Nodes {
				t.Errorf("the diff and the snapshots disagree: %d + %d - %d != %d",
					was.Nodes, added, retracted, is.Nodes)
			}
		})
	}
}

// The tools, called the way a host calls them: JSON in, JSON out, through the
// toolbox's own dispatch rather than the Go methods behind it.
func TestTheTemporalToolsAnswerThroughTheToolbox(t *testing.T) {
	for name, db := range temporalBrains(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			g := db.Graph()
			if err := g.InitGraphSchema(ctx); err != nil {
				t.Fatalf("schema: %v", err)
			}
			if err := g.UpsertNode(ctx, &graph.GraphNode{
				ID: "tool:a", Vector: []float32{1, 0, 0, 0}, Content: "here", NodeType: "thing",
			}); err != nil {
				t.Fatalf("UpsertNode: %v", err)
			}
			t1 := g.Now()
			if err := g.DeleteNode(ctx, "tool:a"); err != nil {
				t.Fatalf("DeleteNode: %v", err)
			}

			tools := db.GraphRAGTools()

			raw, err := tools.Call(ctx, "graph_snapshot", mustJSON(t, GraphSnapshotToolRequest{
				AsOf: t1.Format(time.RFC3339Nano), Sample: 5,
			}))
			if err != nil {
				t.Fatalf("graph_snapshot: %v", err)
			}
			snap, ok := raw.(GraphSnapshot)
			if !ok {
				t.Fatalf("graph_snapshot returned %T", raw)
			}
			if snap.Nodes != 1 {
				t.Errorf("graph_snapshot counted %d nodes at t1, want 1", snap.Nodes)
			}

			raw, err = tools.Call(ctx, "graph_diff", mustJSON(t, GraphDiffToolRequest{
				From: t1.Format(time.RFC3339Nano),
			}))
			if err != nil {
				t.Fatalf("graph_diff: %v", err)
			}
			diff, ok := raw.(graph.GraphDiffResult)
			if !ok {
				t.Fatalf("graph_diff returned %T", raw)
			}
			if len(diff.Nodes) != 1 || diff.Nodes[0].Kind != graph.DiffRetracted {
				t.Errorf("graph_diff = %+v, want one retracted node", diff.Nodes)
			}

			// A malformed instant is refused, not read as now. A tool that
			// answered about the present here would look exactly like a
			// correct answer.
			if _, err := tools.Call(ctx, "graph_snapshot", mustJSON(t, GraphSnapshotToolRequest{
				AsOf: "yesterday",
			})); err == nil {
				t.Error("graph_snapshot accepted a non-RFC-3339 instant")
			}

			// vacuum_graph refuses without a cutoff.
			if _, err := tools.Call(ctx, "vacuum_graph", mustJSON(t, VacuumGraphToolRequest{})); err == nil {
				t.Error("vacuum_graph ran without a cutoff")
			}

			raw, err = tools.Call(ctx, "vacuum_graph", mustJSON(t, VacuumGraphToolRequest{
				Before: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
				DryRun: true,
			}))
			if err != nil {
				t.Fatalf("vacuum_graph dry run: %v", err)
			}
			report, ok := raw.(graph.PurgeReport)
			if !ok {
				t.Fatalf("vacuum_graph returned %T", raw)
			}
			if !report.DryRun || report.Nodes != 1 {
				t.Errorf("vacuum_graph dry run = %+v, want 1 node and DryRun", report)
			}
			// A dry run must not have taken anything.
			if _, err := g.GetNode(AsOf(ctx, t1), "tool:a"); err != nil {
				t.Errorf("the dry run removed history: %v", err)
			}
		})
	}
}

// delete_document_graph keeps its name and its present behaviour, and gains a
// past. The tool is the one place a whole subgraph disappears at once, so it is
// also the one where a hard delete would quietly lose the most.
func TestDeletingADocumentGraphIsARetraction(t *testing.T) {
	for name, db := range temporalBrains(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			tools := db.GraphRAGTools()
			if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{
				DocumentID: "doomed-doc",
				Title:      "Doomed",
				Content:    "sds-meta is the metadata host for the cluster.",
			}); err != nil {
				t.Fatalf("IngestDocument: %v", err)
			}

			before := db.Graph().Now()
			if _, err := tools.DeleteDocumentGraph(ctx, ToolDeleteDocumentGraphRequest{
				DocumentID: "doomed-doc",
			}); err != nil {
				t.Fatalf("DeleteDocumentGraph: %v", err)
			}

			now, err := db.GraphSnapshotAt(ctx, db.Graph().Now(), SnapshotOptions{})
			if err != nil {
				t.Fatalf("snapshot now: %v", err)
			}
			was, err := db.GraphSnapshotAt(ctx, before, SnapshotOptions{})
			if err != nil {
				t.Fatalf("snapshot before: %v", err)
			}
			if was.Nodes <= now.Nodes {
				t.Errorf("the document's graph left no history: %d nodes before, %d now",
					was.Nodes, now.Nodes)
			}
		})
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return b
}
