package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
	"log"
	"strings"
	"sync"
	"time"
)

// GraphNode represents a node in the graph with vector embedding
type GraphNode struct {
	ID         string                 `json:"id"`
	Vector     []float32              `json:"vector"`
	Content    string                 `json:"content,omitempty"`
	NodeType   string                 `json:"node_type,omitempty"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
}

// GraphEdge represents a directed edge between two nodes
type GraphEdge struct {
	ID         string                 `json:"id"`
	FromNodeID string                 `json:"from_node_id"`
	ToNodeID   string                 `json:"to_node_id"`
	EdgeType   string                 `json:"edge_type,omitempty"`
	Weight     float64                `json:"weight"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	Vector     []float32              `json:"vector,omitempty"` // Optional edge embedding
	CreatedAt  time.Time              `json:"created_at"`
}

// GraphFilter defines filtering options for graph queries
type GraphFilter struct {
	NodeTypes []string `json:"node_types,omitempty"`
	EdgeTypes []string `json:"edge_types,omitempty"`
	MaxDepth  int      `json:"max_depth,omitempty"`
}

// HybridQuery represents a combined vector and graph query
type HybridQuery struct {
	Vector          []float32     `json:"vector,omitempty"`
	StartNodeID     string        `json:"start_node_id,omitempty"`
	CenterNodes     []string      `json:"center_nodes,omitempty"`
	GraphFilter     *GraphFilter  `json:"graph_filter,omitempty"`
	TopK            int           `json:"top_k"`
	Threshold       float64       `json:"threshold,omitempty"`
	VectorThreshold float64       `json:"vector_threshold,omitempty"`
	TotalThreshold  float64       `json:"total_threshold,omitempty"`
	VectorWeight    float64       `json:"vector_weight"`
	GraphWeight     float64       `json:"graph_weight"`
	Weights         HybridWeights `json:"weights"`
}

// HybridWeights defines the weights for hybrid scoring
type HybridWeights struct {
	VectorWeight float64 `json:"vector_weight"` // Weight for vector similarity
	GraphWeight  float64 `json:"graph_weight"`  // Weight for graph proximity
	EdgeWeight   float64 `json:"edge_weight"`   // Weight for edge strength
}

// HybridResult represents a result from hybrid search
type HybridResult struct {
	Node          *GraphNode `json:"node"`
	VectorScore   float64    `json:"vector_score"`
	GraphScore    float64    `json:"graph_score"`
	CombinedScore float64    `json:"combined_score"`
	TotalScore    float64    `json:"total_score"`
	Path          []string   `json:"path,omitempty"` // Path from start node
	Distance      int        `json:"distance"`       // Graph distance from start
}

// vectorHost is the part of the underlying store the graph actually needs.
//
// Narrowed from *core.SQLiteStore to two methods because that is all the graph
// ever called — and because a PostgreSQL-backed graph has no SQLiteStore to
// hand it. Widen it only when the graph genuinely needs more.
type vectorHost interface {
	GetSimilarityFunc() core.SimilarityFunc
	Config() core.Config
}

// GraphStore provides graph operations on top of the vector store
type GraphStore struct {
	store vectorHost
	db    *sql.DB
	// dialect spells the parts of a query the two databases disagree about.
	// See pkg/sqldialect: placeholders, the blob type, and what "this column
	// already exists" sounds like.
	dialect   sqldialect.Dialect
	hnswIndex *HNSWGraphIndex // HNSW index for fast vector search
	// Guards one-time creation of the graph schema. A mutex plus a flag rather
	// than sync.Once, because Once latches on panic-free completion even when
	// the work failed: a single cancelled context would otherwise leave this
	// store convinced the tables exist for the rest of its lifetime.
	schemaMu    sync.Mutex
	schemaReady bool

	// vecCap is what in-database vector search can do here, decided once when
	// the schema is created. The zero value — everything false — is the
	// SQLite backend and any PostgreSQL without pgvector, both of which fall
	// back to the scan that was always there.
	vecCap vectorCapability
}

// NewGraphStore creates a new graph store from a SQLite store.
func NewGraphStore(s *core.SQLiteStore) *GraphStore {
	return &GraphStore{
		store:   s,
		db:      s.GetDB(),
		dialect: sqldialect.For(sqldialect.SQLite),
	}
}

