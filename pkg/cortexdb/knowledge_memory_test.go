package cortexdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

type failingTestEmbedder struct {
	base    *MockEmbedder
	trigger string
}

func (e *failingTestEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if strings.Contains(strings.ToLower(text), e.trigger) {
		return nil, fmt.Errorf("forced embedder failure")
	}
	return e.base.Embed(ctx, text)
}

func (e *failingTestEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	for _, text := range texts {
		if strings.Contains(strings.ToLower(text), e.trigger) {
			return nil, fmt.Errorf("forced embedder failure")
		}
	}
	return e.base.EmbedBatch(ctx, texts)
}

func (e *failingTestEmbedder) Dim() int {
	return e.base.Dim()
}

func TestKnowledgeDBAPIWithoutEmbedder(t *testing.T) {
	dbPath := fmt.Sprintf("test_knowledge_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	saveResp, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "knowledge-1",
		Title:       "Alice at Acme",
		Content:     "Alice works at Acme on retrieval systems.",
		ChunkSize:   64,
		Metadata: map[string]string{
			"category": "people",
		},
		Entities: []ToolEntityInput{
			{Name: "Alice", ChunkIDs: []string{"chunk:knowledge-1:000"}},
			{Name: "Acme", ChunkIDs: []string{"chunk:knowledge-1:000"}},
		},
		Relations: []ToolRelationInput{
			{From: "Alice", To: "Acme", Type: "works_at", ChunkIDs: []string{"chunk:knowledge-1:000"}},
		},
	})
	if err != nil {
		t.Fatalf("save knowledge: %v", err)
	}
	if saveResp.DocumentNodeID == "" {
		t.Fatal("expected document node ID")
	}
	if len(saveResp.Knowledge.ChunkIDs) == 0 {
		t.Fatal("expected chunk IDs after save")
	}

	getResp, err := db.GetKnowledge(ctx, KnowledgeGetRequest{KnowledgeID: "knowledge-1"})
	if err != nil {
		t.Fatalf("get knowledge: %v", err)
	}
	if getResp.Knowledge.Title != "Alice at Acme" {
		t.Fatalf("unexpected knowledge title: %s", getResp.Knowledge.Title)
	}
	if len(getResp.Knowledge.Entities) == 0 {
		t.Fatal("expected extracted entities")
	}

	searchResp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{
		Query:            "Where does Alice work?",
		TopK:             2,
		MaxHops:          2,
		MaxRelatedChunks: 2,
		MaxContextChunks: 2,
		MaxContextChars:  240,
		PerDocumentLimit: 1,
		EntityNames:      []string{"Alice", "Acme"},
		Keywords:         []string{"Alice", "Acme", "employer", "works"},
		AlternateQueries: []string{"Alice works at Acme"},
	})
	if err != nil {
		t.Fatalf("search knowledge: %v", err)
	}
	if len(searchResp.Results) == 0 {
		t.Fatal("expected grouped knowledge search results")
	}
	if searchResp.Context == "" {
		t.Fatal("expected knowledge search context")
	}
	if searchResp.Decision.EffectiveMode == "" || searchResp.Plan.Query == "" {
		t.Fatalf("expected knowledge search planning metadata, got %+v", searchResp)
	}

	newTitle := "Alice leads Acme"
	newContent := "Alice leads Acme's knowledge graph team."
	updateResp, err := db.UpdateKnowledge(ctx, KnowledgeUpdateRequest{
		KnowledgeID: "knowledge-1",
		Title:       &newTitle,
		Content:     &newContent,
		Metadata: map[string]string{
			"category": "leadership",
		},
	})
	if err != nil {
		t.Fatalf("update knowledge: %v", err)
	}
	if updateResp.Knowledge.Title != newTitle {
		t.Fatalf("unexpected updated title: %s", updateResp.Knowledge.Title)
	}
	if updateResp.Knowledge.Content != newContent {
		t.Fatalf("unexpected updated content: %s", updateResp.Knowledge.Content)
	}

	oldSearchResp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{
		Query:         "retrieval systems",
		TopK:          2,
		RetrievalMode: RetrievalModeLexical,
	})
	if err != nil {
		t.Fatalf("search old knowledge content: %v", err)
	}
	if len(oldSearchResp.Results) != 0 {
		t.Fatalf("expected old chunks to be removed, got %+v", oldSearchResp.Results)
	}

	if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "knowledge-1"}); err != nil {
		t.Fatalf("delete knowledge: %v", err)
	}
	if _, err := db.GetKnowledge(ctx, KnowledgeGetRequest{KnowledgeID: "knowledge-1"}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestDeleteKnowledgeCleansArtifacts(t *testing.T) {
	dbPath := fmt.Sprintf("test_knowledge_cleanup_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	saveResp, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "knowledge-cleanup",
		Title:       "Alice at Acme",
		Content:     "Alice works at Acme on retrieval systems.",
		ChunkSize:   32,
		Entities: []ToolEntityInput{
			{Name: "Alice", ChunkIDs: []string{"chunk:knowledge-cleanup:000"}},
			{Name: "Acme", ChunkIDs: []string{"chunk:knowledge-cleanup:000"}},
		},
		Relations: []ToolRelationInput{
			{From: "Alice", To: "Acme", Type: "works_at", ChunkIDs: []string{"chunk:knowledge-cleanup:000"}},
		},
	})
	if err != nil {
		t.Fatalf("save knowledge: %v", err)
	}
	if len(saveResp.Knowledge.ChunkIDs) == 0 {
		t.Fatal("expected chunk IDs after save")
	}

	if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "knowledge-cleanup"}); err != nil {
		t.Fatalf("delete knowledge: %v", err)
	}

	for _, chunkID := range saveResp.Knowledge.ChunkIDs {
		if _, err := db.store.GetByID(ctx, chunkID); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("expected chunk %s to be deleted, got %v", chunkID, err)
		}
		if _, err := db.Graph().GetNode(ctx, chunkID); err == nil {
			t.Fatalf("expected chunk node %s to be deleted", chunkID)
		}
	}
	if _, err := db.Graph().GetNode(ctx, saveResp.DocumentNodeID); err == nil {
		t.Fatalf("expected document node %s to be deleted", saveResp.DocumentNodeID)
	}
	for _, entityNodeID := range saveResp.EntityNodeIDs {
		if _, err := db.Graph().GetNode(ctx, entityNodeID); err == nil {
			t.Fatalf("expected entity node %s to be deleted", entityNodeID)
		}
	}

	chunks, err := db.store.GetByDocID(ctx, "knowledge-cleanup")
	if err != nil {
		t.Fatalf("get chunks after delete: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected no chunks after delete, got %d", len(chunks))
	}
	edgeIDs, err := db.graphEdgeIDsByDocument(ctx, "knowledge-cleanup")
	if err != nil {
		t.Fatalf("get graph edges after delete: %v", err)
	}
	if len(edgeIDs) != 0 {
		t.Fatalf("expected no graph edges after delete, got %v", edgeIDs)
	}
}

