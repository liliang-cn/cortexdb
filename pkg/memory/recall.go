package memory

// recall.go implements the TEMPR four-channel retrieval pipeline and Reciprocal Rank Fusion (RRF).
//
// Channels (run concurrently):
//   T – Temporal:  graph-node search filtered by time_ref extracted from queryText
//   E – Embedding: cross-session semantic vector similarity over messages
//   M – (keyword)  BM25 FTS5 full-text search over messages
//   P – (graph)    Structural / relational graph search via HybridSearch
//
// Results from all channels are fused with RRF (k=60) and ranked by combined score.
// Higher-layer nodes (MentalModel > Observation > WorldFact > Experience) receive a
// small additive bonus so they surface above equally-scored lower-layer records.

import (
	"context"
	"database/sql"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/sqvect/v2/internal/encoding"
	"github.com/liliang-cn/sqvect/v2/pkg/core"
	"github.com/liliang-cn/sqvect/v2/pkg/graph"
)

// rrfK is the standard RRF constant from the original paper (Cormack et al., 2009).
const rrfK = 60

// layerBonus returns a small additive bonus applied after RRF to float higher-priority
// layers above equally-scored lower-layer records.
func layerBonus(l MemoryLayer) float64 {
	switch l {
	case LayerMentalModel:
		return 0.04
	case LayerObservation:
		return 0.03
	case LayerWorldFact:
		return 0.02
	default:
		return 0.0
	}
}

// channelItem is a single ranked result from one retrieval channel.
type channelItem struct {
	id      string
	content string
	layer   MemoryLayer
	meta    map[string]interface{}
	source  string
}

// rrfFuse merges ranked lists from multiple channels via Reciprocal Rank Fusion.
// Each item in channelLists[i][rank] contributes 1/(rrfK + rank + 1) to the combined score.
// Returns the top-k results sorted by descending RRF score.
func rrfFuse(channelLists [][]channelItem, topK int) []*RecallResult {
	type accumulator struct {
		score   float64
		sources map[string]struct{}
		item    channelItem
	}

	acc := make(map[string]*accumulator)

	for _, ranked := range channelLists {
		for rank, item := range ranked {
			contribution := 1.0 / float64(rrfK+rank+1)
			if a, ok := acc[item.id]; ok {
				a.score += contribution
				a.sources[item.source] = struct{}{}
			} else {
				acc[item.id] = &accumulator{
					score:   contribution,
					sources: map[string]struct{}{item.source: {}},
					item:    item,
				}
			}
		}
	}

	results := make([]*RecallResult, 0, len(acc))
	for _, a := range acc {
		layer := a.item.layer
		sources := make([]string, 0, len(a.sources))
		for s := range a.sources {
			sources = append(sources, s)
		}
		sort.Strings(sources) // deterministic output

		results = append(results, &RecallResult{
			ID:       a.item.id,
			Content:  a.item.content,
			Layer:    layer,
			RRFScore: a.score + layerBonus(layer),
			Sources:  sources,
			Metadata: a.item.meta,
		})
	}

	// Sort descending by RRFScore, then by ID for determinism.
	sort.Slice(results, func(i, j int) bool {
		if results[i].RRFScore != results[j].RRFScore {
			return results[i].RRFScore > results[j].RRFScore
		}
		return results[i].ID < results[j].ID
	})

	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}
	return results
}

// ---------------------------------------------------------------------------
// Channel E – Embedding (cross-session semantic search)
// ---------------------------------------------------------------------------

// recallSemantic searches all sessions belonging to userID for messages semantically
// similar to queryVec, excluding the current session (already covered by short-term).
func (m *MemoryManager) recallSemantic(ctx context.Context, userID string, queryVec []float32, excludeSession string, limit int) []channelItem {
	msgs, err := m.store.SearchMessagesByUser(ctx, userID, queryVec, excludeSession, limit)
	if err != nil || len(msgs) == 0 {
		return nil
	}

	items := make([]channelItem, 0, len(msgs))
	for _, msg := range msgs {
		items = append(items, channelItem{
			id:      "msg_" + msg.ID,
			content: msg.Content,
			layer:   LayerExperience,
			meta:    msg.Metadata,
			source:  "semantic",
		})
	}
	return items
}

// ---------------------------------------------------------------------------
// Channel M – keyword / BM25 FTS5
// ---------------------------------------------------------------------------