// NewGraphStoreOn creates a graph store over any database this dialect can
// speak, for a host that supplies the vector-side settings.
//
// The same tables and the same queries as NewGraphStore — the graph layer has
// no SQLite-only SQL in it, which is what makes one implementation over two
// databases possible rather than two implementations to keep in step.
func NewGraphStoreOn(db *sql.DB, d sqldialect.Dialect, host vectorHost) *GraphStore {
	if d == nil {
		d = sqldialect.For(sqldialect.SQLite)
	}
	return &GraphStore{store: host, db: db, dialect: d}
}

// exec, query and queryRow run a statement with this store's placeholder
// style. Every call site goes through them so that `?` can stay in the SQL:
// the queries read the same in both worlds and only this layer knows the
// difference.
func (g *GraphStore) exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return g.db.ExecContext(ctx, g.dialect.Rebind(q), args...)
}

func (g *GraphStore) query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return g.db.QueryContext(ctx, g.dialect.Rebind(q), args...)
}

func (g *GraphStore) queryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return g.db.QueryRowContext(ctx, g.dialect.Rebind(q), args...)
}

// txPrepare is the same for a statement prepared once and executed many times.
//
// Missed on the first pass, which cost an afternoon: rebinding exec and query
// leaves prepare untouched, and a batch insert is exactly the path that
// prepares. The symptom was a syntax error at the first ingest — every write
// through the batch path failed on PostgreSQL while the single-row path
// worked, which reads as something much stranger than a missing rebind.
func (g *GraphStore) txPrepare(ctx context.Context, tx *sql.Tx, q string) (*sql.Stmt, error) {
	return tx.PrepareContext(ctx, g.dialect.Rebind(q))
}

// txExec is the same for a statement inside a transaction.
func (g *GraphStore) txExec(ctx context.Context, tx *sql.Tx, q string, args ...any) (sql.Result, error) {
	return tx.ExecContext(ctx, g.dialect.Rebind(q), args...)
}

func (g *GraphStore) txQueryRow(ctx context.Context, tx *sql.Tx, q string, args ...any) *sql.Row {
	return tx.QueryRowContext(ctx, g.dialect.Rebind(q), args...)
}

// InitGraphSchema creates the graph tables if they don't exist.
//
// It is cheap to call repeatedly: after the first success this store remembers
// that its schema is ready and returns without touching SQLite, so write paths
// can guard themselves with it instead of trusting the caller to have done so.
func (g *GraphStore) InitGraphSchema(ctx context.Context) error {
	g.schemaMu.Lock()
	defer g.schemaMu.Unlock()
	if g.schemaReady {
		return nil
	}
	if err := g.createGraphSchema(ctx); err != nil {
		return err
	}
	// Optional, and never fatal: a backend without pgvector still works, it
	// just scans. Logged once, here, because a silently linear search is the
	// kind of thing that is only discovered under load.
	g.vecCap = g.initPgVector(ctx, g.rdfVectorDim())
	if g.vecCap.Enabled && !g.vecCap.Indexed {
		log.Printf("cortexdb/graph: pgvector enabled without an index — %s", g.vecCap.Reason)
	}
	g.schemaReady = true
	return nil
}

