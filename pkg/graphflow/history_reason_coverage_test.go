package graphflow

import (
	"context"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// TestHistoryReasonCoverage exercises every invalidation path a caller reaches
// through the facade and graphflow, then reports how many history rows each
// produced and how many of them say why they ended.
//
// Written against raw SQL and calls that predate the reason columns, so the
// same test measures a build without them (reason share 0, and paths that
// wrote no history row at all show up as missing rows).
func TestHistoryReasonCoverage(t *testing.T) {
	db, ctx := openResolveTestDB(t)
	tools := db.GraphRAGTools()

	step := func(name string, f func() error) {
		t.Helper()
		before := historyRows(t, ctx, db)
		if err := f(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("PATH %-26s history rows +%d", name, historyRows(t, ctx, db)-before)
	}

	step("seed", func() error {
		if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: []cortexdb.ToolEntityInput{
			{Name: "Leo", Type: "Person"}, {Name: "Chengdu", Type: "City"}, {Name: "Vienna", Type: "City"},
			{Name: "Apollo", Type: "Project"}, {Name: "Hermes", Type: "Project"}, {Name: "Zeus", Type: "Project"},
			{Name: "CortexDB", Type: "Project"}, {Name: "Cortex DB", Type: "Project"}, {Name: "This"},
		}}); err != nil {
			return err
		}
		_, err := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: []cortexdb.ToolRelationInput{
			{From: "Leo", To: "Apollo", Type: "works_on"}, {From: "Leo", To: "Hermes", Type: "works_on"},
			{From: "Cortex DB", To: "Apollo", Type: "used_by"}, {From: "This", To: "Zeus", Type: "mentions"},
		}})
		return err
	})
	step("upsert changes content", func() error {
		_, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: []cortexdb.ToolEntityInput{
			{Name: "Apollo", Type: "Project", Description: "the lunar programme"}}})
		return err
	})
	step("delete document graph", func() error {
		if _, err := tools.IngestDocument(ctx, cortexdb.ToolIngestDocumentRequest{
			DocumentID: "doc-1", Title: "Runbook", Content: "Failover moves Apollo from Chengdu to Vienna."}); err != nil {
			return err
		}
		_, err := tools.DeleteDocumentGraph(ctx, cortexdb.ToolDeleteDocumentGraphRequest{DocumentID: "doc-1"})
		return err
	})

	step("temporal supersede", func() error {
		t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		if err := SaveTemporalFact(ctx, db, TemporalFact{From: "Leo", To: "Chengdu", Type: "lives_in", ValidFrom: &t0}); err != nil {
			return err
		}
		return SaveTemporalFact(ctx, db, TemporalFact{From: "Leo", To: "Vienna", Type: "lives_in", ValidFrom: &t1, Supersede: true})
	})
	step("graph edit delete relation", func() error {
		_, err := ApplyGraphEdits(ctx, db, GraphEditPlan{Edits: []GraphEdit{
			{Op: EditOpDelete, Kind: EditKindRelation, From: "Leo", To: "Hermes", RelType: "works_on"}}},
			GraphEditOptions{AllowDelete: true})
		return err
	})
	step("graph edit delete entity", func() error {
		_, err := ApplyGraphEdits(ctx, db, GraphEditPlan{Edits: []GraphEdit{
			{Op: EditOpDelete, Kind: EditKindEntity, Name: "Hermes"}}}, GraphEditOptions{AllowDelete: true})
		return err
	})
	step("resolve entities (merge)", func() error {
		_, err := ResolveEntities(ctx, db, ResolveOptions{})
		return err
	})
	step("delete_entities tool", func() error {
		_, err := tools.DeleteEntities(ctx, cortexdb.ToolDeleteEntitiesRequest{Names: []string{"Zeus"}})
		return err
	})
	step("prune junk entities", func() error {
		_, err := db.PruneJunkEntities(ctx, cortexdb.GraphMaintenanceOptions{})
		return err
	})
	total := historyRows(t, ctx, db)
	withReason := historyRowsWithReason(ctx, db)
	share := 0.0
	if total > 0 {
		share = float64(withReason) / float64(total)
	}
	t.Logf("METRIC history rows=%d with reason=%d share=%.2f", total, withReason, share)
	if total == 0 {
		t.Fatal("no history rows were written")
	}
}

func historyRows(t *testing.T, ctx context.Context, db *cortexdb.DB) int {
	t.Helper()
	var n, m int
	_ = db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_node_history`).Scan(&n)
	_ = db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_edge_history`).Scan(&m)
	return n + m
}

// historyRowsWithReason is 0 on a build whose history has no reason column.
func historyRowsWithReason(ctx context.Context, db *cortexdb.DB) int {
	var n, m int
	if err := db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_node_history WHERE reason IS NOT NULL AND reason <> ''`).Scan(&n); err != nil {
		return 0
	}
	if err := db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_edge_history WHERE reason IS NOT NULL AND reason <> ''`).Scan(&m); err != nil {
		return 0
	}
	return n + m
}

// Asking what was true as of a date must not break writing afterwards. The
// as-of index was an unguarded json_extract, which SQLite evaluates on every
// insert, so once a temporal query had run, ingesting any document failed on
// its chunk edges ("malformed JSON").
func TestIngestAfterAnAsOfQuery(t *testing.T) {
	db, ctx := openResolveTestDB(t)
	if _, err := QueryFactsAsOf(ctx, db, time.Now(), TemporalFilter{}); err != nil {
		t.Fatalf("as-of: %v", err)
	}
	if _, err := db.GraphRAGTools().IngestDocument(ctx, cortexdb.ToolIngestDocumentRequest{
		DocumentID: "doc-after", Title: "After", Content: "Apollo moved to Vienna."}); err != nil {
		t.Fatalf("ingest after an as-of query: %v", err)
	}
}