// recallKeyword performs BM25 full-text search over all messages for userID.
func (m *MemoryManager) recallKeyword(ctx context.Context, userID, queryText, excludeSession string, limit int) []channelItem {
	if strings.TrimSpace(queryText) == "" {
		return nil
	}
	msgs, err := m.store.KeywordSearchMessages(ctx, queryText, userID, excludeSession, limit)
	if err != nil || len(msgs) == 0 {
		return nil
	}

	items := make([]channelItem, 0, len(msgs))
	for _, msg := range msgs {
		items = append(items, channelItem{
			id:      "msg_" + msg.ID,
			content: msg.Content,
			layer:   LayerExperience,
			meta:    msg.Metadata,
			source:  "keyword",
		})
	}
	return items
}

// ---------------------------------------------------------------------------
// Channel P – graph / relational search
// ---------------------------------------------------------------------------

// recallGraph retrieves relevant fact nodes via hybrid (vector + graph) search,
// scoped to the given userID.
func (m *MemoryManager) recallGraph(ctx context.Context, userID string, queryVec []float32, limit int) []channelItem {
	if len(queryVec) == 0 {
		return nil
	}

	nodeTypes := []string{"world_fact", "observation", "mental_model", "experience"}
	results, err := m.graphStore.HybridSearch(ctx, &graph.HybridQuery{
		Vector:      queryVec,
		TopK:        limit,
		GraphFilter: &graph.GraphFilter{NodeTypes: nodeTypes},
		Weights: graph.HybridWeights{
			VectorWeight: 0.6,
			GraphWeight:  0.4,
		},
	})
	if err != nil || len(results) == 0 {
		return nil
	}

	items := make([]channelItem, 0, len(results))
	for _, res := range results {
		uid, _ := res.Node.Properties["user_id"].(string)
		if uid != userID {
			continue
		}
		items = append(items, channelItem{
			id:      res.Node.ID,
			content: res.Node.Content,
			layer:   nodeTypeToLayer(res.Node.NodeType),
			meta:    res.Node.Properties,
			source:  "graph",
		})
	}
	return items
}

// ---------------------------------------------------------------------------
// Channel T – Temporal
// ---------------------------------------------------------------------------

// temporalWindow holds a parsed time range derived from natural-language text.
type temporalWindow struct {
	start time.Time
	end   time.Time
}

// temporalPatterns maps human expressions to relative durations.
var temporalPatterns = []struct {
	re       *regexp.Regexp
	duration time.Duration
}{
	{regexp.MustCompile(`(?i)\byesterday\b`), 48 * time.Hour},
	{regexp.MustCompile(`(?i)\blast\s+day\b`), 48 * time.Hour},
	{regexp.MustCompile(`(?i)\blast\s+week\b`), 8 * 24 * time.Hour},
	{regexp.MustCompile(`(?i)\blast\s+month\b`), 31 * 24 * time.Hour},
	{regexp.MustCompile(`(?i)\blast\s+year\b`), 365 * 24 * time.Hour},
	{regexp.MustCompile(`(?i)\brecently\b`), 7 * 24 * time.Hour},
	{regexp.MustCompile(`(?i)\btoday\b`), 24 * time.Hour},
}

// parseTemporalWindow attempts to extract a time window from the query text.
// Returns nil if no temporal expression is detected.
func parseTemporalWindow(text string) *temporalWindow {
	now := time.Now().UTC()
	for _, p := range temporalPatterns {
		if p.re.MatchString(text) {
			return &temporalWindow{
				start: now.Add(-p.duration),
				end:   now,
			}
		}
	}
	return nil
}

