package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
)

// AggregationType defines the type of aggregation
type AggregationType string

const (
	AggregationCount   AggregationType = "count"
	AggregationSum     AggregationType = "sum"
	AggregationAvg     AggregationType = "avg"
	AggregationMin     AggregationType = "min"
	AggregationMax     AggregationType = "max"
	AggregationGroupBy AggregationType = "group_by"
)

// AggregationRequest defines parameters for aggregation queries
type AggregationRequest struct {
	Type       AggregationType        `json:"type"`
	Field      string                 `json:"field"`           // Metadata field to aggregate
	GroupBy    []string               `json:"group_by"`        // Fields to group by
	Filters    map[string]interface{} `json:"filters"`         // Optional filters
	Collection string                 `json:"collection"`      // Optional collection filter
	Having     map[string]interface{} `json:"having"`          // Post-aggregation filters
	OrderBy    string                 `json:"order_by"`        // Field to order results by
	Limit      int                    `json:"limit"`           // Max results
}

// AggregationResult represents a single aggregation result
type AggregationResult struct {
	GroupKeys map[string]interface{} `json:"group_keys"` // Group by field values
	Value     interface{}            `json:"value"`      // Aggregated value
	Count     int                    `json:"count"`      // Number of items in group
}

// AggregationResponse contains the aggregation results
type AggregationResponse struct {
	Request AggregationRequest  `json:"request"`
	Results []AggregationResult `json:"results"`
	Total   int                 `json:"total"`
}

// Aggregate performs aggregation queries on embeddings metadata
func (s *SQLiteStore) Aggregate(ctx context.Context, req AggregationRequest) (*AggregationResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, wrapError("aggregate", ErrStoreClosed)
	}

	// Validate request
	if err := validateAggregationRequest(req); err != nil {
		return nil, wrapError("aggregate", err)
	}

	switch req.Type {
	case AggregationCount:
		return s.aggregateCount(ctx, req)
	case AggregationSum:
		return s.aggregateSum(ctx, req)
	case AggregationAvg:
		return s.aggregateAvg(ctx, req)
	case AggregationMin, AggregationMax:
		return s.aggregateMinMax(ctx, req)
	case AggregationGroupBy:
		return s.aggregateGroupBy(ctx, req)
	default:
		return nil, wrapError("aggregate", fmt.Errorf("unsupported aggregation type: %s", req.Type))
	}
}

// aggregateCount performs COUNT aggregation
func (s *SQLiteStore) aggregateCount(ctx context.Context, req AggregationRequest) (*AggregationResponse, error) {
	query := `SELECT COUNT(*) FROM embeddings WHERE 1=1`
	args := []interface{}{}

	// Add collection filter
	if req.Collection != "" {
		query += ` AND collection_id = (SELECT id FROM collections WHERE name = ?)`
		args = append(args, req.Collection)
	}

	// Add metadata filters
	query, args = addMetadataFilters(query, args, req.Filters)

	var count int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&count)
	if err != nil {
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

// aggregateSum performs SUM aggregation on a metadata field
func (s *SQLiteStore) aggregateSum(ctx context.Context, req AggregationRequest) (*AggregationResponse, error) {
	if req.Field == "" {
		return nil, fmt.Errorf("field is required for SUM aggregation")
	}

	// Use JSON extraction for metadata field
	query := fmt.Sprintf(`
		SELECT 
			SUM(CAST(json_extract(metadata, '$.%s') AS REAL)) as sum_value,
			COUNT(*) as count
		FROM embeddings 
		WHERE json_extract(metadata, '$.%s') IS NOT NULL`,
		req.Field, req.Field)

	args := []interface{}{}

	// Add collection filter
	if req.Collection != "" {
		query += ` AND collection_id = (SELECT id FROM collections WHERE name = ?)`
		args = append(args, req.Collection)
	}

	// Add metadata filters
	query, args = addMetadataFilters(query, args, req.Filters)

	var sumValue sql.NullFloat64
	var count int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&sumValue, &count)
	if err != nil {
		return nil, err
	}

	value := float64(0)
	if sumValue.Valid {
		value = sumValue.Float64
	}

	return &AggregationResponse{
		Request: req,
		Results: []AggregationResult{
			{Value: value, Count: count},
		},
		Total: 1,
	}, nil
}

