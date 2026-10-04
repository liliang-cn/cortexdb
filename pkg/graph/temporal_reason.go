package graph

// Why a row stopped being current.
//
// temporal.go made the past readable: a retracted or superseded row moves to
// graph_*_history with the instant it closed. What it did not keep is *why*.
// A history row that says "this edge ended at 14:02" and nothing else cannot
// tell a correction from a document being deleted, an entity merge from a
// prune, or name what replaced it — and "why did the graph stop believing
// this" is the question an audit of the past actually asks.
//
// Three columns on each history table answer it:
//
//	reason         the kind of change that closed the row (Reason* below)
//	superseded_by  the id of what replaced it, when something did: the next
//	               version of the same record, the entity it was merged into,
//	               the fact that superseded it
//	producer       which code path or caller made the change
//
// # How a reason reaches the row
//
// On the context, like ReadOptions, and for the same reason: the archive
// statements sit at the bottom of every write path — UpsertNode, the batch
// upserts, DeleteNode, RetractNodes, ArchiveNodesTx from pkg/cortexdb's own
// transaction — and threading a parameter through all of them would change
// every public signature for a value most callers leave at its default.
//
// Every path also has a default, so no archived row is ever written without a
// reason: a version replaced by an upsert is ReasonSuperseded (superseded_by
// is its own id — the next version of the same record), and a delete is
// ReasonRetracted. A caller that knows more says so with WithInvalidation.
//
// Rows archived before this release carry NULL in all three, and are reported
// with an empty Reason: the store genuinely does not know, and inventing one
// would be the one wrong answer.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Reasons a row leaves the live tables. The set is open — a caller may state
// its own — but these are the ones this module writes.
const (
	// ReasonSuperseded: a newer version replaced this one. For a record
	// rewritten in place, SupersededBy is the record's own id.
	ReasonSuperseded = "superseded"
	// ReasonRetracted: deleted, with nothing in its place.
	ReasonRetracted = "retracted"
	// ReasonMerged: the entity was folded into another; SupersededBy names
	// the survivor. Also used for the edges repointed or deduplicated by the
	// merge.
	ReasonMerged = "merged"
	// ReasonDocumentDeleted: removed because the document it came from was.
	ReasonDocumentDeleted = "document_deleted"
	// ReasonOrphaned: an entity no remaining document mentions.
	ReasonOrphaned = "orphaned"
	// ReasonPruned: removed by a maintenance heuristic (junk or dangling).
	ReasonPruned = "pruned"
)

// Producers this package writes when a caller names none.
const (
	ProducerGraphUpsert  = "graph.upsert"
	ProducerGraphRetract = "graph.retract"
	ProducerGraphMerge   = "graph.merge_entities"
)

// Invalidation is why a row left the live tables.
type Invalidation struct {
	Reason       string `json:"reason,omitempty"`
	SupersededBy string `json:"superseded_by,omitempty"`
	Producer     string `json:"producer,omitempty"`
}

// IsZero reports whether nothing is known about the change.
func (inv Invalidation) IsZero() bool {
	return inv.Reason == "" && inv.SupersededBy == "" && inv.Producer == ""
}

type invalidationKey struct{}

// WithInvalidation returns a context whose graph writes record inv on every
// row they move to history. Fields left empty fall back to the path's default.
//
//	ctx = graph.WithInvalidation(ctx, graph.Invalidation{
//	    Reason: graph.ReasonMerged, SupersededBy: canonicalID, Producer: "resolve"})
//	store.DeleteNode(ctx, aliasID)
func WithInvalidation(ctx context.Context, inv Invalidation) context.Context {
	inv.Reason = strings.TrimSpace(inv.Reason)
	inv.SupersededBy = strings.TrimSpace(inv.SupersededBy)
	inv.Producer = strings.TrimSpace(inv.Producer)
	return context.WithValue(ctx, invalidationKey{}, inv)
}

// InvalidationFrom reads back what WithInvalidation put on the context.
func InvalidationFrom(ctx context.Context) Invalidation {
	if ctx == nil {
		return Invalidation{}
	}
	inv, _ := ctx.Value(invalidationKey{}).(Invalidation)
	return inv
}

func (inv Invalidation) withDefaults(reason, supersededBy, producer string) Invalidation {
	if inv.Reason == "" {
		inv.Reason = reason
	}
	if inv.SupersededBy == "" {
		inv.SupersededBy = supersededBy
	}
	if inv.Producer == "" {
		inv.Producer = producer
	}
	return inv
}

