package graphflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func TestToolboxBuildAndAnalyze(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), fmt.Sprintf("%s.db", t.Name()))
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	toolbox, err := NewToolbox(db, FilesystemDetector{}, HeuristicExtractor{})
	if err != nil {
		t.Fatalf("new toolbox: %v", err)
	}
	if len(toolbox.Definitions()) == 0 {
		t.Fatal("expected graphflow tool definitions")
	}

	buildPayload, _ := json.Marshal(map[string]any{
		"extractions": []map[string]any{
			{
				"source_id": "doc-1",
				"nodes": []map[string]any{
					{"id": "doc:doc-1", "label": "Doc", "type": "document"},
					{"id": "entity:apollo", "label": "Apollo", "type": "entity"},
				},
				"edges": []map[string]any{
					{"source": "doc:doc-1", "target": "entity:apollo", "relation": "mentions", "confidence": "EXTRACTED", "directed": true},
				},
			},
		},
	})
	if _, err := toolbox.Call(context.Background(), "graphflow_build", buildPayload); err != nil {
		t.Fatalf("toolbox build: %v", err)
	}

	if _, err := toolbox.Call(context.Background(), "graphflow_analyze", []byte(`{"top_n":3}`)); err != nil {
		t.Fatalf("toolbox analyze: %v", err)
	}
}

func TestToolboxRun(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "plan.md"), []byte("Apollo deadline is Friday. Alice owns Apollo."), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), fmt.Sprintf("%s.db", t.Name()))
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	toolbox, err := NewToolbox(db, FilesystemDetector{}, HeuristicExtractor{})
	if err != nil {
		t.Fatalf("new toolbox: %v", err)
	}

	payload, _ := json.Marshal(RunRequest{
		Root:      root,
		OutputDir: filepath.Join(t.TempDir(), "graphflow-out"),
	})
	resp, err := toolbox.Call(context.Background(), "graphflow_run", payload)
	if err != nil {
		t.Fatalf("toolbox run: %v", err)
	}
	if resp == nil {
		t.Fatal("expected run response")
	}
}
