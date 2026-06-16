package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// dashscopeEmbedder implements cortexdb.Embedder against any OpenAI-compatible
// /embeddings endpoint (here: DashScope text-embedding-v4). It requests a fixed
// dimension so the CortexDB vector index has a stable shape.
type dashscopeEmbedder struct {
	baseURL string
	apiKey  string
	model   string
	dim     int
	http    *http.Client
}

func newDashscopeEmbedder(baseURL, apiKey, model string, dim int) *dashscopeEmbedder {
	return &dashscopeEmbedder{
		baseURL: baseURL, apiKey: apiKey, model: model, dim: dim,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// Dim returns the configured embedding dimension.
func (e *dashscopeEmbedder) Dim() int { return e.dim }

// Embed returns the embedding vector for one text.
func (e *dashscopeEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.embed(ctx, []any{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// EmbedBatch embeds multiple texts in one request.
func (e *dashscopeEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	in := make([]any, len(texts))
	for i, t := range texts {
		in[i] = t
	}
	return e.embed(ctx, in)
}

func (e *dashscopeEmbedder) embed(ctx context.Context, input []any) ([][]float32, error) {
	payload := map[string]any{"model": e.model, "input": input}
	if e.dim > 0 {
		payload["dimensions"] = e.dim // text-embedding-v4 supports configurable dims
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.http.Do(req)
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
	if len(out.Data) != len(input) {
		return nil, fmt.Errorf("embeddings: got %d vectors for %d inputs", len(out.Data), len(input))
	}
	vecs := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		v := make([]float32, len(d.Embedding))
		for j, f := range d.Embedding {
			v[j] = float32(f)
		}
		vecs[i] = v
	}
	return vecs, nil
}
