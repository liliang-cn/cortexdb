package cortexdb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestApplyInferenceRulesAndCleanup(t *testing.T) {
	dbPath := fmt.Sprintf("test_inference_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{
		SchemaID: "inference-schema",
		Activate: true,
		EntityTypes: []OntologyEntityType{
			{Name: "entity"},
			{Name: "person"},
			{Name: "organization"},
			{Name: "city"},
		},
		RelationTypes: []OntologyRelationType{
			{Name: "works_at", AllowedFromTypes: []string{"person"}, AllowedToTypes: []string{"organization"}},
			{Name: "located_in", AllowedFromTypes: []string{"organization"}, AllowedToTypes: []string{"city"}},
			{Name: "works_in_city", AllowedFromTypes: []string{"person"}, AllowedToTypes: []string{"city"}},
		},
	}); err != nil {
		t.Fatalf("save ontology schema: %v", err)
	}

	knowledgeResp, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "knowledge-inference",
		Title:       "Alice at Acme Berlin",
		Content:     "Alice works at Acme. Acme is located in Berlin.",
		ChunkSize:   24,
		Entities: []ToolEntityInput{
			{Name: "Alice", Type: "person", ChunkIDs: []string{"chunk:knowledge-inference:000"}},
			{Name: "Acme", Type: "organization", ChunkIDs: []string{"chunk:knowledge-inference:000"}},
			{Name: "Berlin", Type: "city", ChunkIDs: []string{"chunk:knowledge-inference:000"}},
		},
		Relations: []ToolRelationInput{
			{From: "Alice", To: "Acme", Type: "works_at"},
			{From: "Acme", To: "Berlin", Type: "located_in"},
		},
	})
	if err != nil {
		t.Fatalf("save knowledge: %v", err)
	}
	if len(knowledgeResp.RelationEdgeIDs) != 2 {
		t.Fatalf("expected 2 explicit relation edges, got %d", len(knowledgeResp.RelationEdgeIDs))
	}

	inferenceResp, err := db.ApplyInferenceRules(ctx, ApplyInferenceRequest{
		DocumentID:     "knowledge-inference",
		DeleteExisting: true,
		Rules: []InferenceRule{
			{
				RuleID:             "employment_city",
				LeftRelationType:   "works_at",
				RightRelationType:  "located_in",
				ResultRelationType: "works_in_city",
			},
		},
	})
	if err != nil {
		t.Fatalf("apply inference rules: %v", err)
	}
	if len(inferenceResp.CreatedEdgeIDs) != 1 {
		t.Fatalf("expected 1 inferred edge, got %d", len(inferenceResp.CreatedEdgeIDs))
	}

	edges, err := db.Graph().GetEdges(ctx, graphEntityNodeID("Alice"), "out")
	if err != nil {
		t.Fatalf("get alice edges: %v", err)
	}
	foundInferred := false
	for _, edge := range edges {
		if edge.EdgeType != "works_in_city" {
			continue
		}
		foundInferred = true
		if inferred, ok := edge.Properties["inferred"].(bool); !ok || !inferred {
			t.Fatalf("expected inferred flag on edge properties, got %+v", edge.Properties)
		}
		if provenance, ok := edge.Properties["provenance"].(string); !ok || provenance != "rule" {
			t.Fatalf("expected rule provenance, got %+v", edge.Properties)
		}
	}
	if !foundInferred {
		t.Fatal("expected inferred works_in_city edge")
	}

	if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "knowledge-inference"}); err != nil {
		t.Fatalf("delete knowledge: %v", err)
	}

	postDeleteEdges, err := db.Graph().GetEdges(ctx, graphEntityNodeID("Alice"), "out")
	if err != nil {
		t.Fatalf("get alice edges after delete: %v", err)
	}
	for _, edge := range postDeleteEdges {
		if edge.EdgeType == "works_in_city" {
			t.Fatalf("expected inferred edge cleanup, found %+v", edge)
		}
	}
}

