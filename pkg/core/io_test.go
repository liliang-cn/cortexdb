package core

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

// TestDumpJSON tests JSON export
func TestDumpJSON(t *testing.T) {
	ctx := context.Background()
	dbPath := "dump_json_test.db"
	defer func() { _ = os.Remove(dbPath) }()

	config := DefaultConfig()
	config.VectorDim = 4

	store, err := New(dbPath, config.VectorDim)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	// Insert test data
	testData := []*Embedding{
		{ID: "1", Vector: []float32{1, 0, 0, 0}, Content: "Item 1", Metadata: map[string]string{"category": "tech"}},
		{ID: "2", Vector: []float32{0, 1, 0, 0}, Content: "Item 2", Metadata: map[string]string{"category": "science"}},
		{ID: "3", Vector: []float32{0, 0, 1, 0}, Content: "Item 3", Metadata: map[string]string{"category": "art"}},
	}

	for _, emb := range testData {
		if err := store.Upsert(ctx, emb); err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
	}

	t.Run("Dump to JSON buffer", func(t *testing.T) {
		var buf bytes.Buffer
		opts := DefaultDumpOptions()
		opts.Format = DumpFormatJSON

		stats, err := store.Dump(ctx, &buf, opts)
		if err != nil {
			t.Fatalf("Dump failed: %v", err)
		}

		if stats.TotalEmbeddings != 3 {
			t.Errorf("Expected 3 embeddings, got %d", stats.TotalEmbeddings)
		}

		output := buf.String()
		if !strings.Contains(output, `"id": "1"`) {
			t.Error("Output should contain embedding ID 1")
		}
		if !strings.Contains(output, `"metadata"`) {
			t.Error("Output should contain metadata")
		}
	})

	t.Run("Dump without vectors", func(t *testing.T) {
		var buf bytes.Buffer
		opts := DefaultDumpOptions()
		opts.Format = DumpFormatJSON
		opts.IncludeVectors = false

		_, err := store.Dump(ctx, &buf, opts)
		if err != nil {
			t.Fatalf("Dump failed: %v", err)
		}

		output := buf.String()
		// When vectors are excluded, vector field should be null or empty
		if strings.Contains(output, `"vector":[`) {
			t.Error("Output should not contain vector data when IncludeVectors is false")
		}
	})

	t.Run("Dump with filter", func(t *testing.T) {
		var buf bytes.Buffer
		opts := DefaultDumpOptions()
		opts.Format = DumpFormatJSON
		opts.Filter = NewMetadataFilter().Equal("category", "tech")

		stats, err := store.Dump(ctx, &buf, opts)
		if err != nil {
			t.Fatalf("Dump failed: %v", err)
		}

		if stats.TotalEmbeddings != 1 {
			t.Errorf("Expected 1 embedding matching filter, got %d", stats.TotalEmbeddings)
		}
	})
}

// TestDumpJSONL tests JSON Lines export
func TestDumpJSONL(t *testing.T) {
	ctx := context.Background()
	dbPath := "dump_jsonl_test.db"
	defer func() { _ = os.Remove(dbPath) }()

	config := DefaultConfig()
	config.VectorDim = 3

	store, err := New(dbPath, config.VectorDim)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	// Insert test data
	for i := 1; i <= 3; i++ {
		emb := &Embedding{
			ID:      string(rune('0' + i)),
			Vector:  []float32{float32(i), 0, 0},
			Content: "Item",
		}
		if err := store.Upsert(ctx, emb); err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
	}

	t.Run("Dump to JSONL", func(t *testing.T) {
		var buf bytes.Buffer
		opts := DefaultDumpOptions()
		opts.Format = DumpFormatJSONL

		stats, err := store.Dump(ctx, &buf, opts)
		if err != nil {
			t.Fatalf("Dump failed: %v", err)
		}

		if stats.TotalEmbeddings != 3 {
			t.Errorf("Expected 3 embeddings, got %d", stats.TotalEmbeddings)
		}

		// JSONL should have one JSON object per line
		lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
		if len(lines) != 3 {
			t.Errorf("Expected 3 lines in JSONL, got %d", len(lines))
		}
	})
}

