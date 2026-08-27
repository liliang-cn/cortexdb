package graph

// Vector search that happens in the database.
//
// Without this, a hybrid search on PostgreSQL falls back to GetAllNodes: every
// node in the table is loaded and scored in Go. That is fine for a brain with a
// few thousand nodes and untenable for the deployment this backend exists to
// serve. pgvector moves the top-k into SQL, where an index can serve it.
//
// It is optional on purpose. pgvector is an extension, and the account CortexDB
// connects with may not be allowed to create it. A missing extension therefore
// degrades to the scan that was already there and says so once, rather than
// refusing to start — the graph is still correct, just slower.
//
// Vectors live in their own table rather than as a second column on
// graph_nodes. graph_nodes.vector stays exactly as it is, encoded the same way
// on both backends, so nothing that reads a node changes and this whole file
// can be dropped by dropping one table.

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// pgvectorMaxIndexedDims is where pgvector stops being able to index.
//
// HNSW and IVFFlat both refuse a `vector` column wider than this (halfvec
// reaches 4000, bit 64000). It matters here because it is not hypothetical:
// the qwen3-embedding model on the t2m gateway is 4096-dimensional, so a brain
// configured with it gets exact search and no index — which is a real answer,
// but only if the system says so instead of quietly being slow.
const pgvectorMaxIndexedDims = 2000

// vectorCapability is what this backend can actually do, reported rather than
// assumed.
//
// The same shape as the sandbox backends in harness reporting what they really
// enforce: a caller that must guess whether its search is indexed will guess
// wrong, and the wrongness only shows up as latency under load.
type vectorCapability struct {
	// Enabled is true when pgvector is present and the vector table exists.
	Enabled bool
	// Indexed is true when an ANN index serves the search. False means exact
	// search: correct, and linear in table size.
	Indexed bool
	// Dimensions the table was created for; 0 when unconstrained.
	Dimensions int
	// Reason explains a false in either flag, in words meant for a human
	// reading a log line.
	Reason string
}

// initPgVector prepares in-database vector search, reporting what it managed.
//
// Never returns an error for a missing or forbidden extension: that is a
// deployment fact, not a fault, and the caller has a working fallback.
func (g *GraphStore) initPgVector(ctx context.Context, dim int) vectorCapability {
	if g.dialect == nil || g.dialect.Kind() != sqldialect.Postgres {
		return vectorCapability{Reason: "not a PostgreSQL backend"}
	}

	if _, err := g.db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		return vectorCapability{Reason: "pgvector unavailable: " + err.Error()}
	}

	// An unconstrained `vector` column accepts any width but cannot be
	// indexed, so a known dimension is worth having. dim <= 0 means the store
	// is still auto-detecting.
	colType := "vector"
	if dim > 0 {
		colType = fmt.Sprintf("vector(%d)", dim)
	}
	ddl := fmt.Sprintf(`
	CREATE TABLE IF NOT EXISTS graph_node_vectors (
		node_id   TEXT PRIMARY KEY REFERENCES graph_nodes(id) ON DELETE CASCADE,
		embedding %s NOT NULL
	)`, colType)
	if _, err := g.db.ExecContext(ctx, ddl); err != nil {
		return vectorCapability{Reason: "vector table: " + err.Error()}
	}

	cap := vectorCapability{Enabled: true, Dimensions: dim}
	switch {
	case dim <= 0:
		cap.Reason = "no fixed dimension: an index needs one, so search is exact"
		return cap
	case dim > pgvectorMaxIndexedDims:
		cap.Reason = fmt.Sprintf(
			"%d dimensions exceeds pgvector's %d-dimension index limit, so search is exact",
			dim, pgvectorMaxIndexedDims)
		return cap
	}

	// Cosine, to match the similarity the rest of the graph scores with.
	idx := `CREATE INDEX IF NOT EXISTS idx_graph_node_vectors_cos
	        ON graph_node_vectors USING hnsw (embedding vector_cosine_ops)`
	if _, err := g.db.ExecContext(ctx, idx); err != nil {
		cap.Reason = "index not built, search is exact: " + err.Error()
		return cap
	}
	cap.Indexed = true
	return cap
}

// pgUpsertVector mirrors a node's vector into the searchable table.
func (g *GraphStore) pgUpsertVector(ctx context.Context, nodeID string, vec []float32) error {
	if len(vec) == 0 {
		return nil
	}
	_, err := g.exec(ctx, `
		INSERT INTO graph_node_vectors (node_id, embedding) VALUES (?, ?)
		ON CONFLICT (node_id) DO UPDATE SET embedding = EXCLUDED.embedding`,
		nodeID, pgVectorLiteral(vec))
	return err
}

// pgVectorCandidates asks PostgreSQL for the nearest nodes.
//
// Only the ids and the ordering come from the database; the nodes themselves
// are loaded through the same path every other backend uses, so a result here
// is the same GraphNode a SQLite result would be.
func (g *GraphStore) pgVectorCandidates(ctx context.Context, query *HybridQuery, limit int) ([]*GraphNode, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := g.query(ctx, `
		SELECT node_id FROM graph_node_vectors
		ORDER BY embedding <=> ?
		LIMIT ?`, pgVectorLiteral(query.Vector), limit)
	if err != nil {
		return nil, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}

	byID, err := g.getNodesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	// The database's ordering is the answer; the map lookup must not reorder it.
	out := make([]*GraphNode, 0, len(ids))
	for _, id := range ids {
		node, ok := byID[id]
		if !ok {
			continue
		}
		if query.GraphFilter != nil && len(query.GraphFilter.NodeTypes) > 0 &&
			!contains(query.GraphFilter.NodeTypes, node.NodeType) {
			continue
		}
		out = append(out, node)
	}
	return out, nil
}

func scanIDs(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// pgVectorLiteral renders a vector the way pgvector parses it: [1,2,3].
//
// Sent as a bound parameter, so the text is a value and never SQL. Floats are
// formatted with 'g' and 32-bit precision, which round-trips a float32 exactly
// without spending bytes on digits that do not exist.
func pgVectorLiteral(vec []float32) string {
	var b strings.Builder
	b.Grow(len(vec)*8 + 2)
	b.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(v), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
