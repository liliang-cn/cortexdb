package graph

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A backdated write is a correction, and a correction must not erase what the
// store believed before it.
//
// Every write path is driven the same way: a version is written, then replaced
// by a write whose ValidFrom is earlier than the moment it is recorded. Before
// the fix, the replaced version was archived closed at that ValidFrom, so an
// as-of read between the stated instant and the write saw neither version —
// and a read-modify-write through GetNode, which hands back the version's own
// ValidFrom, made the replaced version invisible at every instant.

// writePath writes one node or edge with the given status and ValidFrom.
type writePath struct {
	name  string
	write func(t *testing.T, g *GraphStore, ctx context.Context, status string, validFrom time.Time)
	// statusAt reads the status as of at, "" when nothing is visible, and
	// how many versions are visible then.
	statusAt func(t *testing.T, g *GraphStore, ctx context.Context, at time.Time) (string, int)
}

func backdateWritePaths() []writePath {
	node := func(status string, validFrom time.Time) *GraphNode {
		return &GraphNode{ID: "bd:step", Vector: vec(), Content: "step", NodeType: "Step",
			Properties: map[string]any{"status": status}, ValidFrom: validFrom}
	}
	edge := func(status string, validFrom time.Time) *GraphEdge {
		return &GraphEdge{ID: "bd:edge", FromNodeID: "bd:run", ToNodeID: "bd:other", EdgeType: "has_step", Weight: 1,
			Properties: map[string]any{"status": status}, ValidFrom: validFrom}
	}
	nodeStatus := func(t *testing.T, g *GraphStore, ctx context.Context, at time.Time) (string, int) {
		t.Helper()
		return versionsAt(t, g, AsOf(ctx, at), true, "bd:step")
	}
	edgeStatus := func(t *testing.T, g *GraphStore, ctx context.Context, at time.Time) (string, int) {
		t.Helper()
		return versionsAt(t, g, AsOf(ctx, at), false, "bd:edge")
	}
	return []writePath{
		{"UpsertNode", func(t *testing.T, g *GraphStore, ctx context.Context, status string, vf time.Time) {
			t.Helper()
			if err := g.UpsertNode(ctx, node(status, vf)); err != nil {
				t.Fatalf("UpsertNode: %v", err)
			}
		}, nodeStatus},
		{"UpsertNodesBatch", func(t *testing.T, g *GraphStore, ctx context.Context, status string, vf time.Time) {
			t.Helper()
			res, err := g.UpsertNodesBatch(ctx, []*GraphNode{node(status, vf)})
			if err == nil {
				err = res.Err()
			}
			if err != nil {
				t.Fatalf("UpsertNodesBatch: %v", err)
			}
		}, nodeStatus},
		{"ExecuteBatch nodes", func(t *testing.T, g *GraphStore, ctx context.Context, status string, vf time.Time) {
			t.Helper()
			res, err := g.ExecuteBatch(ctx, &BatchGraphOperation{NodeUpserts: []*GraphNode{node(status, vf)}})
			if err == nil {
				err = res.Err()
			}
			if err != nil {
				t.Fatalf("ExecuteBatch: %v", err)
			}
		}, nodeStatus},
		{"UpsertEdge", func(t *testing.T, g *GraphStore, ctx context.Context, status string, vf time.Time) {
			t.Helper()
			if err := g.UpsertEdge(ctx, edge(status, vf)); err != nil {
				t.Fatalf("UpsertEdge: %v", err)
			}
		}, edgeStatus},
		{"UpsertEdgesBatch", func(t *testing.T, g *GraphStore, ctx context.Context, status string, vf time.Time) {
			t.Helper()
			res, err := g.UpsertEdgesBatch(ctx, []*GraphEdge{edge(status, vf)})
			if err == nil {
				err = res.Err()
			}
			if err != nil {
				t.Fatalf("UpsertEdgesBatch: %v", err)
			}
		}, edgeStatus},
		{"ExecuteBatch edges", func(t *testing.T, g *GraphStore, ctx context.Context, status string, vf time.Time) {
			t.Helper()
			res, err := g.ExecuteBatch(ctx, &BatchGraphOperation{EdgeUpserts: []*GraphEdge{edge(status, vf)}})
			if err == nil {
				err = res.Err()
			}
			if err != nil {
				t.Fatalf("ExecuteBatch: %v", err)
			}
		}, edgeStatus},
	}
}

