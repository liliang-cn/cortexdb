package liveview

// Reading the graph as it was.
//
// v2.99.0 gave the graph point-in-time reads and this page had no way to ask
// for one. Everything on it — the poll, the diff, the stream — answers exactly
// one question, "what is true now", which is the question the page was built
// for and is not the only one worth asking of a brain that keeps its history.
//
// The as-of read is deliberately not a fourth thing on the hub. Structure is
// polled because the graph moves; the past does not move, so there is nothing
// to poll and nothing to diff. A pinned page is served one snapshot and then
// left alone — see handleStream — which is also why pinning is per-page and not
// a setting on the server: one reader looking at last Tuesday must not stop the
// reader beside them from watching today.
//
// Why not GraphSnapshotAt. It is the right call for the question it answers —
// how big was the graph then, with a bounded sample of rows — and the wrong one
// for this page, which needs the same six-hundred-node most-connected core the
// live read builds, ranked by degree, with chunk nodes and their edges dropped
// and each record's grade alongside. SnapshotOptions.Sample takes rows off the
// top rather than off the ranking, so a page drawn from it would be an
// arbitrary slice with dangling edges — precisely what LoadLocal's degree cap
// exists to avoid. GraphDiff is the same story: it answers what changed between
// two instants, and the page draws a graph rather than a changelog. So the
// columns are read directly, through the store's own as-of machinery rather
// than by hand: graph.AsOf puts the instant on the context and NodeSource /
// EdgeSource turn it into the union of the live table and its history under the
// half-open visibility predicate. That is the same semantics every other past
// read in the module gets, because it is literally the same code path.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// localReadAsOf reads the entity graph as it stood at one instant.
func localReadAsOf(db *cortexdb.DB) func(context.Context, time.Time) ([]Node, []Edge, error) {
	return func(ctx context.Context, at time.Time) ([]Node, []Edge, error) {
		if at.IsZero() {
			// The zero time means "now" to graph.ReadOptions, and a caller
			// that reached this hook meant to ask about an instant. Refusing
			// is better than quietly answering the live question under a
			// banner that says the page is showing the past.
			return nil, nil, fmt.Errorf("read as of: no instant given")
		}
		if err := db.Graph().InitGraphSchema(ctx); err != nil {
			return nil, nil, fmt.Errorf("read as of: %w", err)
		}
		asOf := graph.AsOf(ctx, at)
		nodes, nodeArgs := db.Graph().NodeSource(asOf)
		edges, edgeArgs := db.Graph().EdgeSource(asOf)
		return loadGraph(ctx, db.SQL(), graphSource{
			dialect:  db.Dialect(),
			nodes:    nodes,
			nodeArgs: nodeArgs,
			edges:    edges,
			edgeArgs: edgeArgs,
		})
	}
}

// ParseAsOf reads the instant a page or an embedder pinned itself to.
//
// Two spellings, because two different things write this parameter. The page
// sends unix milliseconds, which is what a datetime field yields and what
// survives a round trip through JavaScript without a timezone argument. A
// person putting ?as_of= in the address bar writes RFC 3339, because that is
// what the rest of this store's timestamps look like and typing a millisecond
// count by hand is not a thing anybody does.
//
// An unparseable value is an error rather than a fallback to now. Everywhere
// else on this page a bad parameter falls back — a page is not worth a 400 —
// but "now" is the one wrong answer this parameter must never give: the whole
// point of the pin is that the reader is told they are looking at the past, and
// silently showing them the present under that banner is worse than showing
// them nothing.
func ParseAsOf(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if ms, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if ms <= 0 {
			return time.Time{}, fmt.Errorf("as_of %q is not an instant", raw)
		}
		return time.UnixMilli(ms).UTC(), nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("as_of %q is neither unix milliseconds nor RFC 3339", raw)
}