// createGraphSchema issues the DDL. Callers must hold schemaMu.
func (g *GraphStore) createGraphSchema(ctx context.Context) error {
	blob := g.dialect.BlobType()
	schema := fmt.Sprintf(`
	-- Graph nodes table (extends embeddings concept)
	CREATE TABLE IF NOT EXISTS graph_nodes (
		id TEXT PRIMARY KEY,
		vector %[1]s NOT NULL,
		content TEXT,
		node_type TEXT,
		properties TEXT, -- JSON
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	-- Graph edges table
	CREATE TABLE IF NOT EXISTS graph_edges (
		id TEXT PRIMARY KEY,
		from_node_id TEXT NOT NULL,
		to_node_id TEXT NOT NULL,
		edge_type TEXT,
		weight REAL DEFAULT 1.0,
		properties TEXT, -- JSON
		vector %[1]s, -- Optional edge embedding
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (from_node_id) REFERENCES graph_nodes(id) ON DELETE CASCADE,
		FOREIGN KEY (to_node_id) REFERENCES graph_nodes(id) ON DELETE CASCADE
	);

	-- Indexes for performance
	CREATE INDEX IF NOT EXISTS idx_edges_from ON graph_edges(from_node_id);
	CREATE INDEX IF NOT EXISTS idx_edges_to ON graph_edges(to_node_id);
	CREATE INDEX IF NOT EXISTS idx_edges_type ON graph_edges(edge_type);
	CREATE INDEX IF NOT EXISTS idx_nodes_type ON graph_nodes(node_type);
	CREATE INDEX IF NOT EXISTS idx_edges_composite ON graph_edges(from_node_id, edge_type);

	-- RDF / Knowledge Graph namespace mappings
	CREATE TABLE IF NOT EXISTS kg_namespaces (
		prefix TEXT PRIMARY KEY,
		uri TEXT NOT NULL UNIQUE,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	-- RDF / Knowledge Graph triples and quads
	CREATE TABLE IF NOT EXISTS kg_triples (
		id TEXT PRIMARY KEY,
		graph_kind TEXT,
		graph_value TEXT,
		subject_kind TEXT NOT NULL,
		subject_value TEXT NOT NULL,
		predicate_value TEXT NOT NULL,
		object_kind TEXT NOT NULL,
		object_value TEXT NOT NULL,
		object_datatype TEXT,
		object_language TEXT,
		inferred INTEGER NOT NULL DEFAULT 0,
		inference_rule TEXT,
		support_ids TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_kg_triples_subject ON kg_triples(subject_kind, subject_value);
	CREATE INDEX IF NOT EXISTS idx_kg_triples_predicate ON kg_triples(predicate_value);
	CREATE INDEX IF NOT EXISTS idx_kg_triples_object ON kg_triples(object_kind, object_value);
	CREATE INDEX IF NOT EXISTS idx_kg_triples_graph ON kg_triples(graph_kind, graph_value);
	CREATE INDEX IF NOT EXISTS idx_kg_triples_inferred ON kg_triples(inferred);
	`, blob)

	if _, err := g.db.ExecContext(ctx, schema); err != nil {
		return err
	}

	// Lightweight migration path for older databases created before inference metadata existed.
	for _, stmt := range []string{
		`ALTER TABLE kg_triples ADD COLUMN inferred INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE kg_triples ADD COLUMN inference_rule TEXT`,
		`ALTER TABLE kg_triples ADD COLUMN support_ids TEXT`,
	} {
		// Idempotent: on every start after the first the column is already
		// there. Which error says so depends on the database — SQLite says
		// "duplicate column name", PostgreSQL says `column "x" of relation
		// "y" already exists` — so the dialect is asked instead of a string
		// being matched here.
		if _, err := g.exec(ctx, stmt); err != nil && !g.dialect.IsDuplicateColumn(err) {
			return err
		}
	}

	return nil
}

// UpsertNode inserts or updates a node in the graph
func (g *GraphStore) UpsertNode(ctx context.Context, node *GraphNode) error {
	if node == nil || node.ID == "" {
		return fmt.Errorf("invalid node: missing ID")
	}

	if len(node.Vector) == 0 {
		return fmt.Errorf("invalid node: missing vector")
	}

	if err := g.InitGraphSchema(ctx); err != nil {
		return fmt.Errorf("init graph schema: %w", err)
	}

	// Encode vector
	vectorBytes, err := encoding.EncodeVector(node.Vector)
	if err != nil {
		return fmt.Errorf("failed to encode vector: %w", err)
	}

	// Encode properties as JSON
	var propertiesJSON []byte
	if node.Properties != nil {
		propertiesJSON, err = json.Marshal(node.Properties)
		if err != nil {
			return fmt.Errorf("failed to encode properties: %w", err)
		}
	}

	query := `
	INSERT INTO graph_nodes (id, vector, content, node_type, properties, updated_at)
	VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(id) DO UPDATE SET
		vector = excluded.vector,
		content = excluded.content,
		node_type = excluded.node_type,
		properties = excluded.properties,
		updated_at = CURRENT_TIMESTAMP
	`

	_, err = g.exec(ctx, query,
		node.ID,
		vectorBytes,
		node.Content,
		node.NodeType,
		string(propertiesJSON),
	)

	if err != nil {
		return err
	}

	if g.hnswIndex != nil {
		g.hnswIndex.index.Remove(node.ID)
		if err := g.hnswIndex.index.Add(node.ID, node.Vector); err != nil {
			return fmt.Errorf("failed to update hnsw index: %w", err)
		}
	}

	// The same bookkeeping for the database-side index. A failure here would
	// leave a node that exists but cannot be found by similarity, so it is an
	// error rather than a warning.
	if g.vecCap.Enabled {
		if err := g.pgUpsertVector(ctx, node.ID, node.Vector); err != nil {
			return fmt.Errorf("mirror vector for search: %w", err)
		}
	}

	return nil
}

