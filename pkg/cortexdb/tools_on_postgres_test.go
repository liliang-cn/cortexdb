package cortexdb

import (
	"context"
	"strings"
	"testing"
)

// The tools, run against PostgreSQL, because the unit suites ran them against
// SQLite and were green while none of these worked.
//
// Each one below failed with a different error that named a SQLite builtin, or
// nothing at all:
//
//	graph-mode retrieval      function json_valid(text) does not exist
//	delete_document_graph     syntax error at end of input   (json_each)
//	apply_inference           function json_valid(text) does not exist
//	object_set_resolve        collation "nocase" ... does not exist
//	memory_search             syntax error at or near "MATCH"
//
// A dual-backend unit test per feature would have caught each one. What was
// missing was any test that ran the *tools* — the surface a caller actually
// reaches — on the second backend at all.

func TestGraphToolsRunOnPostgres(t *testing.T) {
	db := openPostgresBrain(t, 4)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{
		DocumentID: "runbook",
		Title:      "Runbook",
		Content:    "ledger-svc depends on riskd. riskd loads rules from rulex.",
	}); err != nil {
		t.Fatalf("IngestDocument: %v", err)
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		DocumentID: "runbook",
		Entities: []ToolEntityInput{
			{Name: "ledger-svc", Type: "Service"},
			{Name: "riskd", Type: "Service"},
			{Name: "rulex", Type: "Service"},
		},
	}); err != nil {
		t.Fatalf("UpsertEntities: %v", err)
	}
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
		DocumentID: "runbook",
		Relations: []ToolRelationInput{
			{From: "ledger-svc", To: "riskd", Type: "DEPENDS_ON"},
			{From: "riskd", To: "rulex", Type: "LOADS_FROM"},
		},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}

	// The entity-to-chunk hop. This is what read a node's document_id out of
	// the properties JSON, and it took every graph-mode question with it.
	t.Run("chunks by entities", func(t *testing.T) {
		if _, err := tools.SearchChunksByEntities(ctx, ToolSearchChunksByEntitiesRequest{
			EntityNames: []string{"ledger-svc"}, TopK: 5, MaxHops: 2,
		}); err != nil {
			t.Fatalf("SearchChunksByEntities: %v", err)
		}
	})

	// Deterministic inference reads the same properties, and compares a JSON
	// boolean it has to normalise first: json_extract of a JSON true is the
	// integer 1 on SQLite and the text 'true' on PostgreSQL, so a rule that
	// skipped already-inferred edges would have re-derived its own output.
	t.Run("inference", func(t *testing.T) {
		// Written the way inference_test.go writes it — SaveKnowledge, then a
		// rule scoped to that document — so this test is about the backend and
		// not about rediscovering the tool's own contract.
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
			KnowledgeID: "chain",
			Content:     "Alice works at Acme. Acme is located in Berlin.",
			Entities: []ToolEntityInput{
				{Name: "Alice", Type: "Person"},
				{Name: "Acme", Type: "Company"},
				{Name: "Berlin", Type: "City"},
			},
			Relations: []ToolRelationInput{
				{From: "Alice", To: "Acme", Type: "works_at"},
				{From: "Acme", To: "Berlin", Type: "located_in"},
			},
		}); err != nil {
			t.Fatalf("SaveKnowledge: %v", err)
		}

		rule := InferenceRule{
			RuleID:             "employment_city",
			LeftRelationType:   "works_at",
			RightRelationType:  "located_in",
			ResultRelationType: "works_in_city",
		}
		resp, err := db.ApplyInferenceRules(ctx, ApplyInferenceRequest{
			DocumentID: "chain", Rules: []InferenceRule{rule},
		})
		if err != nil {
			t.Fatalf("ApplyInferenceRules: %v", err)
		}
		if len(resp.CreatedEdgeIDs) == 0 {
			t.Fatal("the rule matched nothing; Alice → Acme → Berlin should have fired")
		}

		// Running it again must not build on its own output. json_extract of a
		// JSON true reads back as the integer 1 on SQLite and as the text
		// 'true' on PostgreSQL, so a raw comparison against 1 made every
		// inferred edge look explicit — and the second pass would chain off it.
		again, err := db.ApplyInferenceRules(ctx, ApplyInferenceRequest{
			DocumentID: "chain", Rules: []InferenceRule{rule},
		})
		if err != nil {
			t.Fatalf("ApplyInferenceRules (second run): %v", err)
		}
		if len(again.CreatedEdgeIDs) > len(resp.CreatedEdgeIDs) {
			t.Errorf("second run created %d edges over the first %d — inferred "+
				"edges are being read as explicit",
				len(again.CreatedEdgeIDs), len(resp.CreatedEdgeIDs))
		}
	})

	// The whole-graph listing, and the deletion that walks a JSON array.
	t.Run("list and delete", func(t *testing.T) {
		if _, err := db.ListGraphAll(ctx, GraphListAllRequest{Limit: 100}); err != nil {
			t.Fatalf("ListGraphAll: %v", err)
		}
		dry, err := tools.DeleteDocumentGraph(ctx, ToolDeleteDocumentGraphRequest{
			DocumentID: "runbook", DryRun: true,
		})
		if err != nil {
			t.Fatalf("DeleteDocumentGraph (dry run): %v", err)
		}
		if dry.ChunkNodesDeleted == 0 {
			t.Error("dry run found no chunks to delete for a document that has them")
		}
		if _, err := tools.DeleteDocumentGraph(ctx, ToolDeleteDocumentGraphRequest{
			DocumentID: "runbook",
		}); err != nil {
			t.Fatalf("DeleteDocumentGraph: %v", err)
		}
	})
}

