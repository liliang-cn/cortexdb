// Package pgscan chooses how far a PostgreSQL vector search may lean on a
// pgvector HNSW index, for pkg/core and pkg/graph alike.
package pgscan

import "fmt"

// MaxEfSearch is the largest hnsw.ef_search pgvector accepts.
const MaxEfSearch = 1000

// Setting is the statement a vector search runs, in its own transaction,
// before a query ordered by distance. limit is the query's LIMIT, 0 for none;
// filtered says whether it has a WHERE.
//
// pgvector's HNSW scan returns at most hnsw.ef_search rows, 40 unless set, and
// returns them before the WHERE is applied. So a search for more than 40 came
// back with 40, and a filtered one with the few of the first 40 that passed,
// both without an error. An unfiltered search of up to 1000 rows, the most
// ef_search allows, keeps the index and widens the scan to its limit, or keeps
// a wider hnsw.ef_search the operator has set. Any other is ranked exactly,
// by keeping the planner off index scans.
//
// The scan also counts row versions that are not visible, until a vacuum
// removes them: an unfiltered search on a table with many updated or deleted
// rows can still come back short.
func Setting(limit int, filtered bool) string {
	if filtered || limit <= 0 || limit > MaxEfSearch {
		return "SET LOCAL enable_indexscan = off"
	}
	// set_config(..., true) is SET LOCAL with a computed value. Before
	// pgvector is loaded in the session the setting is unknown, hence the
	// missing_ok read and the default of 40.
	return fmt.Sprintf("SELECT set_config('hnsw.ef_search', GREATEST(COALESCE(NULLIF(current_setting('hnsw.ef_search', true), ''), '40')::int, %d)::text, true)", max(limit, 40))
}
