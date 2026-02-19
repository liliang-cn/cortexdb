package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
	"github.com/liliang-cn/sqvect/v2/pkg/hindsight"
)

func main() {
	dbPath := "advanced_memory.db"
	defer os.Remove(dbPath)

	// -----------------------------------------------------------------------
	// 1. Initialise the Hindsight memory system.
	// -----------------------------------------------------------------------
	sys, err := hindsight.New(&hindsight.Config{
		DBPath:    dbPath,
		VectorDim: 4,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer sys.Close()

	ctx := context.Background()

	const bankID = "alice_travel_agent"

	// -----------------------------------------------------------------------
	// 2. Create a Memory Bank with personality disposition.
	// -----------------------------------------------------------------------
	bank := hindsight.NewBank(bankID, "Travel Assistant for Alice")
	bank.Empathy = 4    // 1=Detached … 5=Empathetic
	bank.Skepticism = 2 // 1=Trusting … 5=Skeptical
	bank.Literalism = 2 // 1=Flexible … 5=Literal
	if err := sys.CreateBank(ctx, bank); err != nil {
		log.Fatal(err)
	}

	fmt.Println("=== sqvect × Hindsight Memory Demo ===")
	fmt.Println()

	// -----------------------------------------------------------------------
	// 3. Retain Phase – store facts with different memory types.
	// -----------------------------------------------------------------------
	fmt.Println("--- [RETAIN] Storing memories ---")

	// OpinionMemory: formed belief about the user.
	_ = sys.Retain(ctx, &hindsight.Memory{
		ID:         "travel_style",
		BankID:     bankID,
		Type:       hindsight.OpinionMemory,
		Content:    "Alice prefers budget backpacker trips with cultural immersion over luxury resorts.",
		Confidence: 0.9,
		Vector:     []float32{0.9, 0.1, 0.0, 0.0},
	})

	// WorldMemory: objective facts about the user.
	_ = sys.Retain(ctx, &hindsight.Memory{
		ID:       "home_city",
		BankID:   bankID,
		Type:     hindsight.WorldMemory,
		Content:  "Alice is based in Berlin, Germany.",
		Vector:   []float32{0.8, 0.0, 0.2, 0.0},
		Entities: []string{"Alice", "Berlin", "Germany"},
	})

	_ = sys.Retain(ctx, &hindsight.Memory{
		ID:      "next_trip",
		BankID:  bankID,
		Type:    hindsight.WorldMemory,
		Content: "Alice is planning a trip to Southeast Asia in March.",
		Vector:  []float32{0.7, 0.3, 0.0, 0.0},
	})

	// BankMemory: the agent's own past actions / recommendations.
	_ = sys.Retain(ctx, &hindsight.Memory{
		ID:      "rec_001",
		BankID:  bankID,
		Type:    hindsight.BankMemory,
		Content: "Recommended Chiang Mai as a budget-friendly base for northern Thailand.",
		Vector:  []float32{0.6, 0.4, 0.0, 0.0},
	})

	fmt.Println("  ✓ 4 memories retained (Opinion, World×2, Bank).")
	fmt.Println()

	// -----------------------------------------------------------------------
	// 4. FactExtractorFn hook – auto-extract facts from raw conversation.
	// -----------------------------------------------------------------------
	fmt.Println("--- [HOOK] FactExtractorFn: auto-extract from conversation ---")

	sys.SetFactExtractor(func(_ context.Context, _ string, msgs []*core.Message) ([]hindsight.ExtractedFact, error) {
		// Production: call your LLM for structured extraction + embeddings.
		// Demo: keyword scan.
		var facts []hindsight.ExtractedFact
		for _, m := range msgs {
			if strings.Contains(strings.ToLower(m.Content), "vegetarian") {
				facts = append(facts, hindsight.ExtractedFact{
					ID:      "diet_preference",
					Type:    hindsight.WorldMemory,
					Content: "Alice is vegetarian.",
					Vector:  []float32{0.3, 0.7, 0.0, 0.0},
				})
			}
		}
		return facts, nil
	})

	convo := []*core.Message{
		{Role: "user", Content: "I'm vegetarian by the way, any food advice?"},
		{Role: "assistant", Content: "Noted – I'll tailor food recommendations accordingly."},
	}
	extractResult, err := sys.RetainFromText(ctx, bankID, convo)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  ✓ RetainFromText: retained=%d skipped=%d\n", extractResult.Retained, extractResult.Skipped)
	if extractResult.Err() != nil {
		fmt.Println("  partial errors:", extractResult.Err())
	}
	fmt.Println()

	// -----------------------------------------------------------------------
	// 5. Recall – TEMPR four-channel retrieval + RRF fusion.
	// -----------------------------------------------------------------------
	fmt.Println("--- [RECALL] TEMPR retrieval (Semantic + Keyword + RRF) ---")
	queryVec := []float32{0.72, 0.28, 0.0, 0.0}

	results, err := sys.Recall(ctx, &hindsight.RecallRequest{
		BankID:      bankID,
		Query:       "budget travel destination Southeast Asia",
		QueryVector: queryVec,
		Strategy:    hindsight.DefaultStrategy(),
		TopK:        5,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  ✓ Recall returned %d memories:\n", len(results))
	for i, r := range results {
		fmt.Printf("    %d. [%-11s score=%.4f] %s\n", i+1, r.Type, r.Score, r.Content)
	}
	fmt.Println()

	// -----------------------------------------------------------------------
	// 6. RerankerFn hook – plug-in cross-encoder after RRF.
	// -----------------------------------------------------------------------
	fmt.Println("--- [HOOK] RerankerFn: cross-encoder reranking ---")

	sys.SetReranker(func(_ context.Context, _ string, candidates []*hindsight.RecallResult) ([]*hindsight.RecallResult, error) {
		// Production: call Cohere Rerank / cross-encoder model.
		// Demo: reverse order to prove the hook runs.
		for i, j := 0, len(candidates)-1; i < j; i, j = i+1, j-1 {
			candidates[i], candidates[j] = candidates[j], candidates[i]
		}
		return candidates, nil
	})

	reranked, _ := sys.Recall(ctx, &hindsight.RecallRequest{
		BankID:      bankID,
		Query:       "budget travel destination Southeast Asia",
		QueryVector: queryVec,
		Strategy:    hindsight.DefaultStrategy(),
		TopK:        3,
	})
	fmt.Printf("  ✓ Reranked top-%d (reversed for demo):\n", len(reranked))
	for i, r := range reranked {
		fmt.Printf("    %d. %s\n", i+1, r.Content)
	}
	fmt.Println()

	// -----------------------------------------------------------------------
	// 7. Reflect – get LLM-ready formatted context.
	// -----------------------------------------------------------------------
	fmt.Println("--- [REFLECT] Building LLM context ---")

	ctxResp, err := sys.Reflect(ctx, &hindsight.ContextRequest{
		BankID:      bankID,
		Query:       "Where should Alice travel next?",
		QueryVector: queryVec,
		Strategy:    hindsight.DefaultStrategy(),
		TopK:        4,
	})
	if err != nil {
		log.Fatal(err)
	}
	preview := ctxResp.Context
	if len(preview) > 400 {
		preview = preview[:400] + "..."
	}
	fmt.Printf("  ✓ Context (~%d tokens):\n%s\n", ctxResp.TokenCount, preview)
	fmt.Println()

	// -----------------------------------------------------------------------
	// 8. Observe – derive new insights through reflection.
	// -----------------------------------------------------------------------
	fmt.Println("--- [OBSERVE] Generating observations ---")

	reflectResp, err := sys.Observe(ctx, &hindsight.ReflectRequest{
		BankID:      bankID,
		Query:       "What travel patterns can we infer about Alice?",
		QueryVector: queryVec,
		Strategy:    hindsight.DefaultStrategy(),
		TopK:        5,
	})
	if err != nil {
		log.Println("  Observe error (expected in minimal demo):", err)
	} else {
		fmt.Printf("  ✓ Generated %d observation(s).\n", len(reflectResp.Observations))
		for i, o := range reflectResp.Observations {
			fmt.Printf("    %d. [%s conf=%.2f] %s\n", i+1, o.ObservationType, o.Confidence, o.Content)
		}
	}
	fmt.Println()

	fmt.Println("=== Demo complete ===")
}
