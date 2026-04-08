package memoryflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func TestToolboxDefinitionsAndCall(t *testing.T) {
	dbPath := fmt.Sprintf("memoryflow_toolbox_%s.db", t.Name())
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	service, err := New(db, nil, nil)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	toolbox, err := NewToolbox(service)
	if err != nil {
		t.Fatalf("new toolbox: %v", err)
	}
	if len(toolbox.Definitions()) == 0 {
		t.Fatal("expected toolbox definitions")
	}

	payload, _ := json.Marshal(IngestTranscriptRequest{
		Transcript: Transcript{
			SessionID: "session-1",
			UserID:    "user-1",
			Source:    "chatgpt",
			Turns: []TranscriptTurn{
				{Role: "user", Content: "Apollo deadline is Friday."},
				{Role: "assistant", Content: "Stored."},
			},
		},
		Scope:     cortexdb.MemoryScopeSession,
		Namespace: "assistant",
	})
	resp, err := toolbox.Call(context.Background(), "memoryflow_ingest_transcript", payload)
	if err != nil {
		t.Fatalf("toolbox ingest call: %v", err)
	}
	if resp == nil {
		t.Fatal("expected ingest response")
	}
}
