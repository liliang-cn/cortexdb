package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// openBrainDB opens the CortexDB database, wiring an OpenAI-compatible embedder
// when one is configured via the environment. Without embedder env vars it
// stays in no-embedder lexical mode (the default). This is the single open path
// used by the server and every one-shot mode, so semantic retrieval turns on
// consistently wherever the bundled binary points at an embeddings endpoint
// (e.g. a local Ollama: CORTEXDB_EMBED_BASE_URL=http://localhost:11434/v1).
//
// Env (CORTEXDB_EMBED_* preferred; OPENAI_* accepted for parity with
// cortexdb-grpc):
//
//	CORTEXDB_EMBED_BASE_URL / OPENAI_BASE_URL  embeddings base URL (enables it)
//	CORTEXDB_EMBED_API_KEY  / OPENAI_API_KEY   API key (Ollama accepts any)
//	CORTEXDB_EMBED_MODEL                       model (default text-embedding-3-small)
//	CORTEXDB_EMBED_DIM                         dimension (default 1536; embeddinggemma=768)
func openBrainDB(dbPath string) (*cortexdb.DB, error) {
	var opts []cortexdb.Option
	baseURL := firstEnv("CORTEXDB_EMBED_BASE_URL", "OPENAI_BASE_URL")
	if baseURL != "" {
		model := envOr("CORTEXDB_EMBED_MODEL", "text-embedding-3-small")
		dim := envIntOr("CORTEXDB_EMBED_DIM", 1536)
		apiKey := firstEnv("CORTEXDB_EMBED_API_KEY", "OPENAI_API_KEY")
		opts = append(opts, cortexdb.WithEmbedder(newOpenAIEmbedder(baseURL, apiKey, model, dim)))
	}
	if r := newReranker(); r != nil {
		opts = append(opts, cortexdb.WithReranker(r))
	}
	return cortexdb.Open(cortexdb.DefaultConfig(dbPath), opts...)
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envIntOr(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// openAIEmbedder calls any OpenAI-compatible /embeddings endpoint using plain
// net/http (deliberately no SDK dependency).
type openAIEmbedder struct {
	baseURL string
	apiKey  string
	model   string
	dim     int
	client  *http.Client
}

func newOpenAIEmbedder(baseURL, apiKey, model string, dim int) *openAIEmbedder {
	return &openAIEmbedder{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		dim:     dim,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (e *openAIEmbedder) Dim() int { return e.dim }

func (e *openAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (e *openAIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]any{"input": texts, "model": e.model})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings endpoint returned %s", resp.Status)
	}
	var parsed struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings endpoint returned %d vectors for %d inputs", len(parsed.Data), len(texts))
	}
	sort.Slice(parsed.Data, func(i, j int) bool { return parsed.Data[i].Index < parsed.Data[j].Index })
	out := make([][]float32, len(parsed.Data))
	for i, d := range parsed.Data {
		out[i] = d.Embedding
	}
	return out, nil
}
