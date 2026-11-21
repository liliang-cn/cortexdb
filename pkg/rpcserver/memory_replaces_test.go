package rpcserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// A changed fact reaches a remote client both ways: the typed SearchMemory
// carries replaces and takes as_of, and the recall tool's JSON carries
// replaces and the change line in its context pack.
func TestRemoteRecallCarriesWhatAMemoryReplacedAndTakesAsOf(t *testing.T) {
	for _, withEmbedder := range []bool{false, true} {
		conn := newTestConn(t, withEmbedder, "")
		tools := rpcv1.NewToolsServiceClient(conn)
		ctx := context.Background()
		for _, args := range []string{
			`{"memory_id":"port-v1","scope":"global","content":"The Athanor dev server listens on port 4173.","metadata":{"source":"notes"}}`,
			`{"memory_id":"port-v2","scope":"global","content":"The Athanor dev server listens on port 6759.","supersedes":["port-v1"]}`,
		} {
			if _, err := tools.CallTool(ctx, &rpcv1.CallToolRequest{Name: "memory_save", ArgsJson: args}); err != nil {
				t.Fatal(err)
			}
		}

		mem := rpcv1.NewMemoryServiceClient(conn)
		resp, err := mem.SearchMemory(ctx, &rpcv1.SearchMemoryRequest{Query: "Athanor dev server port", Scope: "global", RetrievalMode: "lexical"})
		if err != nil {
			t.Fatal(err)
		}
		hits := resp.GetResults()
		if len(hits) != 1 || hits[0].GetMemory().GetId() != "port-v2" {
			t.Fatalf("embedder=%v: hits %v", withEmbedder, hits)
		}
		rep := hits[0].GetReplaces()
		if len(rep) != 1 || rep[0].GetId() != "port-v1" || !strings.Contains(rep[0].GetExcerpt(), "4173") ||
			rep[0].GetSupersededAt() == nil || rep[0].GetSupersededBy() != "port-v2" || rep[0].GetSource() != "notes" {
			t.Fatalf("embedder=%v: replaces %v", withEmbedder, rep)
		}

		past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		before, err := mem.SearchMemory(ctx, &rpcv1.SearchMemoryRequest{Query: "Athanor dev server port", Scope: "global", RetrievalMode: "lexical", AsOf: past})
		if err != nil {
			t.Fatal(err)
		}
		if len(before.GetResults()) != 0 {
			t.Errorf("embedder=%v: as_of before any save found %v", withEmbedder, before.GetResults())
		}

		got, err := mem.GetMemory(ctx, &rpcv1.GetMemoryRequest{MemoryId: "port-v1"})
		if err != nil {
			t.Fatal(err)
		}
		if got.GetMemory().GetSupersededBy() != "port-v2" || got.GetMemory().GetCurrentId() != "port-v2" {
			t.Errorf("embedder=%v: GetMemory %v", withEmbedder, got.GetMemory())
		}

		out, err := tools.CallTool(ctx, &rpcv1.CallToolRequest{Name: "knowledge_memory_recall", ArgsJson: `{"query":"Athanor dev server port"}`})
		if err != nil {
			t.Fatal(err)
		}
		var recall struct {
			Memories []struct {
				Replaces []struct {
					ID string `json:"id"`
				} `json:"replaces"`
			} `json:"memories"`
			ContextPack struct {
				Text string `json:"text"`
			} `json:"context_pack"`
		}
		if err := json.Unmarshal([]byte(out.GetResultJson()), &recall); err != nil {
			t.Fatal(err)
		}
		if len(recall.Memories) == 0 || len(recall.Memories[0].Replaces) != 1 || !strings.Contains(recall.ContextPack.Text, `before: "The Athanor dev server listens on port 4173."`) {
			t.Errorf("embedder=%v: recall JSON %s", withEmbedder, out.GetResultJson())
		}
	}
}
