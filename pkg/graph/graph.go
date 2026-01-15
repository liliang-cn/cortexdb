package graph

import (
	"github.com/liliang-cn/sqvect/v2/internal/encoding"
	"github.com/liliang-cn/sqvect/v2/pkg/core"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	Vector           []float32    `json:"vector,omitempty"`
	StartNodeID      string       `json:"start_node_id,omitempty"`
	CenterNodes      []string     `json:"center_nodes,omitempty"`
	GraphFilter      *GraphFilter `json:"graph_filter,omitempty"`
	TopK             int          `json:"top_k"`
	Threshold        float64      `json:"threshold,omitempty"`
	VectorThreshold  float64      `json:"vector_threshold,omitempty"`
	TotalThreshold   float64      `json:"total_threshold,omitempty"`
	VectorWeight     float64      `json:"vector_weight"`
	GraphWeight      float64      `json:"graph_weight"`
	Weights          HybridWeights `json:"weights"`
}

// HybridWeights defines the weights for hybrid scoring
type HybridWeights struct {
	VectorWeight float64 `json:"vector_weight"` // Weight for vector similarity
	GraphWeight  float64 `json:"graph_weight"`  // Weight for graph proximity
	EdgeWeight   float64 `json:"edge_weight"`   // Weight for edge strength
}

// HybridResult represents a result from hybrid search
type HybridResult struct {
	Node         *GraphNode `json:"node"`
	VectorScore  float64    `json:"vector_score"`
	GraphScore   float64    `json:"graph_score"`
	CombinedScore float64   `json:"combined_score"`
	TotalScore   float64    `json:"total_score"`
	Path         []string   `json:"path,omitempty"` // Path from start node
	Distance     int        `json:"distance"`       // Graph distance from start
}

// GraphStore provides graph operations on top of the vector store
type GraphStore struct {
	store     *core.SQLiteStore
	db        *sql.DB
	hnswIndex *HNSWGraphIndex // HNSW index for fast vector search
}

// NewGraphStore creates a new graph store from a SQLite store
func NewGraphStore(s *core.SQLiteStore) *GraphStore {
	return &GraphStore{
		store: s,
		db:    s.GetDB(),
	}
}

// InitGraphSchema creates the graph tables if they don't exist
func (g *GraphStore) InitGraphSchema(ctx context.Context) error {
	schema := `
	-- Graph nodes table (extends embeddings concept)
	CREATE TABLE IF NOT EXISTS graph_nodes (
		id TEXT PRIMARY KEY,
		vector BLOB NOT NULL,
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
		vector BLOB, -- Optional edge embedding
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
	`

	_, err := g.db.ExecContext(ctx, schema)
	return err
}

// UpsertNode inserts or updates a node in the graph
func (g *GraphStore) UpsertNode(ctx context.Context, node *GraphNode) error {
	if node == nil || node.ID == "" {
		return fmt.Errorf("invalid node: missing ID")
	}

	if len(node.Vector) == 0 {
		return fmt.Errorf("invalid node: missing vector")
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

	_, err = g.db.ExecContext(ctx, query,
		node.ID,
		vectorBytes,
		node.Content,
		node.NodeType,
		string(propertiesJSON),
	)

	return err
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

	err := g.db.QueryRowContext(ctx, query, nodeID).Scan(
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

// DeleteNode removes a node and all its edges
func (g *GraphStore) DeleteNode(ctx context.Context, nodeID string) error {
	// Edges are automatically deleted due to CASCADE
	query := `DELETE FROM graph_nodes WHERE id = ?`
	result, err := g.db.ExecContext(ctx, query, nodeID)
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

	_, err := g.db.ExecContext(ctx, query,
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
		rows, err = g.db.QueryContext(ctx, query, nodeID, nodeID)
	} else {
		rows, err = g.db.QueryContext(ctx, query, nodeID)
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
	result, err := g.db.ExecContext(ctx, query, edgeID)
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