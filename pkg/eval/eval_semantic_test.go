package eval_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/eval"
)

// httpEmbedder is a minimal OpenAI-compatible embedder for the semantic eval.
type httpEmbedder struct {
	base, key, model string
	dim              int
	c                *http.Client
}

func (e *httpEmbedder) Dim() int { return e.dim }
func (e *httpEmbedder) Embed(ctx context.Context, t string) ([]float32, error) {
	v, err := e.batch(ctx, []string{t})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}
func (e *httpEmbedder) EmbedBatch(ctx context.Context, ts []string) ([][]float32, error) {
	return e.batch(ctx, ts)
}
func (e *httpEmbedder) batch(ctx context.Context, ts []string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": e.model, "input": ts})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.base+"/embeddings", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings %s", resp.Status)
	}
	var out struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	vs := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		v := make([]float32, len(d.Embedding))
		for j, x := range d.Embedding {
			v[j] = float32(x)
		}
		vs[i] = v
	}
	return vs, nil
}

// TestSemanticRetrievalQuality measures the semantic (vector) retrieval path
// against the same dataset, using an OpenAI-compatible embeddings endpoint. It
// is skipped unless CORTEXDB_EVAL_EMBED_BASE_URL and _API_KEY are set, so CI (no
// key) skips it while anyone with an endpoint can reproduce lexical-vs-semantic.
//
//	CORTEXDB_EVAL_EMBED_BASE_URL=https://.../compatible-mode/v1 \
//	CORTEXDB_EVAL_EMBED_API_KEY=sk-... \
//	CORTEXDB_EVAL_EMBED_MODEL=text-embedding-v3 CORTEXDB_EVAL_EMBED_DIM=1024 \
//	go test ./pkg/eval -run TestSemanticRetrievalQuality -v
func TestSemanticRetrievalQuality(t *testing.T) {
	base := os.Getenv("CORTEXDB_EVAL_EMBED_BASE_URL")
	key := os.Getenv("CORTEXDB_EVAL_EMBED_API_KEY")
	if base == "" || key == "" {
		t.Skip("set CORTEXDB_EVAL_EMBED_BASE_URL and CORTEXDB_EVAL_EMBED_API_KEY to run the semantic eval")
	}
	model := os.Getenv("CORTEXDB_EVAL_EMBED_MODEL")
	if model == "" {
		model = "text-embedding-v3"
	}
	dim := 1024
	if v := os.Getenv("CORTEXDB_EVAL_EMBED_DIM"); v != "" {
		fmt.Sscanf(v, "%d", &dim)
	}

	ds, err := eval.Builtin()
	if err != nil {
		t.Fatalf("dataset: %v", err)
	}
	emb := &httpEmbedder{base: base, key: key, model: model, dim: dim, c: &http.Client{Timeout: 30 * time.Second}}

	dbPath := fmt.Sprintf("test_semeval_%d.db", time.Now().UnixNano())
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath), cortexdb.WithEmbedder(emb))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, s := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + s)
		}
	})
	ctx := context.Background()
	for _, d := range ds.Documents {
		if _, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{KnowledgeID: d.ID, Title: d.Title, Content: d.Content}); err != nil {
			t.Fatalf("save %q: %v", d.ID, err)
		}
	}

	r := eval.RetrieverFunc(func(ctx context.Context, q string, k int) ([]string, error) {
		resp, err := db.SearchKnowledge(ctx, cortexdb.KnowledgeSearchRequest{Query: q, RetrievalMode: cortexdb.RetrievalModeAuto, TopK: k})
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(resp.Results))
		for _, hit := range resp.Results {
			ids = append(ids, hit.KnowledgeID)
		}
		return ids, nil
	})
	rep, err := eval.Run(ctx, ds, r, 1, 3, 5, 10)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Logf("semantic retrieval quality (%s):\n%s", model, rep.Summary())
	if rep.RecallAtK[10] < 0.70 {
		t.Errorf("semantic recall@10 = %.3f, below floor 0.70", rep.RecallAtK[10])
	}
}
