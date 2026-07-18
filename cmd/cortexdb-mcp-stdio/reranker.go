package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// newReranker builds a cortexdb.Reranker from CORTEXDB_RERANK_* env, or returns
// nil when no base URL is set (retrieval then uses the built-in heuristic
// rerank). It calls a /rerank endpoint in the shape shared by Cohere, Jina,
// vLLM, and Hugging Face text-embeddings-inference (TEI) — so a local
// bge-reranker via TEI, or a hosted reranker, both work.
//
//	CORTEXDB_RERANK_BASE_URL  reranker base URL (enables it). Examples:
//	                          http://localhost:8080  (local TEI bge-reranker)
//	                          https://api.cohere.com/v2
//	CORTEXDB_RERANK_MODEL     model name (sent when set; TEI ignores it)
//	CORTEXDB_RERANK_API_KEY   bearer token (optional; local TEI needs none)
func newReranker() cortexdb.Reranker {
	baseURL := firstEnv("CORTEXDB_RERANK_BASE_URL", "CORTEXDB_RERANK_URL")
	if baseURL == "" {
		return nil
	}
	return &httpReranker{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  firstEnv("CORTEXDB_RERANK_API_KEY"),
		model:   firstEnv("CORTEXDB_RERANK_MODEL"),
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

// httpReranker implements cortexdb.Reranker over a cross-encoder /rerank HTTP
// endpoint using plain net/http (no SDK).
type httpReranker struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

func (r *httpReranker) Rerank(ctx context.Context, query string, documents []string) ([]float64, error) {
	if len(documents) == 0 {
		return nil, nil
	}
	payload := map[string]any{
		"query":     query,
		"documents": documents,
	}
	if r.model != "" {
		payload["model"] = r.model
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rerank endpoint returned %s", resp.Status)
	}
	return parseRerankScores(resp.Body, len(documents))
}

// parseRerankScores handles the two response shapes these endpoints use:
//   - Cohere/Jina/vLLM: {"results":[{"index":i,"relevance_score":s}, …]}
//   - TEI:              [{"index":i,"score":s}, …]
//
// Both return only the requested indices (and may reorder), so results are
// scattered back into an aligned per-document slice.
func parseRerankScores(body io.Reader, n int) ([]float64, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	type entry struct {
		Index          int      `json:"index"`
		Score          *float64 `json:"score"`
		RelevanceScore *float64 `json:"relevance_score"`
	}
	scores := make([]float64, n)

	var wrapped struct {
		Results []entry `json:"results"`
	}
	var entries []entry
	if err := json.Unmarshal(raw, &wrapped); err == nil && len(wrapped.Results) > 0 {
		entries = wrapped.Results
	} else {
		var arr []entry
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, fmt.Errorf("parse rerank response: %w", err)
		}
		entries = arr
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("rerank response had no scores")
	}
	// Keep only entries with valid indices; align by index.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Index < entries[j].Index })
	seen := 0
	for _, e := range entries {
		if e.Index < 0 || e.Index >= n {
			continue
		}
		s := 0.0
		switch {
		case e.RelevanceScore != nil:
			s = *e.RelevanceScore
		case e.Score != nil:
			s = *e.Score
		}
		scores[e.Index] = s
		seen++
	}
	if seen == 0 {
		return nil, fmt.Errorf("rerank response had no aligned scores")
	}
	return scores, nil
}
