package graph

// Write-time supersession of single-valued facts.
//
// "Leo lives in Beijing" followed by "Leo lives in Chengdu" is two edge ids.
// Until now they were joined only when graphflow's own supersede path ran;
// a plain relation upsert left both live, and only verify_claims and
// graph_health noticed. When the ontology says a link reaches one object, the
// write itself has to end the old value — in the transaction that writes the
// new one, so no reader and no crash sees both live, or neither.
//
// This package does not know what an ontology is. The caller hands in a rule:
// which edge types are single-valued, and which schema (id and version) said
// so. The closure records that schema on the history row, so "why did this
// fact end" keeps its answer after the schema changes.
//
// The rule is carried on the context, like Invalidation and ReadOptions, for
// the same reason: the edge writers are called from several layers and a
// parameter would change every signature.
//
// Where the rule does not apply nothing is closed: a type the callback
// reports false for, an edge already closed by a valid_to property (graphflow's
// representation of an ended fact), and an edge pointing at the same object —
// restating a fact is not a contradiction.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Supersession is the rule a write applies to the facts it contradicts.
type Supersession struct {
	// Candidate is a cheap pre-check on the edge type alone: false means no
	// edge of this type can be single-valued, so the subject is not even
	// looked up. Nil means every type is a candidate.
	Candidate func(edgeType string) bool
	// SingleValued reports whether edges of this type reach at most one
	// object from a subject of this node type. Direction matters: Airport
	// departures reach many flights, while each flight reaches one airport,
	// so the same link is single-valued from one end and not from the other.
	// Nil closes nothing.
	SingleValued func(edgeType, subjectType string) bool
	// SchemaID and SchemaVersion identify the schema that decided it.
	SchemaID      string
	SchemaVersion int
	// Producer is recorded on the closed rows; empty records graph.upsert.
	Producer string
}

type supersessionKey struct{}

// WithSupersession returns a context whose edge upserts close the prior
// open values of single-valued links.
func WithSupersession(ctx context.Context, rule Supersession) context.Context {
	if rule.SingleValued == nil {
		return ctx
	}
	return context.WithValue(ctx, supersessionKey{}, rule)
}

func supersessionFrom(ctx context.Context) (Supersession, bool) {
	if ctx == nil {
		return Supersession{}, false
	}
	rule, ok := ctx.Value(supersessionKey{}).(Supersession)
	return rule, ok
}

// supersedePriorEdges moves every live edge from the same subject with the same
// type and a different object into history, superseded by edge.
func (g *GraphStore) supersedePriorEdges(ctx context.Context, tx *sql.Tx, rule Supersession, edge *GraphEdge, at, recorded time.Time) error {
	if rule.SingleValued == nil || (rule.Candidate != nil && !rule.Candidate(edge.EdgeType)) {
		return nil
	}
	var subjectType sql.NullString
	if err := tx.QueryRowContext(ctx, g.dialect.Rebind(`SELECT node_type FROM graph_nodes WHERE id = ?`), edge.FromNodeID).Scan(&subjectType); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("cortexdb/graph: supersede prior edges: %w", err)
	}
	if !rule.SingleValued(edge.EdgeType, subjectType.String) {
		return nil
	}
	closedMarker := g.dialect.JSONTextGuarded("properties", "valid_to")
	rows, err := tx.QueryContext(ctx, g.dialect.Rebind(`
		SELECT id, valid_from FROM graph_edges
		WHERE from_node_id = ? AND edge_type = ? AND id <> ? AND to_node_id <> ?
		  AND `+closedMarker+` IS NULL`),
		edge.FromNodeID, edge.EdgeType, edge.ID, edge.ToNodeID)
	if err != nil {
		return fmt.Errorf("cortexdb/graph: supersede prior edges: %w", err)
	}
	type prior struct {
		id        string
		validFrom time.Time
	}
	var priors []prior
	for rows.Next() {
		var p prior
		var vf sql.NullTime
		if err := rows.Scan(&p.id, &vf); err != nil {
			_ = rows.Close()
			return fmt.Errorf("cortexdb/graph: supersede prior edges: %w", err)
		}
		p.validFrom = nullTime(vf)
		priors = append(priors, p)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()

	producer := strings.TrimSpace(rule.Producer)
	if producer == "" {
		producer = ProducerGraphUpsert
	}
	inv := Invalidation{
		Reason: ReasonSuperseded, SupersededBy: edge.ID, Producer: producer,
		SchemaID: rule.SchemaID, SchemaVersion: rule.SchemaVersion,
	}
	for _, p := range priors {
		// The old value ends where the new one begins. A new value stated as
		// true from before the old one began cannot end it earlier than it
		// started, so the closure falls back to the instant it is recorded.
		closeAt := at
		if !p.validFrom.IsZero() && !p.validFrom.Before(closeAt) {
			closeAt = recorded
		}
		args := append([]any{closeAt}, inv.columnArgs()...)
		args = append(args, p.id)
		if _, err := tx.ExecContext(ctx, g.dialect.Rebind(`
			INSERT INTO graph_edge_history (`+edgeColumns+`, `+invalidationColumnList+`)
			SELECT id, from_node_id, to_node_id, edge_type, weight, properties, vector, created_at,
			       valid_from, COALESCE(valid_to, ?), recorded_at, retracted_at, `+invalidationPlaceholders+`
			FROM graph_edges WHERE id = ?`), args...); err != nil {
			return fmt.Errorf("cortexdb/graph: archive superseded edge %s: %w", p.id, err)
		}
		if _, err := tx.ExecContext(ctx, g.dialect.Rebind(`DELETE FROM graph_edges WHERE id = ?`), p.id); err != nil {
			return fmt.Errorf("cortexdb/graph: close superseded edge %s: %w", p.id, err)
		}
	}
	return nil
}