// retractionFrom is the invalidation a delete path records.
func retractionFrom(ctx context.Context) Invalidation {
	return InvalidationFrom(ctx).withDefaults(ReasonRetracted, "", ProducerGraphRetract)
}

// versionFrom is the invalidation an upsert records for the version it
// replaces: the next version of the same record superseded it.
func versionFrom(ctx context.Context, id string) Invalidation {
	return InvalidationFrom(ctx).withDefaults(ReasonSuperseded, id, ProducerGraphUpsert)
}

// invalidationColumns migrates history tables that predate these columns.
// No DEFAULT: an old row's reason is unknown, and NULL says exactly that.
var invalidationColumns = []string{
	`ALTER TABLE graph_node_history ADD COLUMN reason TEXT`,
	`ALTER TABLE graph_node_history ADD COLUMN superseded_by TEXT`,
	`ALTER TABLE graph_node_history ADD COLUMN producer TEXT`,
	`ALTER TABLE graph_edge_history ADD COLUMN reason TEXT`,
	`ALTER TABLE graph_edge_history ADD COLUMN superseded_by TEXT`,
	`ALTER TABLE graph_edge_history ADD COLUMN producer TEXT`,
}

// invalidationColumnList is appended to the history INSERT column lists. It is
// deliberately not part of nodeColumns/edgeColumns: those are shared with the
// live tables for the as-of UNION, and the live tables have no reason to give.
const invalidationColumnList = `reason, superseded_by, producer`

// archiveNodeVersionArgs and archiveEdgeVersionArgs bind archive*VersionSQL in
// its textual parameter order: the closing instant, the invalidation, then the
// WHERE clause, whose last two parameters are keepsAPartBefore's.
func archiveNodeVersionArgs(ctx context.Context, at, recorded time.Time, id, content, nodeType, properties string) []any {
	inv := versionFrom(ctx, id)
	return []any{at, inv.Reason, nullIfEmpty(inv.SupersededBy), inv.Producer, id, content, nodeType, properties,
		backdated(at, recorded), at}
}

func archiveEdgeVersionArgs(ctx context.Context, at, recorded time.Time, id, from, to, edgeType string, weight float64, properties string) []any {
	inv := versionFrom(ctx, id)
	return []any{at, inv.Reason, nullIfEmpty(inv.SupersededBy), inv.Producer, id, from, to, edgeType, weight, properties,
		backdated(at, recorded), at}
}

// backdated is keepsAPartBefore's flag: 1 when the write opens its version
// before the instant it is recorded. An integer, not a bool, so the comparison
// reads the same on both backends.
func backdated(at, recorded time.Time) int {
	if at.Before(recorded) {
		return 1
	}
	return 0
}

// --- reading -----------------------------------------------------------------

// NodeHistoryEntry is one closed version of a node, with why it closed.
type NodeHistoryEntry struct {
	NodeVersion
	RecordedAt  time.Time `json:"recorded_at,omitzero"`
	RetractedAt time.Time `json:"retracted_at,omitzero"`
	Invalidation
}

// EdgeHistoryEntry is one closed version of an edge, with why it closed.
type EdgeHistoryEntry struct {
	EdgeVersion
	RecordedAt  time.Time `json:"recorded_at,omitzero"`
	RetractedAt time.Time `json:"retracted_at,omitzero"`
	Invalidation
}

