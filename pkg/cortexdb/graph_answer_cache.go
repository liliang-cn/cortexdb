package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// The answers that read the whole graph, kept until the graph changes.
//
// graph_schema enumerates the JSON keys of every node's properties and joins
// every edge to the types of its two ends; graph_property_values reads the
// properties of every node and edge; graph_statistics loads every edge to
// count the connected components. On a graph of 411,000 nodes and a million
// edges that is 8.6 s, 1.4 s and 1.0 s at each call. Agents call them to
// orient themselves, often at the start of every session, on a graph that has
// not changed since the last time anyone asked. So each answer is kept with
// the state of the graph it was computed on, and served while that state
// holds.
//
// Staleness is read from the file, as the PageRank cache reads it
// (graph_pagerank_cache.go): the highest rowid of graph_nodes and of
// graph_edges, which every insert raises, and a counter that triggers bump
// on a delete from either table and on an update of what these answers read
// (a node's id, type or properties; an edge's type, properties or ends). The
// triggers are part of the file, so a write by another process or by an
// older binary is seen too. An update of anything else (a node's content or
// vector, an edge's weight or time stamps) leaves the answers as they are and
// does not count. On PostgreSQL there are no triggers, and every call reads
// the graph.
//
// These tools answer for the whole store and are refused to a confined key
// (pkg/authz, allowlist.go), so an answer never depends on who asks.

const (
	graphAnswerVersionTable = "graph_answer_version"
	// graphAnswerCacheEntries bounds the answers kept; past it the cache
	// starts over.
	graphAnswerCacheEntries = 256
)

var graphAnswerSchema = []string{
	`CREATE TABLE IF NOT EXISTS ` + graphAnswerVersionTable + ` (id INTEGER PRIMARY KEY, version INTEGER NOT NULL)`,
	`INSERT OR IGNORE INTO ` + graphAnswerVersionTable + ` (id, version) VALUES (1, 0)`,
	`CREATE TRIGGER IF NOT EXISTS graph_answer_version_nodes_ad AFTER DELETE ON graph_nodes BEGIN
		UPDATE ` + graphAnswerVersionTable + ` SET version = version + 1 WHERE id = 1;
	END`,
	`CREATE TRIGGER IF NOT EXISTS graph_answer_version_nodes_au AFTER UPDATE OF id, node_type, properties ON graph_nodes
	WHEN OLD.id IS NOT NEW.id OR OLD.node_type IS NOT NEW.node_type OR OLD.properties IS NOT NEW.properties BEGIN
		UPDATE ` + graphAnswerVersionTable + ` SET version = version + 1 WHERE id = 1;
	END`,
	`CREATE TRIGGER IF NOT EXISTS graph_answer_version_edges_ad AFTER DELETE ON graph_edges BEGIN
		UPDATE ` + graphAnswerVersionTable + ` SET version = version + 1 WHERE id = 1;
	END`,
	`CREATE TRIGGER IF NOT EXISTS graph_answer_version_edges_au AFTER UPDATE OF edge_type, properties, from_node_id, to_node_id ON graph_edges
	WHEN OLD.edge_type IS NOT NEW.edge_type OR OLD.properties IS NOT NEW.properties
		OR OLD.from_node_id IS NOT NEW.from_node_id OR OLD.to_node_id IS NOT NEW.to_node_id BEGIN
		UPDATE ` + graphAnswerVersionTable + ` SET version = version + 1 WHERE id = 1;
	END`,
}

// graphAnswerCache is the answers kept, by tool and request, with the state
// of the graph they were computed on.
type graphAnswerCache struct {
	mu      sync.Mutex
	ready   bool
	state   graphAnswerState
	answers map[string][]byte
}

type graphAnswerState struct{ version, maxNode, maxEdge int64 }

// graphAnswerCacheReady installs the version counter once per DB, latching on
// success only. It reports false when the store cannot hold it: PostgreSQL,
// or a file this process may not write.
func (db *DB) graphAnswerCacheReady(ctx context.Context) bool {
	c := &db.graphAnswers
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := db.Dialect(); !c.ready && (d == nil || d.Kind() != sqldialect.Postgres) {
		c.ready = db.graph.InitGraphSchema(ctx) == nil && db.inTx(ctx, func(tx *sql.Tx) error {
			for _, stmt := range graphAnswerSchema {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		}) == nil
		c.answers = map[string][]byte{}
	}
	return c.ready
}

// cachedGraphAnswer serves the answer of tool to request from the cache while
// the graph is in the state it was computed on, and computes it otherwise.
// Each caller gets an answer of its own, decoded from the one kept.
func cachedGraphAnswer[T any](ctx context.Context, db *DB, tool string, request any, compute func() (*T, error)) (*T, error) {
	if !db.graphAnswerCacheReady(ctx) {
		return compute()
	}
	// The requests are plain structs of strings and numbers, which Marshal
	// cannot fail on.
	req, _ := json.Marshal(request)
	key := tool + "\x00" + string(req)
	var state graphAnswerState
	if err := db.queryRow(ctx, `SELECT (SELECT version FROM `+graphAnswerVersionTable+` WHERE id = 1),
		(SELECT COALESCE(MAX(rowid), 0) FROM graph_nodes),
		(SELECT COALESCE(MAX(rowid), 0) FROM graph_edges)`).Scan(&state.version, &state.maxNode, &state.maxEdge); err != nil {
		// A counter dropped since it was installed: the answer is computed,
		// as without a cache.
		return compute()
	}

	c := &db.graphAnswers
	c.mu.Lock()
	if c.state != state || len(c.answers) >= graphAnswerCacheEntries {
		c.state, c.answers = state, map[string][]byte{}
	}
	kept, ok := c.answers[key]
	c.mu.Unlock()
	if ok {
		defer observability.Time(ctx, "graph-answer-cache")()
		var out T
		if err := json.Unmarshal(kept, &out); err == nil {
			return &out, nil
		}
	}

	out, err := compute()
	if err != nil {
		return nil, err
	}
	// The answers are plain structs too.
	encoded, _ := json.Marshal(out)
	c.mu.Lock()
	defer c.mu.Unlock()
	// Kept only if the graph is still in the state the version named: a
	// write since then has made it stale, and the next call will see that.
	if c.state == state {
		c.answers[key] = encoded
	}
	return out, nil
}
