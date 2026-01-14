package core

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// FilterOperator represents logical operators for filters
type FilterOperator string

const (
	FilterAND     FilterOperator = "AND"
	FilterOR      FilterOperator = "OR"
	FilterNOT     FilterOperator = "NOT"
	FilterEQ      FilterOperator = "="
	FilterNE      FilterOperator = "!="
	FilterGT      FilterOperator = ">"
	FilterGTE     FilterOperator = ">="
	FilterLT      FilterOperator = "<"
	FilterLTE     FilterOperator = "<="
	FilterIN      FilterOperator = "IN"
	FilterBETWEEN FilterOperator = "BETWEEN"
	FilterLIKE    FilterOperator = "LIKE"
	FilterREGEX   FilterOperator = "REGEX"
)

// FilterExpression represents a complex filter expression
type FilterExpression struct {
	Operator FilterOperator
	Field    string
	Value    interface{}
	Children []*FilterExpression
}

// AdvancedSearchOptions extends SearchOptions with advanced filtering
type AdvancedSearchOptions struct {
	SearchOptions
	PreFilter     *FilterExpression // Applied before vector search
	PostFilter    *FilterExpression // Applied after vector search
	ArraySupport  bool              // Enable array field filtering
	NumericRanges bool              // Enable numeric range optimization
}

// ParseFilterString parses a string filter expression into FilterExpression
// Example: "(tag:ai OR tag:ml) AND date>2024 AND price BETWEEN 100 AND 500"
func ParseFilterString(filterStr string) (*FilterExpression, error) {
	// Simple parser for demonstration - in production use a proper parser
	filterStr = strings.TrimSpace(filterStr)
	
	// Handle parentheses for grouping
	if strings.HasPrefix(filterStr, "(") && strings.HasSuffix(filterStr, ")") {
		inner := filterStr[1 : len(filterStr)-1]
		return ParseFilterString(inner)
	}
	
	// Check for AND/OR at top level
	if idx := strings.Index(filterStr, " AND "); idx > 0 {
		left, err := ParseFilterString(filterStr[:idx])
		if err != nil {
			return nil, err
		}
		right, err := ParseFilterString(filterStr[idx+5:])
		if err != nil {
			return nil, err
		}
		return &FilterExpression{
			Operator: FilterAND,
			Children: []*FilterExpression{left, right},
		}, nil
	}
	
	if idx := strings.Index(filterStr, " OR "); idx > 0 {
		left, err := ParseFilterString(filterStr[:idx])
		if err != nil {
			return nil, err
		}
		right, err := ParseFilterString(filterStr[idx+4:])
		if err != nil {
			return nil, err
		}
		return &FilterExpression{
			Operator: FilterOR,
			Children: []*FilterExpression{left, right},
		}, nil
	}
	
	// Parse comparison operators
	return parseComparison(filterStr)
}

func parseComparison(expr string) (*FilterExpression, error) {
	// Check for BETWEEN
	if strings.Contains(expr, " BETWEEN ") {
		parts := strings.Split(expr, " BETWEEN ")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid BETWEEN expression: %s", expr)
		}
		
		field := strings.TrimSpace(parts[0])
		rangeParts := strings.Split(parts[1], " AND ")
		if len(rangeParts) != 2 {
			return nil, fmt.Errorf("invalid BETWEEN range: %s", parts[1])
		}
		
		min, err := parseValue(strings.TrimSpace(rangeParts[0]))
		if err != nil {
			return nil, err
		}
		max, err := parseValue(strings.TrimSpace(rangeParts[1]))
		if err != nil {
			return nil, err
		}
		
		return &FilterExpression{
			Operator: FilterBETWEEN,
			Field:    field,
			Value:    []interface{}{min, max},
		}, nil
	}
	
	// Check for IN
	if strings.Contains(expr, " IN ") {
		parts := strings.Split(expr, " IN ")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid IN expression: %s", expr)
		}
		
		field := strings.TrimSpace(parts[0])
		valueStr := strings.TrimSpace(parts[1])
		
		// Parse array values
		if !strings.HasPrefix(valueStr, "(") || !strings.HasSuffix(valueStr, ")") {
			return nil, fmt.Errorf("IN values must be in parentheses: %s", valueStr)
		}
		
		valueStr = valueStr[1 : len(valueStr)-1]
		valueParts := strings.Split(valueStr, ",")
		values := make([]interface{}, len(valueParts))
		for i, v := range valueParts {
			val, err := parseValue(strings.TrimSpace(v))
			if err != nil {
				return nil, err
			}
			values[i] = val
		}
		
		return &FilterExpression{
			Operator: FilterIN,
			Field:    field,
			Value:    values,
		}, nil
	}
	
	// Check other operators
	operators := []struct {
		op  string
		typ FilterOperator
	}{
		{">=", FilterGTE},
		{"<=", FilterLTE},
		{"!=", FilterNE},
		{">", FilterGT},
		{"<", FilterLT},
		{"=", FilterEQ},
		{":=", FilterEQ}, // Alternative syntax
		{":", FilterEQ},  // Shorthand
	}
	
	for _, op := range operators {
		if idx := strings.Index(expr, op.op); idx > 0 {
			field := strings.TrimSpace(expr[:idx])
			valueStr := strings.TrimSpace(expr[idx+len(op.op):])
			value, err := parseValue(valueStr)
			if err != nil {
				return nil, err
			}
			
			return &FilterExpression{
				Operator: op.typ,
				Field:    field,
				Value:    value,
			}, nil
		}
	}
	
	return nil, fmt.Errorf("invalid filter expression: %s", expr)
}