// TestLoadJSON tests JSON import
func TestLoadJSON(t *testing.T) {
	ctx := context.Background()
	dbPath := "load_json_test.db"
	defer func() { _ = os.Remove(dbPath) }()

	config := DefaultConfig()
	config.VectorDim = 4

	store, err := New(dbPath, config.VectorDim)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	// Create test JSON data
	jsonData := `{
		"metadata": {
			"version": "1.0",
			"dimensions": 4,
			"count": 2
		},
		"embeddings": [
			{
				"id": "import1",
				"vector": [1, 0, 0, 0],
				"content": "Imported item 1",
				"metadata": {"category": "tech"}
			},
			{
				"id": "import2",
				"vector": [0, 1, 0, 0],
				"content": "Imported item 2",
				"metadata": {"category": "science"}
			}
		]
	}`

	t.Run("Load from JSON", func(t *testing.T) {
		r := strings.NewReader(jsonData)
		opts := DefaultLoadOptions()
		opts.Format = DumpFormatJSON

		stats, err := store.Load(ctx, r, opts)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}

		if stats.TotalEmbeddings != 2 {
			t.Errorf("Expected 2 embeddings loaded, got %d", stats.TotalEmbeddings)
		}

		// Verify embeddings were loaded
		emb1, err := store.GetByID(ctx, "import1")
		if err != nil {
			t.Errorf("Failed to get imported embedding: %v", err)
		}
		if emb1.Content != "Imported item 1" {
			t.Errorf("Expected 'Imported item 1', got '%s'", emb1.Content)
		}
	})

	t.Run("Load with skip existing", func(t *testing.T) {
		// Load again with SkipExisting
		r := strings.NewReader(jsonData)
		opts := DefaultLoadOptions()
		opts.Format = DumpFormatJSON
		opts.SkipExisting = true

		stats, err := store.Load(ctx, r, opts)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}

		if stats.SkippedCount != 2 {
			t.Errorf("Expected 2 skipped, got %d", stats.SkippedCount)
		}
	})
}

// TestLoadJSONL tests JSON Lines import
func TestLoadJSONL(t *testing.T) {
	ctx := context.Background()
	dbPath := "load_jsonl_test.db"
	defer func() { _ = os.Remove(dbPath) }()

	config := DefaultConfig()
	config.VectorDim = 3

	store, err := New(dbPath, config.VectorDim)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	jsonlData := `{"id":"j1","vector":[1,0,0],"content":"Line 1"}
{"id":"j2","vector":[0,1,0],"content":"Line 2"}
{"id":"j3","vector":[0,0,1],"content":"Line 3"}`

	t.Run("Load from JSONL", func(t *testing.T) {
		r := strings.NewReader(jsonlData)
		opts := DefaultLoadOptions()
		opts.Format = DumpFormatJSONL

		stats, err := store.Load(ctx, r, opts)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}

		if stats.TotalEmbeddings != 3 {
			t.Errorf("Expected 3 embeddings loaded, got %d", stats.TotalEmbeddings)
		}
	})
}

// TestDumpToFile tests file export
func TestDumpToFile(t *testing.T) {
	ctx := context.Background()
	dbPath := "dump_file_test.db"
	exportPath := "test_export.json"

	defer func() {
		_ = os.Remove(dbPath)
		_ = os.Remove(exportPath)
	}()

	config := DefaultConfig()
	config.VectorDim = 3

	store, err := New(dbPath, config.VectorDim)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	// Insert test data
	emb := &Embedding{
		ID:      "1",
		Vector:  []float32{1, 0, 0},
		Content: "Test content",
	}
	if err := store.Upsert(ctx, emb); err != nil {
		t.Fatalf("Failed to insert: %v", err)
	}

	t.Run("Dump to file", func(t *testing.T) {
		opts := DefaultDumpOptions()
		opts.Format = DumpFormatJSON

		stats, err := store.DumpToFile(ctx, exportPath, opts)
		if err != nil {
			t.Fatalf("DumpToFile failed: %v", err)
		}

		if stats.TotalEmbeddings != 1 {
			t.Errorf("Expected 1 embedding, got %d", stats.TotalEmbeddings)
		}

		// Verify file exists
		if _, err := os.Stat(exportPath); os.IsNotExist(err) {
			t.Error("Export file was not created")
		}

		// Read and verify content
		content, err := os.ReadFile(exportPath)
		if err != nil {
			t.Fatalf("Failed to read export file: %v", err)
		}
		if !strings.Contains(string(content), "Test content") {
			t.Error("Export file should contain test content")
		}
	})
}

// TestLoadFromFile tests file import
func TestLoadFromFile(t *testing.T) {
	ctx := context.Background()
	dbPath := "load_file_test.db"
	importPath := "test_import.json"

	defer func() {
		_ = os.Remove(dbPath)
		_ = os.Remove(importPath)
	}()

	config := DefaultConfig()
	config.VectorDim = 3

	// Create import file
	jsonData := `{
		"metadata": {"version": "1.0", "dimensions": 3, "count": 1},
		"embeddings": [
			{"id": "file1", "vector": [1, 0, 0], "content": "From file"}
		]
	}`
	if err := os.WriteFile(importPath, []byte(jsonData), 0644); err != nil {
		t.Fatalf("Failed to create import file: %v", err)
	}

	store, err := New(dbPath, config.VectorDim)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	t.Run("Load from file", func(t *testing.T) {
		opts := DefaultLoadOptions()
		opts.Format = DumpFormatJSON

		stats, err := store.LoadFromFile(ctx, importPath, opts)
		if err != nil {
			t.Fatalf("LoadFromFile failed: %v", err)
		}

		if stats.TotalEmbeddings != 1 {
			t.Errorf("Expected 1 embedding, got %d", stats.TotalEmbeddings)
		}

		// Verify data was loaded
		emb, err := store.GetByID(ctx, "file1")
		if err != nil {
			t.Errorf("Failed to get loaded embedding: %v", err)
		}
		if emb.Content != "From file" {
			t.Errorf("Expected 'From file', got '%s'", emb.Content)
		}
	})
}

