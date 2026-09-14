package core

// Aggregate, spoken to PostgreSQL.
//
// COUNT / SUM / AVG / MIN / MAX / GROUP BY over metadata, matching
// aggregations.go clause for clause. The interesting part is not the SQL, it
// is the two places where "the same query" is not the same answer:
//
//   - SQLite's CAST to REAL never fails and never returns NULL for a value
//     that is present, so a field holding "n/a" contributes 0 to a SUM and is
//     counted in an AVG. PostgreSQL's cast raises instead, and one such row
//     would fail the whole statement. pgMetadataReal is the reconciliation.
//   - SQLite resolves an output alias in HAVING and PostgreSQL does not, so a
//     request saying `having: {count: 2}` compiles on one backend and errors
//     naming a column on the other. Resolved here, against the select list
//     this method just built.
//
// Everything else — which rows a filter selects, what an empty result looks
// like, what a GROUP BY on a field no row carries returns — follows from
// helpers shared with store_postgres_faceted.go.

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// Aggregate performs aggregation queries on embeddings metadata.
func (s *PostgresStore) Aggregate(ctx context.Context, req AggregationRequest) (*AggregationResponse, error) {
	if err := validateAggregationRequest(req); err != nil {
		return nil, wrapError("aggregate", err)
	}

	switch req.Type {
	case AggregationCount:
		return s.aggregateCount(ctx, req)
	case AggregationSum:
		return s.aggregateScalar(ctx, req, "SUM")
	case AggregationAvg:
		return s.aggregateScalar(ctx, req, "AVG")
	case AggregationMin:
		return s.aggregateScalar(ctx, req, "MIN")
	case AggregationMax:
		return s.aggregateScalar(ctx, req, "MAX")
	case AggregationGroupBy:
		return s.aggregateGroupBy(ctx, req)
	default:
		return nil, wrapError("aggregate", fmt.Errorf("unsupported aggregation type: %s", req.Type))
	}
}

// pgAggregateWhere renders the conditions every aggregation shares: the
// collection, then the metadata equality filters.
func pgAggregateWhere(req AggregationRequest, args *pgArgs) []string {
	var where []string
	if req.Collection != "" {
		where = append(where,
			"collection_id = (SELECT id FROM collections WHERE name = "+args.add(req.Collection)+")")
	}
	for field, value := range req.Filters {
		where = append(where, pgMetadataEquals(field, value, args))
	}
	return where
}

func (s *PostgresStore) aggregateCount(ctx context.Context, req AggregationRequest) (*AggregationResponse, error) {
	args := &pgArgs{}
	where := append([]string{"1=1"}, pgAggregateWhere(req, args)...)

	q := `SELECT COUNT(*) FROM embeddings WHERE ` + strings.Join(where, " AND ")

	var count int
	if err := s.db.QueryRowContext(ctx, q, args.vals...).Scan(&count); err != nil {
		return nil, err
	}

	return &AggregationResponse{
		Request: req,
		Results: []AggregationResult{
			{Value: count, Count: count},
		},
		Total: 1,
	}, nil
}

// aggregateScalar covers SUM, AVG, MIN and MAX, which differ only in the
// function name — as they do on the SQLite side, where the three functions
// that implement them are the same twenty lines three times.
//
// Count is COUNT(*) over the rows where the field is present, not over the
// table: the WHERE excludes a missing field, so an AVG over three rows of
// which one lacks the field reports Count 2. That is what SQLite reports.
func (s *PostgresStore) aggregateScalar(ctx context.Context, req AggregationRequest, fn string) (*AggregationResponse, error) {
	if req.Field == "" {
		return nil, fmt.Errorf("field is required for %s aggregation", req.Type)
	}

	args := &pgArgs{}
	value := pgMetadataReal(req.Field, args)
	present := pgMetadataText(req.Field, args) + " IS NOT NULL"
	where := append([]string{present}, pgAggregateWhere(req, args)...)

	q := fmt.Sprintf(`
		SELECT %s(%s) AS agg_value, COUNT(*) AS count
		FROM embeddings
		WHERE %s`, fn, value, strings.Join(where, " AND "))

	var (
		aggValue sql.NullFloat64
		count    int
	)
	if err := s.db.QueryRowContext(ctx, q, args.vals...).Scan(&aggValue, &count); err != nil {
		return nil, err
	}

	// Zero rather than nil when nothing matched, which is what the SQLite
	// version reports and what a caller adding the value to a running total
	// needs it to be.
	result := float64(0)
	if aggValue.Valid {
		result = aggValue.Float64
	}

	return &AggregationResponse{
		Request: req,
		Results: []AggregationResult{
			{Value: result, Count: count},
		},
		Total: 1,
	}, nil
}