func parseValue(str string) (interface{}, error) {
	str = strings.TrimSpace(str)
	
	// String with quotes
	if strings.HasPrefix(str, "'") && strings.HasSuffix(str, "'") {
		return str[1 : len(str)-1], nil
	}
	if strings.HasPrefix(str, "\"") && strings.HasSuffix(str, "\"") {
		return str[1 : len(str)-1], nil
	}
	
	// Boolean
	if str == "true" || str == "TRUE" {
		return true, nil
	}
	if str == "false" || str == "FALSE" {
		return false, nil
	}
	
	// Number
	if num, err := strconv.ParseFloat(str, 64); err == nil {
		return num, nil
	}
	
	// Treat as string without quotes
	return str, nil
}

// BuildSQLFromFilter converts FilterExpression to SQL WHERE clause
func BuildSQLFromFilter(filter *FilterExpression, paramIndex *int) (string, []interface{}) {
	if filter == nil {
		return "", nil
	}
	
	params := []interface{}{}
	
	switch filter.Operator {
	case FilterAND:
		clauses := []string{}
		for _, child := range filter.Children {
			clause, childParams := BuildSQLFromFilter(child, paramIndex)
			if clause != "" {
				clauses = append(clauses, "("+clause+")")
				params = append(params, childParams...)
			}
		}
		return strings.Join(clauses, " AND "), params
		
	case FilterOR:
		clauses := []string{}
		for _, child := range filter.Children {
			clause, childParams := BuildSQLFromFilter(child, paramIndex)
			if clause != "" {
				clauses = append(clauses, "("+clause+")")
				params = append(params, childParams...)
			}
		}
		return strings.Join(clauses, " OR "), params
		
	case FilterEQ:
		*paramIndex++
		params = append(params, filter.Value)
		return fmt.Sprintf("json_extract(metadata, '$.%s') = ?", filter.Field), params
		
	case FilterNE:
		*paramIndex++
		params = append(params, filter.Value)
		return fmt.Sprintf("json_extract(metadata, '$.%s') != ?", filter.Field), params
		
	case FilterGT:
		*paramIndex++
		params = append(params, filter.Value)
		return fmt.Sprintf("CAST(json_extract(metadata, '$.%s') AS REAL) > ?", filter.Field), params
		
	case FilterGTE:
		*paramIndex++
		params = append(params, filter.Value)
		return fmt.Sprintf("CAST(json_extract(metadata, '$.%s') AS REAL) >= ?", filter.Field), params
		
	case FilterLT:
		*paramIndex++
		params = append(params, filter.Value)
		return fmt.Sprintf("CAST(json_extract(metadata, '$.%s') AS REAL) < ?", filter.Field), params
		
	case FilterLTE:
		*paramIndex++
		params = append(params, filter.Value)
		return fmt.Sprintf("CAST(json_extract(metadata, '$.%s') AS REAL) <= ?", filter.Field), params
		
	case FilterBETWEEN:
		values := filter.Value.([]interface{})
		*paramIndex += 2
		params = append(params, values[0], values[1])
		return fmt.Sprintf("CAST(json_extract(metadata, '$.%s') AS REAL) BETWEEN ? AND ?", filter.Field), params
		
	case FilterIN:
		values := filter.Value.([]interface{})
		placeholders := make([]string, len(values))
		for i, v := range values {
			*paramIndex++
			placeholders[i] = "?"
			params = append(params, v)
		}
		return fmt.Sprintf("json_extract(metadata, '$.%s') IN (%s)", 
			filter.Field, strings.Join(placeholders, ",")), params
		
	case FilterLIKE:
		*paramIndex++
		params = append(params, filter.Value)
		return fmt.Sprintf("json_extract(metadata, '$.%s') LIKE ?", filter.Field), params
		
	default:
		return "", nil
	}
}

