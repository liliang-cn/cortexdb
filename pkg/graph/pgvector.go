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
	"log"
	"math"
	"strconv"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
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

	// A lost CREATE EXTENSION race used to be recorded here as "pgvector
	// unavailable" and believed for the life of this store, so a momentary
	// collision at startup demoted every later search to an exact scan.
	if err := sqldialect.EnsureExtension(ctx, g.db, "vector"); err != nil {
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
	// The table may predate this store's settings, created at another width
	// by an earlier open (another embedder, or the 64 of an unset dimension):
	// what it accepts is its column's, not the config's. pgvector keeps a
	// vector column's width as its type modifier, -1 when it has none; a
	// column without one takes any width, and the config's then keeps the
	// table to one.
	var width int
	if err := g.db.QueryRowContext(ctx, `SELECT atttypmod FROM pg_attribute
		WHERE attrelid = 'graph_node_vectors'::regclass AND attname = 'embedding'`).Scan(&width); err == nil && width > 0 {
		dim = width
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

// mirrorable reports whether a vector can be in graph_node_vectors, whose
// column is dim wide (0: any width). pgvector refuses a vector of another
// width, and one holding NaN or an infinity, and inside a batch the refusal
// would abort the transaction and lose every node written with it.
func mirrorable(vec []float32, dim int) bool {
	if len(vec) == 0 || (dim > 0 && len(vec) != dim) {
		return false
	}
	for _, x := range vec {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return false
		}
	}
	return true
}

// pgUpsertVector mirrors a node's vector into the searchable table, inside tx
// when there is one. A node without a vector the table can hold is removed
// from it: a vector a node no longer has must not keep it findable by
// similarity, and one of another width than the table's (an embedder changed
// since the table was made) is left to the search in Go.
func (g *GraphStore) pgUpsertVector(ctx context.Context, tx *sql.Tx, nodeID string, vec []float32) error {
	exec := g.exec
	if tx != nil {
		exec = func(ctx context.Context, q string, args ...any) (sql.Result, error) {
			return g.txExec(ctx, tx, q, args...)
		}
	}
	if !mirrorable(vec, g.vecCap.Dimensions) {
		_, err := exec(ctx, `DELETE FROM graph_node_vectors WHERE node_id = ?`, nodeID)
		return err
	}
	_, err := exec(ctx, `
		INSERT INTO graph_node_vectors (node_id, embedding) VALUES (?, ?)
		ON CONFLICT (node_id) DO UPDATE SET embedding = EXCLUDED.embedding`,
		nodeID, pgVectorLiteral(vec))
	return err
}

// mirrorBackfillPage is how many nodes pgBackfillVectors reads at a time.
var mirrorBackfillPage = 1000

// mirrorStoredVectors runs pgBackfillVectors when the store opens, and logs
// what it did. Batch writes did not keep the mirror before; a store they
// wrote is mirrored here, once. Not fatal, like the rest of the mirror: a node
// it misses is still found by the exact scan while the mirror is empty.
func (g *GraphStore) mirrorStoredVectors(ctx context.Context) {
	mirrored, skipped, err := g.pgBackfillVectors(ctx, g.vecCap.Dimensions)
	if err != nil {
		log.Printf("cortexdb/graph: mirroring stored vectors for search: %v", err)
	}
	if mirrored+skipped > 0 {
		log.Printf("cortexdb/graph: %d stored vectors mirrored for search, %d corrupt or not finite skipped", mirrored, skipped)
	}
}

// pgBackfillVectors mirrors the nodes that have a vector of the table's width
// and no row in graph_node_vectors, which is what batch writes left before
// they kept the mirror. The first open of such a store pays it; later ones
// read only the rows it skipped. Only vectors whose stored bytes have the
// width's length are read; one whose header still disagrees with dim is
// corrupt, and one holding NaN or an infinity pgvector refuses: both are
// skipped and counted, and the pass goes on.
//
// A row a concurrent write puts in first is left as it is: that write has the
// node's current vector, and this pass may have read an older one.
func (g *GraphStore) pgBackfillVectors(ctx context.Context, dim int) (mirrored, skipped int, err error) {
	// An empty vector is encoded as its 4-byte length alone.
	shortest, longest := 5, math.MaxInt32
	if dim > 0 {
		shortest, longest = 4+4*dim, 4+4*dim
	}
	last := ""
	for {
		page, err := g.unmirroredPage(ctx, last, shortest, longest)
		if err != nil {
			return mirrored, skipped, err
		}
		// Written after the page is read, so the scan holds no connection
		// while the writes need one.
		for _, p := range page {
			last = p.id
			vec, derr := encoding.DecodeVector(p.blob)
			if derr != nil || !mirrorable(vec, dim) {
				skipped++
				continue
			}
			res, err := g.exec(ctx, `
				INSERT INTO graph_node_vectors (node_id, embedding) VALUES (?, ?)
				ON CONFLICT (node_id) DO NOTHING`, p.id, pgVectorLiteral(vec))
			if err != nil {
				return mirrored, skipped, err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				mirrored++
			}
		}
		if len(page) < mirrorBackfillPage {
			return mirrored, skipped, nil
		}
	}
}

type storedVector struct {
	id   string
	blob []byte
}

// unmirroredPage reads the next nodes after the id last, in id order, that
// have no mirror row and a stored vector of shortest to longest bytes.
func (g *GraphStore) unmirroredPage(ctx context.Context, last string, shortest, longest int) ([]storedVector, error) {
	rows, err := g.query(ctx, `
		SELECT n.id, n.vector FROM graph_nodes n
		LEFT JOIN graph_node_vectors m ON m.node_id = n.id
		WHERE m.node_id IS NULL AND octet_length(n.vector) BETWEEN ? AND ? AND n.id > ?
		ORDER BY n.id LIMIT ?`, shortest, longest, last, mirrorBackfillPage)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var page []storedVector
	for rows.Next() {
		var v storedVector
		if err := rows.Scan(&v.id, &v.blob); err != nil {
			return nil, err
		}
		page = append(page, v)
	}
	return page, rows.Err()
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
