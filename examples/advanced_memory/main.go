package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
	"github.com/liliang-cn/sqvect/v2/pkg/memory"
	"github.com/liliang-cn/sqvect/v2/pkg/sqvect"
)

func main() {
	dbPath := "advanced_memory.db"
	defer os.Remove(dbPath)

	// -----------------------------------------------------------------------
	// 1. Open database and initialise graph schema.
	// -----------------------------------------------------------------------
	config := sqvect.DefaultConfig(dbPath)
	config.Dimensions = 4
	db, err := sqvect.Open(config)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		log.Fatal(err)
	}

	mem := db.Memory()

	// -----------------------------------------------------------------------
	// 2. Configure a Memory Bank – analogous to Hindsight's bank settings.
	// -----------------------------------------------------------------------
	mem.SetBankConfig(memory.BankConfig{
		Mission: "I am a personalised travel assistant. " +
			"I remember user preferences across sessions to avoid repetitive questions.",
		Directives: []string{
			"Never recommend destinations with active travel warnings.",
			"Always suggest travel insurance for international trips.",
		},
		Disposition: map[string]float32{
			"empathy":       4.5,
			"proactiveness": 3.0,
		},
	})

	fmt.Println("=== sqvect × Hindsight-style Memory Demo ===")
	fmt.Println()

	userID := "user_alice"
	sessionID := "session_current"

	// -----------------------------------------------------------------------
	// 3. Retain Phase – store facts at different memory layers.
	// -----------------------------------------------------------------------
	fmt.Println("--- [RETAIN] Storing facts at different memory layers ---")

	// LayerMentalModel: high-level user-curated summary.
	_ = mem.Retain(ctx, memory.RetainInput{
		UserID:  userID,
		FactID:  "travel_style",
		Content: "Alice prefers budget backpacker trips with cultural immersion over luxury resorts.",
		Vector:  []float32{0.9, 0.1, 0.0, 0.0},
		Layer:   memory.LayerMentalModel,
	})

	// LayerWorldFact: objective user facts.
	_ = mem.Retain(ctx, memory.RetainInput{
		UserID:  userID,
		FactID:  "home_city",
		Content: "Alice is based in Berlin, Germany.",
		Vector:  []float32{0.8, 0.0, 0.2, 0.0},
		Layer:   memory.LayerWorldFact,
	})
	_ = mem.Retain(ctx, memory.RetainInput{
		UserID:  userID,
		FactID:  "next_trip",
		Content: "Alice is planning a trip to Southeast Asia in March.",
		Vector:  []float32{0.7, 0.3, 0.0, 0.0},
		Layer:   memory.LayerWorldFact,
	})

	// LayerExperience: record a past recommendation by the agent.
	_ = mem.Retain(ctx, memory.RetainInput{
		UserID:  userID,
		FactID:  "rec_001",
		Content: "Recommended Chiang Mai as a budget-friendly base for northern Thailand.",
		Vector:  []float32{0.6, 0.4, 0.0, 0.0},
		Layer:   memory.LayerExperience,
	})

	fmt.Println("  ✓ Mental model, world facts, and experience stored.")
	fmt.Println()

	// -----------------------------------------------------------------------
	// 4. Consolidate – synthesise facts into a LayerObservation (LLM hook).
	// -----------------------------------------------------------------------
	fmt.Println("--- [CONSOLIDATE] Synthesising facts into an Observation ---")

	mockLLM := memory.ConsolidateFn(func(_ context.Context, existing string, newFacts []string) (string, error) {
		// In production this would be an LLM call (OpenAI, Anthropic, Ollama…).
		// Here we concatenate for demonstration purposes.
		parts := []string{"[Observation]"}
		if existing != "" {
			parts = append(parts, "Prior: "+existing)
		}
		parts = append(parts, "New: "+strings.Join(newFacts, " | "))
		return strings.Join(parts, " — "), nil
	})

	_ = mem.Consolidate(ctx, userID,
		[]string{
			"Alice prefers budget travel",
			"Alice is planning Southeast Asia trip",
		},
		[]float32{0.75, 0.25, 0.0, 0.0},
		mockLLM,
	)
	fmt.Println("  ✓ Observation node created/updated via ConsolidateFn.")
	fmt.Println()

	// -----------------------------------------------------------------------
	// 5. Short-term memory – current session messages.
	// -----------------------------------------------------------------------
	fmt.Println("--- [SESSION] Adding current conversation messages ---")
	_ = db.Vector().CreateSession(ctx, &core.Session{ID: sessionID, UserID: userID})
	_ = db.Vector().AddMessage(ctx, &core.Message{
		ID:        "msg_1",
		SessionID: sessionID,
		Role:      "user",
		Content:   "Can you suggest a good base city for Cambodia?",
		Vector:    []float32{0.65, 0.35, 0.0, 0.0},
	})
	fmt.Println("  ✓ Session message added.")
	fmt.Println()

	// -----------------------------------------------------------------------
	// 6. Recall – TEMPR four-channel retrieval with RRF fusion.
	// -----------------------------------------------------------------------
	fmt.Println("--- [RECALL] TEMPR four-channel retrieval (RRF fusion) ---")
	queryVec := []float32{0.7, 0.3, 0.0, 0.0}
	queryText := "budget travel destination Southeast Asia"

	mc, err := mem.Recall(ctx, userID, sessionID, queryVec, queryText)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("  Short-term history: %d message(s)\n", len(mc.RecentHistory))
	for _, m := range mc.RecentHistory {
		fmt.Printf("    [%s] %s\n", m.Role, m.Content)
	}

	fmt.Printf("\n  RRF-Ranked long-term memories (%d total):\n", len(mc.RankedMemories))
	for i, r := range mc.RankedMemories {
		fmt.Printf("    %d. [%.4f] [%s via %s] %s\n",
			i+1, r.RRFScore, layerName(r.Layer), strings.Join(r.Sources, "+"), r.Content)
	}
	fmt.Println()

	// -----------------------------------------------------------------------
	// 7. Reflect – mission + directives + memory block ready for LLM prompt.
	// -----------------------------------------------------------------------
	fmt.Println("--- [REFLECT] Building LLM-ready context ---")
	rc, err := mem.Reflect(ctx, userID, sessionID, queryVec, queryText)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\n  [System Prompt]:")
	for _, line := range strings.Split(rc.SystemPrompt, "\n") {
		fmt.Println("    " + line)
	}

	if rc.DispositionHints != "" {
		fmt.Printf("\n  [Disposition] %s\n", rc.DispositionHints)
	}

	fmt.Println("\n  [Memory Block]:")
	for _, line := range strings.Split(rc.MemoryBlock, "\n") {
		fmt.Println("    " + line)
	}

	fmt.Println()
	fmt.Println("=== End of demo ===")
	fmt.Println("The LLM can now use the SystemPrompt + MemoryBlock to answer")
	fmt.Println("Alice's question with full personalised context, zero redundancy.")
}

// layerName returns a short display name for a MemoryLayer.
func layerName(l memory.MemoryLayer) string {
	switch l {
	case memory.LayerMentalModel:
		return "MentalModel"
	case memory.LayerObservation:
		return "Observation"
	case memory.LayerWorldFact:
		return "WorldFact"
	default:
		return "Experience"
	}
}