// MetadataFilter helper for building filter expressions
type MetadataFilter struct {
	expression *FilterExpression
}

// NewMetadataFilter creates a new metadata filter builder
func NewMetadataFilter() *MetadataFilter {
	return &MetadataFilter{}
}

// Equal adds an equality condition
func (f *MetadataFilter) Equal(field string, value interface{}) *MetadataFilter {
	expr := &FilterExpression{
		Operator: FilterEQ,
		Field:    field,
		Value:    value,
	}
	f.combine(expr)
	return f
}

// NotEqual adds a non-equality condition
func (f *MetadataFilter) NotEqual(field string, value interface{}) *MetadataFilter {
	expr := &FilterExpression{
		Operator: FilterNE,
		Field:    field,
		Value:    value,
	}
	f.combine(expr)
	return f
}

// GreaterThan adds a greater than condition
func (f *MetadataFilter) GreaterThan(field string, value interface{}) *MetadataFilter {
	expr := &FilterExpression{
		Operator: FilterGT,
		Field:    field,
		Value:    value,
	}
	f.combine(expr)
	return f
}

// LessThan adds a less than condition
func (f *MetadataFilter) LessThan(field string, value interface{}) *MetadataFilter {
	expr := &FilterExpression{
		Operator: FilterLT,
		Field:    field,
		Value:    value,
	}
	f.combine(expr)
	return f
}

// In adds an IN condition
func (f *MetadataFilter) In(field string, values ...interface{}) *MetadataFilter {
	expr := &FilterExpression{
		Operator: FilterIN,
		Field:    field,
		Value:    values,
	}
	f.combine(expr)
	return f
}

// GreaterThanOrEqual adds a greater than or equal condition
func (f *MetadataFilter) GreaterThanOrEqual(field string, value interface{}) *MetadataFilter {
	expr := &FilterExpression{
		Operator: FilterGTE,
		Field:    field,
		Value:    value,
	}
	f.combine(expr)
	return f
}

// LessThanOrEqual adds a less than or equal condition
func (f *MetadataFilter) LessThanOrEqual(field string, value interface{}) *MetadataFilter {
	expr := &FilterExpression{
		Operator: FilterLTE,
		Field:    field,
		Value:    value,
	}
	f.combine(expr)
	return f
}

// Between adds a BETWEEN condition
func (f *MetadataFilter) Between(field string, min, max interface{}) *MetadataFilter {
	expr := &FilterExpression{
		Operator: FilterBETWEEN,
		Field:    field,
		Value:    []interface{}{min, max},
	}
	f.combine(expr)
	return f
}

// Like adds a LIKE condition
func (f *MetadataFilter) Like(field string, pattern string) *MetadataFilter {
	expr := &FilterExpression{
		Operator: FilterLIKE,
		Field:    field,
		Value:    pattern,
	}
	f.combine(expr)
	return f
}

// NotIn adds a NOT IN condition
func (f *MetadataFilter) NotIn(field string, values ...interface{}) *MetadataFilter {
	// NotIn is usually NOT (field IN values)
	// Or we can add FilterNOTIN operator.
	// Current implementation doesn't support FilterNOTIN directly in BuildSQLFromFilter?
	// Let's implement as NOT(IN)
	
	inExpr := &FilterExpression{
		Operator: FilterIN,
		Field:    field,
		Value:    values,
	}
	
	expr := &FilterExpression{
		Operator: FilterNOT,
		Children: []*FilterExpression{inExpr},
	}
	
	f.combine(expr)
	return f
}

