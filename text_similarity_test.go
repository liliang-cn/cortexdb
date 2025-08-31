package sqvect

import (
	"testing"
)

func TestTextSimilarityConfiguration(t *testing.T) {
	// Test default configuration (no special terms)
	defaultSim := NewTextSimilarity()
	if len(defaultSim.boostTerms) != 0 {
		t.Error("Default configuration should have no boost terms")
	}
	if len(defaultSim.termPairs) != 0 {
		t.Error("Default configuration should have no term pairs")
	}

	// Test custom configuration
	options := TextSimilarityOptions{
		BoostTerms: map[string]float64{
			"test": 1.5,
		},
		TermPairs: map[string][]string{
			"ai": {"artificial intelligence"},
		},
	}
	
	customSim := NewTextSimilarityWithOptions(options)
	if len(customSim.boostTerms) != 1 {
		t.Errorf("Expected 1 boost term, got %d", len(customSim.boostTerms))
	}
	if len(customSim.termPairs) != 1 {
		t.Errorf("Expected 1 term pair, got %d", len(customSim.termPairs))
	}
	
	// Test boost term value
	if customSim.boostTerms["test"] != 1.5 {
		t.Errorf("Expected boost value 1.5, got %f", customSim.boostTerms["test"])
	}
	
	// Test term pair
	if len(customSim.termPairs["ai"]) != 1 || customSim.termPairs["ai"][0] != "artificial intelligence" {
		t.Error("Term pair not configured correctly")
	}
}

func TestDynamicConfiguration(t *testing.T) {
	sim := NewTextSimilarity()
	
	// Test adding boost terms
	sim.AddBoostTerm("dynamic", 1.3)
	if sim.boostTerms["dynamic"] != 1.3 {
		t.Errorf("Expected boost value 1.3, got %f", sim.boostTerms["dynamic"])
	}
	
	// Test adding term pairs
	sim.AddTermPair("test", []string{"testing", "verification"})
	if len(sim.termPairs["test"]) != 2 {
		t.Errorf("Expected 2 translations, got %d", len(sim.termPairs["test"]))
	}
}

func TestDefaultChineseOptions(t *testing.T) {
	options := DefaultChineseOptions()
	
	// Check that we have some boost terms
	if len(options.BoostTerms) == 0 {
		t.Error("DefaultChineseOptions should include boost terms")
	}
	
	// Check that we have some term pairs
	if len(options.TermPairs) == 0 {
		t.Error("DefaultChineseOptions should include term pairs")
	}
	
	// Check specific terms exist
	if _, exists := options.BoostTerms["音书"]; !exists {
		t.Error("Expected '音书' to be in boost terms")
	}
	
	if _, exists := options.TermPairs["yinshu"]; !exists {
		t.Error("Expected 'yinshu' to be in term pairs")
	}
}

func TestSpecialTermMatchWithConfiguration(t *testing.T) {
	// Test with empty configuration
	emptySim := NewTextSimilarity()
	score1 := emptySim.specialTermMatch("test", "testing")
	if score1 != 0.0 {
		t.Errorf("Expected 0.0 for empty configuration, got %f", score1)
	}
	
	// Test with configured terms
	options := TextSimilarityOptions{
		TermPairs: map[string][]string{
			"test": {"testing", "verification"},
		},
	}
	configuredSim := NewTextSimilarityWithOptions(options)
	score2 := configuredSim.specialTermMatch("test", "this is testing")
	if score2 == 0.0 {
		t.Error("Expected non-zero score for configured term match")
	}
}
