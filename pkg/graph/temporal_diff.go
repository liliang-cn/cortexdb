package graph

// What changed between two instants.
//
// A snapshot answers "what did we believe then". A diff answers the question
// somebody actually asks after an incident — "what changed between the last
// good state and now" — and it is the one an as-of read cannot answer by
// itself, because the caller would have to fetch both graphs and compare them
// in its own memory.
//
// The comparison is a merge of two id-ordered streams, one per instant, in
// bounded pages. It never holds a whole graph: the working set is two pages,
// whatever the store contains. That is what makes it safe to expose as a tool
// on a brain with four hundred thousand nodes in it.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// DiffKind is why a row is in the diff.
type DiffKind string

const (
	// DiffAdded: present at `to`, absent at `from`.
	DiffAdded DiffKind = "added"
	// DiffRetracted: present at `from`, absent at `to`. Retracted rather than
	// deleted, because that is what it is now — the row is still readable at
	// any instant before it went.
	DiffRetracted DiffKind = "retracted"
	// DiffChanged: present at both, saying something different.
	DiffChanged DiffKind = "changed"
)

// NodeVersion is a node as it stood at one instant, without its vector.
//
// The vector is left out on purpose: a diff of a thousand nodes would carry
// three megabytes of floats that answer nothing about what changed, and content
// is what changed.
type NodeVersion struct {
	ID         string    `json:"id"`
	Content    string    `json:"content,omitempty"`
	NodeType   string    `json:"node_type,omitempty"`
	Properties string    `json:"properties,omitempty"`
	ValidFrom  time.Time `json:"valid_from,omitzero"`
	ValidTo    time.Time `json:"valid_to,omitzero"`
}

func (v NodeVersion) fingerprint() string {
	return v.Content + "\x00" + v.NodeType + "\x00" + v.Properties
}

// Interval is this version's validity, for Relate.
func (v NodeVersion) Interval() Interval { return IntervalOf(v.ValidFrom, v.ValidTo) }

// EdgeVersion is an edge as it stood at one instant.
type EdgeVersion struct {
	ID         string    `json:"id"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	EdgeType   string    `json:"edge_type,omitempty"`
	Weight     float64   `json:"weight,omitempty"`
	Properties string    `json:"properties,omitempty"`
	ValidFrom  time.Time `json:"valid_from,omitzero"`
	ValidTo    time.Time `json:"valid_to,omitzero"`
}

func (v EdgeVersion) fingerprint() string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%g\x00%s", v.From, v.To, v.EdgeType, v.Weight, v.Properties)
}

// Interval is this version's validity, for Relate.
func (v EdgeVersion) Interval() Interval { return IntervalOf(v.ValidFrom, v.ValidTo) }

// NodeChange and EdgeChange carry both sides, so a reader sees what it said
// before as well as what it says now. Before is nil for an addition and After
// is nil for a retraction; a diff that reported only ids would send the caller
// back for two more reads per row.
type NodeChange struct {
	ID     string       `json:"id"`
	Kind   DiffKind     `json:"kind"`
	Before *NodeVersion `json:"before,omitempty"`
	After  *NodeVersion `json:"after,omitempty"`
}

type EdgeChange struct {
	ID     string       `json:"id"`
	Kind   DiffKind     `json:"kind"`
	Before *EdgeVersion `json:"before,omitempty"`
	After  *EdgeVersion `json:"after,omitempty"`
}

// DiffOptions bounds a diff.
type DiffOptions struct {
	// Limit caps each of the two lists. Zero means 100; a diff is a report, and
	// an unbounded one is a report nobody reads and a response nobody can pass
	// to a model.
	Limit int
	// Cursor continues a previous diff. It is the last id that page emitted;
	// nodes and edges are paged together by the same id, which is safe because
	// both streams are ordered by it.
	Cursor string
	// NodeTypes and EdgeTypes narrow the diff to a kind of thing.
	NodeTypes []string
	EdgeTypes []string
	// MaxIntervalPairs caps the Allen relations reported. Zero means 50.
	MaxIntervalPairs int
}

// GraphDiffResult is what changed, and how the changed facts sit in time.
type GraphDiffResult struct {
	From  time.Time    `json:"from"`
	To    time.Time    `json:"to"`
	Nodes []NodeChange `json:"nodes"`
	Edges []EdgeChange `json:"edges"`
	// NextCursor is non-empty when the limit cut the walk short. Pass it back
	// as DiffOptions.Cursor.
	NextCursor string `json:"next_cursor,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	// IntervalRelations names how the changed edges about one subject sit
	// against each other in time — which is where "the runbook's claim ended
	// when the incident's began" comes from, instead of two rows of timestamps
	// a reader has to compare by eye.
	IntervalRelations []IntervalRelation `json:"interval_relations,omitempty"`
}

