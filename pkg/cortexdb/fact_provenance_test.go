package cortexdb

import (
	"context"
	"path/filepath"
	"testing"
)

func provenanceBrain(t *testing.T) (*DB, *GraphRAGToolbox) {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "prov.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, db.GraphRAGTools()
}

// The question the graph could not answer: says who?
//
// The provenance was already being written on every relation edge and read by
// nothing, which is the same as not having it.
func TestAFactCanNameItsSource(t *testing.T) {
	db, tools := provenanceBrain(t)
	ctx := context.Background()

	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "Leo", Type: "Person"},
		{Name: "LINBIT", Type: "Company"},
	}}); err != nil {
		t.Fatalf("entities: %v", err)
	}

	res, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
		DocumentID: "handbook",
		Relations: []ToolRelationInput{{
			From: "Leo", To: "LINBIT", Type: "works_at",
			ChunkIDs:   []string{"handbook#3"},
			Provenance: "onboarding notes",
		}},
	})
	if err != nil {
		t.Fatalf("relations: %v", err)
	}
	if len(res.EdgeIDs) != 1 {
		t.Fatalf("expected one edge, got %v", res.EdgeIDs)
	}

	prov, err := db.FactProvenanceFor(ctx, res.EdgeIDs[0], false)
	if err != nil {
		t.Fatalf("provenance: %v", err)
	}
	if prov.DocumentID != "handbook" {
		t.Errorf("document = %q, want handbook", prov.DocumentID)
	}
	if len(prov.ChunkIDs) != 1 || prov.ChunkIDs[0] != "handbook#3" {
		t.Errorf("chunk ids = %v, want [handbook#3]", prov.ChunkIDs)
	}
	if prov.Source != "onboarding notes" {
		t.Errorf("source = %q", prov.Source)
	}
	if !prov.Cited() {
		t.Error("a fact with a document and a chunk is not counted as cited")
	}
	if prov.Type != "works_at" || prov.From == "" || prov.To == "" {
		t.Errorf("the fact itself did not survive: %+v", prov)
	}
}

// A citation pointing at text that no longer exists is the finding, not an
// error. Dropping it silently would turn a dangling reference into an
// apparently unsupported fact, which reads as a different problem.
func TestAChunkThatNoLongerExistsIsReportedMissing(t *testing.T) {
	db, tools := provenanceBrain(t)
	ctx := context.Background()

	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "Leo", Type: "Person"}, {Name: "Chengdu", Type: "City"},
	}}); err != nil {
		t.Fatalf("entities: %v", err)
	}
	res, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
		DocumentID: "gone",
		Relations:  []ToolRelationInput{{From: "Leo", To: "Chengdu", Type: "lives_in", ChunkIDs: []string{"gone#1"}}},
	})
	if err != nil {
		t.Fatalf("relations: %v", err)
	}

	prov, err := db.FactProvenanceFor(ctx, res.EdgeIDs[0], true)
	if err != nil {
		t.Fatalf("provenance: %v", err)
	}
	if len(prov.Missing) != 1 || prov.Missing[0] != "gone#1" {
		t.Errorf("missing = %v, want [gone#1] — a dangling citation must be visible", prov.Missing)
	}
}

