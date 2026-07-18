package graphflow

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Temporal / bitemporal facts on top of the existing property graph. A relation
// edge becomes a *fact with a validity interval*: valid_from..valid_to (nil
// valid_to = still true). The wall-clock moment the fact was recorded is kept
// separately as recorded_at, so the graph is bitemporal — you can ask both "what
// did we believe was true as of date X" (valid time, via QueryFactsAsOf) and,
// via recorded_at, when we learned it (transaction time).
//
// Storage reuses the ordinary relation edge: validity is written into the edge's
// JSON `properties` (valid_from / valid_to / recorded_at as RFC3339 strings) via
// the relation Metadata that UpsertRelations lands there. Nothing about the
// graph schema changes — a temporal fact is just an edge carrying valid_from.
//
// Supersession ("the new value replaces the old") is modeled by closing the
// prior open fact(s) for a subject+predicate at the moment the new one starts,
// so a subject's history is a chain of non-overlapping intervals.

// Metadata keys under which validity is stored on a relation edge's properties.
const (
	factValidFromKey  = "valid_from"
	factValidToKey    = "valid_to"
	factRecordedAtKey = "recorded_at"
)

// TemporalFact is a relation (From -Type-> To) that holds over a validity
// interval [ValidFrom, ValidTo). A nil ValidTo means the fact is open-ended —
// still valid now. RecordedAt (transaction time) is set by SaveTemporalFact and
// populated on read by QueryFactsAsOf.
type TemporalFact struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`

	ValidFrom *time.Time `json:"valid_from,omitempty"`
	ValidTo   *time.Time `json:"valid_to,omitempty"`

	// Supersede, when set on SaveTemporalFact, closes any currently-open fact
	// for the same (From, Type) subject at ValidFrom before recording this one
	// — the "new value replaces old" pattern (e.g. a changed job title).
	Supersede bool `json:"supersede,omitempty"`

	// RecordedAt is the wall-clock time the fact was written (transaction time).
	// Set by SaveTemporalFact; read back by QueryFactsAsOf.
	RecordedAt *time.Time `json:"recorded_at,omitempty"`
}

// TemporalFilter optionally scopes QueryFactsAsOf to a subject and/or predicate.
// A zero filter returns every temporal fact valid at the queried instant.
type TemporalFilter struct {
	From string `json:"from,omitempty"` // subject display name or entity id
	Type string `json:"type,omitempty"` // predicate / edge type
}

// SaveTemporalFact records a fact with validity time. valid_from / valid_to /
// recorded_at are written (RFC3339) into the relation edge's JSON properties via
// UpsertRelations. When fact.ValidFrom is nil it defaults to now. When
// fact.Supersede is set, any currently-open fact for the same (From, Type)
// subject is closed at ValidFrom first, so the subject's history stays a chain
// of non-overlapping intervals.
func SaveTemporalFact(ctx context.Context, db *cortexdb.DB, fact TemporalFact) error {
	if db == nil {
		return fmt.Errorf("graphflow: temporal: nil db")
	}
	if strings.TrimSpace(fact.From) == "" || strings.TrimSpace(fact.To) == "" {
		return fmt.Errorf("graphflow: temporal fact requires From and To")
	}
	typ := firstNonEmptyTemporal(fact.Type, "related_to")

	recordedAt := time.Now().UTC()
	validFrom := recordedAt
	if fact.ValidFrom != nil {
		validFrom = fact.ValidFrom.UTC()
	}

	// Supersession: close prior open facts for this subject+predicate so the new
	// value takes over exactly where the old one ends.
	if fact.Supersede {
		if _, err := SupersedeFact(ctx, db, fact.From, typ, validFrom); err != nil {
			return err
		}
	}

	metadata := map[string]string{
		factValidFromKey:  validFrom.Format(time.RFC3339),
		factRecordedAtKey: recordedAt.Format(time.RFC3339),
	}
	if fact.ValidTo != nil {
		metadata[factValidToKey] = fact.ValidTo.UTC().Format(time.RFC3339)
	}

	// Ensure the endpoint entity nodes exist first: relation edges carry a
	// foreign key to graph_nodes, so an edge to a non-existent node is silently
	// dropped. This also seeds the display names QueryFactsAsOf reads back.
	if _, err := db.GraphRAGTools().UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{
		Entities: []cortexdb.ToolEntityInput{{Name: fact.From}, {Name: fact.To}},
	}); err != nil {
		return fmt.Errorf("graphflow: save temporal fact: ensure entities: %w", err)
	}

	if _, err := db.GraphRAGTools().UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{
		Relations: []cortexdb.ToolRelationInput{{
			From:     fact.From,
			To:       fact.To,
			Type:     typ,
			Metadata: metadata,
		}},
	}); err != nil {
		return fmt.Errorf("graphflow: save temporal fact: %w", err)
	}
	return nil
}

// SupersedeFact closes every currently-open fact matching (from, typ) by setting
// their valid_to to asOf, and returns how many were closed. "Open" means the
// edge carries a valid_from but no valid_to. This is the mechanism behind
// SaveTemporalFact's Supersede option and can also be called directly to retire
// a subject's current value without asserting a replacement.
func SupersedeFact(ctx context.Context, db *cortexdb.DB, from, typ string, asOf time.Time) (int, error) {
	if db == nil {
		return 0, fmt.Errorf("graphflow: temporal: nil db")
	}
	fromID := cortexdb.EntityNodeID(from)
	if fromID == "" {
		return 0, fmt.Errorf("graphflow: supersede: empty subject")
	}
	typ = firstNonEmptyTemporal(typ, "related_to")

	res, err := db.SQL().ExecContext(ctx, `
		UPDATE graph_edges
		SET properties = json_set(COALESCE(properties, '{}'), '$.'||?, ?)
		WHERE from_node_id = ?
		  AND edge_type = ?
		  AND json_extract(properties, '$.'||?) IS NOT NULL
		  AND json_extract(properties, '$.'||?) IS NULL`,
		factValidToKey, asOf.UTC().Format(time.RFC3339),
		fromID, typ,
		factValidFromKey, factValidToKey)
	if err != nil {
		return 0, fmt.Errorf("graphflow: supersede: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("graphflow: supersede: rows affected: %w", err)
	}
	return int(n), nil
}

// QueryFactsAsOf returns the temporal facts whose validity interval contains the
// instant `at` — i.e. valid_from <= at AND (valid_to IS NULL OR at < valid_to)
// — optionally scoped by subject and/or predicate. Endpoint node ids are
// resolved to entity display names (falling back to the id suffix), matching the
// community.go loadEntityDisplayNames pattern.
func QueryFactsAsOf(ctx context.Context, db *cortexdb.DB, at time.Time, filter TemporalFilter) ([]TemporalFact, error) {
	if db == nil {
		return nil, fmt.Errorf("graphflow: temporal: nil db")
	}
	// Ensure the graph tables exist: querying facts on a brand-new brain that
	// never wrote a graph would otherwise hit "no such table: graph_edges".
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("graphflow: temporal: init graph schema: %w", err)
	}
	names := loadEntityDisplayNames(ctx, db)

	query := `SELECT from_node_id, to_node_id, COALESCE(edge_type, ''),
	                 json_extract(properties, '$.` + factValidFromKey + `'),
	                 json_extract(properties, '$.` + factValidToKey + `'),
	                 json_extract(properties, '$.` + factRecordedAtKey + `')
	          FROM graph_edges
	          WHERE json_extract(properties, '$.` + factValidFromKey + `') IS NOT NULL`
	args := make([]any, 0, 2)
	if f := strings.TrimSpace(filter.From); f != "" {
		query += ` AND from_node_id = ?`
		args = append(args, cortexdb.EntityNodeID(f))
	}
	if t := strings.TrimSpace(filter.Type); t != "" {
		query += ` AND edge_type = ?`
		args = append(args, t)
	}
	query += ` ORDER BY from_node_id, json_extract(properties, '$.` + factValidFromKey + `')`

	rows, err := db.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("graphflow: query facts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]TemporalFact, 0)
	for rows.Next() {
		var fromID, toID, etype string
		var validFromStr, validToStr, recordedStr sql.NullString
		if err := rows.Scan(&fromID, &toID, &etype, &validFromStr, &validToStr, &recordedStr); err != nil {
			return nil, fmt.Errorf("graphflow: query facts: scan: %w", err)
		}

		validFrom, err := time.Parse(time.RFC3339, validFromStr.String)
		if err != nil {
			continue // malformed valid_from — not a well-formed temporal fact
		}
		if validFrom.After(at) {
			continue // interval starts after the queried instant
		}

		var validTo *time.Time
		if validToStr.Valid && validToStr.String != "" {
			vt, err := time.Parse(time.RFC3339, validToStr.String)
			if err != nil {
				continue
			}
			if !at.Before(vt) {
				continue // at >= valid_to — interval already closed
			}
			validTo = &vt
		}

		fact := TemporalFact{
			From:      displayNameFor(names, fromID),
			To:        displayNameFor(names, toID),
			Type:      etype,
			ValidFrom: &validFrom,
			ValidTo:   validTo,
		}
		if recordedStr.Valid && recordedStr.String != "" {
			if ra, err := time.Parse(time.RFC3339, recordedStr.String); err == nil {
				fact.RecordedAt = &ra
			}
		}
		out = append(out, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graphflow: query facts: %w", err)
	}
	return out, nil
}

// displayNameFor resolves an entity node id to its display name, falling back to
// the id suffix when the node has no stored content (mirrors community.go).
func displayNameFor(names map[string]string, nodeID string) string {
	if name, ok := names[nodeID]; ok && strings.TrimSpace(name) != "" {
		return name
	}
	return trimEntityPrefix(nodeID)
}

// firstNonEmptyTemporal returns the first non-blank string, else the fallback.
func firstNonEmptyTemporal(s, fallback string) string {
	if strings.TrimSpace(s) != "" {
		return s
	}
	return fallback
}