// recallTemporal queries graph_nodes whose stored time_ref falls within the window
// parsed from queryText.  It falls back to a 7-day window when no expression is found.
func (m *MemoryManager) recallTemporal(ctx context.Context, userID, queryText string, limit int) []channelItem {
	win := parseTemporalWindow(queryText)
	if win == nil {
		// No temporal expression found; skip channel to avoid flooding results.
		return nil
	}

	db := m.store.GetDB()
	q := `
		SELECT id, vector, content, node_type, properties
		FROM graph_nodes
		WHERE json_extract(properties, '$.user_id') = ?
		  AND json_extract(properties, '$.time_ref') >= ?
		  AND json_extract(properties, '$.time_ref') <= ?
		ORDER BY json_extract(properties, '$.time_ref') DESC
		LIMIT ?
	`
	rows, err := db.QueryContext(ctx, q,
		userID,
		win.start.Format(time.RFC3339),
		win.end.Format(time.RFC3339),
		limit,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var items []channelItem
	for rows.Next() {
		var id, content, nodeType string
		var vBytes []byte
		var propsJSON sql.NullString

		if err := rows.Scan(&id, &vBytes, &content, &nodeType, &propsJSON); err != nil {
			continue
		}
		// Decode vector to verify it's valid (non-empty is sufficient here).
		if _, err := encoding.DecodeVector(vBytes); err != nil {
			continue
		}

		items = append(items, channelItem{
			id:      id,
			content: content,
			layer:   nodeTypeToLayer(nodeType),
			source:  "temporal",
		})
	}
	return items
}

// ---------------------------------------------------------------------------
// Recall – TEMPR orchestrator
// ---------------------------------------------------------------------------

// Recall performs a four-channel TEMPR retrieval fused with Reciprocal Rank Fusion (RRF).
//
//   - Short-term channel:  last 5 messages from the current session (not ranked, injected directly).
//   - Semantic channel:    cross-session vector similarity over messages.
//   - Keyword channel:     BM25 FTS5 full-text search over messages.
//   - Graph channel:       hybrid vector + relational search over fact nodes.
//   - Temporal channel:    time-range filtered fact nodes (only when query contains temporal cues).
//
// The returned MemoryContext populates both the new RankedMemories field and the legacy
// RelatedFacts / SemanticRecall fields for backward compatibility.
func (m *MemoryManager) Recall(ctx context.Context, userID, sessionID string, queryVec []float32, queryText string) (*MemoryContext, error) {
	const (
		channelLimit = 10
		topK         = 10
		historyLimit = 5
	)

	mc := &MemoryContext{
		RecentHistory:  make([]*core.Message, 0, historyLimit),
		RankedMemories: make([]*RecallResult, 0, topK),
		RelatedFacts:   make([]*graph.GraphNode, 0),
		SemanticRecall: make([]*core.Message, 0),
	}

	// 1. Short-term memory – always run regardless of vector availability.
	if sessionID != "" {
		hist, _ := m.store.GetSessionHistory(ctx, sessionID, historyLimit)
		mc.RecentHistory = hist
	}

	if userID == "" {
		return mc, nil
	}

	// 2. Run TEMPR channels concurrently.
	type chanResult struct {
		items []channelItem
	}

	semCh := make(chan chanResult, 1)
	kwCh := make(chan chanResult, 1)
	grCh := make(chan chanResult, 1)
	tpCh := make(chan chanResult, 1)

	go func() {
		semCh <- chanResult{m.recallSemantic(ctx, userID, queryVec, sessionID, channelLimit)}
	}()
	go func() {
		kwCh <- chanResult{m.recallKeyword(ctx, userID, queryText, sessionID, channelLimit)}
	}()
	go func() {
		grCh <- chanResult{m.recallGraph(ctx, userID, queryVec, channelLimit)}
	}()
	go func() {
		tpCh <- chanResult{m.recallTemporal(ctx, userID, queryText, channelLimit)}
	}()

	semItems := (<-semCh).items
	kwItems := (<-kwCh).items
	grItems := (<-grCh).items
	tpItems := (<-tpCh).items

	// 3. RRF fusion.
	mc.RankedMemories = rrfFuse([][]channelItem{semItems, kwItems, grItems, tpItems}, topK)

	// 4. Optional cross-encoder / LLM reranking.
	// If a RerankerFn is configured it post-processes the RRF list with a more expensive
	// but higher-quality ranking signal. On error we fall back to the RRF order rather
	// than failing the whole Recall operation.
	m.mu.RLock()
	rerankerFn := m.reranker
	m.mu.RUnlock()

	if rerankerFn != nil && len(mc.RankedMemories) > 1 {
		if reranked, err := rerankerFn(ctx, queryText, mc.RankedMemories); err == nil && len(reranked) > 0 {
			mc.RankedMemories = reranked
		}
		// Intentionally swallow reranker errors – Recall must always return a usable result.
	}

	// 5. Fill backward-compatible fields from fused results.
	for _, r := range mc.RankedMemories {
		if r.Layer == LayerExperience && strings.HasPrefix(r.ID, "msg_") {
			mc.SemanticRecall = append(mc.SemanticRecall, &core.Message{
				ID:      strings.TrimPrefix(r.ID, "msg_"),
				Content: r.Content,
			})
		} else {
			node := &graph.GraphNode{
				ID:         r.ID,
				Content:    r.Content,
				NodeType:   layerToNodeType(r.Layer),
				Properties: r.Metadata,
			}
			mc.RelatedFacts = append(mc.RelatedFacts, node)
		}
	}

	return mc, nil
}
