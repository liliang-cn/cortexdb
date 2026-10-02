package graph

import (
	"context"
	"database/sql"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph/cypher"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
)

// CypherRequest is one read-only openCypher/GQL query over the property graph.
// See pkg/graph/cypher for the exact subset.
type CypherRequest struct {
	Query  string
	Params map[string]any
	// MaxRows caps the rows returned (default 1000, at most 10000); a query
	// that had more reports Truncated.
	MaxRows int
	// Timeout bounds the whole query (default 10s, at most 60s).
	Timeout time.Duration
	// AsOf reads the graph as it stood at that instant — both the bitemporal
	// columns (via NodeSource/EdgeSource, as every other read here does) and
	// temporal facts' valid_from/valid_to properties. Zero means now.
	AsOf time.Time
	// IncludeEndedFacts keeps edges whose valid_to property has passed (or
	// whose valid_from has not arrived). By default a query sees only the
	// facts that hold now, which is what an unqualified question means.
	IncludeEndedFacts bool
}

// QueryCypher runs a read-only openCypher/GQL MATCH query.
//
// It lives on GraphStore, rather than taking a *sql.DB, so that it reads
// exactly what every other graph read reads: the same dialect, the same
// rebinding, and the same as-of sources. A Cypher answer that disagreed with
// Neighbors about which edges exist would be two graphs again.
func (g *GraphStore) QueryCypher(ctx context.Context, req CypherRequest) (*cypher.Result, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	if !req.AsOf.IsZero() {
		ctx = AsOf(ctx, req.AsOf)
	}
	return cypher.Execute(ctx, cypherBackend{g}, req.Query, cypher.Options{
		Params:            req.Params,
		MaxRows:           req.MaxRows,
		Timeout:           req.Timeout,
		ValidAt:           req.AsOf,
		IncludeEndedFacts: req.IncludeEndedFacts,
	})
}

// cypherBackend adapts GraphStore to cypher.Backend without exporting the
// store's query helpers.
type cypherBackend struct{ g *GraphStore }

func (b cypherBackend) Dialect() sqldialect.Dialect { return b.g.dialect }

func (b cypherBackend) Query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return b.g.query(ctx, q, args...)
}

func (b cypherBackend) NodeSource(ctx context.Context) (string, []any) { return b.g.NodeSource(ctx) }
func (b cypherBackend) EdgeSource(ctx context.Context) (string, []any) { return b.g.EdgeSource(ctx) }