// GraphDiff reports what the graph gained, lost and changed between two
// instants.
//
// Both instants are read through the same as-of machinery as any other past
// read, so a diff and a pair of snapshots can never disagree. `from` after `to`
// is refused rather than silently swapped: a caller that has them backwards is
// asking a different question than the one it would get.
func (g *GraphStore) GraphDiff(ctx context.Context, from, to time.Time, opts DiffOptions) (*GraphDiffResult, error) {
	if from.IsZero() || to.IsZero() {
		return nil, fmt.Errorf("cortexdb/graph: diff: both instants are required")
	}
	if to.Before(from) {
		return nil, fmt.Errorf("cortexdb/graph: diff: `to` (%s) is before `from` (%s)",
			to.Format(time.RFC3339Nano), from.Format(time.RFC3339Nano))
	}
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	before, after := AsOf(ctx, from), AsOf(ctx, to)
	out := &GraphDiffResult{From: stamp(from), To: stamp(to), Nodes: []NodeChange{}, Edges: []EdgeChange{}}

	nodeCursor, nodeMore, err := diffStream(before, after, opts.Cursor, limit,
		func(c context.Context, cursor string, n int) ([]NodeVersion, error) {
			return g.nodePage(c, cursor, n, opts.NodeTypes)
		},
		func(id string, kind DiffKind, b, a *NodeVersion) {
			out.Nodes = append(out.Nodes, NodeChange{ID: id, Kind: kind, Before: b, After: a})
		})
	if err != nil {
		return nil, err
	}

	edgeCursor, edgeMore, err := diffStream(before, after, opts.Cursor, limit,
		func(c context.Context, cursor string, n int) ([]EdgeVersion, error) {
			return g.edgePage(c, cursor, n, opts.EdgeTypes)
		},
		func(id string, kind DiffKind, b, a *EdgeVersion) {
			out.Edges = append(out.Edges, EdgeChange{ID: id, Kind: kind, Before: b, After: a})
		})
	if err != nil {
		return nil, err
	}

	// One cursor for both lists, and it is the lesser of the two: resuming from
	// the further-along stream's position would step over rows the other stream
	// has not reached. A page that re-reports a change is a nuisance; one that
	// drops it is a wrong answer.
	out.Truncated = nodeMore || edgeMore
	if out.Truncated {
		out.NextCursor = nodeCursor
		if !nodeMore || (edgeMore && edgeCursor < out.NextCursor) {
			out.NextCursor = edgeCursor
		}
	}

	rels, err := g.relateChangedEdges(ctx, out.Edges, opts.MaxIntervalPairs)
	if err != nil {
		return nil, err
	}
	out.IntervalRelations = rels
	return out, nil
}

// versioned is what both streams have in common: an id, a fingerprint and a
// validity interval.
type versioned interface {
	fingerprint() string
}

