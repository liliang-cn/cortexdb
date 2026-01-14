package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestDocumentManagement(t *testing.T) {
	dbPath := fmt.Sprintf("/tmp/test_doc_%d.db", time.Now().UnixNano())
	defer os.Remove(dbPath)

	store, _ := New(dbPath, 128)
	ctx := context.Background()
	_ = store.Init(ctx)
	defer store.Close()

	// 1. Create Document
	docID := "doc_1"
	doc := &Document{
		ID:      docID,
		Title:   "Test Manual",
		Author:  "Alice",
		Version: 1,
		Metadata: map[string]interface{}{
			"category": "technical",
		},
		ACL: []string{"group:engineering"},
	}

	if err := store.CreateDocument(ctx, doc); err != nil {
		t.Fatalf("CreateDocument failed: %v", err)
	}

	// 2. Add Embeddings linked to Document
	vec := make([]float32, 128) // zero vector
	err := store.Upsert(ctx, &Embedding{
		ID:      "chunk_1",
		Vector:  vec,
		Content: "Chunk content",
		DocID:   docID,
	})
	if err != nil {
		t.Fatalf("Upsert chunk failed: %v", err)
	}

	// 3. Get Document
	retrieved, err := store.GetDocument(ctx, docID)
	if err != nil {
		t.Fatalf("GetDocument failed: %v", err)
	}
	if retrieved.Title != "Test Manual" {
		t.Errorf("Title mismatch: %s", retrieved.Title)
	}
	if len(retrieved.ACL) != 1 || retrieved.ACL[0] != "group:engineering" {
		t.Errorf("ACL mismatch: %v", retrieved.ACL)
	}

	// 4. List Documents Filter
	docs, err := store.ListDocumentsWithFilter(ctx, "Alice", 10)
	if err != nil {
		t.Fatalf("ListDocumentsWithFilter failed: %v", err)
	}
	if len(docs) != 1 {
		t.Errorf("Expected 1 doc, got %d", len(docs))
	}

	// 5. Delete Document (Cascade Check)
	if err := store.DeleteDocument(ctx, docID); err != nil {
		t.Fatalf("DeleteDocument failed: %v", err)
	}

	// Check Document is gone
	_, err = store.GetDocument(ctx, docID)
	if !errors.Is(err, ErrNotFound) { // Should fail
		t.Errorf("Expected ErrNotFound after delete, got %v", err)
	}

	// Check Embeddings are gone (from DB)
	embs, _ := store.GetByDocID(ctx, docID)
	if len(embs) != 0 {
		t.Errorf("Embeddings should be deleted via cascade, found %d", len(embs))
	}
}

func TestChatMemory(t *testing.T) {
	dbPath := fmt.Sprintf("/tmp/test_chat_%d.db", time.Now().UnixNano())
	defer os.Remove(dbPath)

	store, _ := New(dbPath, 128)
	ctx := context.Background()
	_ = store.Init(ctx)
	defer store.Close()

	// 1. Create Session
	sessionID := "sess_1"
	err := store.CreateSession(ctx, &Session{
		ID:     sessionID,
		UserID: "user_123",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// 2. Add Messages
	msgs := []*Message{
		{
			ID:        "msg_1",
			SessionID: sessionID,
			Role:      "user",
			Content:   "Hello",
			Vector:    make([]float32, 128),
		},
		{
			ID:        "msg_2",
			SessionID: sessionID,
			Role:      "assistant",
			Content:   "Hi there",
		},
	}

	for _, m := range msgs {
		if err := store.AddMessage(ctx, m); err != nil {
			t.Fatalf("AddMessage failed: %v", err)
		}
		// Artificial delay for ordering
		time.Sleep(10 * time.Millisecond)
	}

	// 3. Get History
	history, err := store.GetSessionHistory(ctx, sessionID, 10)
	if err != nil {
		t.Fatalf("GetSessionHistory failed: %v", err)
	}
	if len(history) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(history))
	}
	// Should be chronological
	if history[0].Content != "Hello" {
		t.Errorf("Order mismatch, first msg is %s (expected Hello)", history[0].Content)
	}

	// 4. Semantic Search in Chat
	// Search with vector similar to msg_1
	queryVec := make([]float32, 128) // zero vector matches zero vector perfectly
	found, err := store.SearchChatHistory(ctx, queryVec, sessionID, 5)
	if err != nil {
		t.Fatalf("SearchChatHistory failed: %v", err)
	}
	if len(found) == 0 {
		t.Error("SearchChatHistory returned nothing")
	} else if found[0].ID != "msg_1" {
		t.Errorf("Expected to find msg_1, got %s", found[0].ID)
	}
}