func TestObjectSetsResolveOnPostgres(t *testing.T) {
	db := openPostgresBrain(t, 4)
	ctx := context.Background()

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{
		Activate: true,
		Schema: OntologySchema{
			SchemaID: "polaris", Name: "Polaris", Version: 1,
			ObjectTypes: []OntologyObjectType{{
				APIName: "Service", PrimaryKey: "name", TitleProperty: "name",
				Properties: []OntologyProperty{{
					APIName: "name", DataType: OntologyDataType{Kind: "string"}, Required: true,
				}},
			}},
			LinkTypes: []OntologyLinkType{{
				APIName: "DEPENDS_ON",
				A:       OntologyLinkSide{APIName: "upstream", ObjectTypeAPIName: "Service", Cardinality: "MANY"},
				B:       OntologyLinkSide{APIName: "downstream", ObjectTypeAPIName: "Service", Cardinality: "MANY"},
			}},
		},
	}); err != nil {
		t.Fatalf("SaveOntologySchema: %v", err)
	}

	// Both arms used COLLATE NOCASE, which PostgreSQL refuses outright.
	if _, err := db.ResolveObjectSetObjects(ctx, ObjectSetResolveRequest{
		ObjectSet: ObjectSet{Kind: "base", ObjectType: "Service"}, Limit: 10,
	}); err != nil {
		t.Fatalf("resolve base object set: %v", err)
	}
	if _, err := db.ResolveObjectSetObjects(ctx, ObjectSetResolveRequest{
		ObjectSet: ObjectSet{
			Kind:   "search_around",
			Source: &ObjectSet{Kind: "static", ObjectIDs: []string{"entity:gateway"}},
			Link:   "downstream",
		},
		Limit: 10,
	}); err != nil {
		t.Fatalf("resolve search_around: %v", err)
	}
}

func TestMemorySearchRunsOnPostgres(t *testing.T) {
	db := openPostgresBrain(t, 4)
	ctx := context.Background()

	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "mem-1",
		Content:  "ledger-svc 的 owner 在 INC-204 之后是赵启明。",
	}); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	// The lexical arm asked PostgreSQL for `messages_fts MATCH ?` — an FTS5
	// virtual table that does not exist there and an operator PostgreSQL does
	// not have. Every memory search failed, and so did every recall that reads
	// memory.
	for _, q := range []string{"owner", "赵启明", "INC-204 之后是谁"} {
		resp, err := db.SearchMemory(ctx, MemorySearchRequest{Query: q, TopK: 5})
		if err != nil {
			if strings.Contains(err.Error(), "MATCH") {
				t.Fatalf("%q: FTS5 syntax reached PostgreSQL: %v", q, err)
			}
			t.Fatalf("SearchMemory(%q): %v", q, err)
		}
		_ = resp
	}
}