func TestUpsertRelationsStoresInferenceProvenance(t *testing.T) {
	dbPath := fmt.Sprintf("test_inference_provenance_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{
		SchemaID: "llm-inference-schema",
		Activate: true,
		EntityTypes: []OntologyEntityType{
			{Name: "entity"},
			{Name: "person"},
			{Name: "organization"},
		},
		RelationTypes: []OntologyRelationType{
			{Name: "works_at", AllowedFromTypes: []string{"person"}, AllowedToTypes: []string{"organization"}},
			{Name: "works_for_group", AllowedFromTypes: []string{"person"}, AllowedToTypes: []string{"organization"}},
		},
	}); err != nil {
		t.Fatalf("save ontology schema: %v", err)
	}

	tools := db.GraphRAGTools()
	ingestResp, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{
		DocumentID: "doc-provenance",
		Content:    "Alice works for Acme Group.",
		ChunkSize:  16,
	})
	if err != nil {
		t.Fatalf("ingest document: %v", err)
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		DocumentID: "doc-provenance",
		Entities: []ToolEntityInput{
			{Name: "Alice", Type: "person", ChunkIDs: []string{ingestResp.ChunkNodeIDs[0]}},
			{Name: "Acme Group", Type: "organization", ChunkIDs: []string{ingestResp.ChunkNodeIDs[0]}},
		},
	}); err != nil {
		t.Fatalf("upsert entities: %v", err)
	}

	resp, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
		DocumentID: "doc-provenance",
		Relations: []ToolRelationInput{
			{
				From:           "Alice",
				To:             "Acme Group",
				Type:           "works_for_group",
				Inferred:       true,
				Provenance:     "llm",
				RuleID:         "llm-group-inference",
				SupportEdgeIDs: []string{"edge:source:1", "edge:source:2"},
			},
		},
	})
	if err != nil {
		t.Fatalf("upsert inferred relation: %v", err)
	}
	if len(resp.EdgeIDs) != 1 {
		t.Fatalf("expected 1 inferred relation edge, got %d", len(resp.EdgeIDs))
	}

	edges, err := db.Graph().GetEdges(ctx, graphEntityNodeID("Alice"), "out")
	if err != nil {
		t.Fatalf("get alice edges: %v", err)
	}
	found := false
	for _, edge := range edges {
		if edge.EdgeType != "works_for_group" {
			continue
		}
		found = true
		if inferred, ok := edge.Properties["inferred"].(bool); !ok || !inferred {
			t.Fatalf("expected inferred property on llm relation, got %+v", edge.Properties)
		}
		if provenance, ok := edge.Properties["provenance"].(string); !ok || provenance != "llm" {
			t.Fatalf("expected llm provenance, got %+v", edge.Properties)
		}
		if ruleID, ok := edge.Properties["rule_id"].(string); !ok || !strings.Contains(ruleID, "llm-group-inference") {
			t.Fatalf("expected rule_id provenance, got %+v", edge.Properties)
		}
	}
	if !found {
		t.Fatal("expected llm-inferred relation edge")
	}
}

func TestApplyInferenceRulesPreservesReservedProvenanceFields(t *testing.T) {
	dbPath := fmt.Sprintf("test_inference_reserved_fields_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{
		SchemaID: "reserved-provenance-schema",
		Activate: true,
		EntityTypes: []OntologyEntityType{
			{Name: "entity"},
			{Name: "person"},
			{Name: "organization"},
			{Name: "city"},
		},
		RelationTypes: []OntologyRelationType{
			{Name: "works_at", AllowedFromTypes: []string{"person"}, AllowedToTypes: []string{"organization"}},
			{Name: "located_in", AllowedFromTypes: []string{"organization"}, AllowedToTypes: []string{"city"}},
			{Name: "works_in_city", AllowedFromTypes: []string{"person"}, AllowedToTypes: []string{"city"}},
		},
	}); err != nil {
		t.Fatalf("save ontology schema: %v", err)
	}

	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "knowledge-reserved-provenance",
		Content:     "Alice works at Acme. Acme is located in Berlin.",
		ChunkSize:   24,
		Entities: []ToolEntityInput{
			{Name: "Alice", Type: "person", ChunkIDs: []string{"chunk:knowledge-reserved-provenance:000"}},
			{Name: "Acme", Type: "organization", ChunkIDs: []string{"chunk:knowledge-reserved-provenance:000"}},
			{Name: "Berlin", Type: "city", ChunkIDs: []string{"chunk:knowledge-reserved-provenance:000"}},
		},
		Relations: []ToolRelationInput{
			{From: "Alice", To: "Acme", Type: "works_at"},
			{From: "Acme", To: "Berlin", Type: "located_in"},
		},
	}); err != nil {
		t.Fatalf("save knowledge: %v", err)
	}

	resp, err := db.ApplyInferenceRules(ctx, ApplyInferenceRequest{
		DocumentID:     "knowledge-reserved-provenance",
		DeleteExisting: true,
		Rules: []InferenceRule{
			{
				RuleID:             "employment_city",
				LeftRelationType:   "works_at",
				RightRelationType:  "located_in",
				ResultRelationType: "works_in_city",
				Metadata: map[string]string{
					"inferred":   "false",
					"provenance": "user",
					"rule_id":    "override",
					"custom":     "kept",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("apply inference rules: %v", err)
	}
	if len(resp.CreatedEdgeIDs) != 1 {
		t.Fatalf("expected 1 inferred edge, got %d", len(resp.CreatedEdgeIDs))
	}

	edges, err := db.Graph().GetEdges(ctx, graphEntityNodeID("Alice"), "out")
	if err != nil {
		t.Fatalf("get inferred edges: %v", err)
	}
	for _, edge := range edges {
		if edge.ID != resp.CreatedEdgeIDs[0] {
			continue
		}
		if inferred, ok := edge.Properties["inferred"].(bool); !ok || !inferred {
			t.Fatalf("expected inferred=true to be preserved, got %+v", edge.Properties)
		}
		if provenance, ok := edge.Properties["provenance"].(string); !ok || provenance != "rule" {
			t.Fatalf("expected provenance=rule to be preserved, got %+v", edge.Properties)
		}
		if ruleID, ok := edge.Properties["rule_id"].(string); !ok || ruleID != "employment_city" {
			t.Fatalf("expected rule_id=employment_city to be preserved, got %+v", edge.Properties)
		}
		if custom, ok := edge.Properties["custom"].(string); !ok || custom != "kept" {
			t.Fatalf("expected custom metadata to survive, got %+v", edge.Properties)
		}
		return
	}
	t.Fatalf("expected inferred edge %s to be present", resp.CreatedEdgeIDs[0])
}
