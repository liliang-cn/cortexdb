package cortexdb

import (
	"context"
	"strings"
	"testing"
)

func BenchmarkEvaluationGraphRAGSearchModes(b *testing.B) {
	ctx := context.Background()
	fixture := newVectorEvaluationFixture(b)
	defer fixture.cleanup(b)

	query := "Where does Alice work?"

	b.Run("lexical", func(b *testing.B) {
		opts := GraphRAGQueryOptions{
			TopK:             1,
			MaxHops:          2,
			MaxRelatedChunks: 2,
			MaxContextChunks: 4,
			RetrievalMode:    RetrievalModeLexical,
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := fixture.db.SearchGraphRAG(ctx, query, opts); err != nil {
				b.Fatalf("search graphrag lexical: %v", err)
			}
		}
	})

	b.Run("graph_light", func(b *testing.B) {
		opts := GraphRAGQueryOptions{
			TopK:                1,
			MaxHops:             2,
			MaxRelatedChunks:    2,
			MaxContextChunks:    4,
			RetrievalMode:       RetrievalModeGraph,
			GraphLight:          true,
			MaxExpansionSeeds:   1,
			MaxTraversalNodes:   4,
			MaxEntitiesPerChunk: 1,
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := fixture.db.SearchGraphRAG(ctx, query, opts); err != nil {
				b.Fatalf("search graphrag graph_light: %v", err)
			}
		}
	})

	b.Run("graph", func(b *testing.B) {
		opts := GraphRAGQueryOptions{
			TopK:             1,
			MaxHops:          2,
			MaxRelatedChunks: 2,
			MaxContextChunks: 4,
			RetrievalMode:    RetrievalModeGraph,
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := fixture.db.SearchGraphRAG(ctx, query, opts); err != nil {
				b.Fatalf("search graphrag graph: %v", err)
			}
		}
	})
}

func BenchmarkEvaluationBuildContext(b *testing.B) {
	ctx := context.Background()
	fixture := newLexicalEvaluationFixture(b)
	defer fixture.cleanup(b)

	longContent := strings.Repeat("Alice works at Acme research in Berlin. ", 24)
	ingestResp, err := fixture.tools.IngestDocument(ctx, ToolIngestDocumentRequest{
		DocumentID: "packing-benchmark",
		Title:      "Packing Benchmark",
		Content:    longContent,
		ChunkSize:  8,
	})
	if err != nil {
		b.Fatalf("ingest packing benchmark document: %v", err)
	}

	_, err = fixture.tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		DocumentID: "packing-benchmark",
		Entities: []ToolEntityInput{
			{Name: "Alice", Type: "person", ChunkIDs: ingestResp.ChunkNodeIDs},
			{Name: "Acme", Type: "organization", ChunkIDs: ingestResp.ChunkNodeIDs},
			{Name: "Berlin", Type: "city", ChunkIDs: ingestResp.ChunkNodeIDs},
		},
	})
	if err != nil {
		b.Fatalf("upsert packing benchmark entities: %v", err)
	}

	req := ToolBuildContextRequest{
		ChunkIDs:            ingestResp.ChunkNodeIDs,
		MaxContextChunks:    6,
		MaxContextChars:     600,
		PerDocumentLimit:    3,
		RetrievalMode:       RetrievalModeGraph,
		MaxEntitiesPerChunk: 2,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := fixture.tools.BuildContext(ctx, req); err != nil {
			b.Fatalf("build context: %v", err)
		}
	}
}
