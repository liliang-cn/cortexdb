package cortexdb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestSearchKnowledgeLexicalNoEmbedder guards the no-embedder retrieval path:
// a knowledge item saved via SaveKnowledge must be retrievable via
// SearchKnowledge in lexical mode, both with planner-supplied keywords and
// with a bare query. This is the path the cortexdb MCP server uses by default
// (lexical, no API key), so a regression here silently breaks recall.
func TestSearchKnowledgeLexicalNoEmbedder(t *testing.T) {
	ctx := context.Background()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "lexical.db"))) // no WithEmbedder
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if db.HasEmbedder() {
		t.Fatal("test must run without an embedder")
	}

	const knowledgeID = "apollo"
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: knowledgeID,
		Title:       "Apollo",
		Content:     "Alice owns the Apollo project. Apollo ships on Friday.",
		ChunkSize:   40,
	}); err != nil {
		t.Fatalf("save knowledge: %v", err)
	}

	cases := []struct {
		name string
		req  KnowledgeSearchRequest
	}{
		{
			name: "lexical with keywords",
			req: KnowledgeSearchRequest{
				Query:         "Who owns Apollo?",
				Keywords:      []string{"Apollo", "Alice", "owns"},
				RetrievalMode: RetrievalModeLexical,
				TopK:          3,
			},
		},
		{
			name: "lexical bare query",
			req: KnowledgeSearchRequest{
				Query:         "Apollo Alice owns",
				RetrievalMode: RetrievalModeLexical,
				TopK:          3,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := db.SearchKnowledge(ctx, tc.req)
			if err != nil {
				t.Fatalf("search knowledge: %v", err)
			}
			if resp.Decision.EffectiveMode != RetrievalModeLexical {
				t.Fatalf("expected lexical mode, got %q", resp.Decision.EffectiveMode)
			}
			if len(resp.Results) == 0 {
				t.Fatalf("expected at least one knowledge hit, got none (chunks=%d)", len(resp.Chunks))
			}
			found := false
			for _, hit := range resp.Results {
				if hit.KnowledgeID == knowledgeID {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected hit for knowledge_id %q, results=%+v", knowledgeID, resp.Results)
			}
			if len(resp.Chunks) == 0 || !strings.Contains(resp.Chunks[0].Content, "Apollo") {
				t.Fatalf("expected retrieved chunk to contain the saved content, chunks=%+v", resp.Chunks)
			}
		})
	}
}
