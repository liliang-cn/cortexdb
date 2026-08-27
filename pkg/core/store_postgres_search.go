package core

// The search surface, spoken to PostgreSQL.
//
// Three methods that used to answer ErrPostgresStoreUnimplemented:
// SearchWithACL, HybridSearch and SearchWithAdvancedFilter. All three are
// written against the SQLite versions rather than against the interface,
// because the shape is the easy half — what matters is that a caller who
// swaps backends gets the same rows in the same order with the same numbers
// on them.
//
// Three decisions carry most of that weight:
//
//   - A score is a similarity, never a distance. Everything here goes through
//     the same `1 - (vector <=> $1)` conversion Search uses, so a Threshold
//     tuned on SQLite still means what it meant.
//   - The keyword arm is PostgresLexicalCondition, not new text matching.
//     fts_postgres.go already worked out which of tsquery / trigram-LIKE /
//     unindexed-LIKE a query belongs in, and it shares the routing decisions
//     (ContainsCJK, BelowTrigramFloor) with the FTS5 side on purpose. A second
//     opinion here is how the two backends start disagreeing about whether a
//     CJK query has a keyword arm at all.
//   - A FilterExpression either compiles completely or errors naming the
//     operator that stopped it. BuildSQLFromFilter returns "" for the
//     operators it does not handle, which reads as "no condition" and hands
//     back rows the caller excluded. Silence is the one outcome not available
//     to a filter.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
)

// --- placeholder bookkeeping -------------------------------------------------

// pgArgs numbers $n placeholders as arguments are bound.
//
// This backend speaks $1..$n while the fragments being assembled here come
// from several places (a filter tree, a lexical condition written with `?`,
// the caller's options), so counting by hand is how an off-by-one silently
// compares the wrong two values.
type pgArgs struct{ vals []any }

// add binds a value and returns the placeholder that refers to it.
func (a *pgArgs) add(v any) string {
	a.vals = append(a.vals, v)
	return fmt.Sprintf("$%d", len(a.vals))
}

// --- SearchWithACL -----------------------------------------------------------

// SearchWithACL performs vector search restricted to what the caller may see.
//
// The rule is SQLite's, unchanged: a row with no acl is public and visible to
// everyone, and a row that has one is visible only to a caller holding at
// least one of its entries. An empty caller ACL is therefore not "an
// administrator" — it is an anonymous reader, who sees the public rows and
// nothing else.
//
// `jsonb_exists_any` is the function behind the `?|` operator, spelled out
// rather than written as `?|` so no driver can mistake it for a placeholder.
// It matches against the top-level array elements, which is exactly what
// SQLite's `EXISTS (SELECT 1 FROM json_each(acl) WHERE value IN (…))` walks.
func (s *PostgresStore) SearchWithACL(ctx context.Context, query []float32, acl []string, opts SearchOptions) ([]ScoredEmbedding, error) {
	// One retry, for a `vector` type replaced under this connection.
	// See IsStaleTypeCache: the statement never ran, and the failure is
	// what clears the cache that caused it.
	var out []ScoredEmbedding
	err := retryOnStaleTypeCache(func() error {
		var e error
		out, e = s.searchWithACL(ctx, query, acl, opts)
		return e
	})
	return out, err
}

func (s *PostgresStore) searchWithACL(ctx context.Context, query []float32, acl []string, opts SearchOptions) ([]ScoredEmbedding, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("search_acl: empty query vector")
	}

	args := &pgArgs{}
	vec := args.add(PgVectorLiteral(query))

	// `acl = 'null'::jsonb` alongside `IS NULL` because SQLite's
	// json_extract(acl, '$') is NULL for both a missing column and a stored
	// JSON null, and a row should not become invisible to everybody because
	// of which of the two an old writer left behind.
	visible := "acl IS NULL OR acl = 'null'::jsonb"
	if len(acl) > 0 {
		visible += " OR jsonb_exists_any(acl, " + args.add(pgTextArray(acl)) + "::text[])"
	}

	where := []string{"(" + visible + ")"}
	where = append(where, s.optionWhere(args, opts)...)

	return s.runVectorSearch(ctx, args, vec, where, opts, nil)
}

// --- HybridSearch ------------------------------------------------------------

