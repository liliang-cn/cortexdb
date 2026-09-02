package main

// A QuerySource backed by Meilisearch, written with net/http and nothing else.
//
// It lives in examples/ rather than pkg/ on purpose, the same rule that keeps
// LLM SDKs out of the library: CortexDB defines the interface, and whoever has
// the search cluster brings the client. Copy this file into your own project
// and it is yours to change.
//
// What it does NOT do is as important as what it does. It returns ids and
// scores. The document text lives in CortexDB, and CortexDB is what hydrates
// the result, so this index going stale degrades recall — it can never put
// words into an answer that the brain never stored.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// MeiliSource is a cortexdb.QuerySource over one Meilisearch index.
type MeiliSource struct {
	BaseURL string // e.g. http://127.0.0.1:43530
	Index   string // e.g. cortexdb
	APIKey  string // master or search key; empty for a keyless dev server
	Client  *http.Client
}

func (m *MeiliSource) Name() string { return "meilisearch" }

func (m *MeiliSource) httpClient() *http.Client {
	if m.Client != nil {
		return m.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (m *MeiliSource) do(ctx context.Context, method, path string, body any, out any) error {
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(m.BaseURL, "/")+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.APIKey)
	}
	resp, err := m.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(buf.String()))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Search is the whole contract: text in, candidate ids out.
//
// Meilisearch ranks by its own relevance, which is not comparable to a cosine
// similarity — and does not need to be. Fusion ranks lanes against each other
// by position, so a lane only has to order its own candidates sensibly.
func (m *MeiliSource) Search(ctx context.Context, req cortexdb.QuerySourceRequest) ([]cortexdb.QuerySourceHit, error) {
	q := strings.TrimSpace(req.Query)
	if q == "" {
		return nil, nil
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	var out struct {
		Hits []struct {
			ID            string  `json:"id"`
			RankingScore  float64 `json:"_rankingScore"`
			SemanticScore float64 `json:"_semanticScore"`
		} `json:"hits"`
	}
	body := map[string]any{
		"q":                    q,
		"limit":                limit,
		"showRankingScore":     true,
		"attributesToRetrieve": []string{"id"},
	}
	if err := m.do(ctx, http.MethodPost, "/indexes/"+m.Index+"/search", body, &out); err != nil {
		return nil, err
	}
	hits := make([]cortexdb.QuerySourceHit, 0, len(out.Hits))
	for _, h := range out.Hits {
		score := h.RankingScore
		if score == 0 {
			score = h.SemanticScore
		}
		hits = append(hits, cortexdb.QuerySourceHit{ID: h.ID, Score: score})
	}
	return hits, nil
}

// Index pushes documents into Meilisearch. Only what the search engine needs to
// find them — the authoritative copy stays in CortexDB.
func (m *MeiliSource) Index_(ctx context.Context, docs []map[string]any) error {
	return m.do(ctx, http.MethodPost, "/indexes/"+m.Index+"/documents?primaryKey=id", docs, nil)
}

// WaitIdle blocks until Meilisearch has finished the tasks queued so far, so a
// demo does not search an index that is still being written.
func (m *MeiliSource) WaitIdle(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var out struct {
			Results []struct {
				Status string `json:"status"`
			} `json:"results"`
		}
		if err := m.do(ctx, http.MethodGet, "/tasks?limit=20", nil, &out); err != nil {
			return err
		}
		pending := false
		for _, r := range out.Results {
			if r.Status == "enqueued" || r.Status == "processing" {
				pending = true
				break
			}
		}
		if !pending {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("meilisearch still busy after %s", timeout)
}