// TestRoundTripJSON tests export and import cycle
func TestRoundTripJSON(t *testing.T) {
	ctx := context.Background()
	dbPath1 := "roundtrip_src.db"
	dbPath2 := "roundtrip_dst.db"
	exportPath := "roundtrip_export.json"

	defer func() {
		_ = os.Remove(dbPath1)
		_ = os.Remove(dbPath2)
		_ = os.Remove(exportPath)
	}()

	config := DefaultConfig()
	config.VectorDim = 4

	// Create source store with data
	srcStore, err := New(dbPath1, config.VectorDim)
	if err != nil {
		t.Fatalf("Failed to create source store: %v", err)
	}

	if err := srcStore.Init(ctx); err != nil {
		t.Fatalf("Failed to init source store: %v", err)
	}

	// Insert test data
	testData := []*Embedding{
		{ID: "1", Vector: []float32{1, 0, 0, 0}, Content: "Item 1", Metadata: map[string]string{"cat": "a"}},
		{ID: "2", Vector: []float32{0, 1, 0, 0}, Content: "Item 2", Metadata: map[string]string{"cat": "b"}},
	}
	for _, emb := range testData {
		if err := srcStore.Upsert(ctx, emb); err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
	}

	// Export
	opts := DefaultDumpOptions()
	opts.Format = DumpFormatJSON
	stats, err := srcStore.DumpToFile(ctx, exportPath, opts)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	_ = srcStore.Close()

	if stats.TotalEmbeddings != 2 {
		t.Errorf("Expected 2 exported, got %d", stats.TotalEmbeddings)
	}

	// Create destination store and import
	dstStore, err := New(dbPath2, config.VectorDim)
	if err != nil {
		t.Fatalf("Failed to create destination store: %v", err)
	}
	defer func() { _ = dstStore.Close() }()

	if err := dstStore.Init(ctx); err != nil {
		t.Fatalf("Failed to init destination store: %v", err)
	}

	loadOpts := DefaultLoadOptions()
	loadOpts.Format = DumpFormatJSON
	importStats, err := dstStore.LoadFromFile(ctx, exportPath, loadOpts)
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}

	if importStats.TotalEmbeddings != 2 {
		t.Errorf("Expected 2 imported, got %d", importStats.TotalEmbeddings)
	}

	// Verify data integrity
	emb1, _ := dstStore.GetByID(ctx, "1")
	if emb1.Content != "Item 1" || emb1.Metadata["cat"] != "a" {
		t.Error("Imported data doesn't match original")
	}
}

// TestBackup tests SQLite database backup
func TestBackup(t *testing.T) {
	ctx := context.Background()
	dbPath := "backup_test.db"
	backupPath := "backup_test_backup.db"

	defer func() {
		_ = os.Remove(dbPath)
		_ = os.Remove(backupPath)
	}()

	config := DefaultConfig()
	config.VectorDim = 3

	store, err := New(dbPath, config.VectorDim)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Init(ctx); err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	// Insert test data
	emb := &Embedding{
		ID:      "1",
		Vector:  []float32{1, 0, 0},
		Content: "Backup test",
	}
	if err := store.Upsert(ctx, emb); err != nil {
		t.Fatalf("Failed to insert: %v", err)
	}

	t.Run("Create backup", func(t *testing.T) {
		_ = os.Remove(backupPath)
		if err := store.Backup(ctx, backupPath); err != nil {
			t.Fatalf("Backup failed: %v", err)
		}

		// Verify backup file exists
		if _, err := os.Stat(backupPath); os.IsNotExist(err) {
			t.Error("Backup file was not created")
		}

		// Open backup and verify data
		backupStore, err := New(backupPath, config.VectorDim)
		if err != nil {
			t.Fatalf("Failed to open backup: %v", err)
		}
		if err := backupStore.Init(ctx); err != nil {
			t.Fatalf("Failed to init backup store: %v", err)
		}
		defer func() { _ = backupStore.Close() }()

		results, err := backupStore.Search(ctx, []float32{1, 0, 0}, SearchOptions{TopK: 10})
		if err != nil {
			t.Fatalf("Search on backup failed: %v", err)
		}

		if len(results) != 1 {
			t.Errorf("Expected 1 result in backup, got %d", len(results))
		}
		if len(results) > 0 && results[0].Content != "Backup test" {
			t.Errorf("Expected 'Backup test', got '%s'", results[0].Content)
		}
	})
}