// diffStream merges two id-ordered pages of the same kind, emitting a change
// per id that differs.
//
// Generic over the row type because the node walk and the edge walk are the
// same walk, and writing it twice is how the two end up disagreeing about what
// "changed" means after the first bug fix in one of them.
func diffStream[T versioned](
	before, after context.Context,
	cursor string,
	limit int,
	page func(ctx context.Context, cursor string, n int) ([]T, error),
	emit func(id string, kind DiffKind, before, after *T),
) (nextCursor string, more bool, err error) {
	// A page four times the limit, so a stretch of identical rows — the common
	// case, since most of a graph does not change — is walked in few round
	// trips rather than one per emitted change.
	pageSize := limit * 4
	if pageSize < 64 {
		pageSize = 64
	}

	var (
		aRows, bRows []T
		aIdx, bIdx   int
		aCursor      = cursor
		bCursor      = cursor
		aDone, bDone bool
		emitted      int
		// lastDone is the greatest id both streams have finished with. It is
		// the cursor a truncated walk hands back: paging from it resumes at the
		// first row that did not fit, with nothing skipped and nothing
		// reported twice.
		lastDone string
	)

	fill := func() error {
		if aIdx >= len(aRows) && !aDone {
			rows, err := page(before, aCursor, pageSize)
			if err != nil {
				return err
			}
			aRows, aIdx = rows, 0
			if len(rows) < pageSize {
				aDone = true
			}
			if len(rows) > 0 {
				aCursor = idOf(rows[len(rows)-1])
			}
		}
		if bIdx >= len(bRows) && !bDone {
			rows, err := page(after, bCursor, pageSize)
			if err != nil {
				return err
			}
			bRows, bIdx = rows, 0
			if len(rows) < pageSize {
				bDone = true
			}
			if len(rows) > 0 {
				bCursor = idOf(rows[len(rows)-1])
			}
		}
		return nil
	}

	for {
		if err := fill(); err != nil {
			return "", false, err
		}
		aHas, bHas := aIdx < len(aRows), bIdx < len(bRows)
		if !aHas && !bHas {
			return "", false, nil
		}
		if emitted >= limit {
			return lastDone, true, nil
		}

		switch {
		case !bHas || (aHas && idOf(aRows[aIdx]) < idOf(bRows[bIdx])):
			row := aRows[aIdx]
			emit(idOf(row), DiffRetracted, &row, nil)
			lastDone = idOf(row)
			aIdx++
			emitted++
		case !aHas || idOf(bRows[bIdx]) < idOf(aRows[aIdx]):
			row := bRows[bIdx]
			emit(idOf(row), DiffAdded, nil, &row)
			lastDone = idOf(row)
			bIdx++
			emitted++
		default:
			a, b := aRows[aIdx], bRows[bIdx]
			if a.fingerprint() != b.fingerprint() {
				emit(idOf(a), DiffChanged, &a, &b)
				emitted++
			}
			lastDone = idOf(a)
			aIdx++
			bIdx++
		}
	}
}

// idOf reads the id out of either version shape. A tiny type switch rather than
// another method on the interface, because NodeVersion and EdgeVersion are part
// of the public response and an exported `fingerprint`-style ID() would add a
// method to a struct that already has the field.
func idOf(v any) string {
	switch t := v.(type) {
	case NodeVersion:
		return t.ID
	case EdgeVersion:
		return t.ID
	}
	return ""
}

