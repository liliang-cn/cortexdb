// Package main demonstrates using the Hindsight memory system.
// This example shows how to:
//   - Create a memory system
//   - Store different types of memories (World, Bank, Opinion)
//   - Recall using TEMPR strategies
//   - Observe - reflect on memories to generate new insights
//
// Note: This example uses mock embeddings. In production, use real embeddings
// from OpenAI, Cohere, or local models.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liliang-cn/sqvect/v2/pkg/hindsight"
)

// mockEmbedding returns a simple mock embedding vector.
// In production, use real embeddings from OpenAI, Cohere, etc.
func mockEmbedding(text string) []float32 {
	// Simple hash-based embedding for demonstration
	vec := make([]float32, 1536) // OpenAI dimension
	for i := range vec {
		vec[i] = 0.01
	}
	// Add some variation based on text length
	for i := 0; i < len(text) && i < len(vec); i++ {
		vec[i] = float32(text[i]) / 256.0
	}
	return vec
}

func main() {
	ctx := context.Background()

	// Create a temporary database
	dbPath := filepath.Join(os.TempDir(), fmt.Sprintf("hindsight_example_%d.db", time.Now().Unix()))
	defer os.Remove(dbPath)

	// Create Hindsight system
	sys, err := hindsight.New(&hindsight.Config{
		DBPath:     dbPath,
		VectorDim:  1536,
		Collection: "memories",
	})
	if err != nil {
		panic(err)
	}
	defer sys.Close()

	// Create a memory bank with custom disposition
	bank := hindsight.NewBank("agent-1", "Assistant Agent")
	bank.Description = "A helpful AI assistant"
	bank.Skepticism = 3 // Balanced skepticism
	if err := sys.CreateBank(ctx, bank); err != nil {
		panic(err)
	}

	fmt.Println("=== Hindsight Memory System Demo ===")
	fmt.Println()

	// === RETAIN: Store different types of memories ===
	fmt.Println("1. RETAIN - Storing memories...")

	// World: Objective facts
	worldMemories := []string{
		"Alice works at Google as a senior software engineer",
		"Bob is a freelance designer living in New York",
		"Charlie prefers Python over JavaScript for data analysis",
		"Jamie prefers concise answers over detailed explanations",
	}

	for _, content := range worldMemories {
		mem := &hindsight.Memory{
			BankID:   "agent-1",
			Type:     hindsight.WorldMemory,
			Content:  content,
			Vector:   mockEmbedding(content),
			Entities: extractEntities(content),
		}
		if err := sys.Retain(ctx, mem); err != nil {
			fmt.Printf("Error storing memory: %v\n", err)
		}
	}

	// Bank: Agent's own experiences
	experienceMemories := []string{
		"I recommended Python to Charlie and he was satisfied",
		"I helped Jamie set up a development environment last week",
		"When I give Jamie detailed explanations, they ask for shorter versions",
	}

	for _, content := range experienceMemories {
		mem := &hindsight.Memory{
			BankID:   "agent-1",
			Type:     hindsight.BankMemory,
			Content:  content,
			Vector:   mockEmbedding(content),
			Entities: extractEntities(content),
		}
		if err := sys.Retain(ctx, mem); err != nil {
			fmt.Printf("Error storing memory: %v\n", err)
		}
	}

	// Opinion: Beliefs with confidence
	opinionMemories := []struct {
		content    string
		confidence float64
	}{
		{"Jamie values practical solutions over theoretical ones", 0.85},
		{"For data analysis, Python is generally better than JavaScript", 0.90},
		{"Alice is likely to prefer technical discussions", 0.75},
	}

	for _, m := range opinionMemories {
		mem := &hindsight.Memory{
			BankID:    "agent-1",
			Type:      hindsight.OpinionMemory,
			Content:   m.content,
			Vector:    mockEmbedding(m.content),
			Entities:  extractEntities(m.content),
			Confidence: m.confidence,
		}
		if err := sys.Retain(ctx, mem); err != nil {
			fmt.Printf("Error storing memory: %v\n", err)
		}
	}

	fmt.Printf("Stored %d world facts, %d experiences, %d opinions\n\n",
		len(worldMemories), len(experienceMemories), len(opinionMemories))

	// === RECALL: Search memories using TEMPR strategies ===
	fmt.Println("2. RECALL - Searching memories...")

	// Semantic search
	query := "What does Jamie prefer?"
	results, err := sys.Recall(ctx, &hindsight.RecallRequest{
		BankID:      "agent-1",
		Query:       query,
		QueryVector: mockEmbedding(query),
		Strategy: &hindsight.RecallStrategy{
			Memory:  true,
			Priming: true,
			TopK:    5,
		},
		TopK: 5,
	})
	if err != nil {
		panic(err)
	}

	fmt.Printf("Query: %s\n", query)
	fmt.Printf("Found %d results:\n", len(results))
	for i, r := range results {
		fmt.Printf("  %d. [%s] %s (score: %.3f)\n", i+1, r.Memory.Type, r.Content, r.Score)
	}
	fmt.Println()

	// Entity search
	entityResults, err := sys.Recall(ctx, &hindsight.RecallRequest{
		BankID:      "agent-1",
		QueryVector: mockEmbedding("Alice"),
		Strategy: &hindsight.RecallStrategy{
			Entity: []string{"Alice", "Google"},
			Memory: true,
			TopK:   5,
		},
		TopK: 3,
	})
	if err != nil {
		panic(err)
	}

	fmt.Printf("Entity search (Alice, Google): %d results\n", len(entityResults))
	for i, r := range entityResults {
		fmt.Printf("  %d. %s\n", i+1, r.Content)
	}
	fmt.Println()

	// === OBSERVE: Reflect on memories to generate new insights ===
	fmt.Println("3. OBSERVE - Generating insights through reflection...")

	reflectQuery := "Tell me about Jamie's preferences"
	reflectResp, err := sys.Observe(ctx, &hindsight.ReflectRequest{
		BankID:          "agent-1",
		Query:           reflectQuery,
		QueryVector:     mockEmbedding(reflectQuery),
		Strategy:        hindsight.DefaultStrategy(),
		TopK:            10,
		MinConfidence:   0.3,
		ObservationTypes: []hindsight.ObservationType{
			hindsight.PreferenceObservation,
			hindsight.PatternObservation,
			hindsight.GeneralizationObservation,
		},
	})
	if err != nil {
		panic(err)
	}

	fmt.Printf("\nReflection Query: %s\n\n", reflectQuery)
	fmt.Printf("Generated Context:\n%s\n", reflectResp.Context)

	fmt.Printf("\nNew Observations Generated: %d\n", len(reflectResp.Observations))
	for i, obs := range reflectResp.Observations {
		fmt.Printf("  %d. [%s] %s (confidence: %.2f)\n", i+1, obs.ObservationType, obs.Content, obs.Confidence)
		fmt.Printf("     Reasoning: %s\n", obs.Reasoning)
		fmt.Printf("     Sources: %d memories\n", len(obs.SourceMemoryIDs))
	}
	fmt.Println()

	// === ADD CUSTOM OBSERVATION ===
	fmt.Println("4. ADD CUSTOM OBSERVATION - LLM-generated insight...")

	// In a real implementation, an LLM would generate this observation
	customObs := &hindsight.Observation{
		BankID:         "agent-1",
		Content:        "Jamie consistently prefers brevity and practicality over detailed explanations",
		Vector:         mockEmbedding("Jamie prefers brief practical answers"),
		Confidence:     0.92,
		ObservationType: hindsight.PreferenceObservation,
		Reasoning:      "Multiple experiences show Jamie asks for shorter versions of detailed explanations",
	}

	if err := sys.AddObservation(ctx, customObs); err != nil {
		fmt.Printf("Error adding observation: %v\n", err)
	} else {
		fmt.Printf("Stored custom observation: %s\n\n", customObs.Content)
	}

	// === RETRIEVE OBSERVATIONS ===
	fmt.Println("5. GET OBSERVATIONS - Retrieving stored insights...")

	storedObs, err := sys.GetObservations(ctx, "agent-1")
	if err != nil {
		panic(err)
	}

	fmt.Printf("Total observations stored: %d\n", len(storedObs))
	for i, obs := range storedObs {
		fmt.Printf("  %d. [%s] %s (confidence: %.2f)\n", i+1, obs.ObservationType, obs.Content, obs.Confidence)
	}

	fmt.Println("\n=== Demo Complete ===")
	fmt.Println("\nKey Concepts:")
	fmt.Println("  - World: Objective facts about the world")
	fmt.Println("  - Bank: Agent's own experiences")
	fmt.Println("  - Opinion: Beliefs with confidence scores")
	fmt.Println("  - Observation: Insights derived from reflection")
}

// extractEntities is a simple entity extractor for demonstration.
// In production, use NER from spaCy, Hugging Face, etc.
func extractEntities(text string) []string {
	// Known entities for this demo
	knownEntities := map[string]bool{
		"Alice": true, "Bob": true, "Charlie": true, "Jamie": true,
		"Google": true, "Python": true, "JavaScript": true,
		"New York": true, "San Francisco": true,
	}

	var result []string
	words := strings.Fields(text)
	for _, word := range words {
		// Remove punctuation
		cleanWord := strings.Trim(word, ".,!?;:")
		if knownEntities[cleanWord] {
			result = append(result, cleanWord)
		}
	}

	return result
}
