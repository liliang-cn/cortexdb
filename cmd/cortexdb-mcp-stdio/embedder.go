package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
//	CORTEXDB_EMBED_BATCH_SIZE                  texts per request (default 4)
//	CORTEXDB_EMBED_TIMEOUT_SECONDS             request timeout (default 300)
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
	if qt := newQueryTransformer(); qt != nil {
		opts = append(opts, cortexdb.WithQueryTransformer(qt))
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
	timeout := time.Duration(envIntOr("CORTEXDB_EMBED_TIMEOUT_SECONDS", 300)) * time.Second
	return &openAIEmbedder{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		dim:     dim,
		client:  &http.Client{Timeout: timeout},
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
	batchSize := envIntOr("CORTEXDB_EMBED_BATCH_SIZE", 4)
	if len(texts) > batchSize {
		out := make([][]float32, 0, len(texts))
		for start := 0; start < len(texts); start += batchSize {
			end := start + batchSize
			if end > len(texts) {
				end = len(texts)
			}
			batch, err := e.EmbedBatch(ctx, texts[start:end])
			if err != nil {
				return nil, fmt.Errorf("embedding batch %d-%d: %w", start, end, err)
			}
			out = append(out, batch...)
		}
		return out, nil
	}
	return e.embedAdaptive(ctx, texts)
}

func (e *openAIEmbedder) embedAdaptive(ctx context.Context, texts []string) ([][]float32, error) {
	vecs, err := e.embedRequest(ctx, texts)
	if err == nil {
		return vecs, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(texts) > 1 {
		mid := len(texts) / 2
		left, leftErr := e.embedAdaptive(ctx, texts[:mid])
		if leftErr != nil {
			return nil, leftErr
		}
		right, rightErr := e.embedAdaptive(ctx, texts[mid:])
		if rightErr != nil {
			return nil, rightErr
		}
		return append(left, right...), nil
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			timer := time.NewTimer(time.Duration(attempt-1) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		vecs, lastErr = e.embedRequest(ctx, texts)
		if lastErr == nil {
			return vecs, nil
		}
	}
	return nil, fmt.Errorf("embedding input failed after 3 attempts (%d chars): %w", len(texts[0]), lastErr)
}

func (e *openAIEmbedder) embedRequest(ctx context.Context, texts []string) ([][]float32, error) {
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
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
		return nil, fmt.Errorf("embeddings endpoint returned %s: %s", resp.Status, strings.TrimSpace(string(message)))
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
