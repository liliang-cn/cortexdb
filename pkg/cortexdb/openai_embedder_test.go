package cortexdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIEmbedder(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/embeddings" || r.Header.Get("Authorization") != "Bearer k" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		var in struct{ Input []string }
		json.NewDecoder(r.Body).Decode(&in)
		type d struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		}
		out := []d{}
		for i := len(in.Input) - 1; i >= 0; i-- { // out of order on purpose
			out = append(out, d{[]float32{float32(len(in.Input[i])), 0}, i})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": out})
	}))
	defer srv.Close()

	e := NewOpenAIEmbedder(OpenAIEmbedderConfig{BaseURL: srv.URL + "/v1/", APIKey: "k", Model: "m", Dim: 2, Batch: 2})
	vs, err := e.EmbedBatch(context.Background(), []string{"a", "bb", "ccc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 3 || vs[0][0] != 1 || vs[1][0] != 2 || vs[2][0] != 3 {
		t.Fatalf("vectors out of order: %v", vs)
	}
	if calls != 2 {
		t.Errorf("%d requests for 3 texts in batches of 2", calls)
	}
	if e.Dim() != 2 || e.Model() != "m" {
		t.Errorf("dim %d model %q", e.Dim(), e.Model())
	}
}

func TestEmbedderFromEnv(t *testing.T) {
	t.Setenv("CORTEXDB_EMBED_BASE_URL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	if EmbedderFromEnv() != nil {
		t.Fatal("an embedder with no endpoint configured")
	}
	t.Setenv("CORTEXDB_EMBED_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("CORTEXDB_EMBED_MODEL", "embeddinggemma")
	t.Setenv("CORTEXDB_EMBED_DIM", "768")
	e := EmbedderFromEnv()
	if e == nil || e.Dim() != 768 || e.Model() != "embeddinggemma" {
		t.Fatalf("%+v", e)
	}
}