// An ontology action, applied on PostgreSQL, with its audit row read back.
//
// ApplyAction creates its audit table lazily on first use, and the DDL said
// `id INTEGER PRIMARY KEY AUTOINCREMENT` — SQLite's spelling, which PostgreSQL
// rejects at the parser. So the table was never created there and every action
// apply failed with it. The gap was in the coverage, not the intent: the action
// tests all run on SQLite, and the PostgreSQL tool test above only *listed*
// action types, which needs no table.
func TestOntologyActionsApplyOnPostgres(t *testing.T) {
	db := openPostgresBrain(t, 4)
	ctx := context.Background()

	activateAviationActions(t, db)

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
		Actor:      "tester",
	}); err != nil {
		t.Fatalf("apply action on postgres: %v", err)
	}

	// The audit trail is the half that needed the table, so it is the half
	// worth reading back.
	var actor string
	if err := db.queryRow(ctx,
		`SELECT actor FROM ontology_action_audit WHERE action_api_name = ?`,
		"registerAirport").Scan(&actor); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if actor != "tester" {
		t.Fatalf("audited actor = %q, want %q", actor, "tester")
	}

	// And the action's actual effect, so a passing audit cannot stand in for
	// work that did not happen.
	if _, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR")); err != nil {
		t.Fatalf("the action's object was not written: %v", err)
	}
}

// Recall accounting is best-effort by design — it must never fail a search —
// which means the only way to know it works is to assert on its effect.
//
// It wrote with json_set, two paths at once, which is SQLite's own; on
// PostgreSQL the UPDATE errored and the error was discarded. Every memory's
// recall_count stayed at zero and the usage report showed a brain nobody had
// ever read from. Getting the SQL to run then found the second half: the
// expression came back as text and messages.metadata is jsonb there, a
// combination PostgreSQL has no assignment cast for.
func TestRecallAccountingActuallyWritesOnPostgres(t *testing.T) {
	db := openPostgresBrain(t, 4)
	ctx := context.Background()

	if _, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID: "mem-counted",
		Content:  "ledger-svc 的 owner 在 INC-204 之后是赵启明。",
	}); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}
	before, err := db.GetMemory(ctx, MemoryGetRequest{MemoryID: "mem-counted"})
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if got := memoryRecallCount(before.Memory.Metadata); got != 0 {
		t.Fatalf("a memory nobody has read has recall_count %v", got)
	}

	if _, err := db.SearchMemory(ctx, MemorySearchRequest{Query: "赵启明", TopK: 5}); err != nil {
		t.Fatalf("SearchMemory: %v", err)
	}

	after, err := db.GetMemory(ctx, MemoryGetRequest{MemoryID: "mem-counted"})
	if err != nil {
		t.Fatalf("GetMemory after recall: %v", err)
	}
	if got := memoryRecallCount(after.Memory.Metadata); got < 1 {
		t.Errorf("recall_count = %v after a search that returned this memory — "+
			"the accounting UPDATE failed and said nothing", got)
	}
	if after.Memory.Metadata["last_recalled_at"] == nil {
		t.Error("last_recalled_at was never written")
	}
}

// Recall accounting, on both backends, because it was written for one.
//
// recordMemoryRecalls patched the metadata column with json_set — SQLite's
// spelling — and discards its errors by design, so on PostgreSQL it wrote
// nothing and reported nothing. Every memory's recall_count stayed at zero and
// the usage report showed a brain nobody had ever read from. A dialect-aware
// json_set would not have fixed it either: this column is TEXT on SQLite and
// JSONB on PostgreSQL.
func TestRecallAccountingSurvivesTheBackend(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(t *testing.T) *DB
	}{
		{"sqlite", openLexicalTestDB},
		{"postgres", func(t *testing.T) *DB { return openPostgresBrain(t, 4) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := tc.open(t)
			ctx := context.Background()

			if _, err := db.SaveMemory(ctx, MemorySaveRequest{
				MemoryID: "mem-recall",
				Scope:    MemoryScopeGlobal,
				Content:  "ledger-svc 的 owner 在 INC-204 之后是赵启明。",
			}); err != nil {
				t.Fatalf("save: %v", err)
			}

			for i := 0; i < 2; i++ {
				if _, err := db.SearchMemory(ctx, MemorySearchRequest{
					Query: "owner", Scope: MemoryScopeGlobal, TopK: 5,
				}); err != nil {
					t.Fatalf("search %d: %v", i, err)
				}
			}

			got, err := db.GetMemory(ctx, MemoryGetRequest{MemoryID: "mem-recall"})
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if n := memoryRecallCount(got.Memory.Metadata); n < 1 {
				t.Fatalf("recall_count = %d after two searches, want at least 1 — "+
					"the accounting write is being swallowed", n)
			}
			if _, ok := got.Memory.Metadata["last_recalled_at"].(string); !ok {
				t.Fatalf("last_recalled_at missing from %v", got.Memory.Metadata)
			}
		})
	}
}
