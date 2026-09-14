package core

// The faceted and range search surface, spoken to PostgreSQL.
//
// Three methods that existed only on SQLiteStore: SearchWithFacets,
// RangeSearch's real body, and BatchRangeSearch. They had no callers, which is
// exactly why they could sit there half-implemented — cortexdb.DB holds its
// store as an interface, so nothing could reach them without first narrowing
// to *SQLiteStore, and any code that did would have broken the moment someone
// pointed the same brain at PostgreSQL.
//
// Written against the SQLite versions rather than against a clean sheet. Where
// the SQLite behaviour is odd — a CAST that reads "n/a" as zero, a comparison
// that never matches across storage classes — this reproduces the oddity
// rather than correcting it on one backend only. A method that means two
// things depending on the DSN is worse than one that means the same slightly
// wrong thing everywhere.
//
// One deliberate departure from pkg/sqldialect: the JSON reads here are
// written as `metadata ->> $n` rather than through Dialect.JSONText. That
// helper guards a TEXT column that might not hold JSON at all, with a
// ::text/::jsonb round trip per row; embeddings.metadata is jsonb on this
// backend and carries a GIN index, and the round trip both re-parses every row
// and hides the column from that index. pgTextField and pgFilterSQL already
// established the direct form for this table, and the key is bound rather than
// interpolated for the same reason they bind it.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// --- RangeSearch -------------------------------------------------------------

// pgCosineFixedPoint is what rangeDistance needs from the metric: the score a
// vector gets against itself.
//
// A constant rather than a call to GetSimilarityFunc, because this backend has
// exactly one metric. Everything here scores with 1 - (vector <=> q), and
// cosine gives a vector against itself a similarity of 1. Asking
// config.SimilarityFn instead would let a store configured with EuclideanDist
// convert cosine scores with euclidean's fixed point of 0, which is how the
// radius would come to mean two things on one backend.
const pgCosineFixedPoint = 1.0

// pgRangeDistance is rangeDistance transcribed into SQL, over a score
// expression instead of a Go float64.
//
// Written as `1 - score` rather than algebraically folded back into the raw
// cosine distance. The score is `1 - d` and `1 - (1 - d)` is not `d` in IEEE
// 754 — for d = 0.3 it is 0.30000000000000004 — so folding would let the
// database accept a row sitting exactly on the radius that the Go-side check
// then dropped, and that disagreement presents as one missing result in a
// hundred rather than as anything a reader could name.
func pgRangeDistance(score string) string {
	return fmt.Sprintf("(%v - (%s))", pgCosineFixedPoint, score)
}

