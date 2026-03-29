package cortexdb

import (
	"context"
	"strings"
	"testing"
)

func TestEvaluationRetrievalModes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	lexicalFixture := newLexicalEvaluationFixture(t)
	defer lexicalFixture.cleanup(t)

	lexicalResult, err := lexicalFixture.tools.SearchGraphRAGLexical(ctx, ToolSearchGraphRAGLexicalRequest{
		Query:            "Where does Alice work?",
		TopK:             1,
		MaxHops:          2,
		MaxRelatedChunks: 2,
		MaxContextChunks: 4,
		RetrievalMode:    RetrievalModeLexical,
	})
	if err != nil {
		t.Fatalf("lexical graphrag search: %v", err)
	}
	if lexicalResult.Decision.UseGraph {
		t.Fatalf("expected lexical mode to skip graph expansion, got %+v", lexicalResult.Decision)
	}
	if len(lexicalResult.Chunks) != 1 {
		t.Fatalf("expected lexical mode to keep one seed chunk, got %d", len(lexicalResult.Chunks))
	}
	if lexicalResult.Chunks[0].DocumentID != "employment" {
		t.Fatalf("expected lexical mode to return the employment chunk first, got %+v", lexicalResult.Chunks[0])
	}
	if strings.Contains(strings.ToLower(lexicalResult.Context), "berlin") {
		t.Fatalf("expected lexical mode context to exclude employer profile, got %q", lexicalResult.Context)
	}

	vectorFixture := newVectorEvaluationFixture(t)
	defer vectorFixture.cleanup(t)

	vectorResult, err := vectorFixture.db.SearchGraphRAG(ctx, "Where does Alice work?", GraphRAGQueryOptions{
		TopK:             1,
		MaxHops:          2,
		MaxRelatedChunks: 2,
		MaxContextChunks: 4,
		RetrievalMode:    RetrievalModeLexical,
	})
	if err != nil {
		t.Fatalf("vector graphrag lexical search: %v", err)
	}
	if vectorResult.Decision.UseGraph {
		t.Fatalf("expected vector lexical mode to skip graph expansion, got %+v", vectorResult.Decision)
	}
	if len(vectorResult.Chunks) != 1 {
		t.Fatalf("expected vector lexical mode to keep one seed chunk, got %d", len(vectorResult.Chunks))
	}
	if vectorResult.Chunks[0].DocumentID != "employment" {
		t.Fatalf("expected vector lexical mode to return employment chunk first, got %+v", vectorResult.Chunks[0])
	}
	if strings.Contains(strings.ToLower(vectorResult.Context), "berlin") {
		t.Fatalf("expected vector lexical mode context to exclude employer profile, got %q", vectorResult.Context)
	}

	graphResult, err := vectorFixture.db.SearchGraphRAG(ctx, "Where does Alice work?", GraphRAGQueryOptions{
		TopK:             1,
		MaxHops:          2,
		MaxRelatedChunks: 2,
		MaxContextChunks: 4,
		RetrievalMode:    RetrievalModeGraph,
	})
	if err != nil {
		t.Fatalf("vector graphrag graph search: %v", err)
	}
	if !graphResult.Decision.UseGraph || graphResult.Decision.EffectiveMode != RetrievalModeGraph {
		t.Fatalf("expected graph mode decision metadata, got %+v", graphResult.Decision)
	}
	if len(graphResult.Chunks) <= len(vectorResult.Chunks) {
		t.Fatalf("expected graph mode to recover related chunks, got graph=%d vector=%d", len(graphResult.Chunks), len(vectorResult.Chunks))
	}
	if !strings.Contains(strings.ToLower(graphResult.Context), "berlin") {
		t.Fatalf("expected graph mode context to recover employer profile, got %q", graphResult.Context)
	}
}

func TestEvaluationSchemaValidationAndInference(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fixture := newLexicalEvaluationFixture(t)
	defer fixture.cleanup(t)

	_, err := fixture.db.SaveOntologySchema(ctx, OntologySaveRequest{
		SchemaID: "evaluation-schema",
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
	})
	if err != nil {
		t.Fatalf("save ontology schema: %v", err)
	}

	_, err = fixture.db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "knowledge-evaluation",
		Title:       "Alice at Acme Berlin",
		Content:     "Alice works at Acme. Acme is located in Berlin.",
		ChunkSize:   24,
		Entities: []ToolEntityInput{
			{Name: "Alice", Type: "person", ChunkIDs: []string{"chunk:knowledge-evaluation:000"}},
			{Name: "Acme", Type: "organization", ChunkIDs: []string{"chunk:knowledge-evaluation:000"}},
			{Name: "Berlin", Type: "city", ChunkIDs: []string{"chunk:knowledge-evaluation:000"}},
		},
		Relations: []ToolRelationInput{
			{From: "Alice", To: "Acme", Type: "works_at"},
			{From: "Acme", To: "Berlin", Type: "located_in"},
		},
	})
	if err != nil {
		t.Fatalf("save knowledge fixture: %v", err)
	}

	_, err = fixture.tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{
		DocumentID: "knowledge-evaluation",
		Relations: []ToolRelationInput{
			{From: "Berlin", To: "Alice", Type: "works_at"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "does not allow source entity type city") {
		t.Fatalf("expected ontology validation error for invalid type pair, got %v", err)
	}

	inferenceResp, err := fixture.db.ApplyInferenceRules(ctx, ApplyInferenceRequest{
		DocumentID:     "knowledge-evaluation",
		DeleteExisting: true,
		Rules: []InferenceRule{
			{
				RuleID:             "evaluation-employment-city",
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
		t.Fatalf("expected one inferred edge, got %d", len(inferenceResp.CreatedEdgeIDs))
	}

	edges, err := fixture.db.Graph().GetEdges(ctx, graphEntityNodeID("Alice"), "out")
	if err != nil {
		t.Fatalf("get alice edges: %v", err)
	}

	found := false
	for _, edge := range edges {
		if edge.EdgeType != "works_in_city" {
			continue
		}
		found = true
		if inferred, ok := edge.Properties["inferred"].(bool); !ok || !inferred {
			t.Fatalf("expected inferred flag on evaluation edge, got %+v", edge.Properties)
		}
		if provenance, ok := edge.Properties["provenance"].(string); !ok || provenance != "rule" {
			t.Fatalf("expected rule provenance on evaluation edge, got %+v", edge.Properties)
		}
	}
	if !found {
		t.Fatal("expected inferred works_in_city edge in evaluation fixture")
	}
}
