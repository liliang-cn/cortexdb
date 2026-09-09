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

	// The bitemporal columns. See pkg/graph/temporal.go for what the two axes
	// mean and why NULL is unbounded on all four.
	//
	// ValidFrom is the only one a caller may set on a write: it says when the
	// fact became true in the world, and defaults to the moment of the write.
	// The other three are set by the store and ignored on write, the same way
	// CreatedAt and UpdatedAt already were — a live row always has an open
	// ValidTo and no RetractedAt, because a row that has ended or been
	// retracted lives in graph_node_history instead.
	ValidFrom   time.Time `json:"valid_from,omitzero"`
	ValidTo     time.Time `json:"valid_to,omitzero"`
	RecordedAt  time.Time `json:"recorded_at,omitzero"`
	RetractedAt time.Time `json:"retracted_at,omitzero"`
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

	// The bitemporal columns, with the same rules as GraphNode's: ValidFrom is
	// an input, the other three are outputs. An edge is where these earn their
	// keep — "the runbook recommended sds-meta" is a claim with a beginning and
	// an end, and Relate names how two such claims sit against each other.
	ValidFrom   time.Time `json:"valid_from,omitzero"`
	ValidTo     time.Time `json:"valid_to,omitzero"`
	RecordedAt  time.Time `json:"recorded_at,omitzero"`
	RetractedAt time.Time `json:"retracted_at,omitzero"`
}

// GraphFilter defines filtering options for graph queries
type GraphFilter struct {
	NodeTypes []string `json:"node_types,omitempty"`
	EdgeTypes []string `json:"edge_types,omitempty"`
	MaxDepth  int      `json:"max_depth,omitempty"`
	// Properties scopes a query to nodes whose properties JSON carries every
	// one of these top-level string fields with these values.
	//
	// It exists because a store this one shares with everything else on the
	// machine had no way to ask for one importer's rows. node_type was the
	// only filter, and a type name is not a batch: an importer that wrote a
	// thousand Person nodes on Tuesday and a thousand more on Friday could
	// not ask for either set, only for all two thousand. Every writer that
	// cared already stamped a batch onto properties — alchemy's connector
	// writes "run" — and nothing could read it back.
	//
	// The fields are ANDed, and each is compared as text: properties is a
	// JSON object serialized by json.Marshal, and the guarded read in
	// pkg/sqldialect is what keeps a row with no properties at all from
	// failing the whole query rather than simply not matching.
	Properties map[string]string `json:"properties,omitempty"`
	// Contains narrows to nodes whose property holds this text somewhere in
	// it, compared case-insensitively and ANDed with everything else.
	//
	// Separate from Properties because the two are different questions and
	// only one of them can use an index: Properties is how a caller names a
	// batch it already knows, Contains is how a search enters the graph at
	// all. Folding them into one map would make every batch scope a LIKE.
	//
	// The value is matched literally — % and _ in it mean those characters,
	// not wildcards — because the text comes from whoever typed the query and
	// a name with an underscore in it is ordinary.
	Contains map[string]string `json:"contains,omitempty"`
	// Limit caps the rows a query returns. Zero means no cap, which is what
	// every caller before this field got and still gets.
	//
	// A cap alone would be worse than none: a caller shown 100 of 4000 rows
	// with nothing saying so reports 100. CountNodes answers the other half,
	// and the two are meant to be used together.
	Limit int `json:"limit,omitempty"`
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
	// Guards the rule table the same way, separately: rules are opt-in, so a
	// brain that never declares one never grows the table.
	ruleSchemaMu    sync.Mutex
	ruleSchemaReady bool

	// clock hands out the strictly increasing instants the bitemporal columns
	// are stamped with. See temporal.go: a wall clock repeats, and two writes
	// at the same instant produce a version interval nothing can read.
	clock temporalClock

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

	// Last, and guarded the same way: the bitemporal columns and the history
	// tables. A brain written before this release gains them here, with every
	// existing row reading as current.
	return g.createTemporalSchema(ctx)
}

