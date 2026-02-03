package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
	"github.com/liliang-cn/sqvect/v2/pkg/sqvect"
)

func main() {
	dbPath := "chat_memory.db"
	_ = os.Remove(dbPath)

	config := sqvect.DefaultConfig(dbPath)
	config.Dimensions = 4
	db, err := sqvect.Open(config)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		db.Close()
		os.Remove(dbPath)
	}()

	ctx := context.Background()
	store := db.Vector()

	// 1. Start a Chat Session
	sessionID := "session_user_alice_001"
	fmt.Printf("Creating session: %s\n", sessionID)
	err = store.CreateSession(ctx, &core.Session{
		ID:     sessionID,
		UserID: "alice",
		Metadata: map[string]interface{}{
			"topic": "tech_support",
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	// 2. Simulate Conversation
	// User asks a question
	userMsg := &core.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		SessionID: sessionID,
		Role:      "user",
		Content:   "My screen is flickering.",
		Vector:    []float32{0.9, 0.1, 0.0, 0.0}, // Simulated semantic vector
	}
	store.AddMessage(ctx, userMsg)
	fmt.Printf("User: %s\n", userMsg.Content)

	// Assistant replies
	botMsg := &core.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()+1),
		SessionID: sessionID,
		Role:      "assistant",
		Content:   "Have you tried restarting the monitor?",
	}
	store.AddMessage(ctx, botMsg)
	fmt.Printf("Bot: %s\n", botMsg.Content)

	// ... some time passes ...
	time.Sleep(100 * time.Millisecond)

	// User replies
	userMsg2 := &core.Message{
		ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()+2),
		SessionID: sessionID,
		Role:      "user",
		Content:   "Yes, I did. Still flickering.",
		Vector:    []float32{0.95, 0.05, 0.0, 0.0},
	}
	store.AddMessage(ctx, userMsg2)
	fmt.Printf("User: %s\n", userMsg2.Content)

	// 3. Retrieve Context for LLM
	// When sending the next prompt to LLM, we need the last N messages
	fmt.Println("\n--- Retrieving Context Window (Last 5 messages) ---")
	history, err := store.GetSessionHistory(ctx, sessionID, 5)
	if err != nil {
		log.Fatal(err)
	}

	for _, msg := range history {
		fmt.Printf("[%s]: %s\n", msg.Role, msg.Content)
	}

	// 4. Semantic Recall (Long-term Memory)
	// Suppose user asks about "screen issues" in a NEW session days later.
	// We can search across previous messages if we indexed them globally,
	// or search within this session to find relevant past details.
	
	fmt.Println("\n--- Semantic Search in Session (Recall) ---")
	query := "monitor display issue"
	queryVec := []float32{0.9, 0.1, 0.0, 0.0} // Similar to first message
	
	fmt.Printf("Searching memory for: '%s'\n", query)
	found, _ := store.SearchChatHistory(ctx, queryVec, sessionID, 2)
	
	for _, msg := range found {
		fmt.Printf("Recall Found: [%s] %s\n", msg.Role, msg.Content)
	}
}
