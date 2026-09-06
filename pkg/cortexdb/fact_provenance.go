package cortexdb

// Walking back from a fact to the words that produced it.
//
// The provenance was already being written. Every relation edge carries its
// document_id, its chunk_ids, whether it was inferred and by which rule — put
// there by graphrag_tool_ingest and then read by nothing. Data with no way out
// is the same as no data: the graph could say "Leo works at LINBIT" and there
// was no query that answered "says who?".
//
// That question is the whole of citation checking. A fact whose supporting
// text no longer says what the fact says is the failure mode that matters in a
// knowledge base — not a missing row, a stale one that still sounds right —
// and it cannot be looked for until the text can be reached from the fact.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// FactProvenance is where one edge came from.
type FactProvenance struct {
	EdgeID     string   `json:"edge_id"`
	From       string   `json:"from"`
	To         string   `json:"to"`
	Type       string   `json:"type"`
	DocumentID string   `json:"document_id,omitempty"`
	ChunkIDs   []string `json:"chunk_ids,omitempty"`
	// Inferred says the fact was derived rather than read, in which case Rule
	// names the derivation and the chunks below support the premises, not this
	// conclusion.
	Inferred bool   `json:"inferred,omitempty"`
	Rule     string `json:"rule,omitempty"`
	// Source is free-text provenance an ingester attached.
	Source string `json:"source,omitempty"`
	// Chunks is the supporting text itself, when it was asked for and still
	// exists. A chunk id that no longer resolves is reported in Missing rather
	// than dropped: a citation pointing at deleted text is exactly the thing
	// worth surfacing.
	Chunks  []ToolChunk `json:"chunks,omitempty"`
	Missing []string    `json:"missing_chunk_ids,omitempty"`
}

// Cited reports whether anything at all backs this fact.
//
// An inferred fact with no supporting chunks is still accounted for — its rule
// is its account — but a stated fact with neither a document nor a chunk was
// written by something that did not say where it got it.
func (p FactProvenance) Cited() bool {
	return len(p.ChunkIDs) > 0 || p.DocumentID != "" || (p.Inferred && p.Rule != "")
}

// FactProvenanceFor returns where an edge came from, optionally loading the
// supporting text.
//
// withText is a parameter rather than always-on because the two questions have
// different costs: "is this cited at all" is one row, and "show me the words"
// is a second query plus the chunk bodies.
func (db *DB) FactProvenanceFor(ctx context.Context, edgeID string, withText bool) (*FactProvenance, error) {
	if db == nil || strings.TrimSpace(edgeID) == "" {
		return nil, fmt.Errorf("cortexdb: fact provenance: edge id is required")
	}
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("cortexdb: fact provenance: %w", err)
	}

	var (
		from, to string
		edgeType string
		propsRaw string
	)
	// The edge source rather than the table, so a retracted fact can still say
	// where it came from. That is the point of retracting instead of deleting:
	// "says who?" about a withdrawn claim is exactly the question an audit
	// asks, and under graph.AsOf(ctx, before) this resolves it. Without an
	// as-of on the context this is the bare table and the query is unchanged.
	src, srcArgs := db.Graph().EdgeSource(ctx)
	err := db.queryRow(ctx, db.Dialect().Rebind(`
		SELECT from_node_id, to_node_id, COALESCE(edge_type, ''), COALESCE(properties, '')
		FROM `+src+` AS e WHERE id = ?`), append(srcArgs, edgeID)...).Scan(&from, &to, &edgeType, &propsRaw)
	if err != nil {
		return nil, fmt.Errorf("cortexdb: fact provenance: %w", err)
	}

	out := &FactProvenance{EdgeID: edgeID, From: from, To: to, Type: edgeType}
	if propsRaw != "" {
		var props map[string]any
		if err := json.Unmarshal([]byte(propsRaw), &props); err != nil {
			// Malformed properties are reported as "no provenance" rather than
			// as an error: the edge is still a real fact, it just cannot say
			// where it came from, which is what the caller needs to know.
			return out, nil
		}
		out.DocumentID, _ = props["document_id"].(string)
		out.Source, _ = props["provenance"].(string)
		out.Rule, _ = props["rule_id"].(string)
		out.Inferred, _ = props["inferred"].(bool)
		out.ChunkIDs = stringsFromAny(props["chunk_ids"])
	}

	if !withText || len(out.ChunkIDs) == 0 {
		return out, nil
	}

	chunks, err := db.GraphRAGTools().GetChunks(ctx, ToolGetChunksRequest{
		ChunkIDs:     out.ChunkIDs,
		DisableGraph: true, // the text is the point; the graph is where we came from
	})
	if err != nil {
		return nil, fmt.Errorf("cortexdb: fact provenance: load chunks: %w", err)
	}
	found := map[string]bool{}
	for _, c := range chunks.Chunks {
		found[c.ID] = true
	}
	out.Chunks = chunks.Chunks
	for _, id := range out.ChunkIDs {
		if !found[id] {
			out.Missing = append(out.Missing, id)
		}
	}
	return out, nil
}

