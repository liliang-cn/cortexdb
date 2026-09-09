package cortexdb

// Point-in-time reads at the facade.
//
// pkg/graph holds the storage half — the bitemporal columns, the history
// tables, the as-of union. This is the half a caller sees: one way to say "read
// the graph as of then", a snapshot, a diff, and the vacuum an operator needs
// when history has grown past what the answers are worth.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// AsOf returns a context whose graph reads answer as the store stood at that
// instant.
//
// Re-exported from pkg/graph so the ordinary caller — which holds a *DB and
// never imports the graph package — can reach it:
//
//	past := cortexdb.AsOf(ctx, before)
//	nodes, err := db.Graph().ListNodes(past, nil)
//	prov, err := db.FactProvenanceFor(past, edgeID, true)
//
// It is a context value and not an argument on purpose; pkg/graph's ReadOptions
// says why. The one rule a caller has to keep is that it is a *read* setting:
// every write refuses a context carrying one rather than writing at the wrong
// moment.
func AsOf(ctx context.Context, at time.Time) context.Context { return graph.AsOf(ctx, at) }

// GraphSnapshot is the shape of the graph at one instant.
type GraphSnapshot struct {
	AsOf  time.Time `json:"as_of"`
	Nodes int       `json:"nodes"`
	Edges int       `json:"edges"`
	// Orphans is how many of those nodes no edge touched — the same health
	// number Connectivity reports for the present, so a caller can watch it
	// move rather than only read today's.
	Orphans int `json:"orphans"`
	// NodeSample and EdgeSample are filled only when the caller asked to see
	// rows. A snapshot is a count first: "how big was the graph then" is the
	// question, and returning four hundred thousand nodes to answer it is not
	// a listing, it is a denial of service on the caller's context window.
	NodeSample []graph.NodeVersion `json:"node_sample,omitempty"`
	EdgeSample []graph.EdgeVersion `json:"edge_sample,omitempty"`
	Truncated  bool                `json:"truncated,omitempty"`
}

// SnapshotOptions narrows and bounds a snapshot.
type SnapshotOptions struct {
	NodeTypes []string
	EdgeTypes []string
	// Sample lists up to this many nodes and edges alongside the counts. Zero
	// lists none.
	Sample int
}

// GraphSnapshotAt counts the graph as it stood at an instant, optionally
// listing some of it.
//
// The counts come from the same as-of machinery every other past read uses, so
// a snapshot and a GetNode at the same instant can never disagree about whether
// something was there.
func (db *DB) GraphSnapshotAt(ctx context.Context, at time.Time, opts SnapshotOptions) (*GraphSnapshot, error) {
	if db == nil {
		return nil, fmt.Errorf("cortexdb: graph snapshot: nil db")
	}
	if at.IsZero() {
		return nil, fmt.Errorf("cortexdb: graph snapshot: an instant is required")
	}
	g := db.Graph()
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("cortexdb: graph snapshot: %w", err)
	}

	past := graph.AsOf(ctx, at)
	out := &GraphSnapshot{AsOf: at.UTC()}

	conn, err := g.Connectivity(past)
	if err != nil {
		return nil, fmt.Errorf("cortexdb: graph snapshot: %w", err)
	}
	out.Nodes, out.Orphans = conn.Nodes, conn.Orphans
	if len(opts.NodeTypes) > 0 {
		// A type filter is a different count from Connectivity's, which is
		// deliberately over everything; ask again rather than report a total
		// that does not match the filter the caller gave.
		n, err := g.CountNodes(past, &graph.GraphFilter{NodeTypes: opts.NodeTypes})
		if err != nil {
			return nil, fmt.Errorf("cortexdb: graph snapshot: %w", err)
		}
		out.Nodes = n
	}

	src, args := g.EdgeSource(past)
	q := `SELECT COUNT(*) FROM ` + src + ` AS e`
	if len(opts.EdgeTypes) > 0 {
		holes := make([]string, len(opts.EdgeTypes))
		for i, t := range opts.EdgeTypes {
			holes[i] = "?"
			args = append(args, t)
		}
		q += " WHERE edge_type IN (" + strings.Join(holes, ",") + ")"
	}
	if err := db.queryRow(ctx, db.Dialect().Rebind(q), args...).Scan(&out.Edges); err != nil {
		return nil, fmt.Errorf("cortexdb: graph snapshot: count edges: %w", err)
	}

	if opts.Sample <= 0 {
		return out, nil
	}

	// The listing is a diff against the empty graph — every row reads as
	// "added" — which reuses the paging and the projection instead of a second
	// way of walking the same rows.
	listing, err := g.GraphDiff(ctx, time.Unix(0, 0).UTC(), at, graph.DiffOptions{
		Limit:     opts.Sample,
		NodeTypes: opts.NodeTypes,
		EdgeTypes: opts.EdgeTypes,
	})
	if err != nil {
		return nil, fmt.Errorf("cortexdb: graph snapshot: listing: %w", err)
	}
	for _, c := range listing.Nodes {
		if c.After != nil {
			out.NodeSample = append(out.NodeSample, *c.After)
		}
	}
	for _, c := range listing.Edges {
		if c.After != nil {
			out.EdgeSample = append(out.EdgeSample, *c.After)
		}
	}
	out.Truncated = listing.Truncated
	return out, nil
}

// GraphDiff reports what changed between two instants. See
// graph.GraphStore.GraphDiff; this is the facade's spelling of it, so a caller
// holding a *DB does not have to reach through Graph().
func (db *DB) GraphDiff(ctx context.Context, from, to time.Time, opts graph.DiffOptions) (*graph.GraphDiffResult, error) {
	if db == nil {
		return nil, fmt.Errorf("cortexdb: graph diff: nil db")
	}
	return db.Graph().GraphDiff(ctx, from, to, opts)
}

// VacuumGraph physically removes graph history that closed before a cutoff.
//
// The only hard delete in the temporal machinery, and the reason everything
// else can keep the record: retraction and versioning both trade storage for
// the ability to answer "what did we believe then", and an operator has to be
// able to decide how far back that is worth paying for.
func (db *DB) VacuumGraph(ctx context.Context, before time.Time, dryRun bool) (*graph.PurgeReport, error) {
	if db == nil {
		return nil, fmt.Errorf("cortexdb: vacuum graph: nil db")
	}
	return db.Graph().Purge(ctx, before, dryRun)
}
