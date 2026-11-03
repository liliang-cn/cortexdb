package cortexdb

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

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
)

// OpenAIEmbedder calls any OpenAI-compatible /embeddings endpoint using plain
// net/http (deliberately no SDK dependency).
type OpenAIEmbedder struct {
	baseURL string
	apiKey  string
	model   string
	dim     int
	batch   int
	client  *http.Client
}

// OpenAIEmbedderConfig says where an OpenAIEmbedder sends texts and what comes back.
type OpenAIEmbedderConfig struct {
	BaseURL string // e.g. http://localhost:11434/v1 for a local Ollama
	APIKey  string // sent as a bearer token when set (Ollama needs none)
	Model   string
	Dim     int           // the model's vector size (embeddinggemma: 768)
	Batch   int           // texts per request, 4 when 0
	Timeout time.Duration // per request, 5 minutes when 0
}

// NewOpenAIEmbedder embeds through any OpenAI-compatible /embeddings endpoint: OpenAI, Ollama,
// vLLM, LM Studio and the like.
func NewOpenAIEmbedder(cfg OpenAIEmbedderConfig) *OpenAIEmbedder {
	if cfg.Batch <= 0 {
		cfg.Batch = 4
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Minute
	}
	return &OpenAIEmbedder{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		dim:     cfg.Dim,
		batch:   cfg.Batch,
		client:  &http.Client{Timeout: cfg.Timeout},
	}
}

// EmbedderFromEnv is the embedder the environment configures, nil when none is (CortexDB then
// searches by words and the graph). The same variables cortexdb-mcp reads, so a store written by
// one program is searched by another with the same model:
//
//	CORTEXDB_EMBED_BASE_URL / OPENAI_BASE_URL  the endpoint (setting it turns embedding on)
//	CORTEXDB_EMBED_API_KEY  / OPENAI_API_KEY   its key, if it needs one
//	CORTEXDB_EMBED_MODEL                       the model (default text-embedding-3-small)
//	CORTEXDB_EMBED_DIM                         its vector size (default 1536; embeddinggemma 768)
//	CORTEXDB_EMBED_BATCH_SIZE                  texts per request (default 4)
//	CORTEXDB_EMBED_TIMEOUT_SECONDS             per request (default 300)
func EmbedderFromEnv() *OpenAIEmbedder {
	base := firstEnv("CORTEXDB_EMBED_BASE_URL", "OPENAI_BASE_URL")
	if base == "" {
		return nil
	}
	return NewOpenAIEmbedder(OpenAIEmbedderConfig{
		BaseURL: base,
		APIKey:  firstEnv("CORTEXDB_EMBED_API_KEY", "OPENAI_API_KEY"),
		Model:   cmpOr(firstEnv("CORTEXDB_EMBED_MODEL"), "text-embedding-3-small"),
		Dim:     envInt("CORTEXDB_EMBED_DIM", 1536),
		Batch:   envInt("CORTEXDB_EMBED_BATCH_SIZE", 4),
		Timeout: time.Duration(envInt("CORTEXDB_EMBED_TIMEOUT_SECONDS", 300)) * time.Second,
	})
}

// Model is the model the embedder asks for.
func (e *OpenAIEmbedder) Model() string { return e.model }

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil && n > 0 {
		return n
	}
	return def
}

func (e *OpenAIEmbedder) Dim() int { return e.dim }

func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (e *OpenAIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	batchSize := e.batch
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

func (e *OpenAIEmbedder) embedAdaptive(ctx context.Context, texts []string) ([][]float32, error) {
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

func (e *OpenAIEmbedder) embedRequest(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]any{"input": texts, "model": e.model})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	observability.InjectHTTP(ctx, req.Header)
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