// stringsFromAny reads a JSON array of strings back out of a decoded property.
func stringsFromAny(v any) []string {
	switch t := v.(type) {
	case []string:
		return append([]string(nil), t...)
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if t == "" {
			return nil
		}
		var arr []string
		if err := json.Unmarshal([]byte(t), &arr); err == nil {
			return arr
		}
		return []string{t}
	}
	return nil
}

// UncitedFacts lists edges that cannot say where they came from.
//
// The single lookup above answers "says who?" for one fact. This is the
// question a knowledge base has to be able to ask about itself: how much of
// what I am about to tell someone is backed by anything?
//
// An edge counts as cited when it carries supporting chunks, a document, or —
// for a derived fact — the rule that derived it. Nothing else is required:
// this reports what is missing, it does not judge whether the citation is any
// good. Checking that the text still says what the fact says needs the text,
// which is what FactProvenanceFor is for.
func (db *DB) UncitedFacts(ctx context.Context, limit int) ([]FactProvenance, error) {
	if db == nil {
		return nil, fmt.Errorf("cortexdb: uncited facts: nil db")
	}
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("cortexdb: uncited facts: %w", err)
	}
	if limit <= 0 {
		limit = 100
	}

	// Filtered in SQL so a brain with a million cited edges does not have to
	// hand all of them to Go to find the few that are not. Asked of the
	// dialect because json_extract is SQLite's alone.
	d := db.Dialect()
	chunkIDs := d.JSONText("properties", "chunk_ids")
	docID := d.JSONText("properties", "document_id")
	ruleID := d.JSONText("properties", "rule_id")

	src, srcArgs := db.Graph().EdgeSource(ctx)
	query := d.Rebind(`
		SELECT id, from_node_id, to_node_id, COALESCE(edge_type, '')
		FROM ` + src + ` AS e
		WHERE (` + chunkIDs + ` IS NULL OR ` + chunkIDs + ` IN ('', '[]'))
		  AND (` + docID + ` IS NULL OR ` + docID + ` = '')
		  AND (` + ruleID + ` IS NULL OR ` + ruleID + ` = '')
		ORDER BY id
		LIMIT ?`)

	rows, err := db.query(ctx, query, append(srcArgs, limit)...)
	if err != nil {
		return nil, fmt.Errorf("cortexdb: uncited facts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]FactProvenance, 0)
	for rows.Next() {
		var p FactProvenance
		if err := rows.Scan(&p.EdgeID, &p.From, &p.To, &p.Type); err != nil {
			return nil, fmt.Errorf("cortexdb: uncited facts: scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- MCP request/response shapes ---------------------------------------------

// ToolFactProvenanceRequest asks where one edge came from.
type ToolFactProvenanceRequest struct {
	EdgeID   string `json:"edge_id"`
	WithText bool   `json:"with_text,omitempty"`
}

// ToolFactProvenanceResponse is the edge's account of itself.
type ToolFactProvenanceResponse struct {
	Provenance FactProvenance `json:"provenance"`
	// Cited is carried explicitly rather than left to the caller to work out
	// from the fields, so a model asking "is this backed by anything" gets an
	// answer instead of a rule to apply.
	Cited bool `json:"cited"`
}

// ToolUncitedFactsRequest sweeps for facts with no source.
type ToolUncitedFactsRequest struct {
	Limit int `json:"limit,omitempty"`
}

// ToolUncitedFactsResponse lists them.
type ToolUncitedFactsResponse struct {
	Facts []FactProvenance `json:"facts"`
	Count int              `json:"count"`
	// Truncated says the limit cut the list short, so "12 uncited facts" is
	// never mistaken for "only 12 uncited facts".
	Truncated bool `json:"truncated,omitempty"`
}

// FactProvenanceTool serves the MCP tool of the same name.
func (db *DB) FactProvenanceTool(ctx context.Context, req ToolFactProvenanceRequest) (ToolFactProvenanceResponse, error) {
	prov, err := db.FactProvenanceFor(ctx, req.EdgeID, req.WithText)
	if err != nil {
		return ToolFactProvenanceResponse{}, err
	}
	return ToolFactProvenanceResponse{Provenance: *prov, Cited: prov.Cited()}, nil
}

// UncitedFactsTool serves the MCP tool of the same name.
func (db *DB) UncitedFactsTool(ctx context.Context, req ToolUncitedFactsRequest) (ToolUncitedFactsResponse, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	facts, err := db.UncitedFacts(ctx, limit)
	if err != nil {
		return ToolUncitedFactsResponse{}, err
	}
	return ToolUncitedFactsResponse{
		Facts:     facts,
		Count:     len(facts),
		Truncated: len(facts) == limit,
	}, nil
}