// What a knowledge base has to be able to ask about itself: how much of what
// I am about to say is backed by anything?
func TestUncitedFactsFindsTheOnesWithNoSource(t *testing.T) {
	db, tools := provenanceBrain(t)
	ctx := context.Background()

	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "Leo", Type: "Person"},
		{Name: "LINBIT", Type: "Company"},
		{Name: "Chengdu", Type: "City"},
		{Name: "Rene", Type: "Person"},
	}}); err != nil {
		t.Fatalf("entities: %v", err)
	}

	// Cited: a document and a chunk behind it.
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
		DocumentID: "handbook",
		Relations: []ToolRelationInput{
			{From: "Leo", To: "LINBIT", Type: "works_at", ChunkIDs: []string{"handbook#3"}},
		},
	}); err != nil {
		t.Fatalf("cited: %v", err)
	}

	// Derived: no chunks, but the rule accounts for it.
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
		Relations: []ToolRelationInput{
			{From: "Leo", To: "Rene", Type: "colleague_of", Inferred: true, RuleID: "same-employer"},
		},
	}); err != nil {
		t.Fatalf("inferred: %v", err)
	}

	// Uncited: asserted by nobody.
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
		Relations: []ToolRelationInput{
			{From: "Leo", To: "Chengdu", Type: "lives_in"},
		},
	}); err != nil {
		t.Fatalf("uncited: %v", err)
	}

	found, err := db.UncitedFacts(ctx, 100)
	if err != nil {
		t.Fatalf("UncitedFacts: %v", err)
	}
	if len(found) != 1 {
		types := make([]string, len(found))
		for i, f := range found {
			types[i] = f.Type
		}
		t.Fatalf("%d uncited facts %v, want exactly 1 (lives_in)", len(found), types)
	}
	if found[0].Type != "lives_in" {
		t.Errorf("uncited fact is %q, want lives_in", found[0].Type)
	}

	// A derived fact is accounted for by its rule, not by chunks.
	prov, err := db.FactProvenanceFor(ctx, found[0].EdgeID, false)
	if err != nil {
		t.Fatalf("provenance: %v", err)
	}
	if prov.Cited() {
		t.Error("the uncited fact claims to be cited")
	}
}

func TestAnUnknownEdgeIsAnErrorNotAnEmptyAnswer(t *testing.T) {
	db, _ := provenanceBrain(t)
	if _, err := db.FactProvenanceFor(context.Background(), "no-such-edge", false); err == nil {
		t.Error("asking about an edge that does not exist returned no error — an empty provenance and a missing fact are different findings")
	}
	if _, err := db.FactProvenanceFor(context.Background(), "  ", false); err == nil {
		t.Error("an empty edge id was accepted")
	}
}

// A capability nothing can call is the same gap one layer up: the provenance
// was written and unreadable, and a reader nothing exposes is unreachable.
func TestTheProvenanceToolsAreDeclaredAndReachable(t *testing.T) {
	db, tools := provenanceBrain(t)
	ctx := context.Background()

	declared := map[string]bool{}
	for _, def := range tools.Definitions() {
		declared[def.Name] = true
	}
	for _, name := range []string{"fact_provenance", "uncited_facts"} {
		if !declared[name] {
			t.Errorf("%s is implemented but not declared as a tool", name)
		}
	}

	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "Leo", Type: "Person"}, {Name: "Chengdu", Type: "City"},
	}}); err != nil {
		t.Fatalf("entities: %v", err)
	}
	res, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
		DocumentID: "notes",
		Relations:  []ToolRelationInput{{From: "Leo", To: "Chengdu", Type: "lives_in", ChunkIDs: []string{"notes#1"}}},
	})
	if err != nil {
		t.Fatalf("relations: %v", err)
	}

	got, err := db.FactProvenanceTool(ctx, ToolFactProvenanceRequest{EdgeID: res.EdgeIDs[0]})
	if err != nil {
		t.Fatalf("fact_provenance: %v", err)
	}
	// Carried explicitly so a model asking "is this backed by anything" gets an
	// answer rather than a rule to apply.
	if !got.Cited {
		t.Error("a fact with a document and a chunk came back uncited")
	}
	if got.Provenance.DocumentID != "notes" {
		t.Errorf("document = %q", got.Provenance.DocumentID)
	}

	// Truncated must be honest, or "12 uncited facts" reads as "only 12".
	swept, err := db.UncitedFactsTool(ctx, ToolUncitedFactsRequest{Limit: 1})
	if err != nil {
		t.Fatalf("uncited_facts: %v", err)
	}
	if swept.Count != len(swept.Facts) {
		t.Errorf("count %d does not match the %d facts returned", swept.Count, len(swept.Facts))
	}
}