// GetNode retrieves a node by ID
func (g *GraphStore) GetNode(ctx context.Context, nodeID string) (*GraphNode, error) {
	query := `
	SELECT id, vector, content, node_type, properties, created_at, updated_at
	FROM graph_nodes
	WHERE id = ?
	`

	var node GraphNode
	var vectorBytes []byte
	var propertiesJSON sql.NullString

	err := g.queryRow(ctx, query, nodeID).Scan(
		&node.ID,
		&vectorBytes,
		&node.Content,
		&node.NodeType,
		&propertiesJSON,
		&node.CreatedAt,
		&node.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("node not found: %s", nodeID)
	}
	if err != nil {
		return nil, err
	}

	// Decode vector
	node.Vector, err = encoding.DecodeVector(vectorBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to decode vector: %w", err)
	}

	// Decode properties
	if propertiesJSON.Valid && propertiesJSON.String != "" {
		err = json.Unmarshal([]byte(propertiesJSON.String), &node.Properties)
		if err != nil {
			return nil, fmt.Errorf("failed to decode properties: %w", err)
		}
	}

	return &node, nil
}

// MergeEntities collapses surface-form duplicate nodes into one canonical node:
// every edge referencing an alias is repointed to canonicalID, self-loops created
// by the merge are dropped, and the alias nodes are deleted. Used for entity
// resolution (e.g. unifying "r0" / "DRBD resource" / "resources" into one entity).
func (g *GraphStore) MergeEntities(ctx context.Context, canonicalID string, aliasIDs []string) error {
	if canonicalID == "" {
		return fmt.Errorf("canonicalID required")
	}
	for _, alias := range aliasIDs {
		if alias == "" || alias == canonicalID {
			continue
		}
		if _, err := g.exec(ctx, `UPDATE graph_edges SET from_node_id = ? WHERE from_node_id = ?`, canonicalID, alias); err != nil {
			return fmt.Errorf("repoint from-edges: %w", err)
		}
		if _, err := g.exec(ctx, `UPDATE graph_edges SET to_node_id = ? WHERE to_node_id = ?`, canonicalID, alias); err != nil {
			return fmt.Errorf("repoint to-edges: %w", err)
		}
		if _, err := g.exec(ctx, `DELETE FROM graph_edges WHERE from_node_id = to_node_id`); err != nil {
			return fmt.Errorf("drop self-loops: %w", err)
		}
		if err := g.DeleteNode(ctx, alias); err != nil && err.Error() != fmt.Sprintf("node not found: %s", alias) {
			return err
		}
	}
	return nil
}

// DeleteNode removes a node and all its edges
func (g *GraphStore) DeleteNode(ctx context.Context, nodeID string) error {
	// Edges are automatically deleted due to CASCADE
	query := `DELETE FROM graph_nodes WHERE id = ?`
	result, err := g.exec(ctx, query, nodeID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return fmt.Errorf("node not found: %s", nodeID)
	}

	if g.hnswIndex != nil {
		g.hnswIndex.index.Remove(nodeID)
	}

	return nil
}

// UpsertEdge inserts or updates an edge in the graph
func (g *GraphStore) UpsertEdge(ctx context.Context, edge *GraphEdge) error {
	if edge == nil || edge.ID == "" {
		return fmt.Errorf("invalid edge: missing ID")
	}

	if edge.FromNodeID == "" || edge.ToNodeID == "" {
		return fmt.Errorf("invalid edge: missing node IDs")
	}

	if err := g.InitGraphSchema(ctx); err != nil {
		return fmt.Errorf("init graph schema: %w", err)
	}

	// Set default weight if not specified
	if edge.Weight == 0 {
		edge.Weight = 1.0
	}

	// Encode properties as JSON
	var propertiesJSON []byte
	if edge.Properties != nil {
		var err error
		propertiesJSON, err = json.Marshal(edge.Properties)
		if err != nil {
			return fmt.Errorf("failed to encode properties: %w", err)
		}
	}

	// Encode vector if present
	var vectorBytes []byte
	if len(edge.Vector) > 0 {
		var err error
		vectorBytes, err = encoding.EncodeVector(edge.Vector)
		if err != nil {
			return fmt.Errorf("failed to encode vector: %w", err)
		}
	}

	query := `
	INSERT INTO graph_edges (id, from_node_id, to_node_id, edge_type, weight, properties, vector)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		from_node_id = excluded.from_node_id,
		to_node_id = excluded.to_node_id,
		edge_type = excluded.edge_type,
		weight = excluded.weight,
		properties = excluded.properties,
		vector = excluded.vector
	`

	_, err := g.exec(ctx, query,
		edge.ID,
		edge.FromNodeID,
		edge.ToNodeID,
		edge.EdgeType,
		edge.Weight,
		string(propertiesJSON),
		vectorBytes,
	)

	return err
}