// aggregateAvg performs AVG aggregation on a metadata field
func (s *SQLiteStore) aggregateAvg(ctx context.Context, req AggregationRequest) (*AggregationResponse, error) {
	if req.Field == "" {
		return nil, fmt.Errorf("field is required for AVG aggregation")
	}

	query := fmt.Sprintf(`
		SELECT 
			AVG(CAST(json_extract(metadata, '$.%s') AS REAL)) as avg_value,
			COUNT(*) as count
		FROM embeddings 
		WHERE json_extract(metadata, '$.%s') IS NOT NULL`,
		req.Field, req.Field)

	args := []interface{}{}

	// Add collection filter
	if req.Collection != "" {
		query += ` AND collection_id = (SELECT id FROM collections WHERE name = ?)`
		args = append(args, req.Collection)
	}

	// Add metadata filters
	query, args = addMetadataFilters(query, args, req.Filters)

	var avgValue sql.NullFloat64
	var count int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&avgValue, &count)
	if err != nil {
		return nil, err
	}

	value := float64(0)
	if avgValue.Valid {
		value = avgValue.Float64
	}

	return &AggregationResponse{
		Request: req,
		Results: []AggregationResult{
			{Value: value, Count: count},
		},
		Total: 1,
	}, nil
}

// aggregateMinMax performs MIN or MAX aggregation
func (s *SQLiteStore) aggregateMinMax(ctx context.Context, req AggregationRequest) (*AggregationResponse, error) {
	if req.Field == "" {
		return nil, fmt.Errorf("field is required for %s aggregation", req.Type)
	}

	aggFunc := "MIN"
	if req.Type == AggregationMax {
		aggFunc = "MAX"
	}

	query := fmt.Sprintf(`
		SELECT 
			%s(CAST(json_extract(metadata, '$.%s') AS REAL)) as value,
			COUNT(*) as count
		FROM embeddings 
		WHERE json_extract(metadata, '$.%s') IS NOT NULL`,
		aggFunc, req.Field, req.Field)

	args := []interface{}{}

	// Add collection filter
	if req.Collection != "" {
		query += ` AND collection_id = (SELECT id FROM collections WHERE name = ?)`
		args = append(args, req.Collection)
	}

	// Add metadata filters
	query, args = addMetadataFilters(query, args, req.Filters)

	var value sql.NullFloat64
	var count int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&value, &count)
	if err != nil {
		return nil, err
	}

	result := float64(0)
	if value.Valid {
		result = value.Float64
	}

	return &AggregationResponse{
		Request: req,
		Results: []AggregationResult{
			{Value: result, Count: count},
		},
		Total: 1,
	}, nil
}