func (s *PostgresStore) aggregateGroupBy(ctx context.Context, req AggregationRequest) (*AggregationResponse, error) {
	if len(req.GroupBy) == 0 {
		return nil, fmt.Errorf("group_by fields are required for GROUP BY aggregation")
	}

	args := &pgArgs{}

	// Built once and reused verbatim in GROUP BY, HAVING and ORDER BY. Calling
	// the helper again would bind a second copy of the key and produce an
	// expression PostgreSQL considers unrelated to the grouped one.
	groupExprs := make([]string, len(req.GroupBy))
	for i, field := range req.GroupBy {
		groupExprs[i] = pgMetadataText(field, args)
	}

	// The same switch aggregations.go writes, reproduced including the part
	// that cannot run: Aggregate only reaches here for AggregationGroupBy, so
	// the Sum/Avg/Min/Max arms are unreachable through the public entry point
	// on both backends. Kept in step so that if the dispatch ever widens, the
	// two backends widen together.
	aggExpr := "COUNT(*)"
	if req.Field != "" {
		switch req.Type {
		case AggregationSum:
			aggExpr = "SUM(" + pgMetadataReal(req.Field, args) + ")"
		case AggregationAvg:
			aggExpr = "AVG(" + pgMetadataReal(req.Field, args) + ")"
		case AggregationMin:
			aggExpr = "MIN(" + pgMetadataReal(req.Field, args) + ")"
		case AggregationMax:
			aggExpr = "MAX(" + pgMetadataReal(req.Field, args) + ")"
		}
	}

	where := append([]string{"1=1"}, pgAggregateWhere(req, args)...)

	q := fmt.Sprintf(`
		SELECT %s, %s AS agg_value, COUNT(*) AS count
		FROM embeddings
		WHERE %s
		GROUP BY %s`,
		strings.Join(groupExprs, ", "), aggExpr,
		strings.Join(where, " AND "), strings.Join(groupExprs, ", "))

	// HAVING. SQLite writes `HAVING <key> = ?` and resolves <key> against the
	// select list; PostgreSQL resolves output aliases in ORDER BY but not in
	// HAVING, so the same request would fail here naming a column that does
	// not exist. pgAggregateAlias maps the name back to the expression it
	// stands for.
	if len(req.Having) > 0 {
		havingClauses := make([]string, 0, len(req.Having))
		for field, value := range req.Having {
			expr := pgAggregateAlias(field, req.GroupBy, groupExprs, aggExpr)
			havingClauses = append(havingClauses,
				fmt.Sprintf("%s = %s", expr, args.add(value)))
		}
		q += " HAVING " + strings.Join(havingClauses, " AND ")
	}

	if req.OrderBy != "" {
		q += " ORDER BY " + pgAggregateAlias(req.OrderBy, req.GroupBy, groupExprs, aggExpr) + " DESC"
	} else {
		q += " ORDER BY COUNT(*) DESC"
	}

	if req.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", req.Limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args.vals...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	results := []AggregationResult{}
	for rows.Next() {
		scanValues := make([]interface{}, len(req.GroupBy)+2)
		for i := range req.GroupBy {
			scanValues[i] = new(sql.NullString)
		}
		var aggValue sql.NullFloat64
		var count int
		scanValues[len(req.GroupBy)] = &aggValue
		scanValues[len(req.GroupBy)+1] = &count

		if err := rows.Scan(scanValues...); err != nil {
			return nil, err
		}

		groupKeys := make(map[string]interface{})
		for i, field := range req.GroupBy {
			if val := scanValues[i].(*sql.NullString); val.Valid {
				// Numbers come back as numbers, strings as strings — the same
				// guess the SQLite side makes, and the reason a GROUP BY on a
				// field holding "10" yields the float 10 in the group key.
				if num, err := strconv.ParseFloat(val.String, 64); err == nil {
					groupKeys[field] = num
				} else {
					groupKeys[field] = val.String
				}
			} else {
				// A field no row carries is one group with a nil key, not zero
				// groups.
				groupKeys[field] = nil
			}
		}

		value := interface{}(nil)
		if aggValue.Valid {
			value = aggValue.Float64
		}

		results = append(results, AggregationResult{
			GroupKeys: groupKeys,
			Value:     value,
			Count:     count,
		})
	}

	return &AggregationResponse{
		Request: req,
		Results: results,
		Total:   len(results),
	}, rows.Err()
}

// pgAggregateAlias resolves a name the caller wrote in OrderBy or Having
// against the select list aggregateGroupBy just built.
//
// SQLite lets both clauses name an output alias, so `order_by: "count"` means
// COUNT(*) and `order_by: "category"` means the grouped expression. Handing
// either spelling straight to PostgreSQL gets "column does not exist" in
// HAVING, and in ORDER BY only works for the aliases that happen to be legal
// identifiers. Resolving is what makes one request mean one thing.
//
// A name that matches nothing is passed through as SQLite passes it through,
// where the database rejects it by name — the only interpolation of caller
// text in this file, and the same one aggregations.go already performs.
func pgAggregateAlias(name string, groupBy, groupExprs []string, aggExpr string) string {
	switch name {
	case "count":
		return "COUNT(*)"
	case "agg_value":
		return aggExpr
	}
	for i, field := range groupBy {
		if field == name {
			return groupExprs[i]
		}
	}
	return name
}