// HybridSearch combines a vector arm and a keyword arm with Reciprocal Rank
// Fusion, the same fusion and the same default k the SQLite store uses.
//
// RRF fuses ranks, not scores, which is why nothing here has to reconcile a
// cosine similarity with a ts_rank — two quantities that share no scale and
// whose weighted sum would mean whatever the corpus happened to make it mean.
// A row's contribution from an arm is 1/(k + its rank in that arm), summed
// across the arms that found it. Consequently a hybrid score is NOT a
// similarity and must not be compared against a Threshold; the threshold
// applies inside the vector arm, where it still means what it always did.
//
// The failure policy is SQLite's, and it is asymmetric on purpose. When the
// keyword arm is the only arm — no vector supplied — a failure is returned,
// because swallowing it reports "nothing matched" for a query that never ran.
// When a vector arm is also going to answer, the search degrades to
// vector-only and says so in the log, because silently vector-only results are
// indistinguishable from ordinary ones.
func (s *PostgresStore) HybridSearch(ctx context.Context, vectorQuery []float32, textQuery string, opts HybridSearchOptions) ([]ScoredEmbedding, error) {
	// One retry, for a `vector` type replaced under this connection.
	// See IsStaleTypeCache: the statement never ran, and the failure is
	// what clears the cache that caused it.
	var out []ScoredEmbedding
	err := retryOnStaleTypeCache(func() error {
		var e error
		out, e = s.hybridSearch(ctx, vectorQuery, textQuery, opts)
		return e
	})
	return out, err
}

func (s *PostgresStore) hybridSearch(ctx context.Context, vectorQuery []float32, textQuery string, opts HybridSearchOptions) ([]ScoredEmbedding, error) {
	var (
		vectorResults []ScoredEmbedding
		err           error
	)
	if len(vectorQuery) > 0 {
		vectorResults, err = s.Search(ctx, vectorQuery, opts.SearchOptions)
		if err != nil {
			return nil, fmt.Errorf("vector search failed: %w", err)
		}
	}

	var textResults []ScoredEmbedding
	if strings.TrimSpace(textQuery) != "" {
		textResults, err = s.lexicalArm(ctx, textQuery, opts)
		switch {
		case err != nil && len(vectorQuery) == 0:
			return nil, fmt.Errorf("keyword search failed: %w", err)
		case err != nil:
			log.Printf("[HYBRID] keyword arm failed, continuing with vector results only: %v", err)
		}
	}

	k := opts.RRFK
	if k == 0 {
		k = 60
	}

	fused := make(map[string]float64, len(vectorResults)+len(textResults))
	byID := make(map[string]ScoredEmbedding, len(vectorResults)+len(textResults))
	for i, r := range vectorResults {
		fused[r.ID] = 1.0 / (k + float64(i+1))
		byID[r.ID] = r
	}
	for i, r := range textResults {
		fused[r.ID] += 1.0 / (k + float64(i+1))
		if _, seen := byID[r.ID]; !seen {
			byID[r.ID] = r
		}
	}

	out := make([]ScoredEmbedding, 0, len(fused))
	for id, score := range fused {
		e := byID[id]
		e.Score = score
		out = append(out, e)
	}
	// The id tiebreak is not decoration: the loop above walks a map, and
	// without it two runs of the same query over the same corpus can return
	// tied rows in different orders — which is exactly the kind of flake that
	// gets blamed on the retrieval quality instead of on the sort.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})

	if opts.TopK > 0 && len(out) > opts.TopK {
		out = out[:opts.TopK]
	}
	return out, nil
}