// UpsertNode inserts or updates a node in the graph
func (g *GraphStore) UpsertNode(ctx context.Context, node *GraphNode) error {
	if node == nil || node.ID == "" {
		return fmt.Errorf("invalid node: missing ID")
	}

	if len(node.Vector) == 0 {
		return fmt.Errorf("invalid node: missing vector")
	}

	if err := errIfAsOf(ctx); err != nil {
		return err
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

	// The version this write opens. A caller may state when the fact became
	// true; most do not, and then it became true when it was written.
	at, recorded := g.versionStamps(node.ValidFrom)

	// Archive-then-write, in one transaction. Two statements outside one would
	// be two autocommits and two WAL syncs for what is a single change, which
	// cost more than the versioning itself — and would leave a window in which
	// the old version was recorded and the new one was not.
	//
	// The archive closes the old version at `at`, but only if the content
	// actually differs; that comparison happens in the database, so an
	// unchanged upsert costs one index probe and writes nothing. See
	// archiveNodeVersion.
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := g.archiveNodeVersion(ctx, tx, node.ID, at,
		node.Content, node.NodeType, string(propertiesJSON)); err != nil {
		return err
	}

	if _, err = g.txExec(ctx, tx, upsertNodeSQL,
		node.ID,
		vectorBytes,
		node.Content,
		node.NodeType,
		string(propertiesJSON),
		at,
		recorded,
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
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
	src, srcArgs := g.nodeSource(ctx)
	query := `
	SELECT id, vector, content, node_type, properties, created_at, updated_at` + temporalSelect + `
	FROM ` + src + ` AS n
	WHERE id = ?
	`

	var node GraphNode
	var vectorBytes []byte
	var propertiesJSON sql.NullString
	var tt temporalScan

	err := g.queryRow(ctx, query, append(srcArgs, nodeID)...).Scan(
		&node.ID,
		&vectorBytes,
		&node.Content,
		&node.NodeType,
		&propertiesJSON,
		&node.CreatedAt,
		&node.UpdatedAt,
		&tt.validFrom, &tt.validTo, &tt.recordedAt, &tt.retractedAt,
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
	tt.applyNode(&node)

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

// DeleteNode removes a node and all its edges from the current graph.
//
// It is a retraction now, not a hard delete: the node and its edges move to
// graph_node_history / graph_edge_history with retracted_at set, so a read as
// of any instant before this one still sees them. Every current read is
// unaffected — the live tables no longer hold the row — which is why the name,
// the signature and the "node not found" error are all unchanged.
//
// RetractNodeAt is the same operation with the instant stated, for a fact
// discovered today to have stopped being believed last Tuesday. Purge is the
// only thing that removes any of it for good.
func (g *GraphStore) DeleteNode(ctx context.Context, nodeID string) error {
	return g.RetractNodeAt(ctx, nodeID, g.clock.now())
}

// UpsertEdge inserts or updates an edge in the graph
func (g *GraphStore) UpsertEdge(ctx context.Context, edge *GraphEdge) error {
	if edge == nil || edge.ID == "" {
		return fmt.Errorf("invalid edge: missing ID")
	}

	if edge.FromNodeID == "" || edge.ToNodeID == "" {
		return fmt.Errorf("invalid edge: missing node IDs")
	}

	if err := errIfAsOf(ctx); err != nil {
		return err
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

	at, recorded := g.versionStamps(edge.ValidFrom)

	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := g.archiveEdgeVersion(ctx, tx, edge.ID, at,
		edge.FromNodeID, edge.ToNodeID, edge.EdgeType, edge.Weight, string(propertiesJSON)); err != nil {
		return err
	}
	if _, err := g.txExec(ctx, tx, upsertEdgeSQL,
		edge.ID,
		edge.FromNodeID,
		edge.ToNodeID,
		edge.EdgeType,
		edge.Weight,
		string(propertiesJSON),
		vectorBytes,
		at,
		recorded,
	); err != nil {
		return err
	}

	return tx.Commit()
}

// GetEdges retrieves edges for a node.
//
// Ordered by id, which matters more than it looks: this feeds Neighbors, and
// Neighbors feeds graph-mode retrieval. Without an ORDER BY, SQLite happens to
// return insertion order while PostgreSQL returns whatever the plan produced —
// so the same question could retrieve a different set of chunks on the two
// backends, and a different set on PostgreSQL from one run to the next once
// the table had been updated.
func (g *GraphStore) GetEdges(ctx context.Context, nodeID string, direction string) ([]*GraphEdge, error) {
	src, srcArgs := g.edgeSource(ctx)
	projection := `SELECT id, from_node_id, to_node_id, edge_type, weight, properties, vector, created_at` +
		temporalSelect + ` FROM ` + src + ` AS e `

	var query string
	switch direction {
	case "out":
		query = projection + `WHERE from_node_id = ? ORDER BY id`
	case "in":
		query = projection + `WHERE to_node_id = ? ORDER BY id`
	case "both", "":
		query = projection + `WHERE from_node_id = ? OR to_node_id = ? ORDER BY id`
	default:
		return nil, fmt.Errorf("invalid direction: %s (use 'in', 'out', or 'both')", direction)
	}

	var rows *sql.Rows
	var err error

	if direction == "both" || direction == "" {
		rows, err = g.query(ctx, query, append(srcArgs, nodeID, nodeID)...)
	} else {
		rows, err = g.query(ctx, query, append(srcArgs, nodeID)...)
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
		var tt temporalScan

		err := rows.Scan(
			&edge.ID,
			&edge.FromNodeID,
			&edge.ToNodeID,
			&edge.EdgeType,
			&edge.Weight,
			&propertiesJSON,
			&vectorBytes,
			&edge.CreatedAt,
			&tt.validFrom, &tt.validTo, &tt.recordedAt, &tt.retractedAt,
		)
		if err != nil {
			return nil, err
		}
		tt.applyEdge(&edge)

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

// DeleteEdge removes an edge from the current graph.
//
// A retraction, like DeleteNode: the row moves to graph_edge_history with
// retracted_at set, so "what did this fact say before we withdrew it" has an
// answer and FactProvenanceFor can still resolve it as of an earlier instant.
// Current reads see exactly what they saw before.
func (g *GraphStore) DeleteEdge(ctx context.Context, edgeID string) error {
	return g.RetractEdgeAt(ctx, edgeID, g.clock.now())
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

	src, srcArgs := g.nodeSource(ctx)
	query := fmt.Sprintf(`
	SELECT id, vector, content, node_type, properties, created_at, updated_at`+temporalSelect+`
	FROM `+src+` AS n
	WHERE id IN (%s)
	`, strings.Join(placeholders, ","))

	rows, err := g.query(ctx, query, append(srcArgs, args...)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	nodes := make(map[string]*GraphNode, len(nodeIDs))
	for rows.Next() {
		var node GraphNode
		var vectorBytes []byte
		var propertiesJSON sql.NullString
		var tt temporalScan

		err := rows.Scan(
			&node.ID,
			&vectorBytes,
			&node.Content,
			&node.NodeType,
			&propertiesJSON,
			&node.CreatedAt,
			&node.UpdatedAt,
			&tt.validFrom, &tt.validTo, &tt.recordedAt, &tt.retractedAt,
		)
		if err != nil {
			return nil, err
		}
		tt.applyNode(&node)

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
