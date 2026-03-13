package cortexdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

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