// lexicalArm runs the keyword half of a hybrid search, ranked.
//
// The pool is deliberately wider than TopK — fusion needs candidates below the
// cut to have anything to promote — matching the SQLite arm's TopK*3.
func (s *PostgresStore) lexicalArm(ctx context.Context, textQuery string, opts HybridSearchOptions) ([]ScoredEmbedding, error) {
	cond, lexArgs, _ := PostgresLexicalCondition("content", textQuery)

	args := &pgArgs{}
	// PostgresLexicalCondition writes one `?` per value for the dialect to
	// rebind; this dialect is $n. The CJK arm writes one per term, so the
	// count is whatever it returned rather than always one.
	var firstPlaceholder string
	for _, v := range lexArgs {
		ph := args.add(v)
		if firstPlaceholder == "" {
			firstPlaceholder = ph
		}
		cond = strings.Replace(cond, "?", ph+"::text", 1)
	}

	where := []string{"(" + cond + ")"}
	// The keyword arm has to be restricted exactly like the vector arm. Left
	// unrestricted it answers a question about one collection with rows from
	// another, and with no vector supplied it is the only arm, so the filter
	// the caller asked for would have no effect at all.
	where = append(where, s.optionWhere(args, opts.SearchOptions)...)

	// Ranking is available only on the tsquery branch. CJK goes through LIKE
	// — pg_trgm accelerates it but does not score it — so those rows are
	// ordered by id: arbitrary, but stable, which is what fusion needs. The
	// FTS5 side degrades in the same place for the same reason (see
	// BelowTrigramFloor), so neither backend pretends to rank what it cannot.
	order := "id"
	if !ContainsCJK(textQuery) {
		// Safe to reuse the first placeholder: on this branch
		// PostgresLexicalCondition binds the query itself — one value — not
		// the LIKE patterns the CJK branch builds from it.
		order = fmt.Sprintf(
			"ts_rank_cd(to_tsvector('simple', content), plainto_tsquery('simple', %s::text)) DESC, id",
			firstPlaceholder)
	}

	limit := opts.TopK * 3
	if limit <= 0 {
		limit = 30
	}

	q := fmt.Sprintf(`
		SELECT %s, 0::float8 AS score
		FROM embeddings
		WHERE %s
		ORDER BY %s
		LIMIT %s`,
		pgSearchColumns, strings.Join(where, " AND "), order, args.add(limit))

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
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- SearchWithAdvancedFilter ------------------------------------------------

// SearchWithAdvancedFilter performs vector search with a FilterExpression tree
// on either side of the scoring.
//
// PreFilter is compiled to SQL and narrows what the database looks at;
// PostFilter is evaluated in Go by evaluateFilter — the same function the
// SQLite store calls, so the two backends cannot drift on what a tree means.
// Both are checked for compilability up front: a PostFilter carrying an
// operator nothing evaluates would otherwise reject every row and look like an
// empty corpus.
func (s *PostgresStore) SearchWithAdvancedFilter(ctx context.Context, query []float32, opts AdvancedSearchOptions) ([]ScoredEmbedding, error) {
	// One retry, for a `vector` type replaced under this connection.
	// See IsStaleTypeCache: the statement never ran, and the failure is
	// what clears the cache that caused it.
	var out []ScoredEmbedding
	err := retryOnStaleTypeCache(func() error {
		var e error
		out, e = s.searchWithAdvancedFilter(ctx, query, opts)
		return e
	})
	return out, err
}

func (s *PostgresStore) searchWithAdvancedFilter(ctx context.Context, query []float32, opts AdvancedSearchOptions) ([]ScoredEmbedding, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("advanced_search: empty query vector")
	}
	if err := checkFilterSupported(opts.PostFilter); err != nil {
		return nil, fmt.Errorf("advanced_search post-filter: %w", err)
	}

	args := &pgArgs{}
	vec := args.add(PgVectorLiteral(query))

	var where []string
	if opts.PreFilter != nil {
		clause, err := pgFilterSQL(opts.PreFilter, args)
		if err != nil {
			return nil, fmt.Errorf("advanced_search pre-filter: %w", err)
		}
		if clause != "" {
			where = append(where, "("+clause+")")
		}
	}
	where = append(where, s.optionWhere(args, opts.SearchOptions)...)

	return s.runVectorSearch(ctx, args, vec, where, opts.SearchOptions, opts.PostFilter)
}

// --- shared machinery --------------------------------------------------------

// pgSearchColumns is the projection every arm uses, so a row looks the same
// whichever arm produced it.
const pgSearchColumns = `id, collection_id, content, doc_id, metadata, acl, ` +
	// The collection NAME, not just its id. SQLite's search joins collections
	// and fills it in; PostgreSQL projected only the id, so every scored row
	// came back with an empty Collection. Callers that route on the name —
	// cortex_query reports it, and retrieval filters by it — saw null where
	// SQLite saw "graphrag_chunks".
	//
	// A correlated subquery rather than a LEFT JOIN so the two arms above keep
	// their unqualified column names and their plans.
	`COALESCE((SELECT name FROM collections WHERE collections.id = embeddings.collection_id), '') AS collection_name`