// StringIn is alias for In for string values
func (f *MetadataFilter) StringIn(field string, values ...string) *MetadataFilter {
	ifStr := make([]interface{}, len(values))
	for i, v := range values {
		ifStr[i] = v
	}
	return f.In(field, ifStr...)
}

// Build returns the underlying expression (for tests)
func (f *MetadataFilter) Build() *FilterExpression {
	return f.expression
}

// And combines with another filter using AND
func (f *MetadataFilter) And(other *MetadataFilter) *MetadataFilter {
	if other == nil || other.expression == nil {
		return f
	}
	if f.expression == nil {
		f.expression = other.expression
		return f
	}
	
	// If current is AND, append to children to keep tree flat?
	// For simplicity, just create new AND node
	f.expression = &FilterExpression{
		Operator: FilterAND,
		Children: []*FilterExpression{f.expression, other.expression},
	}
	return f
}

// Or combines with another filter using OR
func (f *MetadataFilter) Or(other *MetadataFilter) *MetadataFilter {
	if other == nil || other.expression == nil {
		return f
	}
	if f.expression == nil {
		f.expression = other.expression
		return f
	}
	
	f.expression = &FilterExpression{
		Operator: FilterOR,
		Children: []*FilterExpression{f.expression, other.expression},
	}
	return f
}

// combine adds expression to the filter, combining with AND if needed
func (f *MetadataFilter) combine(expr *FilterExpression) {
	if f.expression == nil {
		f.expression = expr
	} else {
		// Combine with AND
		f.expression = &FilterExpression{
			Operator: FilterAND,
			Children: []*FilterExpression{f.expression, expr},
		}
	}
}

// BuildSQL returns the SQL WHERE clause for the filter
func (f *MetadataFilter) BuildSQL() (string, []interface{}) {
	if f.expression == nil {
		return "", nil
	}
	paramIndex := 0
	return BuildSQLFromFilter(f.expression, &paramIndex)
}

// ToSQL is an alias for BuildSQL
func (f *MetadataFilter) ToSQL() (string, []interface{}) {
	return f.BuildSQL()
}

// IsEmpty checks if the filter is empty
func (f *MetadataFilter) IsEmpty() bool {
	return f == nil || f.expression == nil
}

// SearchWithAdvancedFilter performs vector search with advanced filtering
func (s *SQLiteStore) SearchWithAdvancedFilter(ctx context.Context, query []float32, opts AdvancedSearchOptions) ([]ScoredEmbedding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return nil, wrapError("advanced_search", ErrStoreClosed)
	}
	
	// Build SQL query with pre-filter
	var whereClause string
	var params []interface{}
	
	if opts.PreFilter != nil {
		paramIndex := 0
		whereClause, params = BuildSQLFromFilter(opts.PreFilter, &paramIndex)
	}
	
	// Fetch candidates with pre-filter
	candidates, err := s.fetchCandidatesWithSQL(ctx, whereClause, params, opts.SearchOptions)
	if err != nil {
		return nil, err
	}
	
	// Score candidates
	results := make([]ScoredEmbedding, 0, len(candidates))
	for _, candidate := range candidates {
		score := s.similarityFn(query, candidate.Vector)
		candidate.Score = score
		
		// Apply post-filter if specified
		if opts.PostFilter != nil {
			// Convert metadata to interface{} map
			metadataIntf := make(map[string]interface{})
			for k, v := range candidate.Metadata {
				metadataIntf[k] = v
			}
			if !evaluateFilter(opts.PostFilter, metadataIntf) {
				continue
			}
		}
		
		results = append(results, candidate)
	}
	
	// Sort by score (descending)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	
	// Apply TopK limit
	if opts.TopK > 0 && len(results) > opts.TopK {
		results = results[:opts.TopK]
	}
	
	return results, nil
}

