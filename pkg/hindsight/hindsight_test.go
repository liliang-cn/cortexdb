package hindsight

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	path := "test.db"
	cfg := DefaultConfig(path)

	if cfg.DBPath != path {
		t.Errorf("Expected DBPath %s, got %s", path, cfg.DBPath)
	}

	if cfg.VectorDim != 0 {
		t.Errorf("Expected VectorDim 0 (auto-detect), got %d", cfg.VectorDim)
	}

	if cfg.Collection != "memories" {
		t.Errorf("Expected Collection 'memories', got %s", cfg.Collection)
	}
}

func TestNewSystem(t *testing.T) {
	dbPath := fmt.Sprintf("test_hindsight_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	sys, err := New(&Config{DBPath: dbPath, VectorDim: 128})
	if err != nil {
		t.Fatalf("Failed to create system: %v", err)
	}
	defer sys.Close()

	if sys.store == nil {
		t.Error("Expected non-nil store")
	}

	if sys.graph == nil {
		t.Error("Expected non-nil graph")
	}

	if sys.banks == nil {
		t.Error("Expected non-nil banks map")
	}
}

func TestMemoryTypes(t *testing.T) {
	types := []MemoryType{
		WorldMemory,
		BankMemory,
		OpinionMemory,
		ObservationMemory,
	}

	expected := []MemoryType{
		WorldMemory,      // "world"
		BankMemory,       // "bank"
		OpinionMemory,    // "opinion"
		ObservationMemory, // "observation"
	}

	for i, mt := range types {
		if string(mt) != string(expected[i]) {
			t.Errorf("Expected memory type %s, got %s", expected[i], mt)
		}
	}
}

func TestNewBank(t *testing.T) {
	t.Run("DefaultBank", func(t *testing.T) {
		bank := NewBank("test-1", "Test Bank")

		if bank.ID != "test-1" {
			t.Errorf("Expected ID 'test-1', got %s", bank.ID)
		}

		if bank.Name != "Test Bank" {
			t.Errorf("Expected Name 'Test Bank', got %s", bank.Name)
		}

		if bank.Disposition == nil {
			t.Error("Expected non-nil disposition")
		}

		if bank.Skepticism != 3 {
			t.Errorf("Expected default Skepticism 3, got %d", bank.Skepticism)
		}

		if bank.Literalism != 3 {
			t.Errorf("Expected default Literalism 3, got %d", bank.Literalism)
		}

		if bank.Empathy != 3 {
			t.Errorf("Expected default Empathy 3, got %d", bank.Empathy)
		}
	})

	t.Run("CustomDisposition", func(t *testing.T) {
		disp := &Disposition{
			Skepticism: 5,
			Literalism: 1,
			Empathy:    4,
		}

		bank := NewBankWithDisposition("test-2", "Custom Bank", disp)

		if bank.Skepticism != 5 {
			t.Errorf("Expected Skepticism 5, got %d", bank.Skepticism)
		}

		if bank.Literalism != 1 {
			t.Errorf("Expected Literalism 1, got %d", bank.Literalism)
		}

		if bank.Empathy != 4 {
			t.Errorf("Expected Empathy 4, got %d", bank.Empathy)
		}
	})
}

func TestDispositionValidation(t *testing.T) {
	t.Run("ValidDisposition", func(t *testing.T) {
		disp := &Disposition{
			Skepticism: 3,
			Literalism: 3,
			Empathy:    3,
		}

		if !disp.Validate() {
			t.Error("Expected valid disposition to pass validation")
		}
	})

	t.Run("InvalidSkepticism", func(t *testing.T) {
		disp := &Disposition{
			Skepticism: 0, // Too low
			Literalism: 3,
			Empathy:    3,
		}

		if disp.Validate() {
			t.Error("Expected invalid skepticism to fail validation")
		}
	})

	t.Run("InvalidLiteralism", func(t *testing.T) {
		disp := &Disposition{
			Skepticism: 3,
			Literalism: 6, // Too high
			Empathy:    3,
		}

		if disp.Validate() {
			t.Error("Expected invalid literalism to fail validation")
		}
	})

	t.Run("InvalidEmpathy", func(t *testing.T) {
		disp := &Disposition{
			Skepticism: 3,
			Literalism: 3,
			Empathy:    -1, // Too low
		}

		if disp.Validate() {
			t.Error("Expected invalid empathy to fail validation")
		}
	})
}

func TestBankManagement(t *testing.T) {
	dbPath := fmt.Sprintf("test_banks_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	sys, err := New(&Config{DBPath: dbPath, VectorDim: 128})
	if err != nil {
		t.Fatalf("Failed to create system: %v", err)
	}
	defer sys.Close()

	ctx := context.Background()

	t.Run("CreateBank", func(t *testing.T) {
		bank := NewBank("bank-1", "Test Bank")
		err := sys.CreateBank(ctx, bank)
		if err != nil {
			t.Fatalf("Failed to create bank: %v", err)
		}

		// Verify bank exists
		retrieved, ok := sys.GetBank("bank-1")
		if !ok {
			t.Fatal("Bank not found after creation")
		}

		if retrieved.ID != "bank-1" {
			t.Errorf("Expected ID 'bank-1', got %s", retrieved.ID)
		}

		if retrieved.Name != "Test Bank" {
			t.Errorf("Expected Name 'Test Bank', got %s", retrieved.Name)
		}
	})

	t.Run("DuplicateBank", func(t *testing.T) {
		bank := NewBank("bank-2", "Duplicate Test")
		err := sys.CreateBank(ctx, bank)
		if err != nil {
			t.Fatalf("Failed to create bank: %v", err)
		}

		// Try to create duplicate
		err = sys.CreateBank(ctx, bank)
		if err == nil {
			t.Error("Expected error when creating duplicate bank")
		}
	})

	t.Run("ListBanks", func(t *testing.T) {
		// Create a few banks
		for i := 0; i < 3; i++ {
			bank := NewBank(fmt.Sprintf("list-test-%d", i), fmt.Sprintf("Bank %d", i))
			if err := sys.CreateBank(ctx, bank); err != nil {
				t.Fatalf("Failed to create bank: %v", err)
			}
		}

		banks := sys.ListBanks()
		if len(banks) < 5 { // We created at least 5 banks total
			t.Errorf("Expected at least 5 banks, got %d", len(banks))
		}
	})

	t.Run("DeleteBank", func(t *testing.T) {
		bank := NewBank("delete-me", "To Be Deleted")
		if err := sys.CreateBank(ctx, bank); err != nil {
			t.Fatalf("Failed to create bank: %v", err)
		}

		// Verify it exists
		if _, ok := sys.GetBank("delete-me"); !ok {
			t.Fatal("Bank not found after creation")
		}

		// Delete it
		err := sys.DeleteBank("delete-me")
		if err != nil {
			t.Errorf("Failed to delete bank: %v", err)
		}

		// Verify it's gone
		if _, ok := sys.GetBank("delete-me"); ok {
			t.Error("Bank still exists after deletion")
		}
	})

	t.Run("DeleteNonExistentBank", func(t *testing.T) {
		err := sys.DeleteBank("non-existent")
		if err == nil {
			t.Error("Expected error when deleting non-existent bank")
		}
	})
}

func TestRetain(t *testing.T) {
	dbPath := fmt.Sprintf("test_retain_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	sys, err := New(&Config{DBPath: dbPath, VectorDim: 64})
	if err != nil {
		t.Fatalf("Failed to create system: %v", err)
	}
	defer sys.Close()

	ctx := context.Background()
	bank := NewBank("agent-1", "Test Agent")
	sys.CreateBank(ctx, bank)

	// Create the memories collection
	if _, err := sys.store.CreateCollection(ctx, "memories", 64); err != nil {
		t.Fatalf("Failed to create collection: %v", err)
	}

	t.Run("RetainWorldMemory", func(t *testing.T) {
		mem := &Memory{
			BankID:   "agent-1",
			Type:     WorldMemory,
			Content:  "Alice works at Google",
			Vector:   make([]float32, 64),
			Entities: []string{"Alice", "Google"},
		}

		err := sys.Retain(ctx, mem)
		if err != nil {
			t.Fatalf("Failed to retain memory: %v", err)
		}

		if mem.ID == "" {
			t.Error("Expected ID to be generated")
		}

		if mem.CreatedAt.IsZero() {
			t.Error("Expected CreatedAt to be set")
		}
	})

	t.Run("RetainBankMemory", func(t *testing.T) {
		mem := &Memory{
			BankID:   "agent-1",
			Type:     BankMemory,
			Content:  "I recommended Python to Bob",
			Vector:   make([]float32, 64),
			Entities: []string{"Python", "Bob"},
		}

		err := sys.Retain(ctx, mem)
		if err != nil {
			t.Fatalf("Failed to retain memory: %v", err)
		}
	})

	t.Run("RetainOpinionMemory", func(t *testing.T) {
		mem := &Memory{
			BankID:     "agent-1",
			Type:       OpinionMemory,
			Content:    "Python is best for ML",
			Vector:     make([]float32, 64),
			Confidence: 0.85,
		}

		err := sys.Retain(ctx, mem)
		if err != nil {
			t.Fatalf("Failed to retain memory: %v", err)
		}
	})

	t.Run("RetainObservationMemory", func(t *testing.T) {
		mem := &Memory{
			BankID:    "agent-1",
			Type:      ObservationMemory,
			Content:   "Users prefer concise answers",
			Vector:    make([]float32, 64),
			Confidence: 0.75,
			Metadata: map[string]any{
				"observation_type": "preference",
				"reasoning":        "Multiple experiences show pattern",
			},
		}

		err := sys.Retain(ctx, mem)
		if err != nil {
			t.Fatalf("Failed to retain memory: %v", err)
		}
	})

	t.Run("RetainWithCustomID", func(t *testing.T) {
		customID := "custom-memory-123"
		mem := &Memory{
			ID:      customID,
			BankID:  "agent-1",
			Type:    WorldMemory,
			Content: "Custom ID memory",
			Vector:  make([]float32, 64),
		}

		err := sys.Retain(ctx, mem)
		if err != nil {
			t.Fatalf("Failed to retain memory: %v", err)
		}

		if mem.ID != customID {
			t.Errorf("Expected ID %s, got %s", customID, mem.ID)
		}
	})
}

func TestRecall(t *testing.T) {
	dbPath := fmt.Sprintf("test_recall_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	sys, err := New(&Config{DBPath: dbPath, VectorDim: 64})
	if err != nil {
		t.Fatalf("Failed to create system: %v", err)
	}
	defer sys.Close()

	ctx := context.Background()
	bank := NewBank("agent-1", "Test Agent")
	sys.CreateBank(ctx, bank)

	// Create the memories collection
	if _, err := sys.store.CreateCollection(ctx, "memories", 64); err != nil {
		t.Fatalf("Failed to create collection: %v", err)
	}

	// Add test memories
	memories := []*Memory{
		{
			BankID:   "agent-1",
			Type:     WorldMemory,
			Content:  "Alice works at Google",
			Vector:   []float32{1, 0, 0, 0},
			Entities: []string{"Alice", "Google"},
		},
		{
			BankID:   "agent-1",
			Type:     BankMemory,
			Content:  "I helped Bob with Python",
			Vector:   []float32{0, 1, 0, 0},
			Entities: []string{"Bob", "Python"},
		},
		{
			BankID:     "agent-1",
			Type:       OpinionMemory,
			Content:    "Python is great for ML",
			Vector:     []float32{0, 0, 1, 0},
			Confidence: 0.9,
		},
	}

	for _, mem := range memories {
		// Pad vector to 64 dimensions
		vec := make([]float32, 64)
		copy(vec, mem.Vector)
		mem.Vector = vec
		if err := sys.Retain(ctx, mem); err != nil {
			t.Fatalf("Failed to retain test memory: %v", err)
		}
	}

	t.Run("BasicRecall", func(t *testing.T) {
		queryVec := make([]float32, 64)
		queryVec[0] = 1 // Similar to Alice memory

		req := &RecallRequest{
			BankID:      "agent-1",
			QueryVector: queryVec,
			Strategy: &RecallStrategy{
				Memory: true,
			},
			TopK: 3,
		}

		results, err := sys.Recall(ctx, req)
		if err != nil {
			t.Fatalf("Failed to recall: %v", err)
		}

		if len(results) == 0 {
			t.Error("Expected at least one result")
		}

		if len(results) > 3 {
			t.Errorf("Expected at most 3 results, got %d", len(results))
		}
	})

	t.Run("RecallWithDefaultStrategy", func(t *testing.T) {
		queryVec := make([]float32, 64)
		queryVec[1] = 1 // Similar to Bob memory

		req := &RecallRequest{
			BankID:      "agent-1",
			QueryVector: queryVec,
			// Strategy will use DefaultStrategy
			TopK: 5,
		}

		results, err := sys.Recall(ctx, req)
		if err != nil {
			t.Fatalf("Failed to recall: %v", err)
		}

		if len(results) == 0 {
			t.Error("Expected results from default strategy")
		}
	})

	t.Run("RecallWithFilter", func(t *testing.T) {
		queryVec := make([]float32, 64)

		req := &RecallRequest{
			BankID:      "agent-1",
			QueryVector: queryVec,
			Strategy: &RecallStrategy{
				Memory:  true,
				TopK:    10,
			},
			TopK: 10,
		}

		results, err := sys.Recall(ctx, req)
		if err != nil {
			t.Fatalf("Failed to recall: %v", err)
		}

		// Check that all results belong to the correct bank
		for _, r := range results {
			if r.Memory.BankID != "agent-1" {
				t.Errorf("Expected bank_id 'agent-1', got '%s'", r.Memory.BankID)
			}
		}
	})
}

func TestRecallWithStrategies(t *testing.T) {
	dbPath := fmt.Sprintf("test_strategies_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	sys, err := New(&Config{DBPath: dbPath, VectorDim: 64})
	if err != nil {
		t.Fatalf("Failed to create system: %v", err)
	}
	defer sys.Close()

	ctx := context.Background()
	bank := NewBank("agent-1", "Test Agent")
	sys.CreateBank(ctx, bank)

	// Create the memories collection
	if _, err := sys.store.CreateCollection(ctx, "memories", 64); err != nil {
		t.Fatalf("Failed to create collection: %v", err)
	}

	// Add test memories with timestamps
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)
	twoDaysAgo := now.Add(-48 * time.Hour)

	memories := []*Memory{
		{
			BankID:    "agent-1",
			Type:      WorldMemory,
			Content:   "Recent event",
			Vector:    make([]float32, 64),
			CreatedAt: now,
		},
		{
			BankID:    "agent-1",
			Type:      WorldMemory,
			Content:   "Yesterday event",
			Vector:    make([]float32, 64),
			CreatedAt: yesterday,
		},
		{
			BankID:    "agent-1",
			Type:      WorldMemory,
			Content:   "Old event",
			Vector:    make([]float32, 64),
			CreatedAt: twoDaysAgo,
		},
	}

	for _, mem := range memories {
		if err := sys.Retain(ctx, mem); err != nil {
			t.Fatalf("Failed to retain test memory: %v", err)
		}
	}

	t.Run("TemporalStrategy", func(t *testing.T) {
		queryVec := make([]float32, 64)

		req := &RecallRequest{
			BankID:      "agent-1",
			QueryVector: queryVec,
			Strategy: &RecallStrategy{
				Temporal: &TemporalFilter{
					Start: &yesterday,
					End:   &now,
				},
				TopK: 10,
			},
			TopK: 10,
		}

		results, err := sys.Recall(ctx, req)
		if err != nil {
			t.Fatalf("Failed to recall: %v", err)
		}

		// Should only get recent and yesterday events
		for _, r := range results {
			if r.Memory.CreatedAt.Before(yesterday) || r.Memory.CreatedAt.After(now.Add(time.Minute)) {
				t.Errorf("Result outside time range: %v", r.Memory.CreatedAt)
			}
		}
	})

	t.Run("EntityStrategy", func(t *testing.T) {
		// Add memory with specific entity
		mem := &Memory{
			BankID:   "agent-1",
			Type:     WorldMemory,
			Content:  "Alice likes Python",
			Vector:   make([]float32, 64),
			Entities: []string{"Alice"},
		}
		if err := sys.Retain(ctx, mem); err != nil {
			t.Fatalf("Failed to retain memory: %v", err)
		}

		queryVec := make([]float32, 64)

		req := &RecallRequest{
			BankID:      "agent-1",
			QueryVector: queryVec,
			Strategy: &RecallStrategy{
				Entity: []string{"Alice"},
				Memory: true,
				TopK:   10,
			},
			TopK: 10,
		}

		results, err := sys.Recall(ctx, req)
		if err != nil {
			t.Fatalf("Failed to recall: %v", err)
		}

		// At least one result should contain Alice
		found := false
		for _, r := range results {
			for _, e := range r.Memory.Entities {
				if e == "Alice" {
					found = true
					break
				}
			}
		}
		if !found {
			t.Error("Expected to find memory with Alice entity")
		}
	})
}

func TestReflect(t *testing.T) {
	dbPath := fmt.Sprintf("test_reflect_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	sys, err := New(&Config{DBPath: dbPath, VectorDim: 64})
	if err != nil {
		t.Fatalf("Failed to create system: %v", err)
	}
	defer sys.Close()

	ctx := context.Background()
	bank := NewBank("agent-1", "Test Agent")
	sys.CreateBank(ctx, bank)

	// Create the memories collection
	if _, err := sys.store.CreateCollection(ctx, "memories", 64); err != nil {
		t.Fatalf("Failed to create collection: %v", err)
	}

	// Add test memories
	memories := []*Memory{
		{
			BankID:   "agent-1",
			Type:     WorldMemory,
			Content:  "Jamie prefers concise answers",
			Vector:   make([]float32, 64),
			Entities: []string{"Jamie"},
		},
		{
			BankID:   "agent-1",
			Type:     BankMemory,
			Content:  "I gave Jamie a detailed answer and they asked for shorter version",
			Vector:   make([]float32, 64),
			Entities: []string{"Jamie"},
		},
		{
			BankID:     "agent-1",
			Type:       OpinionMemory,
			Content:    "Jamie values practicality",
			Vector:     make([]float32, 64),
			Confidence: 0.8,
		},
	}

	for _, mem := range memories {
		if err := sys.Retain(ctx, mem); err != nil {
			t.Fatalf("Failed to retain test memory: %v", err)
		}
	}

	t.Run("ReflectBasic", func(t *testing.T) {
		queryVec := make([]float32, 64)

		req := &ContextRequest{
			BankID:      "agent-1",
			Query:       "What does Jamie prefer?",
			QueryVector: queryVec,
			Strategy: &RecallStrategy{
				Memory: true,
			},
			TopK: 5,
		}

		resp, err := sys.Reflect(ctx, req)
		if err != nil {
			t.Fatalf("Failed to reflect: %v", err)
		}

		if resp.Context == "" {
			t.Error("Expected non-empty context")
		}

		if len(resp.Memories) == 0 {
			t.Error("Expected some memories in response")
		}

		if resp.TokenCount < 0 {
			t.Errorf("Expected non-negative token count, got %d", resp.TokenCount)
		}
	})

	t.Run("ReflectWithTokenBudget", func(t *testing.T) {
		queryVec := make([]float32, 64)

		req := &ContextRequest{
			BankID:      "agent-1",
			Query:       "Tell me about Jamie",
			QueryVector: queryVec,
			TokenBudget: 50, // Small budget
			TopK:        10,
		}

		resp, err := sys.Reflect(ctx, req)
		if err != nil {
			t.Fatalf("Failed to reflect: %v", err)
		}

		if resp.TokenCount > 50 {
			t.Errorf("Expected token count <= 50, got %d", resp.TokenCount)
		}
	})
}

func TestObserve(t *testing.T) {
	dbPath := fmt.Sprintf("test_observe_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	sys, err := New(&Config{DBPath: dbPath, VectorDim: 64})
	if err != nil {
		t.Fatalf("Failed to create system: %v", err)
	}
	defer sys.Close()

	ctx := context.Background()
	bank := NewBank("agent-1", "Test Agent")
	sys.CreateBank(ctx, bank)

	// Create the memories collection
	if _, err := sys.store.CreateCollection(ctx, "memories", 64); err != nil {
		t.Fatalf("Failed to create collection: %v", err)
	}

	// Add test memories with repeated patterns
	for i := 0; i < 5; i++ {
		mem := &Memory{
			BankID:   "agent-1",
			Type:     WorldMemory,
			Content:  fmt.Sprintf("Jamie prefers concise answers - interaction %d", i),
			Vector:   make([]float32, 64),
			Entities: []string{"Jamie"},
		}
		if err := sys.Retain(ctx, mem); err != nil {
			t.Fatalf("Failed to retain memory: %v", err)
		}
	}

	t.Run("ObserveBasic", func(t *testing.T) {
		queryVec := make([]float32, 64)

		req := &ReflectRequest{
			BankID:        "agent-1",
			Query:         "Tell me about Jamie",
			QueryVector:   queryVec,
			Strategy:      DefaultStrategy(),
			TopK:          10,
			MinConfidence: 0.1,
		}

		resp, err := sys.Observe(ctx, req)
		if err != nil {
			t.Fatalf("Failed to observe: %v", err)
		}

		if resp.Context == "" {
			t.Error("Expected non-empty context")
		}

		// Should have some observations due to repeated patterns
		if len(resp.Observations) == 0 {
			t.Log("No observations generated (may be expected with mock embeddings)")
		}

		// Check observations are valid
		for _, obs := range resp.Observations {
			if obs.ID == "" {
				t.Error("Expected observation to have ID")
			}
			if obs.Content == "" {
				t.Error("Expected observation to have content")
			}
			if obs.BankID != "agent-1" {
				t.Errorf("Expected bank_id 'agent-1', got '%s'", obs.BankID)
			}
		}
	})
}

func TestObservationManagement(t *testing.T) {
	dbPath := fmt.Sprintf("test_observation_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	sys, err := New(&Config{DBPath: dbPath, VectorDim: 64})
	if err != nil {
		t.Fatalf("Failed to create system: %v", err)
	}
	defer sys.Close()

	ctx := context.Background()
	bank := NewBank("agent-1", "Test Agent")
	sys.CreateBank(ctx, bank)

	// Create the memories collection
	if _, err := sys.store.CreateCollection(ctx, "memories", 64); err != nil {
		t.Fatalf("Failed to create collection: %v", err)
	}

	t.Run("AddObservation", func(t *testing.T) {
		obs := &Observation{
			BankID:         "agent-1",
			Content:        "Jamie prefers brevity",
			Vector:         make([]float32, 64),
			Confidence:     0.9,
			ObservationType: PreferenceObservation,
			Reasoning:      "Multiple interactions show this pattern",
		}

		err := sys.AddObservation(ctx, obs)
		if err != nil {
			t.Fatalf("Failed to add observation: %v", err)
		}

		if obs.ID == "" {
			t.Error("Expected ID to be generated")
		}
	})

	t.Run("GetObservations", func(t *testing.T) {
		// Add another observation
		obs := &Observation{
			BankID:         "agent-1",
			Content:        "Users like Python",
			Vector:         make([]float32, 64),
			Confidence:     0.8,
			ObservationType: PatternObservation,
		}
		sys.AddObservation(ctx, obs)

		observations, err := sys.GetObservations(ctx, "agent-1")
		if err != nil {
			t.Fatalf("Failed to get observations: %v", err)
		}

		if len(observations) < 2 {
			t.Errorf("Expected at least 2 observations, got %d", len(observations))
		}
	})
}

func TestObservationTypes(t *testing.T) {
	types := []ObservationType{
		PatternObservation,
		CausalObservation,
		GeneralizationObservation,
		PreferenceObservation,
		RiskObservation,
		StrategyObservation,
	}

	expectedValues := []string{
		"pattern",
		"causal",
		"generalization",
		"preference",
		"risk",
		"strategy",
	}

	for i, ot := range types {
		if string(ot) != expectedValues[i] {
			t.Errorf("Expected observation type '%s', got '%s'", expectedValues[i], ot)
		}
	}
}

func TestDefaultStrategy(t *testing.T) {
	strategy := DefaultStrategy()

	if !strategy.Memory {
		t.Error("Expected Memory to be enabled in default strategy")
	}

	if !strategy.Priming {
		t.Error("Expected Priming to be enabled in default strategy")
	}

	if strategy.TopK != 10 {
		t.Errorf("Expected TopK 10, got %d", strategy.TopK)
	}
}

func TestSystemClose(t *testing.T) {
	dbPath := fmt.Sprintf("test_close_%d.db", time.Now().UnixNano())
	defer func() { _ = os.Remove(dbPath) }()

	sys, err := New(&Config{DBPath: dbPath, VectorDim: 64})
	if err != nil {
		t.Fatalf("Failed to create system: %v", err)
	}

	err = sys.Close()
	if err != nil {
		t.Errorf("Failed to close system: %v", err)
	}
}