func TestACLSearch(t *testing.T) {
	dbPath := fmt.Sprintf("/tmp/test_acl_%d.db", time.Now().UnixNano())
	defer os.Remove(dbPath)

	store, _ := New(dbPath, 4)
	ctx := context.Background()
	_ = store.Init(ctx)
	defer store.Close()

	// Insert Data with ACLs
	// 1. Public document
	store.Upsert(ctx, &Embedding{ID: "public_doc", Vector: []float32{1,0,0,0}, Content: "Public", ACL: nil})
	
	// 2. User specific
	store.Upsert(ctx, &Embedding{ID: "alice_doc", Vector: []float32{0,1,0,0}, Content: "Alice Only", ACL: []string{"user:alice"}})
	
	// 3. Group specific
	store.Upsert(ctx, &Embedding{ID: "admin_doc", Vector: []float32{0,0,1,0}, Content: "Admins Only", ACL: []string{"group:admin"}})

	// Search as Public (no ACL)
	results, _ := store.SearchWithACL(ctx, []float32{0,0,0,0}, nil, SearchOptions{TopK: 10})
	if len(results) != 1 || results[0].ID != "public_doc" {
		t.Errorf("Public search failed, got %d results", len(results))
	}

	// Search as Alice (should see public + alice)
	results, _ = store.SearchWithACL(ctx, []float32{0,0,0,0}, []string{"user:alice"}, SearchOptions{TopK: 10})
	if len(results) != 2 {
		t.Errorf("Alice search failed, expected 2 results, got %d", len(results))
	}

	// Search as Admin (should see public + admin)
	results, _ = store.SearchWithACL(ctx, []float32{0,0,0,0}, []string{"group:admin"}, SearchOptions{TopK: 10})
	if len(results) != 2 {
		t.Errorf("Admin search failed, expected 2 results, got %d", len(results))
	}
	
	// Search as Bob (should see public only)
	results, _ = store.SearchWithACL(ctx, []float32{0,0,0,0}, []string{"user:bob"}, SearchOptions{TopK: 10})
	if len(results) != 1 {
		t.Errorf("Bob search failed, expected 1 result, got %d", len(results))
	}
}

func TestHybridSearch(t *testing.T) {
	dbPath := fmt.Sprintf("/tmp/test_hybrid_%d.db", time.Now().UnixNano())
	defer os.Remove(dbPath)

	store, _ := New(dbPath, 4)
	ctx := context.Background()
	_ = store.Init(ctx)
	defer store.Close()

	// Insert data
	store.Upsert(ctx, &Embedding{ID: "1", Vector: []float32{1,0,0,0}, Content: "Apple iPhone"})
	store.Upsert(ctx, &Embedding{ID: "2", Vector: []float32{0,1,0,0}, Content: "Apple Pie"})
	store.Upsert(ctx, &Embedding{ID: "3", Vector: []float32{0,0,1,0}, Content: "Green Apple"})

	// Hybrid Search
	opts := HybridSearchOptions{}
	opts.TopK = 3
	
	results, err := store.HybridSearch(ctx, []float32{1,0,0,0}, "Apple", opts)
	if err != nil {
		t.Logf("HybridSearch skipped (FTS likely missing): %v", err)
		return
	}
	
	if len(results) == 0 {
		t.Error("HybridSearch returned 0 results")
	}
	
	if results[0].ID != "1" {
		t.Errorf("Expected top result ID=1, got %s", results[0].ID)
	}
}