func TestUpdateKnowledgePreservesExistingArtifactsOnPlanFailure(t *testing.T) {
	dbPath := fmt.Sprintf("test_knowledge_atomic_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath), WithEmbedder(&failingTestEmbedder{
		base:    NewMockEmbedder(8),
		trigger: "broken",
	}))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	saveResp, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: "knowledge-atomic",
		Title:       "Alice at Acme",
		Content:     "Alice works at Acme on retrieval systems.",
		ChunkSize:   32,
	})
	if err != nil {
		t.Fatalf("save knowledge: %v", err)
	}

	newContent := "Broken update content that should fail embedding."
	_, err = db.UpdateKnowledge(ctx, KnowledgeUpdateRequest{
		KnowledgeID: "knowledge-atomic",
		Content:     &newContent,
	})
	if err == nil {
		t.Fatal("expected update to fail")
	}

	getResp, err := db.GetKnowledge(ctx, KnowledgeGetRequest{KnowledgeID: "knowledge-atomic"})
	if err != nil {
		t.Fatalf("get knowledge after failed update: %v", err)
	}
	if getResp.Knowledge.Content != "Alice works at Acme on retrieval systems." {
		t.Fatalf("expected original content to remain after failed update, got %q", getResp.Knowledge.Content)
	}

	if _, err := db.Graph().GetNode(ctx, saveResp.DocumentNodeID); err != nil {
		t.Fatalf("expected original document node to remain after failed update: %v", err)
	}

	oldSearchResp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{
		Query:         "retrieval systems",
		TopK:          2,
		RetrievalMode: RetrievalModeLexical,
	})
	if err != nil {
		t.Fatalf("search original knowledge after failed update: %v", err)
	}
	if len(oldSearchResp.Results) == 0 {
		t.Fatal("expected original knowledge to remain searchable after failed update")
	}

	newSearchResp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{
		Query:         "broken update content",
		TopK:          2,
		RetrievalMode: RetrievalModeLexical,
	})
	if err != nil {
		t.Fatalf("search failed-update content: %v", err)
	}
	if len(newSearchResp.Results) != 0 {
		t.Fatalf("expected failed-update content to stay absent, got %+v", newSearchResp.Results)
	}
}