// aggregateGroupBy performs GROUP BY aggregation
func (s *SQLiteStore) aggregateGroupBy(ctx context.Context, req AggregationRequest) (*AggregationResponse, error) {
	if len(req.GroupBy) == 0 {
		return nil, fmt.Errorf("group_by fields are required for GROUP BY aggregation")
	}

	// Build SELECT clause for group keys
	selectClauses := []string{}
	groupByClauses := []string{}
	for _, field := range req.GroupBy {
		selectClauses = append(selectClauses, 
			fmt.Sprintf("json_extract(metadata, '$.%s') as %s", field, field))
		groupByClauses = append(groupByClauses, 
			fmt.Sprintf("json_extract(metadata, '$.%s')", field))
	}

	// Add aggregation based on field
	aggClause := "COUNT(*) as agg_value"
	if req.Field != "" {
		switch req.Type {
		case AggregationSum:
			aggClause = fmt.Sprintf("SUM(CAST(json_extract(metadata, '$.%s') AS REAL)) as agg_value", req.Field)
		case AggregationAvg:
			aggClause = fmt.Sprintf("AVG(CAST(json_extract(metadata, '$.%s') AS REAL)) as agg_value", req.Field)
		case AggregationMin:
			aggClause = fmt.Sprintf("MIN(CAST(json_extract(metadata, '$.%s') AS REAL)) as agg_value", req.Field)
		case AggregationMax:
			aggClause = fmt.Sprintf("MAX(CAST(json_extract(metadata, '$.%s') AS REAL)) as agg_value", req.Field)
		default:
			aggClause = "COUNT(*) as agg_value"
		}
	}

	query := fmt.Sprintf(`
		SELECT 
			%s,
			%s,
			COUNT(*) as count
		FROM embeddings 
		WHERE 1=1`,
		joinStrings(selectClauses, ", "),
		aggClause)

	args := []interface{}{}

	// Add collection filter
	if req.Collection != "" {
		query += ` AND collection_id = (SELECT id FROM collections WHERE name = ?)`
		args = append(args, req.Collection)
	}

	// Add metadata filters
	query, args = addMetadataFilters(query, args, req.Filters)

	// Add GROUP BY clause
	query += fmt.Sprintf(" GROUP BY %s", joinStrings(groupByClauses, ", "))

	// Add HAVING clause for post-aggregation filters
	if len(req.Having) > 0 {
		havingClauses := []string{}
		for field, value := range req.Having {
			havingClauses = append(havingClauses, fmt.Sprintf("%s = ?", field))
			args = append(args, value)
		}
		query += fmt.Sprintf(" HAVING %s", joinStrings(havingClauses, " AND "))
	}

	// Add ORDER BY clause
	if req.OrderBy != "" {
		query += fmt.Sprintf(" ORDER BY %s DESC", req.OrderBy)
	} else {
		query += " ORDER BY count DESC"
	}

	// Add LIMIT clause
	if req.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", req.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	results := []AggregationResult{}
	for rows.Next() {
		// Scan dynamic columns based on group by fields
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

		// Build group keys
		groupKeys := make(map[string]interface{})
		for i, field := range req.GroupBy {
			if val := scanValues[i].(*sql.NullString); val.Valid {
				// Try to parse as number
				if num, err := strconv.ParseFloat(val.String, 64); err == nil {
					groupKeys[field] = num
				} else {
					groupKeys[field] = val.String
				}
			} else {
				groupKeys[field] = nil
			}
		}

		// Get aggregated value
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

// validateAggregationRequest validates the aggregation request
func validateAggregationRequest(req AggregationRequest) error {
	if req.Type == "" {
		return fmt.Errorf("aggregation type is required")
	}

	// Validate field requirement for certain aggregations
	needsField := []AggregationType{AggregationSum, AggregationAvg, AggregationMin, AggregationMax}
	for _, t := range needsField {
		if req.Type == t && req.Field == "" {
			return fmt.Errorf("field is required for %s aggregation", req.Type)
		}
	}

	// Validate group by for GROUP BY aggregation
	if req.Type == AggregationGroupBy && len(req.GroupBy) == 0 {
		return fmt.Errorf("group_by fields are required for GROUP BY aggregation")
	}

	return nil
}

// addMetadataFilters adds metadata filters to the query
func addMetadataFilters(query string, args []interface{}, filters map[string]interface{}) (string, []interface{}) {
	for field, value := range filters {
		query += fmt.Sprintf(" AND json_extract(metadata, '$.%s') = ?", field)
		
		// Convert value to appropriate SQL type
		switch v := value.(type) {
		case string:
			args = append(args, v)
		case int:
			args = append(args, v)
		case float64:
			args = append(args, v)
		case bool:
			if v {
				args = append(args, 1)
			} else {
				args = append(args, 0)
			}
		default:
			// Try to convert to JSON string
			if jsonBytes, err := json.Marshal(v); err == nil {
				args = append(args, string(jsonBytes))
			} else {
				args = append(args, fmt.Sprintf("%v", v))
			}
		}
	}
	return query, args
}

// joinStrings joins strings with a separator
func joinStrings(strs []string, sep string) string {
	result := ""
	for i, s := range strs {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}