// optionWhere renders the parts of SearchOptions that are conditions rather
// than scoring: the collection and the metadata equality filter.
//
// Both are pushed into SQL — the filter through the jsonb containment operator
// the GIN index serves — rather than dropped the way the SQLite ACL and
// advanced paths drop opts.Filter today. A condition the caller supplied and
// the store ignores is a wrong answer that looks like a right one.
func (s *PostgresStore) optionWhere(args *pgArgs, opts SearchOptions) []string {
	var where []string
	if opts.Collection != "" {
		where = append(where, fmt.Sprintf(
			"collection_id = (SELECT id FROM collections WHERE name = %s)", args.add(opts.Collection)))
	}
	if len(opts.Filter) > 0 {
		filter, err := json.Marshal(opts.Filter)
		if err != nil {
			// map[string]string cannot fail to marshal; a condition that
			// vanished here would be worse than one that never matched.
			where = append(where, "FALSE")
			return where
		}
		where = append(where, fmt.Sprintf("metadata @> %s::jsonb", args.add(string(filter))))
	}
	return where
}

// runVectorSearch executes an ordered vector scan with the given conditions and
// applies whatever cannot be said in SQL.
//
// Threshold and the post filter are applied in Go, for the reason Search gives:
// the threshold is on similarity while the ORDER BY is on distance, so a WHERE
// would have to restate the conversion and the two could drift.
//
// When a post filter is present the SQL carries no LIMIT, because a LIMIT
// applied before the filter returns fewer than TopK rows while matching rows
// sit just past the cut. The rows arrive in distance order and neither the
// threshold nor the filter reorders them, so stopping once TopK have been
// accepted gives exactly the top TopK — bounded work, and the same answer
// SQLite reaches by scoring everything.
func (s *PostgresStore) runVectorSearch(
	ctx context.Context,
	args *pgArgs,
	vec string,
	where []string,
	opts SearchOptions,
	post *FilterExpression,
) ([]ScoredEmbedding, error) {
	topK := opts.TopK
	if topK <= 0 {
		topK = 10 // Search's default, on both backends
	}

	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	limit := ""
	if post == nil {
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
		if post != nil && !evaluateFilter(post, metadataAsAny(e.Metadata)) {
			continue
		}
		out = append(out, e)
		if len(out) >= topK {
			break
		}
	}
	return out, rows.Err()
}

// scanPgScored reads one row of pgSearchColumns plus its score.
func scanPgScored(rows *sql.Rows) (ScoredEmbedding, error) {
	var (
		e        ScoredEmbedding
		docID    sql.NullString
		metadata []byte
		acl      []byte
	)
	var collectionName sql.NullString
	if err := rows.Scan(&e.ID, &e.CollectionID, &e.Content, &docID, &metadata, &acl, &collectionName, &e.Score); err != nil {
		return e, err
	}
	e.DocID = docID.String
	e.Collection = collectionName.String
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &e.Metadata); err != nil {
			return e, err
		}
	}
	if len(acl) > 0 {
		if err := json.Unmarshal(acl, &e.ACL); err != nil {
			return e, err
		}
	}
	return e, nil
}

// metadataAsAny widens the stored metadata for evaluateFilter, which compares
// across types.
func metadataAsAny(meta map[string]string) map[string]interface{} {
	out := make(map[string]interface{}, len(meta))
	for k, v := range meta {
		out[k] = v
	}
	return out
}

// --- compiling a FilterExpression to SQL -------------------------------------

// pgNumericRe matches the text a numeric comparison may cast.
//
// The guard exists because `'abc'::double precision` is an error in PostgreSQL,
// not a 0 — one non-numeric value in one row would fail the whole query.
const pgNumericRe = `^[+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)([eE][+-]?[0-9]+)?$`

