package cortexdb

import (
	"context"
	"path/filepath"
	"testing"
)

// "next" used to be skipped by name, alongside has_chunk, as a structural edge
// type. But it is also the obvious name for one thing following another — a
// plan's steps, a run's trace — and a caller who modelled a sequence got its
// nodes back with every link between them silently missing. What makes an edge
// structural is that it wires chunks, not what it is called.
func TestListGraphAllKeepsNextBetweenRealNodes(t *testing.T) {
	db, ctx := openGraphEdgeTestDB(t)

	mustExecGraph(t, db, ctx, `INSERT INTO graph_nodes (id, vector, content, node_type) VALUES
		('step:1', x'00', 'Read the config', 'step'),
		('step:2', x'00', 'Dial the gateway', 'step'),
		('doc:1',  x'00', 'A document',      'document'),
		('chunk:1', x'00', 'first chunk',    'chunk'),
		('chunk:2', x'00', 'second chunk',   'chunk')`)
	mustExecGraph(t, db, ctx, `INSERT INTO graph_edges (id, from_node_id, to_node_id, edge_type) VALUES
		('e1', 'step:1',  'step:2',  'next'),
		('e2', 'chunk:1', 'chunk:2', 'next'),
		('e3', 'doc:1',   'chunk:1', 'has_chunk')`)

	resp, err := db.ListGraphAll(ctx, GraphListAllRequest{})
	if err != nil {
		t.Fatalf("ListGraphAll: %v", err)
	}

	var kept, chunkNext, hasChunk int
	for _, e := range resp.Edges {
		switch {
		case e.From == "step:1" && e.To == "step:2":
			kept++
		case e.From == "chunk:1" || e.To == "chunk:1" || e.From == "chunk:2" || e.To == "chunk:2":
			chunkNext++
		}
		if e.Type == "has_chunk" {
			hasChunk++
		}
	}
	if kept != 1 {
		t.Errorf("the next edge between two steps was dropped; edges = %+v", resp.Edges)
	}
	if chunkNext != 0 {
		t.Errorf("an edge touching a chunk survived; edges = %+v", resp.Edges)
	}
	if hasChunk != 0 {
		t.Errorf("has_chunk survived; edges = %+v", resp.Edges)
	}
}

// Degree drives which nodes a truncated listing keeps, so chunk wiring must not
// count towards it either — otherwise a heavily chunked document outranks the
// entities the caller actually wants to see.
func TestListGraphAllDoesNotCountChunkEdgesTowardsDegree(t *testing.T) {
	db, ctx := openGraphEdgeTestDB(t)

	mustExecGraph(t, db, ctx, `INSERT INTO graph_nodes (id, vector, content, node_type) VALUES
		('doc:1', x'00', 'A document', 'document'),
		('entity:a', x'00', 'A', 'entity'),
		('chunk:1', x'00', 'c1', 'chunk'),
		('chunk:2', x'00', 'c2', 'chunk'),
		('chunk:3', x'00', 'c3', 'chunk')`)
	mustExecGraph(t, db, ctx, `INSERT INTO graph_edges (id, from_node_id, to_node_id, edge_type) VALUES
		('e1', 'doc:1', 'chunk:1', 'has_chunk'),
		('e2', 'doc:1', 'chunk:2', 'has_chunk'),
		('e3', 'doc:1', 'chunk:3', 'has_chunk'),
		('e4', 'doc:1', 'entity:a', 'mentions')`)

	resp, err := db.ListGraphAll(ctx, GraphListAllRequest{})
	if err != nil {
		t.Fatalf("ListGraphAll: %v", err)
	}
	if len(resp.Edges) != 1 || resp.Edges[0].Type != "mentions" {
		t.Errorf("edges = %+v, want only the mentions edge", resp.Edges)
	}
	for _, n := range resp.Nodes {
		if n.Type == "chunk" {
			t.Errorf("a chunk node was listed: %+v", n)
		}
	}
}

func openGraphEdgeTestDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "edges.db")))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		t.Fatalf("init graph schema: %v", err)
	}
	return db, ctx
}

func mustExecGraph(t *testing.T, db *DB, ctx context.Context, q string) {
	t.Helper()
	if _, err := db.SQL().ExecContext(ctx, q); err != nil {
		t.Fatalf("exec %s: %v", q, err)
	}
}