func TestMemoryDBAPIWithEmbedder(t *testing.T) {
	dbPath := fmt.Sprintf("test_memory_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := Open(DefaultConfig(dbPath), WithEmbedder(NewMockEmbedder(8)))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	saveResp, err := db.SaveMemory(ctx, MemorySaveRequest{
		MemoryID:   "memory-1",
		UserID:     "user-1",
		Scope:      MemoryScopeUser,
		Namespace:  "assistant",
		Content:    "Alice prefers concise answers.",
		Importance: 0.8,
	})
	if err != nil {
		t.Fatalf("save memory: %v", err)
	}
	if saveResp.Memory.SessionID == "" {
		t.Fatal("expected resolved memory bucket session ID")
	}
	if saveResp.Memory.Scope != MemoryScopeUser {
		t.Fatalf("unexpected memory scope: %s", saveResp.Memory.Scope)
	}

	getResp, err := db.GetMemory(ctx, MemoryGetRequest{MemoryID: "memory-1"})
	if err != nil {
		t.Fatalf("get memory: %v", err)
	}
	if getResp.Memory.Namespace != "assistant" {
		t.Fatalf("unexpected memory namespace: %s", getResp.Memory.Namespace)
	}

	searchResp, err := db.SearchMemory(ctx, MemorySearchRequest{
		Query:         "How should I answer Alice?",
		UserID:        "user-1",
		Scope:         MemoryScopeUser,
		Namespace:     "assistant",
		TopK:          3,
		RetrievalMode: RetrievalModeAuto,
	})
	if err != nil {
		t.Fatalf("search memory: %v", err)
	}
	if len(searchResp.Results) == 0 {
		t.Fatal("expected memory search results")
	}
	if searchResp.Decision.EffectiveMode != RetrievalModeAuto {
		t.Fatalf("expected auto memory decision, got %+v", searchResp.Decision)
	}
	if searchResp.Plan.Query == "" {
		t.Fatalf("expected memory search plan metadata, got %+v", searchResp)
	}

	newContent := "Alice prefers short factual answers."
	ttlSeconds := 3600
	updateResp, err := db.UpdateMemory(ctx, MemoryUpdateRequest{
		MemoryID:   "memory-1",
		Content:    &newContent,
		TTLSeconds: &ttlSeconds,
	})
	if err != nil {
		t.Fatalf("update memory: %v", err)
	}
	if updateResp.Memory.Content != newContent {
		t.Fatalf("unexpected updated memory content: %s", updateResp.Memory.Content)
	}
	if updateResp.Memory.ExpiresAt == nil {
		t.Fatal("expected ttl to produce an expiration time")
	}

	oldSearchResp, err := db.SearchMemory(ctx, MemorySearchRequest{
		Query:         "concise",
		UserID:        "user-1",
		Scope:         MemoryScopeUser,
		Namespace:     "assistant",
		TopK:          3,
		RetrievalMode: RetrievalModeLexical,
	})
	if err != nil {
		t.Fatalf("search old memory content: %v", err)
	}
	if len(oldSearchResp.Results) != 0 {
		t.Fatalf("expected old memory content to be removed, got %+v", oldSearchResp.Results)
	}

	newSearchResp, err := db.SearchMemory(ctx, MemorySearchRequest{
		Query:            "How should I answer Alice?",
		UserID:           "user-1",
		Scope:            MemoryScopeUser,
		Namespace:        "assistant",
		TopK:             3,
		Keywords:         []string{"Alice", "short", "factual", "answers"},
		AlternateQueries: []string{"Alice prefers factual answers"},
		RetrievalMode:    RetrievalModeLexical,
	})
	if err != nil {
		t.Fatalf("search new memory content: %v", err)
	}
	if len(newSearchResp.Results) == 0 {
		t.Fatal("expected updated memory to be searchable")
	}

	if _, err := db.DeleteMemory(ctx, MemoryDeleteRequest{MemoryID: "memory-1"}); err != nil {
		t.Fatalf("delete memory: %v", err)
	}
	if _, err := db.GetMemory(ctx, MemoryGetRequest{MemoryID: "memory-1"}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}