// versionsAt reads the status property of every version of id visible on ctx
// (an as-of context), through the same sources every graph read uses.
func versionsAt(t *testing.T, g *GraphStore, ctx context.Context, node bool, id string) (string, int) {
	t.Helper()
	src, args := g.EdgeSource(ctx)
	if node {
		src, args = g.NodeSource(ctx)
	}
	rows, err := g.query(ctx, `SELECT COALESCE(properties, '') FROM `+src+` AS v WHERE id = ?`, append(args, id)...)
	if err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	defer func() { _ = rows.Close() }()
	status, n := "", 0
	for rows.Next() {
		var props string
		if err := rows.Scan(&props); err != nil {
			t.Fatalf("scan %s: %v", id, err)
		}
		status = statusFromJSON(props)
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return status, n
}

func statusFromJSON(props string) string {
	for _, s := range []string{"running", "done", "corrected"} {
		if strings.Contains(props, `"status":"`+s+`"`) {
			return s
		}
	}
	return "?"
}

func setupBackdateGraph(t *testing.T, g *GraphStore, ctx context.Context) {
	t.Helper()
	if err := g.InitGraphSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	mustNode(t, g, ctx, "bd:run", "run")
	mustNode(t, g, ctx, "bd:other", "other")
}

// Read-modify-write: the replacement states the replaced version's own
// ValidFrom, so it covers the whole of the old version's validity.
func TestWritingBackTheReplacedVersionsValidFromKeepsItReadable(t *testing.T) {
	for _, p := range backdateWritePaths() {
		for _, b := range backends(t) { // a fresh store per path: they share ids
			t.Run(b.name+"/"+p.name, func(t *testing.T) {
				ctx := context.Background()
				setupBackdateGraph(t, b.store, ctx)
				before := b.store.Now()
				opened := b.store.Now()
				p.write(t, b.store, ctx, "running", opened)
				during := b.store.Now()
				p.write(t, b.store, ctx, "done", opened)

				if got, n := p.statusAt(t, b.store, ctx, during); got != "running" || n != 1 {
					t.Errorf("as of the run: %q in %d version(s), want running in 1", got, n)
				}
				if got, n := p.statusAt(t, b.store, ctx, b.store.Now()); got != "done" || n != 1 {
					t.Errorf("now: %q in %d version(s), want done in 1", got, n)
				}
				if _, n := p.statusAt(t, b.store, ctx, before); n != 0 {
					t.Errorf("before the first write: %d version(s) visible, want 0", n)
				}
			})
		}
	}
}

// A correction backdated into the middle of the old version: the part before
// the stated instant is still true, and the rest was believed until the write.
func TestABackdatedCorrectionKeepsWhatWasBelievedUntilIt(t *testing.T) {
	for _, p := range backdateWritePaths() {
		for _, b := range backends(t) { // a fresh store per path: they share ids
			t.Run(b.name+"/"+p.name, func(t *testing.T) {
				ctx := context.Background()
				setupBackdateGraph(t, b.store, ctx)
				p.write(t, b.store, ctx, "running", time.Time{})
				early := b.store.Now()
				correctedFrom := b.store.Now()
				late := b.store.Now()
				p.write(t, b.store, ctx, "corrected", correctedFrom)
				after := b.store.Now()

				for _, c := range []struct {
					name string
					at   time.Time
					want string
				}{
					{"before the stated instant", early, "running"},
					{"between the stated instant and the write", late, "running"},
					{"after the write", after, "corrected"},
				} {
					if got, n := p.statusAt(t, b.store, ctx, c.at); got != c.want || n != 1 {
						t.Errorf("%s: %q in %d version(s), want %s in 1", c.name, got, n, c.want)
					}
				}
			})
		}
	}
}

// The ordinary write — recorded when it says it became true — archives exactly
// what it did before: one row, closed where the new version opens, never
// retracted.
func TestAnOrdinaryWriteArchivesOneUnretractedRow(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			setupBackdateGraph(t, b.store, ctx)
			for _, status := range []string{"running", "done"} {
				if err := b.store.UpsertNode(ctx, &GraphNode{ID: "bd:step", Vector: vec(), Content: "step",
					Properties: map[string]any{"status": status}}); err != nil {
					t.Fatalf("UpsertNode: %v", err)
				}
			}
			hist, err := b.store.NodeHistory(ctx, "bd:step")
			if err != nil {
				t.Fatalf("NodeHistory: %v", err)
			}
			if len(hist) != 1 {
				t.Fatalf("history has %d rows, want 1", len(hist))
			}
			if !hist[0].RetractedAt.IsZero() {
				t.Errorf("an ordinary supersede was recorded as retracted at %s", hist[0].RetractedAt)
			}
			if hist[0].Reason != ReasonSuperseded {
				t.Errorf("reason = %q, want %q", hist[0].Reason, ReasonSuperseded)
			}
		})
	}
}

// The correction row is a superseded version like any other, and says when the
// store stopped believing it.
func TestACorrectionRowSaysWhenItWasRetracted(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			setupBackdateGraph(t, b.store, ctx)
			opened := b.store.Now()
			for _, status := range []string{"running", "done"} {
				if err := b.store.UpsertNode(ctx, &GraphNode{ID: "bd:step", Vector: vec(), Content: "step",
					Properties: map[string]any{"status": status}, ValidFrom: opened}); err != nil {
					t.Fatalf("UpsertNode: %v", err)
				}
			}
			live, err := b.store.GetNode(ctx, "bd:step")
			if err != nil {
				t.Fatalf("GetNode: %v", err)
			}
			hist, err := b.store.NodeHistory(ctx, "bd:step")
			if err != nil {
				t.Fatalf("NodeHistory: %v", err)
			}
			if len(hist) != 1 {
				t.Fatalf("history has %d rows, want 1 (the corrected belief, and no empty interval)", len(hist))
			}
			h := hist[0]
			if !h.RetractedAt.Equal(live.RecordedAt) {
				t.Errorf("retracted at %s, want the instant the correction was recorded, %s", h.RetractedAt, live.RecordedAt)
			}
			if !h.ValidTo.IsZero() {
				t.Errorf("the corrected belief was closed in valid time at %s; it was believed open-ended", h.ValidTo)
			}
			if h.Reason != ReasonSuperseded || h.SupersededBy != "bd:step" {
				t.Errorf("invalidation = %+v, want superseded by its own id", h.Invalidation)
			}
		})
	}
}