// nodePage and edgePage read one id-ordered page from the source the context
// names — which is the live table for a `to` of now and the union for anything
// past, without either of them knowing which.
func (g *GraphStore) nodePage(ctx context.Context, cursor string, limit int, nodeTypes []string) ([]NodeVersion, error) {
	src, args := g.nodeSource(ctx)
	q := `SELECT id, COALESCE(content, ''), COALESCE(node_type, ''), COALESCE(properties, ''), valid_from, valid_to
	      FROM ` + src + ` AS n WHERE id > ?`
	args = append(args, cursor)
	if len(nodeTypes) > 0 {
		holes := make([]string, len(nodeTypes))
		for i, t := range nodeTypes {
			holes[i] = "?"
			args = append(args, t)
		}
		q += " AND node_type IN (" + strings.Join(holes, ",") + ")"
	}
	q += " ORDER BY id LIMIT ?"
	args = append(args, limit)

	rows, err := g.query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("cortexdb/graph: diff: node page: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]NodeVersion, 0, limit)
	for rows.Next() {
		var v NodeVersion
		var tt temporalScan
		if err := rows.Scan(&v.ID, &v.Content, &v.NodeType, &v.Properties,
			&tt.validFrom, &tt.validTo); err != nil {
			return nil, fmt.Errorf("cortexdb/graph: diff: node page: %w", err)
		}
		v.ValidFrom, v.ValidTo = nullTime(tt.validFrom), nullTime(tt.validTo)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (g *GraphStore) edgePage(ctx context.Context, cursor string, limit int, edgeTypes []string) ([]EdgeVersion, error) {
	src, args := g.edgeSource(ctx)
	q := `SELECT id, from_node_id, to_node_id, COALESCE(edge_type, ''), COALESCE(weight, 0), COALESCE(properties, ''), valid_from, valid_to
	      FROM ` + src + ` AS e WHERE id > ?`
	args = append(args, cursor)
	if len(edgeTypes) > 0 {
		holes := make([]string, len(edgeTypes))
		for i, t := range edgeTypes {
			holes[i] = "?"
			args = append(args, t)
		}
		q += " AND edge_type IN (" + strings.Join(holes, ",") + ")"
	}
	q += " ORDER BY id LIMIT ?"
	args = append(args, limit)

	rows, err := g.query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("cortexdb/graph: diff: edge page: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]EdgeVersion, 0, limit)
	for rows.Next() {
		var v EdgeVersion
		var tt temporalScan
		if err := rows.Scan(&v.ID, &v.From, &v.To, &v.EdgeType, &v.Weight, &v.Properties,
			&tt.validFrom, &tt.validTo); err != nil {
			return nil, fmt.Errorf("cortexdb/graph: diff: edge page: %w", err)
		}
		v.ValidFrom, v.ValidTo = nullTime(tt.validFrom), nullTime(tt.validTo)
		out = append(out, v)
	}
	return out, rows.Err()
}

// relateChangedEdges names how the changed edges about one subject sit in time.
//
// The interval used is the fact's final one, not the one it had at either
// endpoint of the diff, and that distinction is the whole value of the output.
// A retracted edge read as of `from` still has an open interval — at that
// instant nobody knew it would end — so relating the two snapshots as they
// stood would compare two open-ended claims and report `overlaps` for the pair
// whose whole story is that one ended exactly where the other began. What a
// reader wants is the best account available now, so the closing instant is
// read back out of history for the retracted ones.
func (g *GraphStore) relateChangedEdges(ctx context.Context, changes []EdgeChange, maxPairs int) ([]IntervalRelation, error) {
	closed := make([]string, 0)
	for _, c := range changes {
		if c.Kind == DiffRetracted {
			closed = append(closed, c.ID)
		}
	}
	ends, err := g.finalEdgeIntervals(ctx, closed)
	if err != nil {
		return nil, err
	}

	edges := make([]*GraphEdge, 0, len(changes))
	for _, c := range changes {
		v := c.After
		if v == nil {
			v = c.Before
		}
		if v == nil {
			continue
		}
		iv := IntervalOf(v.ValidFrom, v.ValidTo)
		if final, ok := ends[c.ID]; ok {
			iv = final
		}
		edges = append(edges, &GraphEdge{
			ID:         v.ID,
			FromNodeID: v.From,
			ToNodeID:   v.To,
			EdgeType:   v.EdgeType,
			ValidFrom:  iv.From,
			ValidTo:    iv.To,
		})
	}
	rels := RelateEdges(edges, maxPairs)
	if len(rels) == 0 {
		return nil, nil
	}
	return rels, nil
}

// finalEdgeIntervals reads the last recorded validity of edges that are no
// longer current.
//
// The latest archived row per id, which is the one that closed: earlier rows
// are superseded versions whose ends are the next version's beginnings. Bounded
// by the diff's own page, so this is one extra query per diff and not one per
// edge.
func (g *GraphStore) finalEdgeIntervals(ctx context.Context, ids []string) (map[string]Interval, error) {
	out := map[string]Interval{}
	if len(ids) == 0 {
		return out, nil
	}
	for _, chunk := range idChunks(ids) {
		holes, args := placeholderList(chunk)
		// valid_to and retracted_at are selected separately and folded in Go
		// rather than COALESCEd in SQL. modernc's SQLite driver decides to
		// parse a value as a time from the *declared column type*, and a
		// COALESCE has none — the row comes back as a string and the scan
		// fails with "unsupported Scan, storing driver.Value type string into
		// type *time.Time", naming the expression and not the cause.
		rows, err := g.query(ctx, `
			SELECT id, valid_from, valid_to, retracted_at
			FROM graph_edge_history
			WHERE id IN (`+holes+`)
			ORDER BY id, valid_from`, args...)
		if err != nil {
			return nil, fmt.Errorf("cortexdb/graph: diff: final intervals: %w", err)
		}
		for rows.Next() {
			var id string
			var tt temporalScan
			if err := rows.Scan(&id, &tt.validFrom, &tt.validTo, &tt.retractedAt); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("cortexdb/graph: diff: final intervals: %w", err)
			}
			end := nullTime(tt.validTo)
			if end.IsZero() {
				end = nullTime(tt.retractedAt)
			}
			// Ordered by valid_from, so the last row wins.
			out[id] = IntervalOf(nullTime(tt.validFrom), end)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}