// rangeSearch is RangeSearch without the stale-type retry, which lives on the
// exported method in store_postgres.go.
func (s *PostgresStore) rangeSearch(ctx context.Context, query []float32, radius float32, opts SearchOptions) ([]ScoredEmbedding, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("range_search: empty query vector")
	}
	// SQLite refuses a non-positive radius and so does this. The old
	// implementation turned it into a similarity threshold instead, where a
	// radius of 0 quietly meant "everything at or above similarity 1" and a
	// negative radius meant "everything at all".
	if radius <= 0 {
		return nil, fmt.Errorf("radius must be positive, got %f", radius)
	}

	args := &pgArgs{}
	vec := args.add(PgVectorLiteral(query))

	// pgvector's <=> is cosine distance, and cosine is the only metric this
	// backend has: the column is indexed with vector_cosine_ops and every
	// search here converts with 1 - (vector <=> q). config.SimilarityFn is
	// consulted only by GetSimilarityFunc, for the callers that score in Go.
	// So a store configured with EuclideanDist still range-searches in cosine,
	// and the radius is a cosine distance. Saying so is more honest than a
	// euclidean branch that could never run.
	score := fmt.Sprintf("1 - (vector <=> %s)", vec)

	where := s.optionWhere(args, opts)
	where = append(where, pgRangeDistance(score)+" <= "+args.add(float64(radius))+"::double precision")

	q := fmt.Sprintf(`
		SELECT %s, %s AS score
		FROM embeddings
		WHERE %s
		ORDER BY vector <=> %s`,
		pgSearchColumns, score, strings.Join(where, " AND "), vec)

	// Ordering by distance ascending is ordering by score descending — the
	// score is 1 - distance, so the two are the same permutation and the
	// database can serve it from the index.
	//
	// TopK is the caller's cap and nothing else's. The old implementation
	// defaulted it to 1000, so a query matching more than that lost the rest
	// and still reported success. Zero now means every match; the rows stream
	// out of the ordered scan rather than being buffered a page at a time.
	if opts.TopK > 0 {
		q += " LIMIT " + args.add(opts.TopK)
	}

	rows, err := s.db.QueryContext(ctx, q, args.vals...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ScoredEmbedding
	for rows.Next() {
		e, err := scanPgScored(rows)
		if err != nil {
			return nil, err
		}
		// The predicate above already excluded this row's opposites, so this
		// re-check normally passes. It is here so the radius test on both
		// backends goes through rangeDistance and cannot be changed in one
		// place only.
		if rangeDistance(pgCosineFixedPoint, e.Score) > float64(radius) {
			continue
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// BatchRangeSearch performs range search for multiple queries.
//
// Results stay grouped by input query and in input order — the index into the
// outer slice is the index of the query that produced it, which is the only
// thing tying a result back to its query. Same loop as the SQLite store,
// including that the first failing query fails the batch: a partial batch
// whose gaps are indistinguishable from empty result sets is worse than none.
func (s *PostgresStore) BatchRangeSearch(ctx context.Context, queries [][]float32, radius float32, opts SearchOptions) ([][]ScoredEmbedding, error) {
	results := make([][]ScoredEmbedding, len(queries))

	for i, query := range queries {
		res, err := s.RangeSearch(ctx, query, radius, opts)
		if err != nil {
			return nil, err
		}
		results[i] = res
	}

	return results, nil
}

// --- SearchWithFacets --------------------------------------------------------

// SearchWithFacets performs vector search with faceted filtering.
//
// The facet conditions narrow the scan, the database ranks what survives, and
// the counts — when asked for — are a second pass. That second pass is
// deliberately as wide as SQLite's: computeFacetCounts ignores the collection
// and the metadata filter and counts the whole table. It is a strange choice
// (see the note there) but it is the choice the other backend makes, and a
// facet sidebar that adds up differently depending on the DSN would be worse
// than one that is consistently too generous.
func (s *PostgresStore) SearchWithFacets(ctx context.Context, query []float32, opts FacetedSearchOptions) ([]ScoredEmbedding, []FacetResult, error) {
	if err := validateFacetedSearchOptions(opts); err != nil {
		return nil, nil, wrapError("search_faceted", err)
	}
	var (
		results []ScoredEmbedding
		err     error
	)
	// One retry, for a `vector` type replaced under this connection.
	// See IsStaleTypeCache: the statement never ran, and the failure is
	// what clears the cache that caused it.
	err = retryOnStaleTypeCache(func() error {
		var e error
		results, e = s.searchWithFacets(ctx, query, opts)
		return e
	})
	if err != nil {
		return nil, nil, wrapError("search_faceted", err)
	}

	var facetResults []FacetResult
	if opts.ReturnFacets {
		facetResults, err = s.computeFacetCounts(ctx, opts)
		if err != nil {
			// Don't fail search if facet counting fails
			facetResults = []FacetResult{}
		}
	}

	return results, facetResults, nil
}

func (s *PostgresStore) searchWithFacets(ctx context.Context, query []float32, opts FacetedSearchOptions) ([]ScoredEmbedding, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("search_faceted: empty query vector")
	}

	args := &pgArgs{}
	vec := args.add(PgVectorLiteral(query))

	// optionWhere for the collection and the metadata equality filter: its
	// `metadata @> $n::jsonb` is the same test SQLite's
	// `json_extract(metadata, '$.k') = ?` makes for a string value, and it is
	// the one the GIN index serves.
	where := s.optionWhere(args, opts.SearchOptions)
	where = append(where, pgFacetConditions(opts.Facets, args)...)

	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	// scoreCandidates defaults TopK to 10 on the SQLite side, so an
	// unspecified TopK returns ten rows there. Same here rather than
	// everything, because "all of them" is what RangeSearch is for.
	topK := opts.TopK
	if topK <= 0 {
		topK = 10
	}

	// The LIMIT goes in the statement only when nothing after it can reject a
	// row. With a threshold the rejections happen in Go, and a SQL LIMIT
	// applied first would return fewer than TopK rows while qualifying rows
	// sat just past the cut — which is not what SQLite does: it thresholds the
	// whole candidate set and then takes TopK.
	limit := ""
	if opts.Threshold <= 0 {
		limit = " LIMIT " + args.add(topK)
	}

	q := fmt.Sprintf(`
		SELECT %[1]s, 1 - (vector <=> %[2]s) AS score
		FROM embeddings%[3]s
		ORDER BY vector <=> %[2]s%[4]s`,
		pgSearchColumns, vec, clause, limit)

	rows, err := s.db.QueryContext(ctx, q, args.vals...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ScoredEmbedding
	for rows.Next() {
		e, err := scanPgScored(rows)
		if err != nil {
			return nil, err
		}
		if opts.Threshold > 0 && e.Score < opts.Threshold {
			continue
		}
		out = append(out, e)
		if len(out) >= topK {
			break
		}
	}
	return out, rows.Err()
}

// computeFacetCounts counts distinct values per facet field.
//
// Deliberately unfiltered, because the SQLite version is: neither the
// collection nor the search's own filters narrow it, so the counts describe
// the whole table rather than the result set. That is almost certainly not
// what a facet sidebar wants, and it is not changed here — one backend
// quietly disagreeing with the other about what a count covers is the failure
// this file exists to prevent.
func (s *PostgresStore) computeFacetCounts(ctx context.Context, opts FacetedSearchOptions) ([]FacetResult, error) {
	results := []FacetResult{}

	for field := range opts.Facets {
		limit := opts.MaxFacetValues
		if limit <= 0 {
			limit = 10
		}

		args := &pgArgs{}
		value := pgMetadataText(field, args)
		notNull := pgMetadataText(field, args)
		// Ordinals rather than the output aliases SQLite leans on: `value` and
		// `count` are both spellable as PostgreSQL aliases, but GROUP BY 1 /
		// ORDER BY 2 says what is meant without depending on which of the two
		// databases resolves an alias in which clause.
		q := fmt.Sprintf(`
			SELECT %s AS value, COUNT(*) AS count
			FROM embeddings
			WHERE %s IS NOT NULL
			GROUP BY 1
			ORDER BY 2 DESC
			LIMIT %s`, value, notNull, args.add(limit))

		rows, err := s.db.QueryContext(ctx, q, args.vals...)
		if err != nil {
			continue
		}

		facetResult := FacetResult{
			Field:  field,
			Values: make(map[string]int),
		}
		for rows.Next() {
			var v string
			var count int
			if err := rows.Scan(&v, &count); err == nil {
				facetResult.Values[v] = count
				facetResult.Total += count
			}
		}
		if err := rows.Close(); err != nil {
			_ = err
		}

		if len(facetResult.Values) > 0 {
			results = append(results, facetResult)
		}
	}

	return results, nil
}

// --- facet filters -----------------------------------------------------------

// pgFacetConditions renders every facet filter as a WHERE fragment.
//
// Map order, as on SQLite: the conditions are ANDed, so the order they are
// generated in changes the SQL text and nothing else. Each fragment carries
// its own placeholders, so numbering survives whatever order the range lands
// in.
func pgFacetConditions(facets map[string]FacetFilter, args *pgArgs) []string {
	var conds []string
	for field, filter := range facets {
		if c := pgFacetCondition(field, filter, args); c != "" {
			conds = append(conds, c)
		}
	}
	return conds
}

func pgFacetCondition(field string, filter FacetFilter, args *pgArgs) string {
	switch filter.Type {
	case FilterTypeEquals:
		if len(filter.Values) > 0 {
			return pgMetadataEquals(field, filter.Values[0], args)
		}

	case FilterTypeIn:
		if len(filter.Values) > 0 {
			// An OR of equalities rather than `= ANY(...)`, because each value
			// carries its own storage class and IN on SQLite compares each one
			// separately. A text array would flatten a number into its digits
			// and start matching rows SQLite excludes.
			parts := make([]string, 0, len(filter.Values))
			for _, v := range filter.Values {
				parts = append(parts, pgMetadataEquals(field, v, args))
			}
			return "(" + strings.Join(parts, " OR ") + ")"
		}

	case FilterTypeRange:
		var conds []string
		if filter.Min != nil {
			conds = append(conds, pgRangeBound(field, filter.Min, ">=", args))
		}
		if filter.Max != nil {
			conds = append(conds, pgRangeBound(field, filter.Max, "<=", args))
		}
		if len(conds) > 0 {
			return strings.Join(conds, " AND ")
		}

	case FilterTypeContains:
		if filter.Pattern != "" {
			return pgMetadataText(field, args) + pgLikeOperator + args.add("%"+filter.Pattern+"%") + "::text"
		}

	case FilterTypePrefix:
		if filter.Pattern != "" {
			return pgMetadataText(field, args) + pgLikeOperator + args.add(filter.Pattern+"%") + "::text"
		}

	case FilterTypeExists:
		return pgMetadataText(field, args) + " IS NOT NULL"

	case FilterTypeNested:
		return pgFacetNested(field, filter, args)
	}

	return ""
}

// pgLikeOperator is ILIKE rather than LIKE, because SQLite's LIKE is not the
// case-sensitive operator the SQL standard describes.
//
// SQLite folds ASCII case in LIKE by default, so `contains: "US"` matches a
// region stored as "us" there. PostgreSQL's LIKE does not fold at all, so the
// same facet would have quietly returned nothing on one backend and the rows
// on the other — a filter that narrows differently per DSN, with no error.
//
// Not an exact match either way: ILIKE folds case for non-ASCII too, where
// SQLite's LIKE stops at ASCII. That leaves a divergence for a pattern like
// "STRASSE" against "straße", against a divergence for every uppercase ASCII
// pattern there is, and the narrower one is the one to keep.
const pgLikeOperator = " ILIKE "

// pgFacetNested mirrors buildNestedCondition, including that every nested
// filter reads the same field the outer one names.
func pgFacetNested(field string, filter FacetFilter, args *pgArgs) string {
	if len(filter.Nested) == 0 {
		return ""
	}

	var conditions []string
	for _, nested := range filter.Nested {
		if c := pgFacetCondition(field, nested, args); c != "" {
			conditions = append(conditions, "("+c+")")
		}
	}
	if len(conditions) == 0 {
		return ""
	}

	switch filter.Operator {
	case OperatorOR:
		return strings.Join(conditions, " OR ")
	case OperatorNOT:
		return "NOT (" + strings.Join(conditions, " AND ") + ")"
	default:
		return strings.Join(conditions, " AND ")
	}
}

// pgRangeBound renders one side of a numeric range the way SQLite compares it.
//
// SQLite's `CAST(json_extract(...) AS REAL) >= ?` is a comparison between a
// REAL and whatever storage class the bound arrived as, and SQLite orders the
// classes: NULL < REAL < TEXT. So a text bound makes `>=` false for every row
// and `<=` true for every row that has the field at all — never a numeric
// comparison. Reproduced rather than fixed, because a caller passing "10"
// instead of 10 should not get different rows from different backends; the
// fix belongs in aggregations.go, on both sides at once.
func pgRangeBound(field string, bound any, op string, args *pgArgs) string {
	num, isNum, _ := pgFilterBinding(bound)
	if !isNum {
		if op == ">=" {
			return "FALSE"
		}
		return pgMetadataText(field, args) + " IS NOT NULL"
	}
	return fmt.Sprintf("%s %s %s::double precision",
		pgMetadataReal(field, args), op, args.add(num))
}

// --- reading a metadata field ------------------------------------------------

// pgMetadataText reads a metadata field as text, binding the key.
//
// The key is a parameter rather than string-interpolated the way the SQLite
// side builds '$.%s'. Every caller passes a constant today; that is exactly
// when the interpolation is easy to leave in and hard to notice later.
func pgMetadataText(field string, args *pgArgs) string {
	return fmt.Sprintf("(metadata ->> %s::text)", args.add(field))
}

// pgNumericPrefixRe matches what SQLite's CAST to REAL would read off the
// front of a string.
//
// Non-capturing groups throughout: substring(text from pattern) returns the
// first parenthesised subexpression when the pattern has one, not the whole
// match, so a capturing group here would silently return the digits after the
// decimal point.
const pgNumericPrefixRe = `^[[:space:]]*[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?`

// pgMetadataReal is SQLite's `CAST(json_extract(metadata, '$.k') AS REAL)`,
// including the two things about that cast that are easy to miss.
//
// It never fails: SQLite reads the longest numeric prefix and yields 0.0 when
// there is none, so a field holding "n/a" sums as zero rather than raising.
// PostgreSQL's ::double precision raises on that text and one such row would
// fail the whole statement, so the prefix comes out through a regex first.
// And CAST(NULL AS REAL) is NULL rather than 0, which is why the outer CASE is
// here: a row missing the field must drop out of a range comparison instead of
// sorting as zero.
//
// The one place this does not follow SQLite is a JSON boolean, which
// json_extract yields as the integer 1 and `->>` yields as the text 'true'.
// Embedding.Metadata is map[string]string, so every value in the column is a
// JSON string and no writer in this module can produce one.
func pgMetadataReal(field string, args *pgArgs) string {
	t := pgMetadataText(field, args)
	return fmt.Sprintf(
		"(CASE WHEN %[1]s IS NULL THEN NULL ELSE COALESCE(substring(%[1]s from '%[2]s')::double precision, 0) END)",
		t, pgNumericPrefixRe)
}

// pgMetadataEquals compares a metadata field against a filter value the way
// SQLite's `json_extract(metadata, '$.k') = ?` does, storage class and all.
//
// SQLite never compares across storage classes. json_extract of a JSON string
// is TEXT and of a JSON number is INTEGER or REAL, and TEXT never equals a
// bound number however the digits read. Embedding.Metadata is
// map[string]string, so every value in the column is a JSON string and a
// string filter is the only kind that has ever matched — but `metadata ->> k`
// flattens a stored number to its digits and would start matching filters
// SQLite rejects. jsonb_typeof restores the distinction, so both backends
// answer the same for a filter neither was really built for.
func pgMetadataEquals(field string, value any, args *pgArgs) string {
	if value == nil {
		// `= NULL` is never true on either side.
		return "FALSE"
	}
	num, isNum, text := pgFilterBinding(value)
	key := args.add(field)
	if isNum {
		return fmt.Sprintf(
			"(jsonb_typeof(metadata -> %[1]s::text) = 'number' AND (metadata ->> %[1]s::text)::double precision = %[2]s::double precision)",
			key, args.add(num))
	}
	return fmt.Sprintf(
		"(jsonb_typeof(metadata -> %[1]s::text) = 'string' AND (metadata ->> %[1]s::text) = %[2]s::text)",
		key, args.add(text))
}

// pgFilterBinding says which SQLite storage class a filter value would have
// been bound as, mirroring addMetadataFilters' type switch.
//
// Which class it lands in is the whole question, because that is what decides
// whether the comparison can match at all. Note that int64 and float32 fall
// through to json.Marshal and arrive as TEXT — surprising, and what the other
// backend does.
func pgFilterBinding(value any) (num float64, isNum bool, text string) {
	switch v := value.(type) {
	case string:
		return 0, false, v
	case int:
		return float64(v), true, ""
	case float64:
		return v, true, ""
	case bool:
		// addMetadataFilters binds a bool as the integer 1 or 0, so SQLite
		// compares it against JSON numbers and never against JSON true.
		if v {
			return 1, true, ""
		}
		return 0, true, ""
	default:
		if b, err := json.Marshal(v); err == nil {
			return 0, false, string(b)
		}
		return 0, false, fmt.Sprintf("%v", v)
	}
}