// pgFilterSQL compiles a filter tree into a WHERE fragment, binding as it goes.
//
// Every field name is bound as a parameter rather than interpolated, so a
// metadata key can contain anything at all and still not be SQL. `->>` on the
// key also means what a flat map[string]string metadata says: a key with a dot
// in it is that key, not a path into a nested object the way SQLite's
// '$.a.b' would read it.
//
// An operator this cannot express returns an error naming itself. That is the
// whole point of the function existing next to BuildSQLFromFilter, which
// returns "" for the same cases — an empty fragment disappears into the
// surrounding AND and the caller gets rows they asked to exclude.
func pgFilterSQL(f *FilterExpression, args *pgArgs) (string, error) {
	if f == nil {
		return "", nil
	}

	switch f.Operator {
	case FilterAND, FilterOR:
		join, empty := " AND ", "TRUE"
		if f.Operator == FilterOR {
			join, empty = " OR ", "FALSE"
		}
		clauses := make([]string, 0, len(f.Children))
		for _, child := range f.Children {
			c, err := pgFilterSQL(child, args)
			if err != nil {
				return "", err
			}
			if c == "" {
				continue
			}
			clauses = append(clauses, "("+c+")")
		}
		if len(clauses) == 0 {
			// A childless AND constrains nothing; a childless OR is satisfied
			// by nothing. evaluateFilter agrees, and a degenerate node should
			// not mean different things on the two sides of the scorer.
			return empty, nil
		}
		return strings.Join(clauses, join), nil

	case FilterNOT:
		if len(f.Children) == 0 {
			return "FALSE", nil
		}
		c, err := pgFilterSQL(f.Children[0], args)
		if err != nil {
			return "", err
		}
		if c == "" {
			return "FALSE", nil
		}
		// COALESCE, because a comparison against a missing key is NULL and
		// `NOT NULL` is NULL — which excludes the row. evaluateFilter's NOT
		// keeps it: the inner test was false, so its negation is true.
		return "NOT COALESCE(" + c + ", FALSE)", nil
	}

	// Everything below is a comparison, and a comparison needs a field.
	if f.Field == "" {
		return "", fmt.Errorf("filter operator %s has no field", f.Operator)
	}

	switch f.Operator {
	case FilterEQ:
		return fmt.Sprintf("%s = %s::text", pgTextField(f.Field, args), args.add(filterText(f.Value))), nil

	case FilterNE:
		// `<>` rather than IS DISTINCT FROM, matching BuildSQLFromFilter: a row
		// without the key compares NULL and is excluded. That differs from
		// evaluateFilter's post-filter NE, which keeps it — a SQLite quirk
		// reproduced rather than quietly corrected, so a tree does not change
		// meaning by moving from PreFilter to PostFilter on one backend only.
		return fmt.Sprintf("%s <> %s::text", pgTextField(f.Field, args), args.add(filterText(f.Value))), nil

	case FilterGT, FilterGTE, FilterLT, FilterLTE:
		op := map[FilterOperator]string{
			FilterGT: ">", FilterGTE: ">=", FilterLT: "<", FilterLTE: "<=",
		}[f.Operator]
		if num, ok := toFloat64(f.Value); ok {
			return fmt.Sprintf("%s %s %s::double precision",
				pgNumericField(f.Field, args), op, args.add(num)), nil
		}
		// Non-numeric bound: compare as text, which is what compareValues
		// falls back to when either side will not parse as a number.
		return fmt.Sprintf("%s %s %s::text",
			pgTextField(f.Field, args), op, args.add(filterText(f.Value))), nil

	case FilterBETWEEN:
		vals, err := filterList(f.Value)
		if err != nil || len(vals) != 2 {
			return "", fmt.Errorf("filter operator BETWEEN on %q needs exactly two bounds", f.Field)
		}
		lo, loOK := toFloat64(vals[0])
		hi, hiOK := toFloat64(vals[1])
		if loOK && hiOK {
			return fmt.Sprintf("%s BETWEEN %s::double precision AND %s::double precision",
				pgNumericField(f.Field, args), args.add(lo), args.add(hi)), nil
		}
		return fmt.Sprintf("%s BETWEEN %s::text AND %s::text",
			pgTextField(f.Field, args), args.add(filterText(vals[0])), args.add(filterText(vals[1]))), nil

	case FilterIN:
		vals, err := filterList(f.Value)
		if err != nil {
			return "", fmt.Errorf("filter operator IN on %q: %w", f.Field, err)
		}
		if len(vals) == 0 {
			// IN () matches nothing, and that is a real answer rather than a
			// clause to drop.
			return "FALSE", nil
		}
		texts := make([]string, len(vals))
		for i, v := range vals {
			texts[i] = filterText(v)
		}
		// = ANY(array) rather than a generated IN list: one placeholder, no
		// ceiling on how many values a caller may pass.
		return fmt.Sprintf("%s = ANY(%s::text[])",
			pgTextField(f.Field, args), args.add(pgTextArray(texts))), nil

	case FilterLIKE:
		return fmt.Sprintf("%s LIKE %s::text", pgTextField(f.Field, args), args.add(filterText(f.Value))), nil

	case FilterREGEX:
		// Refused rather than approximated. PostgreSQL's `~` is POSIX ARE and
		// evaluateFilter's side is Go's RE2; the two disagree on backreferences,
		// lookaround and character-class names, so the same tree would select
		// different rows depending on which side of the scorer it landed on.
		// Naming it lets the caller rewrite the filter; compiling it would let
		// them find out from a row count months later.
		return "", fmt.Errorf(
			"filter operator %s cannot be compiled to SQL by the PostgreSQL store: "+
				"POSIX regular expressions and Go's would not select the same rows", FilterREGEX)

	default:
		return "", fmt.Errorf(
			"filter operator %q is not one the PostgreSQL store can compile", string(f.Operator))
	}
}

