package cortexdb

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkRAGSaveKnowledge(b *testing.B) {
	ctx := context.Background()
	db := newBenchmarkCortexDB(b, nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("bench-knowledge-%d", i)
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
			KnowledgeID: id,
			Title:       "Benchmark knowledge",
			Content:     "Alice owns Apollo. Apollo ships on Friday. Bob writes release notes.",
			ChunkSize:   24,
			Entities: []ToolEntityInput{
				{Name: "Alice", Type: "person", ChunkIDs: []string{fmt.Sprintf("chunk:%s:000", id)}},
				{Name: "Apollo", Type: "project", ChunkIDs: []string{fmt.Sprintf("chunk:%s:000", id)}},
				{Name: "Bob", Type: "person", ChunkIDs: []string{fmt.Sprintf("chunk:%s:000", id)}},
			},
			Relations: []ToolRelationInput{
				{From: "Alice", To: "Apollo", Type: "owns"},
				{From: "Bob", To: "Apollo", Type: "documents"},
			},
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRAGSearchKnowledgeLexical(b *testing.B) {
	ctx := context.Background()
	db := newBenchmarkCortexDB(b, nil)
	seedBenchmarkKnowledge(b, ctx, db, 500)

	req := KnowledgeSearchRequest{
		Query:         "Who owns Apollo and when does it ship?",
		Keywords:      []string{"Apollo", "Alice", "Friday", "launch"},
		EntityNames:   []string{"Apollo", "Alice"},
		RetrievalMode: RetrievalModeLexical,
		TopK:          5,
		GraphLight:    true,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.SearchKnowledge(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRAGSearchKnowledgeGraphLight(b *testing.B) {
	ctx := context.Background()
	db := newBenchmarkCortexDB(b, nil)
	seedBenchmarkKnowledge(b, ctx, db, 500)

	req := KnowledgeSearchRequest{
		Query:               "Who owns Apollo and what related context matters?",
		Keywords:            []string{"Apollo", "Alice", "Friday", "release"},
		EntityNames:         []string{"Apollo", "Alice"},
		RetrievalMode:       RetrievalModeGraph,
		TopK:                5,
		MaxHops:             2,
		MaxRelatedChunks:    4,
		MaxContextChunks:    6,
		GraphLight:          true,
		MaxExpansionSeeds:   2,
		MaxTraversalNodes:   8,
		MaxEntitiesPerChunk: 3,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.SearchKnowledge(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRAGBuildContext(b *testing.B) {
	ctx := context.Background()
	db := newBenchmarkCortexDB(b, nil)
	tools := db.GraphRAGTools()
	longContent := strings.Repeat("Alice owns Apollo. Apollo ships on Friday. Bob writes release notes. ", 48)
	ingest, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{
		DocumentID: "context-pack",
		Title:      "Context Pack",
		Content:    longContent,
		ChunkSize:  12,
	})
	if err != nil {
		b.Fatal(err)
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		DocumentID: "context-pack",
		Entities: []ToolEntityInput{
			{Name: "Alice", Type: "person", ChunkIDs: ingest.ChunkNodeIDs},
			{Name: "Apollo", Type: "project", ChunkIDs: ingest.ChunkNodeIDs},
			{Name: "Bob", Type: "person", ChunkIDs: ingest.ChunkNodeIDs},
		},
	}); err != nil {
		b.Fatal(err)
	}

	req := ToolBuildContextRequest{
		ChunkIDs:            ingest.ChunkNodeIDs,
		MaxContextChunks:    8,
		MaxContextChars:     1200,
		PerDocumentLimit:    4,
		RetrievalMode:       RetrievalModeGraph,
		GraphLight:          true,
		MaxEntitiesPerChunk: 3,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := tools.BuildContext(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

func newBenchmarkCortexDB(b *testing.B, opts ...Option) *DB {
	b.Helper()
	cleanOpts := make([]Option, 0, len(opts))
	for _, opt := range opts {
		if opt != nil {
			cleanOpts = append(cleanOpts, opt)
		}
	}
	db, err := Open(DefaultConfig(filepath.Join(b.TempDir(), "rag-bench.db")), cleanOpts...)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })
	return db
}

func seedBenchmarkKnowledge(b *testing.B, ctx context.Context, db *DB, count int) {
	b.Helper()
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("apollo-%d", i)
		chunkID := fmt.Sprintf("chunk:%s:000", id)
		if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
			KnowledgeID: id,
			Title:       fmt.Sprintf("Apollo note %d", i),
			Content:     fmt.Sprintf("Alice owns Apollo project %d. Apollo ships on Friday. Bob writes release notes.", i),
			ChunkSize:   24,
			Entities: []ToolEntityInput{
				{Name: "Alice", Type: "person", ChunkIDs: []string{chunkID}},
				{Name: "Apollo", Type: "project", ChunkIDs: []string{chunkID}},
				{Name: "Bob", Type: "person", ChunkIDs: []string{chunkID}},
			},
			Relations: []ToolRelationInput{
				{From: "Alice", To: "Apollo", Type: "owns"},
				{From: "Bob", To: "Apollo", Type: "documents"},
			},
		}); err != nil {
			b.Fatal(err)
		}
	}
}
