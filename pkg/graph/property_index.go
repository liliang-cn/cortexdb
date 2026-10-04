package graph

// Indexes on node properties.
//
// A node's properties are a JSON document in one TEXT column, so a filter on
// one of them — every step of a run (run_id), every failed step (status) —
// read every node in the graph and parsed its JSON. On a 20,000-step execution
// graph that was 60–180ms a question, growing with the graph, for the most
// ordinary questions an agent's execution record is asked.
//
// IndexNodeProperty puts an expression index on one property. The indexed
// expression is exactly the dialect's JSONTextGuarded("properties", key), the
// expression GraphFilter.Properties already filters with, so ListNodes and
// friends use the index with no change; the Cypher engine reads the catalog
// below and writes its filters on an indexed key the same way (see
// IndexedNodeProperties and pkg/graph/cypher's propCond).
//
// Guarded, as graphflow's valid_from index learned it must be: an expression
// index is evaluated on every write, and a bare json_extract fails the write of
// any node whose properties are empty.
//
// Opt-in, per key, because an index is paid for on every write: measure the
// write path of the brain it is added to. The catalog is a table rather than
// a scan of the database's own index list so both backends answer it the same
// way, and so a key is spliced into SQL only after it has been checked here.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
)

// propertyKeyPattern is what an indexed key may be. Keys are spliced into DDL
// and into the expression a query must repeat to use the index, so they are
// held to identifier characters; 40 keeps the index name inside PostgreSQL's
// 63-byte identifier limit.
var propertyKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,39}$`)

// ValidIndexedPropertyKey reports whether key can be indexed.
func ValidIndexedPropertyKey(key string) bool { return propertyKeyPattern.MatchString(key) }

func propertyIndexName(key string) string { return "idx_graph_nodes_prop_" + key }

// IndexNodeProperty indexes one node property for equality, IN and — on
// SQLite — numeric range filters. Idempotent. On PostgreSQL the index is built
// with a plain CREATE INDEX, which blocks writes to graph_nodes while it runs.
func (g *GraphStore) IndexNodeProperty(ctx context.Context, key string) error {
	if !ValidIndexedPropertyKey(key) {
		return fmt.Errorf("cortexdb/graph: property %q cannot be indexed: a key is letters, digits and _, starts with a letter or _, and is at most 40 long", key)
	}
	if err := errIfAsOf(ctx); err != nil {
		return err
	}
	if err := g.InitGraphSchema(ctx); err != nil {
		return err
	}
	// Doubled parentheses: PostgreSQL takes an index expression that is not a
	// bare function call only parenthesised, and SQLite reads the extra pair
	// as the same expression, which its planner still matches.
	ddl := `CREATE INDEX IF NOT EXISTS ` + propertyIndexName(key) +
		` ON graph_nodes ((` + g.dialect.JSONTextGuarded("properties", key) + `))`
	if _, err := g.exec(ctx, ddl); err != nil {
		return fmt.Errorf("cortexdb/graph: index property %s: %w", key, err)
	}
	if _, err := g.exec(ctx,
		`INSERT INTO graph_property_indexes (prop_key) VALUES (?) ON CONFLICT (prop_key) DO NOTHING`, key); err != nil {
		return fmt.Errorf("cortexdb/graph: record property index %s: %w", key, err)
	}
	return nil
}

// DropNodePropertyIndex removes what IndexNodeProperty made. Dropping a key
// that is not indexed is not an error.
func (g *GraphStore) DropNodePropertyIndex(ctx context.Context, key string) error {
	if !ValidIndexedPropertyKey(key) {
		return fmt.Errorf("cortexdb/graph: property %q cannot be indexed, so there is no index to drop", key)
	}
	if err := errIfAsOf(ctx); err != nil {
		return err
	}
	if err := g.InitGraphSchema(ctx); err != nil {
		return err
	}
	// Catalog first: a query that still finds the key there while the index
	// is gone is slow; one that does not find it while the index exists is
	// merely not using it.
	if _, err := g.exec(ctx, `DELETE FROM graph_property_indexes WHERE prop_key = ?`, key); err != nil {
		return fmt.Errorf("cortexdb/graph: unrecord property index %s: %w", key, err)
	}
	if _, err := g.exec(ctx, `DROP INDEX IF EXISTS `+propertyIndexName(key)); err != nil {
		return fmt.Errorf("cortexdb/graph: drop property index %s: %w", key, err)
	}
	return nil
}

// NodePropertyIndexes lists the indexed node properties, sorted.
func (g *GraphStore) NodePropertyIndexes(ctx context.Context) ([]string, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := g.query(ctx, `SELECT prop_key FROM graph_property_indexes`)
	if err != nil {
		return nil, fmt.Errorf("cortexdb/graph: list property indexes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		// A row written by hand that would not pass IndexNodeProperty is
		// not a key anything may splice.
		if ValidIndexedPropertyKey(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, rows.Err()
}