// pgTextField renders a metadata key as text, binding the key name.
func pgTextField(field string, args *pgArgs) string {
	return fmt.Sprintf("(metadata ->> %s::text)", args.add(field))
}

// pgNumericField renders a metadata key as a number, or NULL when the stored
// value is not one.
//
// CASE rather than `… ~ pattern AND …::float > x`: PostgreSQL does not promise
// to evaluate the arms of an AND left to right, so the guard could run after
// the cast it is guarding. CASE is defined to short-circuit.
//
// A non-numeric value therefore drops out of a numeric comparison instead of
// being read as SQLite's CAST-to-0.0, which would rank "n/a" below every real
// measurement and above nothing.
func pgNumericField(field string, args *pgArgs) string {
	p := args.add(field)
	return fmt.Sprintf(
		"(CASE WHEN (metadata ->> %[1]s::text) ~ '%[2]s' THEN (metadata ->> %[1]s::text)::double precision END)",
		p, pgNumericRe)
}

// checkFilterSupported walks a tree for operators nothing on this backend can
// evaluate, so a filter says so instead of matching nothing.
func checkFilterSupported(f *FilterExpression) error {
	if f == nil {
		return nil
	}
	switch f.Operator {
	case FilterAND, FilterOR, FilterNOT:
		for _, child := range f.Children {
			if err := checkFilterSupported(child); err != nil {
				return err
			}
		}
		return nil
	case FilterEQ, FilterNE, FilterGT, FilterGTE, FilterLT, FilterLTE,
		FilterBETWEEN, FilterIN, FilterLIKE:
		return nil
	case FilterREGEX:
		return fmt.Errorf("filter operator %s is not evaluated by this store", FilterREGEX)
	default:
		return fmt.Errorf("filter operator %q is not one this store can evaluate", string(f.Operator))
	}
}

// filterText renders a filter value the way the stored metadata reads: as
// text. Embedding.Metadata is map[string]string, so every value in the column
// is a JSON string and a text comparison is the only one that can match.
func filterText(v any) string {
	if v == nil {
		return ""
	}
	if f, ok := v.(float64); ok {
		// %v would render 1e+06 for a value parsed out of "1000000".
		return fmt.Sprintf("%v", trimFloat(f))
	}
	return fmt.Sprintf("%v", v)
}

// trimFloat renders a float that happens to be integral without a fractional
// part, because ParseFilterString turns every number into a float64 and a
// filter for count=3 must match the metadata string "3", not "3.000000".
func trimFloat(f float64) any {
	if f == float64(int64(f)) {
		return int64(f)
	}
	return f
}

// filterList normalises the several shapes a multi-value filter arrives in.
func filterList(v any) ([]any, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []any:
		return t, nil
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected a list of values, got %T", v)
	}
}
