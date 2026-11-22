package cortexdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// synonymEmbedder maps several words to one dimension, so a chunk can be close
// to a query in vector space while sharing no word with it.
type synonymEmbedder struct{ dims map[string]int }

func (s synonymEmbedder) Dim() int { return 3 }

func (s synonymEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, 3)
	for _, token := range strings.Fields(strings.ToLower(text)) {
		if idx, ok := s.dims[strings.Trim(token, ".,!?;:\"'()")]; ok {
			vec[idx]++
		}
	}
	return vec, nil
}

func (s synonymEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := s.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		out[i] = vec
	}
	return out, nil
}

// One long document, one chunk per sentence: a synonym chunk (close in vector
// space, no word of the query), the target, and keyword chunks (every word of
// the query, a vector pointing elsewhere).
var perDocSentences = []string{
	"Automobile motor hums along nicely today.",
	"Automobile automobile automobile motor motor hums today.",
	"Car car engine hums along nicely today.",
	"Car engine bolt nut noise noise noise noise noise.",
	"Car engine bolt noise noise noise noise noise.",
}

const perDocTarget = "Car car engine hums along nicely today."

// For fusion: ranked by vectors, the synonym chunk, the target, two chunks of
// another document, then the keyword chunk; ranked by keywords, the keyword
// chunk, then the target. Fused, the target ranks first (1/62 + 1/62 against
// the keyword chunk's 1/61 + 1/65).
var fusionSentences = []string{
	"Automobile motor hums along nicely today.",
	perDocTarget,
	"Car engine bolt nut noise noise noise noise noise.",
}

var brochureSentences = []string{
	"Automobile automobile automobile motor shines brightly outdoors.",
	"Automobile automobile automobile motor gleams brightly outdoors.",
}

func openPerDocBrain(t *testing.T, docs map[string][]string) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), fmt.Sprintf("test_perdoc_%d.db", testname.Nano()))
	config := DefaultConfig(path)
	config.IndexType = core.IndexTypeFlat
	config.Dimensions = 3
	db, err := Open(config, WithEmbedder(synonymEmbedder{dims: map[string]int{
		"car": 0, "automobile": 0, "engine": 1, "motor": 1, "noise": 2,
	}}))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(path + suffix)
		}
	})
	for id, sentences := range docs {
		if _, err := db.SaveKnowledge(context.Background(), KnowledgeSaveRequest{
			KnowledgeID: id,
			Title:       id,
			Content:     strings.Join(sentences, " "),
			ChunkSize:   9,
		}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	return db
}

func hasChunk(chunks []GraphRAGChunkResult, content string) bool {
	for _, c := range chunks {
		if strings.TrimSpace(c.Content) == content {
			return true
		}
	}
	return false
}

// The per-document limit applies to the fused list, not to each retriever's
// list before fusion: capping each path at one kept the vector path's best and
// the keyword path's best, and dropped the chunk both ranked second, which
// fused ranks first (#239).
func TestHybridPerDocumentLimitAppliesAfterFusion(t *testing.T) {
	db := openPerDocBrain(t, map[string][]string{"manual": fusionSentences, "brochure": brochureSentences})
	resp, err := db.SearchKnowledge(context.Background(), KnowledgeSearchRequest{
		Query: "car engine bolt nut", TopK: 5, MaxContextChunks: 5,
		PerDocumentLimit: 1, RetrievalMode: RetrievalModeAuto, DisableRerank: true,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !hasChunk(resp.Chunks, perDocTarget) {
		t.Fatalf("the chunk fusion ranks first was dropped by a per-path cap; got %v", chunkContents(resp.Chunks))
	}
	manual := 0
	for _, c := range resp.Chunks {
		if strings.Contains(c.DocumentID, "manual") {
			manual++
		}
	}
	if manual > 1 {
		t.Fatalf("PerDocumentLimit 1 returned %d chunks of one document: %v", manual, chunkContents(resp.Chunks))
	}
}

// Unset, the per-document limit does not cap a long document at two chunks
// (#240): every chunk of the one document that matches can be returned.
func TestPerDocumentLimitDefaultsToNoCap(t *testing.T) {
	db := openPerDocBrain(t, map[string][]string{"manual": perDocSentences})
	resp, err := db.SearchKnowledge(context.Background(), KnowledgeSearchRequest{
		Query: "car engine bolt nut", TopK: 5, MaxContextChunks: 5,
		RetrievalMode: RetrievalModeLexical, DisableRerank: true,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if n := len(resp.Chunks); n < 3 {
		t.Fatalf("default per-document limit returned %d chunks of one document, want all 3 keyword matches: %v", n, chunkContents(resp.Chunks))
	}
	if !hasChunk(resp.Chunks, perDocTarget) {
		t.Fatalf("default per-document limit dropped a matching chunk: %v", chunkContents(resp.Chunks))
	}
}

func chunkContents(chunks []GraphRAGChunkResult) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = strings.TrimSpace(c.Content)
	}
	return out
}