// fetchCandidatesWithSQL fetches candidates with custom SQL WHERE clause
func (s *SQLiteStore) fetchCandidatesWithSQL(ctx context.Context, whereClause string, params []interface{}, opts SearchOptions) ([]ScoredEmbedding, error) {
	query := `
		SELECT e.id, e.collection_id, c.name, e.vector, e.content, e.doc_id, e.metadata
		FROM embeddings e
		LEFT JOIN collections c ON e.collection_id = c.id
	`
	
	conditions := []string{}
	
	if whereClause != "" {
		conditions = append(conditions, whereClause)
	}
	
	if opts.Collection != "" {
		conditions = append(conditions, "c.name = ?")
		params = append(params, opts.Collection)
	}
	
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	
	rows, err := s.db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, fmt.Errorf("failed to query with filter: %w", err)
	}
	defer func() { _ = rows.Close() }()
	
	var candidates []ScoredEmbedding
	for rows.Next() {
		candidate, err := s.scanEmbedding(rows)
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate)
	}
	
	return candidates, rows.Err()
}

// evaluateFilter evaluates a filter expression against metadata
func evaluateFilter(filter *FilterExpression, metadata map[string]interface{}) bool {
	if filter == nil {
		return true
	}
	
	switch filter.Operator {
	case FilterAND:
		for _, child := range filter.Children {
			if !evaluateFilter(child, metadata) {
				return false
			}
		}
		return true
		
	case FilterOR:
		for _, child := range filter.Children {
			if evaluateFilter(child, metadata) {
				return true
			}
		}
		return false
		
	case FilterNOT:
		if len(filter.Children) > 0 {
			return !evaluateFilter(filter.Children[0], metadata)
		}
		return false
		
	case FilterEQ:
		val, exists := metadata[filter.Field]
		if !exists {
			return false
		}
		return compareValues(val, filter.Value, FilterEQ)
		
	case FilterNE:
		val, exists := metadata[filter.Field]
		if !exists {
			return true
		}
		return compareValues(val, filter.Value, FilterNE)
		
	case FilterGT, FilterGTE, FilterLT, FilterLTE:
		val, exists := metadata[filter.Field]
		if !exists {
			return false
		}
		return compareValues(val, filter.Value, filter.Operator)
		
	case FilterBETWEEN:
		val, exists := metadata[filter.Field]
		if !exists {
			return false
		}
		values := filter.Value.([]interface{})
		return compareValues(val, values[0], FilterGTE) && compareValues(val, values[1], FilterLTE)
		
	case FilterIN:
		val, exists := metadata[filter.Field]
		if !exists {
			return false
		}
		values := filter.Value.([]interface{})
		for _, v := range values {
			if compareValues(val, v, FilterEQ) {
				return true
			}
		}
		return false
		
	case FilterLIKE:
		val, exists := metadata[filter.Field]
		if !exists {
			return false
		}
		pattern := fmt.Sprintf("%v", filter.Value)
		text := fmt.Sprintf("%v", val)
		// Convert SQL LIKE pattern to regex
		pattern = strings.ReplaceAll(pattern, "%", ".*")
		pattern = strings.ReplaceAll(pattern, "_", ".")
		matched, _ := regexp.MatchString("^"+pattern+"$", text)
		return matched
		
	default:
		return false
	}
}

// compareValues compares two values based on operator
func compareValues(a, b interface{}, op FilterOperator) bool {
	// Convert to comparable types
	aFloat, aIsNum := toFloat64(a)
	bFloat, bIsNum := toFloat64(b)
	
	if aIsNum && bIsNum {
		switch op {
		case FilterEQ:
			return aFloat == bFloat
		case FilterNE:
			return aFloat != bFloat
		case FilterGT:
			return aFloat > bFloat
		case FilterGTE:
			return aFloat >= bFloat
		case FilterLT:
			return aFloat < bFloat
		case FilterLTE:
			return aFloat <= bFloat
		}
	}
	
	// String comparison
	aStr := fmt.Sprintf("%v", a)
	bStr := fmt.Sprintf("%v", b)
	
	switch op {
	case FilterEQ:
		return aStr == bStr
	case FilterNE:
		return aStr != bStr
	case FilterGT:
		return aStr > bStr
	case FilterGTE:
		return aStr >= bStr
	case FilterLT:
		return aStr < bStr
	case FilterLTE:
		return aStr <= bStr
	}
	
	return false
}

// toFloat64 attempts to convert value to float64
func toFloat64(val interface{}) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}