// NodeHistory returns every closed version of a node, oldest first. The
// current version, if any, is not included — GetNode reads it.
func (g *GraphStore) NodeHistory(ctx context.Context, id string) ([]NodeHistoryEntry, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := g.query(ctx, `
		SELECT id, COALESCE(content, ''), COALESCE(node_type, ''), COALESCE(properties, ''),
		       valid_from, valid_to, recorded_at, retracted_at,
		       COALESCE(reason, ''), COALESCE(superseded_by, ''), COALESCE(producer, '')
		FROM graph_node_history WHERE id = ?
		ORDER BY recorded_at, valid_from`, id)
	if err != nil {
		return nil, fmt.Errorf("cortexdb/graph: node history: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []NodeHistoryEntry{}
	for rows.Next() {
		var e NodeHistoryEntry
		var tt temporalScan
		if err := rows.Scan(&e.ID, &e.Content, &e.NodeType, &e.Properties,
			&tt.validFrom, &tt.validTo, &tt.recordedAt, &tt.retractedAt,
			&e.Reason, &e.SupersededBy, &e.Producer); err != nil {
			return nil, fmt.Errorf("cortexdb/graph: node history: %w", err)
		}
		e.ValidFrom, e.ValidTo = nullTime(tt.validFrom), nullTime(tt.validTo)
		e.RecordedAt, e.RetractedAt = nullTime(tt.recordedAt), nullTime(tt.retractedAt)
		out = append(out, e)
	}
	return out, rows.Err()
}

// EdgeHistory returns every closed version of an edge, oldest first.
func (g *GraphStore) EdgeHistory(ctx context.Context, id string) ([]EdgeHistoryEntry, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := g.query(ctx, `
		SELECT id, from_node_id, to_node_id, COALESCE(edge_type, ''), COALESCE(weight, 0), COALESCE(properties, ''),
		       valid_from, valid_to, recorded_at, retracted_at,
		       COALESCE(reason, ''), COALESCE(superseded_by, ''), COALESCE(producer, '')
		FROM graph_edge_history WHERE id = ?
		ORDER BY recorded_at, valid_from`, id)
	if err != nil {
		return nil, fmt.Errorf("cortexdb/graph: edge history: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []EdgeHistoryEntry{}
	for rows.Next() {
		var e EdgeHistoryEntry
		var tt temporalScan
		if err := rows.Scan(&e.ID, &e.From, &e.To, &e.EdgeType, &e.Weight, &e.Properties,
			&tt.validFrom, &tt.validTo, &tt.recordedAt, &tt.retractedAt,
			&e.Reason, &e.SupersededBy, &e.Producer); err != nil {
			return nil, fmt.Errorf("cortexdb/graph: edge history: %w", err)
		}
		e.ValidFrom, e.ValidTo = nullTime(tt.validFrom), nullTime(tt.validTo)
		e.RecordedAt, e.RetractedAt = nullTime(tt.recordedAt), nullTime(tt.retractedAt)
		out = append(out, e)
	}
	return out, rows.Err()
}

// HistoryReasonCount is how many history rows carry each reason, with rows
// that predate the reason columns counted under "".
type HistoryReasonCount struct {
	Nodes map[string]int `json:"nodes"`
	Edges map[string]int `json:"edges"`
}

// HistoryReasons tallies history rows by reason — the share with an empty
// reason is how much of the recorded past cannot say why it ended.
func (g *GraphStore) HistoryReasons(ctx context.Context) (*HistoryReasonCount, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	out := &HistoryReasonCount{Nodes: map[string]int{}, Edges: map[string]int{}}
	for table, dst := range map[string]map[string]int{"graph_node_history": out.Nodes, "graph_edge_history": out.Edges} {
		rows, err := g.query(ctx, `SELECT COALESCE(reason, ''), COUNT(*) FROM `+table+` GROUP BY COALESCE(reason, '')`)
		if err != nil {
			return nil, fmt.Errorf("cortexdb/graph: history reasons: %w", err)
		}
		for rows.Next() {
			var r string
			var n int
			if err := rows.Scan(&r, &n); err != nil {
				_ = rows.Close()
				return nil, err
			}
			dst[r] = n
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}

// closingInvalidations finds, for each id, the history row that closed the
// version current at `from` — the first closure after `from` and no later than
// `to` — and returns why it closed. A diff attaches this to its retracted and
// changed rows so a reader sees the cause beside the change.
//
// valid_to and retracted_at are read separately and folded in Go, for the
// reason finalEdgeIntervals gives: a COALESCE has no declared type and
// modernc's driver will not scan it as a time.
func (g *GraphStore) closingInvalidations(ctx context.Context, table string, ids []string, from, to time.Time) (map[string]Invalidation, error) {
	out := map[string]Invalidation{}
	if len(ids) == 0 {
		return out, nil
	}
	from, to = stamp(from), stamp(to)
	best := map[string]time.Time{}
	for _, chunk := range idChunks(ids) {
		holes, args := placeholderList(chunk)
		rows, err := g.query(ctx, `
			SELECT id, valid_to, retracted_at,
			       COALESCE(reason, ''), COALESCE(superseded_by, ''), COALESCE(producer, '')
			FROM `+table+` WHERE id IN (`+holes+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("cortexdb/graph: diff: invalidations: %w", err)
		}
		for rows.Next() {
			var id string
			var validTo, retractedAt sql.NullTime
			var inv Invalidation
			if err := rows.Scan(&id, &validTo, &retractedAt, &inv.Reason, &inv.SupersededBy, &inv.Producer); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("cortexdb/graph: diff: invalidations: %w", err)
			}
			closed := nullTime(retractedAt)
			if closed.IsZero() {
				closed = nullTime(validTo)
			}
			if closed.IsZero() || !closed.After(from) || closed.After(to) {
				continue
			}
			if prev, ok := best[id]; ok && !closed.Before(prev) {
				continue
			}
			best[id] = closed
			out[id] = inv
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}