// GetEdges retrieves edges for a node
func (g *GraphStore) GetEdges(ctx context.Context, nodeID string, direction string) ([]*GraphEdge, error) {
	var query string
	switch direction {
	case "out":
		query = `SELECT id, from_node_id, to_node_id, edge_type, weight, properties, vector, created_at
				FROM graph_edges WHERE from_node_id = ?`
	case "in":
		query = `SELECT id, from_node_id, to_node_id, edge_type, weight, properties, vector, created_at
				FROM graph_edges WHERE to_node_id = ?`
	case "both", "":
		query = `SELECT id, from_node_id, to_node_id, edge_type, weight, properties, vector, created_at
				FROM graph_edges WHERE from_node_id = ? OR to_node_id = ?`
	default:
		return nil, fmt.Errorf("invalid direction: %s (use 'in', 'out', or 'both')", direction)
	}

	var rows *sql.Rows
	var err error

	if direction == "both" || direction == "" {
		rows, err = g.query(ctx, query, nodeID, nodeID)
	} else {
		rows, err = g.query(ctx, query, nodeID)
	}

	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var edges []*GraphEdge
	for rows.Next() {
		var edge GraphEdge
		var propertiesJSON sql.NullString
		var vectorBytes []byte

		err := rows.Scan(
			&edge.ID,
			&edge.FromNodeID,
			&edge.ToNodeID,
			&edge.EdgeType,
			&edge.Weight,
			&propertiesJSON,
			&vectorBytes,
			&edge.CreatedAt,
		)
		if err != nil {
			return nil, err
		}

		// Decode properties
		if propertiesJSON.Valid && propertiesJSON.String != "" {
			err = json.Unmarshal([]byte(propertiesJSON.String), &edge.Properties)
			if err != nil {
				return nil, fmt.Errorf("failed to decode properties: %w", err)
			}
		}

		// Decode vector if present
		if len(vectorBytes) > 0 {
			edge.Vector, err = encoding.DecodeVector(vectorBytes)
			if err != nil {
				return nil, fmt.Errorf("failed to decode vector: %w", err)
			}
		}

		edges = append(edges, &edge)
	}

	return edges, rows.Err()
}

// DeleteEdge removes an edge from the graph
func (g *GraphStore) DeleteEdge(ctx context.Context, edgeID string) error {
	query := `DELETE FROM graph_edges WHERE id = ?`
	result, err := g.exec(ctx, query, edgeID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return fmt.Errorf("edge not found: %s", edgeID)
	}

	return nil
}

func (g *GraphStore) getNodesByIDs(ctx context.Context, nodeIDs []string) (map[string]*GraphNode, error) {
	if len(nodeIDs) == 0 {
		return map[string]*GraphNode{}, nil
	}

	placeholders := make([]string, len(nodeIDs))
	args := make([]interface{}, len(nodeIDs))
	for i, nodeID := range nodeIDs {
		placeholders[i] = "?"
		args[i] = nodeID
	}

	query := fmt.Sprintf(`
	SELECT id, vector, content, node_type, properties, created_at, updated_at
	FROM graph_nodes
	WHERE id IN (%s)
	`, strings.Join(placeholders, ","))

	rows, err := g.query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	nodes := make(map[string]*GraphNode, len(nodeIDs))
	for rows.Next() {
		var node GraphNode
		var vectorBytes []byte
		var propertiesJSON sql.NullString

		err := rows.Scan(
			&node.ID,
			&vectorBytes,
			&node.Content,
			&node.NodeType,
			&propertiesJSON,
			&node.CreatedAt,
			&node.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		node.Vector, err = encoding.DecodeVector(vectorBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to decode vector: %w", err)
		}

		if propertiesJSON.Valid && propertiesJSON.String != "" {
			err = json.Unmarshal([]byte(propertiesJSON.String), &node.Properties)
			if err != nil {
				return nil, fmt.Errorf("failed to decode properties: %w", err)
			}
		}

		nodeCopy := node
		nodes[node.ID] = &nodeCopy
	}

	return nodes, rows.Err()
}
