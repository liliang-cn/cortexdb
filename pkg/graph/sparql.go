package graph

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const (
	// SPARQLQuerySelect executes a tabular SELECT query.
	SPARQLQuerySelect = "select"
	// SPARQLQueryAsk executes a boolean ASK query.
	SPARQLQueryAsk = "ask"
	// SPARQLQueryConstruct executes a graph-producing CONSTRUCT query.
	SPARQLQueryConstruct = "construct"
	// SPARQLQueryDescribe executes a graph-producing DESCRIBE query.
	SPARQLQueryDescribe = "describe"
	// SPARQLQueryInsertData executes an INSERT DATA update.
	SPARQLQueryInsertData = "insert_data"
	// SPARQLQueryDeleteData executes a DELETE DATA update.
	SPARQLQueryDeleteData = "delete_data"
	// SPARQLQueryDeleteWhere executes a DELETE WHERE update.
	SPARQLQueryDeleteWhere = "delete_where"
	// SPARQLQueryModify executes INSERT ... WHERE / DELETE ... INSERT ... WHERE style updates.
	SPARQLQueryModify = "modify"
)

const (
	sparqlPathDirect      = "direct"
	sparqlPathInverse     = "inverse"
	sparqlPathAlternative = "alternative"
	sparqlPathZeroOrMore  = "zero_or_more"
	sparqlPathOneOrMore   = "one_or_more"
)

// SPARQLResult contains the result of executing a SPARQL query.
type SPARQLResult struct {
	QueryType string               `json:"query_type"`
	Vars      []string             `json:"vars,omitempty"`
	Bindings  []map[string]RDFTerm `json:"bindings,omitempty"`
	Triples   []RDFTriple          `json:"triples,omitempty"`
	Boolean   bool                 `json:"boolean,omitempty"`
	Count     int                  `json:"count"`
}

type sparqlQuery struct {
	Prefixes    map[string]string
	QueryType   string
	SelectAll   bool
	Distinct    bool
	Vars        []string
	SelectItems []sparqlSelectItem
	Template    []sparqlPattern
	Delete      []sparqlPattern
	Insert      []sparqlPattern
	Describe    []sparqlTermPattern
	Group       sparqlGroup
	With        *RDFTerm
	Using       []RDFTerm
	UsingNamed  []RDFTerm
	GroupBy     []sparqlGroupKey
	Having      []sparqlFilter
	OrderBy     []sparqlOrderClause
	Offset      int
	Limit       int
}

type sparqlGroup struct {
	Steps []sparqlStep
}

type sparqlExecOptions struct {
	DefaultGraphs []RDFTerm
	NamedGraphs   []RDFTerm
}

type sparqlStep interface {
	sparqlStep()
}

type sparqlPatternStep struct {
	Pattern sparqlPattern
}

type sparqlFilterStep struct {
	Filter sparqlFilter
}

type sparqlOptionalStep struct {
	Group sparqlGroup
}

type sparqlUnionStep struct {
	Branches []sparqlGroup
}

type sparqlSubQueryStep struct {
	Query *sparqlQuery
}

func (sparqlSubQueryStep) sparqlStep() {}

type sparqlGroupStep struct {
	Group sparqlGroup
}

type sparqlMinusStep struct {
	Group sparqlGroup
}

type sparqlValuesStep struct {
	Variables []string
	Rows      []map[string]RDFTerm
}

type sparqlBindStep struct {
	Variable string
	Expr     sparqlValueExpr
}

type sparqlPattern struct {
	Subject   sparqlTermPattern
	Predicate sparqlTermPattern
	Path      *sparqlPropertyPath
	Object    sparqlTermPattern
	Graph     *sparqlTermPattern
}

type sparqlPropertyPath struct {
	Kind  string
	Terms []RDFTerm
}

type sparqlSelectItem struct {
	Alias string
	Expr  sparqlValueExpr
}

type sparqlGroupKey struct {
	Alias string
	Expr  sparqlValueExpr
}

type sparqlOrderClause struct {
	Desc bool
	Expr sparqlValueExpr
}

type sparqlTermPattern struct {
	Variable string
	Term     *RDFTerm
}

type sparqlFilter interface {
	Eval(binding map[string]RDFTerm) (bool, error)
	EvalGroup(bindings []map[string]RDFTerm) (bool, error)
}

type sparqlRuntimeFilter interface {
	EvalRuntime(ctx context.Context, store *GraphStore, opts sparqlExecOptions, binding map[string]RDFTerm) (bool, error)
	EvalGroupRuntime(ctx context.Context, store *GraphStore, opts sparqlExecOptions, bindings []map[string]RDFTerm) (bool, error)
}

type sparqlValueExpr interface {
	Eval(binding map[string]RDFTerm) (RDFTerm, bool, error)
	EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error)
	IsAggregate() bool
}

type sparqlBoundFilter struct {
	Variable string
}

type sparqlCompareFilter struct {
	Op    string
	Left  sparqlValueExpr
	Right sparqlValueExpr
}

type sparqlContainsFilter struct {
	Haystack sparqlValueExpr
	Needle   sparqlValueExpr
}

type sparqlStrStartsFilter struct {
	Haystack sparqlValueExpr
	Prefix   sparqlValueExpr
}

type sparqlRegexFilter struct {
	Value   sparqlValueExpr
	Pattern sparqlValueExpr
	Flags   sparqlValueExpr
}

type sparqlAndFilter struct {
	Left  sparqlFilter
	Right sparqlFilter
}

type sparqlOrFilter struct {
	Left  sparqlFilter
	Right sparqlFilter
}

type sparqlNotFilter struct {
	Inner sparqlFilter
}

type sparqlExprFilter struct {
	Expr sparqlValueExpr
}

type sparqlExistsFilter struct {
	Group   sparqlGroup
	Negated bool
}

type sparqlVarExpr struct {
	Variable string
}

type sparqlLiteralExpr struct {
	Term RDFTerm
}

type sparqlStrFuncExpr struct {
	Inner sparqlValueExpr
}

type sparqlLCaseFuncExpr struct {
	Inner sparqlValueExpr
}

type sparqlLangFuncExpr struct {
	Inner sparqlValueExpr
}

type sparqlDatatypeFuncExpr struct {
	Inner sparqlValueExpr
}

type sparqlCountFuncExpr struct {
	Inner    sparqlValueExpr
	Wildcard bool
	Distinct bool
}

type sparqlAggregateFuncExpr struct {
	Name      string
	Inner     sparqlValueExpr
	Distinct  bool
	Separator string
}

type sparqlUnaryNumericExpr struct {
	Op    string
	Inner sparqlValueExpr
}

type sparqlArithmeticExpr struct {
	Op    string
	Left  sparqlValueExpr
	Right sparqlValueExpr
}

type sparqlCoalesceFuncExpr struct {
	Args []sparqlValueExpr
}

type sparqlIfFuncExpr struct {
	Cond sparqlFilter
	Then sparqlValueExpr
	Else sparqlValueExpr
}

func (sparqlPatternStep) sparqlStep()  {}
func (sparqlFilterStep) sparqlStep()   {}
func (sparqlOptionalStep) sparqlStep() {}
func (sparqlUnionStep) sparqlStep()    {}
func (sparqlGroupStep) sparqlStep()    {}
func (sparqlMinusStep) sparqlStep()    {}
func (sparqlValuesStep) sparqlStep()   {}
func (sparqlBindStep) sparqlStep()     {}

// ExecuteSPARQL runs a practical SPARQL SELECT/ASK subset against the embedded RDF layer.
func (g *GraphStore) ExecuteSPARQL(ctx context.Context, query string) (*SPARQLResult, error) {
	parsed, err := g.parseSPARQL(ctx, query)
	if err != nil {
		return nil, err
	}
	execOptions := buildSPARQLExecOptions(parsed)

	if parsed.QueryType == SPARQLQueryInsertData {
		count, err := g.executeSPARQLInsertData(ctx, parsed.Template)
		if err != nil {
			return nil, err
		}
		return &SPARQLResult{QueryType: parsed.QueryType, Count: count}, nil
	}
	if parsed.QueryType == SPARQLQueryDeleteData {
		count, err := g.executeSPARQLDeleteData(ctx, parsed.Template)
		if err != nil {
			return nil, err
		}
		return &SPARQLResult{QueryType: parsed.QueryType, Count: count}, nil
	}

	bindings, err := g.executeSPARQLGroup(ctx, parsed.Group, []map[string]RDFTerm{{}}, execOptions)
	if err != nil {
		return nil, err
	}

	if parsed.QueryType == SPARQLQueryDeleteWhere {
		count, err := g.executeSPARQLDeleteWhere(ctx, parsed.Template, bindings, parsed.With)
		if err != nil {
			return nil, err
		}
		return &SPARQLResult{QueryType: parsed.QueryType, Count: count}, nil
	}
	if parsed.QueryType == SPARQLQueryModify {
		count, err := g.executeSPARQLModify(ctx, parsed.Delete, parsed.Insert, bindings, parsed.With)
		if err != nil {
			return nil, err
		}
		return &SPARQLResult{QueryType: parsed.QueryType, Count: count}, nil
	}

	result := &SPARQLResult{
		QueryType: parsed.QueryType,
		Count:     len(bindings),
	}

	if parsed.QueryType == SPARQLQueryAsk {
		result.Boolean = len(bindings) > 0
		if result.Boolean {
			result.Count = 1
		}
		return result, nil
	}

	if len(parsed.OrderBy) > 0 {
		sortSPARQLBindings(bindings, parsed.OrderBy)
	}

	if parsed.QueryType == SPARQLQueryConstruct {
		bindings = applyOffsetLimit(bindings, parsed.Offset, parsed.Limit)
		triples := materializeTemplateTriplesWithDefaultGraph(parsed.Template, bindings, parsed.With)
		result.Triples = triples
		result.Count = len(triples)
		return result, nil
	}
	if parsed.QueryType == SPARQLQueryDescribe {
		bindings = applyOffsetLimit(bindings, parsed.Offset, parsed.Limit)
		triples, err := g.materializeDescribeTriples(ctx, parsed.Describe, bindings)
		if err != nil {
			return nil, err
		}
		result.Triples = triples
		result.Count = len(triples)
		return result, nil
	}

	selectResult, err := g.executeSPARQLSelect(ctx, parsed, bindings, execOptions)
	if err != nil {
		return nil, err
	}
	selectResult.QueryType = parsed.QueryType
	return selectResult, nil
}

func applyOffsetLimit[T any](values []T, offset, limit int) []T {
	if offset > 0 {
		if offset >= len(values) {
			return nil
		}
		values = values[offset:]
	}
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}
	return values
}

func buildSPARQLExecOptions(parsed *sparqlQuery) sparqlExecOptions {
	opts := sparqlExecOptions{}
	if len(parsed.Using) > 0 {
		opts.DefaultGraphs = append(opts.DefaultGraphs, parsed.Using...)
	} else if parsed.With != nil {
		opts.DefaultGraphs = append(opts.DefaultGraphs, *parsed.With)
	}
	if len(parsed.UsingNamed) > 0 {
		opts.NamedGraphs = append(opts.NamedGraphs, parsed.UsingNamed...)
	}
	return opts
}

func (g *GraphStore) executeSPARQLSelect(ctx context.Context, parsed *sparqlQuery, bindings []map[string]RDFTerm, opts sparqlExecOptions) (*SPARQLResult, error) {
	result := &SPARQLResult{}
	isGrouped := len(parsed.GroupBy) > 0 || sparqlQueryUsesGrouping(parsed)

	if !isGrouped {
		if len(parsed.OrderBy) > 0 {
			sortSPARQLBindings(bindings, parsed.OrderBy)
		}
		vars, projected, err := projectSPARQLBindings(parsed, bindings)
		if err != nil {
			return nil, err
		}
		if parsed.Distinct {
			projected = distinctBindings(projected, vars)
		}
		projected = applyOffsetLimit(projected, parsed.Offset, parsed.Limit)
		result.Vars = vars
		result.Bindings = projected
		result.Count = len(projected)
		return result, nil
	}

	groups, err := buildSPARQLGroups(parsed, bindings)
	if err != nil {
		return nil, err
	}
	if len(parsed.Having) > 0 {
		filteredGroups := make([][]map[string]RDFTerm, 0, len(groups))
		for _, group := range groups {
			keep := true
			for _, filter := range parsed.Having {
				ok, err := evalSPARQLFilterGroup(ctx, g, opts, filter, group)
				if err != nil {
					return nil, err
				}
				if !ok {
					keep = false
					break
				}
			}
			if keep {
				filteredGroups = append(filteredGroups, group)
			}
		}
		groups = filteredGroups
	}
	if len(parsed.OrderBy) > 0 {
		sortSPARQLGroups(groups, parsed.OrderBy)
	}
	groups = applyOffsetLimit(groups, parsed.Offset, parsed.Limit)
	vars, projected, err := projectSPARQLGroups(parsed, groups)
	if err != nil {
		return nil, err
	}
	if parsed.Distinct {
		projected = distinctBindings(projected, vars)
	}
	result.Vars = vars
	result.Bindings = projected
	result.Count = len(projected)
	return result, nil
}

func projectSPARQLBindings(parsed *sparqlQuery, bindings []map[string]RDFTerm) ([]string, []map[string]RDFTerm, error) {
	if parsed.SelectAll {
		vars := collectBindingVars(bindings)
		projected := make([]map[string]RDFTerm, 0, len(bindings))
		for _, binding := range bindings {
			row := make(map[string]RDFTerm, len(vars))
			for _, variable := range vars {
				if value, ok := binding[variable]; ok {
					row[variable] = value
				}
			}
			projected = append(projected, row)
		}
		return vars, projected, nil
	}
	vars := make([]string, 0, len(parsed.SelectItems))
	for _, item := range parsed.SelectItems {
		vars = append(vars, item.Alias)
	}
	projected := make([]map[string]RDFTerm, 0, len(bindings))
	for _, binding := range bindings {
		row := make(map[string]RDFTerm, len(parsed.SelectItems))
		for _, item := range parsed.SelectItems {
			value, ok, err := item.Expr.Eval(binding)
			if err != nil {
				return nil, nil, err
			}
			if ok {
				row[item.Alias] = value
			}
		}
		projected = append(projected, row)
	}
	return vars, projected, nil
}

func projectSPARQLGroups(parsed *sparqlQuery, groups [][]map[string]RDFTerm) ([]string, []map[string]RDFTerm, error) {
	if parsed.SelectAll {
		if len(groups) == 0 {
			return nil, nil, nil
		}
		projected := make([]map[string]RDFTerm, 0, len(groups))
		varsSet := make(map[string]struct{})
		for _, group := range groups {
			row := make(map[string]RDFTerm)
			for _, binding := range group {
				for variable, value := range binding {
					row[variable] = value
					varsSet[variable] = struct{}{}
				}
			}
			projected = append(projected, row)
		}
		vars := make([]string, 0, len(varsSet))
		for variable := range varsSet {
			vars = append(vars, variable)
		}
		sort.Strings(vars)
		return vars, projected, nil
	}
	vars := make([]string, 0, len(parsed.SelectItems))
	for _, item := range parsed.SelectItems {
		vars = append(vars, item.Alias)
	}
	projected := make([]map[string]RDFTerm, 0, len(groups))
	for _, group := range groups {
		row := make(map[string]RDFTerm, len(parsed.SelectItems))
		for _, item := range parsed.SelectItems {
			value, ok, err := item.Expr.EvalGroup(group)
			if err != nil {
				return nil, nil, err
			}
			if ok {
				row[item.Alias] = value
			}
		}
		projected = append(projected, row)
	}
	return vars, projected, nil
}

func sparqlQueryUsesGrouping(parsed *sparqlQuery) bool {
	for _, item := range parsed.SelectItems {
		if item.Expr.IsAggregate() {
			return true
		}
	}
	for _, clause := range parsed.OrderBy {
		if clause.Expr.IsAggregate() {
			return true
		}
	}
	for _, filter := range parsed.Having {
		if filterUsesAggregate(filter) {
			return true
		}
	}
	return false
}

func filterUsesAggregate(filter sparqlFilter) bool {
	switch f := filter.(type) {
	case sparqlCompareFilter:
		return f.Left.IsAggregate() || f.Right.IsAggregate()
	case sparqlContainsFilter:
		return f.Haystack.IsAggregate() || f.Needle.IsAggregate()
	case sparqlStrStartsFilter:
		return f.Haystack.IsAggregate() || f.Prefix.IsAggregate()
	case sparqlRegexFilter:
		return f.Value.IsAggregate() || f.Pattern.IsAggregate() || (f.Flags != nil && f.Flags.IsAggregate())
	case sparqlAndFilter:
		return filterUsesAggregate(f.Left) || filterUsesAggregate(f.Right)
	case sparqlOrFilter:
		return filterUsesAggregate(f.Left) || filterUsesAggregate(f.Right)
	case sparqlNotFilter:
		return filterUsesAggregate(f.Inner)
	case sparqlExprFilter:
		return f.Expr.IsAggregate()
	default:
		return false
	}
}

func buildSPARQLGroups(parsed *sparqlQuery, bindings []map[string]RDFTerm) ([][]map[string]RDFTerm, error) {
	if len(parsed.GroupBy) == 0 {
		if len(bindings) == 0 {
			return [][]map[string]RDFTerm{{}}, nil
		}
		return [][]map[string]RDFTerm{bindings}, nil
	}
	groups := make(map[string][]map[string]RDFTerm)
	order := make([]string, 0)
	for _, binding := range bindings {
		var key strings.Builder
		for _, groupKey := range parsed.GroupBy {
			value, ok, err := groupKey.Expr.Eval(binding)
			if err != nil {
				return nil, err
			}
			if !ok {
				key.WriteString(groupKey.Alias)
				key.WriteString("=;")
				continue
			}
			key.WriteString(groupKey.Alias)
			key.WriteByte('=')
			key.WriteString(value.Kind)
			key.WriteByte('|')
			key.WriteString(value.Value)
			key.WriteByte('|')
			key.WriteString(value.Language)
			key.WriteByte('|')
			key.WriteString(value.Datatype)
			key.WriteByte(';')
		}
		groupID := key.String()
		if _, exists := groups[groupID]; !exists {
			order = append(order, groupID)
		}
		groupBinding := cloneBinding(binding)
		for _, groupKey := range parsed.GroupBy {
			if groupKey.Alias == "" {
				continue
			}
			if _, exists := groupBinding[groupKey.Alias]; exists {
				continue
			}
			value, ok, err := groupKey.Expr.Eval(binding)
			if err != nil {
				return nil, err
			}
			if ok {
				groupBinding[groupKey.Alias] = value
			}
		}
		groups[groupID] = append(groups[groupID], groupBinding)
	}
	out := make([][]map[string]RDFTerm, 0, len(order))
	for _, groupID := range order {
		out = append(out, groups[groupID])
	}
	return out, nil
}

func sortSPARQLGroups(groups [][]map[string]RDFTerm, clauses []sparqlOrderClause) {
	sort.SliceStable(groups, func(i, j int) bool {
		for _, clause := range clauses {
			left, leftOK, leftErr := clause.Expr.EvalGroup(groups[i])
			right, rightOK, rightErr := clause.Expr.EvalGroup(groups[j])
			if leftErr != nil || rightErr != nil {
				continue
			}
			if !leftOK && !rightOK {
				continue
			}
			if !leftOK {
				return false
			}
			if !rightOK {
				return true
			}
			cmp := compareRDFTermsForOrder(left, right)
			if cmp == 0 {
				continue
			}
			if clause.Desc {
				return cmp > 0
			}
			return cmp < 0
		}
		return false
	})
}

func (g *GraphStore) executeSPARQLInsertData(ctx context.Context, templates []sparqlPattern) (int, error) {
	triples := materializeTemplateTriples(templates, []map[string]RDFTerm{{}})
	for _, triple := range triples {
		tripleCopy := triple
		if err := g.UpsertTriple(ctx, &tripleCopy); err != nil {
			return 0, err
		}
	}
	return len(triples), nil
}

func (g *GraphStore) executeSPARQLDeleteData(ctx context.Context, templates []sparqlPattern) (int, error) {
	triples := materializeTemplateTriples(templates, []map[string]RDFTerm{{}})
	deleted := 0
	for _, triple := range triples {
		if err := g.DeleteTriple(ctx, triple); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func (g *GraphStore) executeSPARQLDeleteWhere(ctx context.Context, templates []sparqlPattern, bindings []map[string]RDFTerm, defaultGraph *RDFTerm) (int, error) {
	triples := materializeTemplateTriplesWithDefaultGraph(templates, bindings, defaultGraph)
	deleted := 0
	for _, triple := range triples {
		if err := g.DeleteTriple(ctx, triple); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func (g *GraphStore) executeSPARQLModify(ctx context.Context, deletes, inserts []sparqlPattern, bindings []map[string]RDFTerm, defaultGraph *RDFTerm) (int, error) {
	changed := 0
	deleteTriples := materializeTemplateTriplesWithDefaultGraph(deletes, bindings, defaultGraph)
	for _, triple := range deleteTriples {
		if err := g.DeleteTriple(ctx, triple); err != nil {
			return changed, err
		}
		changed++
	}
	insertTriples := materializeTemplateTriplesWithDefaultGraph(inserts, bindings, defaultGraph)
	for _, triple := range insertTriples {
		tripleCopy := triple
		if err := g.UpsertTriple(ctx, &tripleCopy); err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}

func (g *GraphStore) executeSPARQLGroup(ctx context.Context, group sparqlGroup, bindings []map[string]RDFTerm, opts sparqlExecOptions) ([]map[string]RDFTerm, error) {
	current := cloneBindingSlice(bindings)
	for _, rawStep := range group.Steps {
		switch step := rawStep.(type) {
		case sparqlPatternStep:
			nextBindings, err := g.executeSPARQLPattern(ctx, step.Pattern, current, opts)
			if err != nil {
				return nil, err
			}
			current = nextBindings
		case sparqlFilterStep:
			nextBindings := make([]map[string]RDFTerm, 0, len(current))
			for _, binding := range current {
				keep, err := evalSPARQLFilter(ctx, g, opts, step.Filter, binding)
				if err != nil {
					return nil, err
				}
				if keep {
					nextBindings = append(nextBindings, binding)
				}
			}
			current = nextBindings
		case sparqlOptionalStep:
			nextBindings := make([]map[string]RDFTerm, 0, len(current))
			for _, binding := range current {
				matches, err := g.executeSPARQLGroup(ctx, step.Group, []map[string]RDFTerm{binding}, opts)
				if err != nil {
					return nil, err
				}
				if len(matches) == 0 {
					nextBindings = append(nextBindings, cloneBinding(binding))
					continue
				}
				nextBindings = append(nextBindings, matches...)
			}
			current = nextBindings
		case sparqlUnionStep:
			nextBindings := make([]map[string]RDFTerm, 0)
			for _, branch := range step.Branches {
				branchBindings, err := g.executeSPARQLGroup(ctx, branch, current, opts)
				if err != nil {
					return nil, err
				}
				nextBindings = append(nextBindings, branchBindings...)
			}
			current = nextBindings
		case sparqlGroupStep:
			nextBindings, err := g.executeSPARQLGroup(ctx, step.Group, current, opts)
			if err != nil {
				return nil, err
			}
			current = nextBindings
		case sparqlSubQueryStep:
			subBindings, err := g.executeSPARQLGroup(ctx, step.Query.Group, []map[string]RDFTerm{{}}, opts)
			if err != nil {
				return nil, err
			}
			subResult, err := g.executeSPARQLSelect(ctx, step.Query, subBindings, opts)
			if err != nil {
				return nil, err
			}

			nextBindings := make([]map[string]RDFTerm, 0, len(current)*len(subResult.Bindings))
			for _, outer := range current {
				for _, inner := range subResult.Bindings {
					if merged, ok := mergeValueRow(outer, inner); ok {
						nextBindings = append(nextBindings, merged)
					}
				}
			}
			current = nextBindings
		case sparqlMinusStep:
			minusBindings, err := g.executeSPARQLGroup(ctx, step.Group, []map[string]RDFTerm{{}}, opts)
			if err != nil {
				return nil, err
			}
			nextBindings := make([]map[string]RDFTerm, 0, len(current))
			for _, binding := range current {
				remove := false
				for _, minusBinding := range minusBindings {
					if bindingsCompatibleAndShared(binding, minusBinding) {
						remove = true
						break
					}
				}
				if !remove {
					nextBindings = append(nextBindings, binding)
				}
			}
			current = nextBindings
		case sparqlValuesStep:
			nextBindings := make([]map[string]RDFTerm, 0)
			for _, binding := range current {
				for _, row := range step.Rows {
					merged, ok := mergeValueRow(binding, row)
					if ok {
						nextBindings = append(nextBindings, merged)
					}
				}
			}
			current = nextBindings
		case sparqlBindStep:
			nextBindings := make([]map[string]RDFTerm, 0, len(current))
			for _, binding := range current {
				value, ok, err := step.Expr.Eval(binding)
				if err != nil {
					return nil, err
				}
				if !ok {
					continue
				}
				merged := cloneBinding(binding)
				if existing, exists := merged[step.Variable]; exists && !termsEqual(existing, value) {
					continue
				}
				merged[step.Variable] = value
				nextBindings = append(nextBindings, merged)
			}
			current = nextBindings
		default:
			return nil, fmt.Errorf("unsupported sparql step type %T", rawStep)
		}
		if len(current) == 0 {
			break
		}
	}
	return current, nil
}

func (g *GraphStore) executeSPARQLPattern(ctx context.Context, pattern sparqlPattern, bindings []map[string]RDFTerm, opts sparqlExecOptions) ([]map[string]RDFTerm, error) {
	nextBindings := make([]map[string]RDFTerm, 0)
	for _, binding := range bindings {
		if pattern.Path != nil {
			matches, err := g.findSPARQLPathMatches(ctx, pattern, binding, opts)
			if err != nil {
				return nil, err
			}
			for _, match := range matches {
				merged, ok := unifyPathBinding(binding, pattern, match)
				if ok {
					nextBindings = append(nextBindings, merged)
				}
			}
			continue
		}
		triples, err := g.findSPARQLPatternTriples(ctx, pattern, binding, opts)
		if err != nil {
			return nil, err
		}
		for _, triple := range triples {
			if !sparqlTripleAllowedForGraph(pattern, triple, opts) {
				continue
			}
			merged, ok := unifyBinding(binding, pattern, triple)
			if ok {
				nextBindings = append(nextBindings, merged)
			}
		}
	}
	return nextBindings, nil
}

func sparqlTripleAllowedForGraph(pattern sparqlPattern, triple RDFTriple, opts sparqlExecOptions) bool {
	if pattern.Graph == nil && triple.Graph != nil && len(opts.DefaultGraphs) == 0 {
		return false
	}
	if pattern.Graph != nil && triple.Graph == nil {
		return false
	}
	if pattern.Graph != nil && len(opts.NamedGraphs) > 0 && (triple.Graph == nil || !containsTerm(opts.NamedGraphs, *triple.Graph)) {
		return false
	}
	return true
}

type sparqlPathMatch struct {
	Subject RDFTerm
	Object  RDFTerm
	Graph   *RDFTerm
}

func (g *GraphStore) findSPARQLPatternTriples(ctx context.Context, pattern sparqlPattern, binding map[string]RDFTerm, opts sparqlExecOptions) ([]RDFTriple, error) {
	basePattern, err := resolveTriplePattern(pattern, binding)
	if err != nil {
		return nil, err
	}
	if pattern.Graph != nil || len(opts.DefaultGraphs) == 0 {
		return g.FindTriples(ctx, basePattern)
	}
	out := make([]RDFTriple, 0)
	for _, defaultGraph := range opts.DefaultGraphs {
		graphCopy := defaultGraph
		patternWithGraph := basePattern
		patternWithGraph.Graph = &graphCopy
		triples, err := g.FindTriples(ctx, patternWithGraph)
		if err != nil {
			return nil, err
		}
		out = append(out, triples...)
	}
	return out, nil
}

func (g *GraphStore) findSPARQLPathMatches(ctx context.Context, pattern sparqlPattern, binding map[string]RDFTerm, opts sparqlExecOptions) ([]sparqlPathMatch, error) {
	if pattern.Path == nil || len(pattern.Path.Terms) == 0 {
		return nil, nil
	}

	switch pattern.Path.Kind {
	case sparqlPathInverse:
		triples, err := g.findSPARQLPathTriples(ctx, pattern, binding, opts, pattern.Path.Terms[0], true)
		if err != nil {
			return nil, err
		}
		return buildSPARQLPathMatchesFromTriples(filterSPARQLPathTriples(pattern, triples, opts), true), nil
	case sparqlPathAlternative:
		all := make([]sparqlPathMatch, 0)
		seen := make(map[string]struct{})
		for _, predicate := range pattern.Path.Terms {
			triples, err := g.findSPARQLPathTriples(ctx, pattern, binding, opts, predicate, false)
			if err != nil {
				return nil, err
			}
			for _, match := range buildSPARQLPathMatchesFromTriples(filterSPARQLPathTriples(pattern, triples, opts), false) {
				key := sparqlPathMatchKey(match)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				all = append(all, match)
			}
		}
		return all, nil
	case sparqlPathZeroOrMore, sparqlPathOneOrMore:
		return g.findSPARQLRepeatedPathMatches(ctx, pattern, binding, opts, pattern.Path.Terms[0], pattern.Path.Kind == sparqlPathZeroOrMore)
	default:
		return nil, fmt.Errorf("unsupported property path kind: %s", pattern.Path.Kind)
	}
}

func (g *GraphStore) findSPARQLPathTriples(ctx context.Context, pattern sparqlPattern, binding map[string]RDFTerm, opts sparqlExecOptions, predicate RDFTerm, inverse bool) ([]RDFTriple, error) {
	subjectPattern := pattern.Subject
	objectPattern := pattern.Object
	if inverse {
		subjectPattern, objectPattern = objectPattern, subjectPattern
	}
	basePattern := sparqlPattern{
		Subject:   subjectPattern,
		Predicate: sparqlTermPattern{Term: &predicate},
		Object:    objectPattern,
		Graph:     pattern.Graph,
	}
	return g.findSPARQLPatternTriples(ctx, basePattern, binding, opts)
}

func buildSPARQLPathMatchesFromTriples(triples []RDFTriple, inverse bool) []sparqlPathMatch {
	matches := make([]sparqlPathMatch, 0, len(triples))
	for _, triple := range triples {
		match := sparqlPathMatch{
			Subject: triple.Subject,
			Object:  triple.Object,
			Graph:   cloneGraphTerm(triple.Graph),
		}
		if inverse {
			match.Subject, match.Object = match.Object, match.Subject
		}
		matches = append(matches, match)
	}
	return matches
}

func filterSPARQLPathTriples(pattern sparqlPattern, triples []RDFTriple, opts sparqlExecOptions) []RDFTriple {
	out := make([]RDFTriple, 0, len(triples))
	for _, triple := range triples {
		if sparqlTripleAllowedForGraph(pattern, triple, opts) {
			out = append(out, triple)
		}
	}
	return out
}

func (g *GraphStore) findSPARQLRepeatedPathMatches(ctx context.Context, pattern sparqlPattern, binding map[string]RDFTerm, opts sparqlExecOptions, predicate RDFTerm, includeZero bool) ([]sparqlPathMatch, error) {
	triples, err := g.findSPARQLPathTriples(ctx, sparqlPattern{
		Graph: pattern.Graph,
	}, binding, opts, predicate, false)
	if err != nil {
		return nil, err
	}
	graphAdj := make(map[string]map[string][]RDFTerm)
	graphReverse := make(map[string]map[string][]RDFTerm)
	graphTerms := make(map[string]*RDFTerm)
	nodesByGraph := make(map[string]map[string]RDFTerm)
	for _, triple := range triples {
		if !sparqlTripleAllowedForGraph(pattern, triple, opts) {
			continue
		}
		graphKey := sparqlGraphKey(triple.Graph)
		if _, ok := graphAdj[graphKey]; !ok {
			graphAdj[graphKey] = make(map[string][]RDFTerm)
			graphReverse[graphKey] = make(map[string][]RDFTerm)
			nodesByGraph[graphKey] = make(map[string]RDFTerm)
			graphTerms[graphKey] = cloneGraphTerm(triple.Graph)
		}
		subjectKey := inferenceTermKey(triple.Subject)
		objectKey := inferenceTermKey(triple.Object)
		graphAdj[graphKey][subjectKey] = append(graphAdj[graphKey][subjectKey], triple.Object)
		graphReverse[graphKey][objectKey] = append(graphReverse[graphKey][objectKey], triple.Subject)
		nodesByGraph[graphKey][subjectKey] = triple.Subject
		nodesByGraph[graphKey][objectKey] = triple.Object
	}

	subjectTerm, err := resolvePatternTerm(pattern.Subject, binding)
	if err != nil {
		return nil, err
	}
	objectTerm, err := resolvePatternTerm(pattern.Object, binding)
	if err != nil {
		return nil, err
	}
	graphTerm, err := resolveOptionalPatternTerm(pattern.Graph, binding)
	if err != nil {
		return nil, err
	}

	matches := make([]sparqlPathMatch, 0)
	seen := make(map[string]struct{})
	for graphKey, adj := range graphAdj {
		if graphTerm != nil {
			if graphKey != sparqlGraphKey(graphTerm) {
				continue
			}
		}
		nodes := nodesByGraph[graphKey]
		if objectTerm != nil && subjectTerm == nil {
			sources := collectRepeatedPathSources(graphReverse[graphKey], *objectTerm, includeZero)
			for _, source := range sources {
				match := sparqlPathMatch{Subject: source, Object: *objectTerm, Graph: cloneGraphTerm(graphTerms[graphKey])}
				key := sparqlPathMatchKey(match)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				matches = append(matches, match)
			}
			continue
		}
		starts := collectPathStartTerms(subjectTerm, nodes)
		for _, start := range starts {
			if _, ok := nodes[inferenceTermKey(start)]; !ok {
				if includeZero && (objectTerm == nil || termsEqual(start, *objectTerm)) {
					match := sparqlPathMatch{Subject: start, Object: start, Graph: cloneGraphTerm(graphTerms[graphKey])}
					key := sparqlPathMatchKey(match)
					if _, exists := seen[key]; !exists {
						seen[key] = struct{}{}
						matches = append(matches, match)
					}
				}
				continue
			}
			ends := collectRepeatedPathTargets(adj, start, includeZero)
			for _, end := range ends {
				if objectTerm != nil && !termsEqual(end, *objectTerm) {
					continue
				}
				match := sparqlPathMatch{Subject: start, Object: end, Graph: cloneGraphTerm(graphTerms[graphKey])}
				key := sparqlPathMatchKey(match)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				matches = append(matches, match)
			}
		}
	}
	return matches, nil
}

func resolveOptionalPatternTerm(pattern *sparqlTermPattern, binding map[string]RDFTerm) (*RDFTerm, error) {
	if pattern == nil {
		return nil, nil
	}
	return resolvePatternTerm(*pattern, binding)
}

func collectPathStartTerms(subjectTerm *RDFTerm, nodes map[string]RDFTerm) []RDFTerm {
	if subjectTerm != nil {
		return []RDFTerm{*subjectTerm}
	}
	out := make([]RDFTerm, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, node)
	}
	return out
}

func collectRepeatedPathTargets(adj map[string][]RDFTerm, start RDFTerm, includeZero bool) []RDFTerm {
	out := make([]RDFTerm, 0)
	seen := make(map[string]struct{})
	queue := make([]RDFTerm, 0)
	if includeZero {
		key := inferenceTermKey(start)
		seen[key] = struct{}{}
		out = append(out, start)
	}
	for _, next := range adj[inferenceTermKey(start)] {
		key := inferenceTermKey(next)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		queue = append(queue, next)
		out = append(out, next)
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range adj[inferenceTermKey(current)] {
			key := inferenceTermKey(next)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			queue = append(queue, next)
			out = append(out, next)
		}
	}
	return out
}

func collectRepeatedPathSources(reverse map[string][]RDFTerm, target RDFTerm, includeZero bool) []RDFTerm {
	out := make([]RDFTerm, 0)
	seen := make(map[string]struct{})
	queue := make([]RDFTerm, 0)
	if includeZero {
		key := inferenceTermKey(target)
		seen[key] = struct{}{}
		out = append(out, target)
	}
	for _, prev := range reverse[inferenceTermKey(target)] {
		key := inferenceTermKey(prev)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		queue = append(queue, prev)
		out = append(out, prev)
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, prev := range reverse[inferenceTermKey(current)] {
			key := inferenceTermKey(prev)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			queue = append(queue, prev)
			out = append(out, prev)
		}
	}
	return out
}

func sparqlPathMatchKey(match sparqlPathMatch) string {
	return inferenceTermKey(match.Subject) + "->" + inferenceTermKey(match.Object) + "@" + sparqlGraphKey(match.Graph)
}

func sparqlGraphKey(graph *RDFTerm) string {
	if graph == nil {
		return ""
	}
	return inferenceTermKey(*graph)
}

func resolveTriplePattern(pattern sparqlPattern, binding map[string]RDFTerm) (TriplePattern, error) {
	out := TriplePattern{}

	subject, err := resolvePatternTerm(pattern.Subject, binding)
	if err != nil {
		return TriplePattern{}, err
	}
	out.Subject = subject

	predicate, err := resolvePatternTerm(pattern.Predicate, binding)
	if err != nil {
		return TriplePattern{}, err
	}
	out.Predicate = predicate

	object, err := resolvePatternTerm(pattern.Object, binding)
	if err != nil {
		return TriplePattern{}, err
	}
	out.Object = object

	if pattern.Graph != nil {
		graphTerm, err := resolvePatternTerm(*pattern.Graph, binding)
		if err != nil {
			return TriplePattern{}, err
		}
		out.Graph = graphTerm
	}

	return out, nil
}

func resolvePatternTerm(pattern sparqlTermPattern, binding map[string]RDFTerm) (*RDFTerm, error) {
	if pattern.Term != nil {
		term := *pattern.Term
		return &term, nil
	}
	if pattern.Variable == "" {
		return nil, nil
	}
	if value, ok := binding[pattern.Variable]; ok {
		valueCopy := value
		return &valueCopy, nil
	}
	return nil, nil
}

func unifyBinding(binding map[string]RDFTerm, pattern sparqlPattern, triple RDFTriple) (map[string]RDFTerm, bool) {
	merged := cloneBinding(binding)
	if !bindPatternTerm(merged, pattern.Subject, triple.Subject) {
		return nil, false
	}
	if !bindPatternTerm(merged, pattern.Predicate, triple.Predicate) {
		return nil, false
	}
	if !bindPatternTerm(merged, pattern.Object, triple.Object) {
		return nil, false
	}
	if pattern.Graph != nil {
		graphTerm := RDFTerm{}
		if triple.Graph != nil {
			graphTerm = *triple.Graph
		}
		if !bindPatternTerm(merged, *pattern.Graph, graphTerm) {
			return nil, false
		}
	}
	return merged, true
}

func unifyPathBinding(binding map[string]RDFTerm, pattern sparqlPattern, match sparqlPathMatch) (map[string]RDFTerm, bool) {
	merged := cloneBinding(binding)
	if !bindPatternTerm(merged, pattern.Subject, match.Subject) {
		return nil, false
	}
	if !bindPatternTerm(merged, pattern.Object, match.Object) {
		return nil, false
	}
	if pattern.Graph == nil {
		return merged, true
	}
	if match.Graph == nil {
		return nil, false
	}
	if !bindPatternTerm(merged, *pattern.Graph, *match.Graph) {
		return nil, false
	}
	return merged, true
}

func bindPatternTerm(binding map[string]RDFTerm, pattern sparqlTermPattern, value RDFTerm) bool {
	if pattern.Term != nil {
		return termsEqual(*pattern.Term, value)
	}
	if pattern.Variable == "" {
		return true
	}
	if existing, ok := binding[pattern.Variable]; ok {
		return termsEqual(existing, value)
	}
	binding[pattern.Variable] = value
	return true
}

func termsEqual(a, b RDFTerm) bool {
	return a.Kind == b.Kind &&
		a.Value == b.Value &&
		a.Datatype == b.Datatype &&
		a.Language == b.Language
}

func containsTerm(terms []RDFTerm, value RDFTerm) bool {
	for _, term := range terms {
		if termsEqual(term, value) {
			return true
		}
	}
	return false
}

func cloneBinding(binding map[string]RDFTerm) map[string]RDFTerm {
	out := make(map[string]RDFTerm, len(binding))
	for key, value := range binding {
		out[key] = value
	}
	return out
}

func mergeValueRow(binding map[string]RDFTerm, row map[string]RDFTerm) (map[string]RDFTerm, bool) {
	merged := cloneBinding(binding)
	for variable, value := range row {
		if existing, ok := merged[variable]; ok && !termsEqual(existing, value) {
			return nil, false
		}
		merged[variable] = value
	}
	return merged, true
}

func bindingsCompatibleAndShared(left, right map[string]RDFTerm) bool {
	shared := false
	for key, leftValue := range left {
		rightValue, ok := right[key]
		if !ok {
			continue
		}
		shared = true
		if !termsEqual(leftValue, rightValue) {
			return false
		}
	}
	return shared
}

func cloneBindingSlice(bindings []map[string]RDFTerm) []map[string]RDFTerm {
	out := make([]map[string]RDFTerm, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, cloneBinding(binding))
	}
	return out
}

func collectBindingVars(bindings []map[string]RDFTerm) []string {
	set := make(map[string]struct{})
	for _, binding := range bindings {
		for variable := range binding {
			set[variable] = struct{}{}
		}
	}
	vars := make([]string, 0, len(set))
	for variable := range set {
		vars = append(vars, variable)
	}
	sort.Strings(vars)
	return vars
}

func distinctBindings(bindings []map[string]RDFTerm, vars []string) []map[string]RDFTerm {
	seen := make(map[string]struct{}, len(bindings))
	out := make([]map[string]RDFTerm, 0, len(bindings))
	for _, binding := range bindings {
		var key strings.Builder
		for _, variable := range vars {
			key.WriteString(variable)
			key.WriteByte('=')
			if value, ok := binding[variable]; ok {
				key.WriteString(value.Kind)
				key.WriteByte('|')
				key.WriteString(value.Value)
				key.WriteByte('|')
				key.WriteString(value.Language)
				key.WriteByte('|')
				key.WriteString(value.Datatype)
			}
			key.WriteByte(';')
		}
		if _, ok := seen[key.String()]; ok {
			continue
		}
		seen[key.String()] = struct{}{}
		out = append(out, binding)
	}
	return out
}

func materializeConstructTriples(templates []sparqlPattern, bindings []map[string]RDFTerm) []RDFTriple {
	return materializeTemplateTriples(templates, bindings)
}

func constructBoundTerm(pattern sparqlTermPattern, binding map[string]RDFTerm) (RDFTerm, bool) {
	if pattern.Term != nil {
		return *pattern.Term, true
	}
	if pattern.Variable == "" {
		return RDFTerm{}, false
	}
	value, ok := binding[pattern.Variable]
	return value, ok
}

func materializeTemplateTriples(templates []sparqlPattern, bindings []map[string]RDFTerm) []RDFTriple {
	return materializeTemplateTriplesWithDefaultGraph(templates, bindings, nil)
}

func materializeTemplateTriplesWithDefaultGraph(templates []sparqlPattern, bindings []map[string]RDFTerm, defaultGraph *RDFTerm) []RDFTriple {
	out := make([]RDFTriple, 0)
	seen := make(map[string]struct{})
	for _, binding := range bindings {
		for _, template := range templates {
			subject, ok := constructBoundTerm(template.Subject, binding)
			if !ok || (subject.Kind != RDFTermIRI && subject.Kind != RDFTermBlankNode) {
				continue
			}
			predicate, ok := constructBoundTerm(template.Predicate, binding)
			if !ok || predicate.Kind != RDFTermIRI {
				continue
			}
			object, ok := constructBoundTerm(template.Object, binding)
			if !ok {
				continue
			}
			triple := RDFTriple{
				Subject:   subject,
				Predicate: predicate,
				Object:    object,
			}
			if template.Graph != nil {
				graphTerm, ok := constructBoundTerm(*template.Graph, binding)
				if !ok || (graphTerm.Kind != RDFTermIRI && graphTerm.Kind != RDFTermBlankNode) {
					continue
				}
				graphCopy := graphTerm
				triple.Graph = &graphCopy
			} else if defaultGraph != nil {
				graphCopy := *defaultGraph
				triple.Graph = &graphCopy
			}
			key := triple.String()
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, triple)
		}
	}
	return out
}

func (g *GraphStore) materializeDescribeTriples(ctx context.Context, describes []sparqlTermPattern, bindings []map[string]RDFTerm) ([]RDFTriple, error) {
	targets := make([]RDFTerm, 0)
	for _, describe := range describes {
		if describe.Term != nil {
			targets = append(targets, *describe.Term)
			continue
		}
		for _, binding := range bindings {
			if value, ok := binding[describe.Variable]; ok {
				targets = append(targets, value)
			}
		}
	}

	seenTargets := make(map[string]struct{})
	uniqueTargets := make([]RDFTerm, 0, len(targets))
	for _, target := range targets {
		if target.Kind != RDFTermIRI && target.Kind != RDFTermBlankNode {
			continue
		}
		key := target.String()
		if _, ok := seenTargets[key]; ok {
			continue
		}
		seenTargets[key] = struct{}{}
		uniqueTargets = append(uniqueTargets, target)
	}

	seenTriples := make(map[string]struct{})
	out := make([]RDFTriple, 0)
	for _, target := range uniqueTargets {
		outgoing, err := g.FindTriples(ctx, TriplePattern{Subject: &target})
		if err != nil {
			return nil, err
		}
		for _, triple := range outgoing {
			key := triple.String()
			if _, ok := seenTriples[key]; ok {
				continue
			}
			seenTriples[key] = struct{}{}
			out = append(out, triple)
		}
		incoming, err := g.FindTriples(ctx, TriplePattern{Object: &target})
		if err != nil {
			return nil, err
		}
		for _, triple := range incoming {
			key := triple.String()
			if _, ok := seenTriples[key]; ok {
				continue
			}
			seenTriples[key] = struct{}{}
			out = append(out, triple)
		}
	}
	return out, nil
}

func sortSPARQLBindings(bindings []map[string]RDFTerm, clauses []sparqlOrderClause) {
	sort.SliceStable(bindings, func(i, j int) bool {
		for _, clause := range clauses {
			left, leftOK, leftErr := clause.Expr.Eval(bindings[i])
			right, rightOK, rightErr := clause.Expr.Eval(bindings[j])
			if leftErr != nil || rightErr != nil {
				continue
			}
			if !leftOK && !rightOK {
				continue
			}
			if !leftOK {
				return false
			}
			if !rightOK {
				return true
			}
			cmp := compareRDFTermsForOrder(left, right)
			if cmp == 0 {
				continue
			}
			if clause.Desc {
				return cmp > 0
			}
			return cmp < 0
		}
		return false
	})
}

func compareRDFTermsForOrder(left, right RDFTerm) int {
	leftKey := left.Kind + "\x00" + left.Value + "\x00" + left.Language + "\x00" + left.Datatype
	rightKey := right.Kind + "\x00" + right.Value + "\x00" + right.Language + "\x00" + right.Datatype
	switch {
	case leftKey < rightKey:
		return -1
	case leftKey > rightKey:
		return 1
	default:
		return 0
	}
}

func compareRDFTerms(left, right RDFTerm) (int, error) {
	leftNumber, leftIsNumber := rdfNumericValue(left)
	rightNumber, rightIsNumber := rdfNumericValue(right)
	if leftIsNumber && rightIsNumber {
		switch {
		case leftNumber < rightNumber:
			return -1, nil
		case leftNumber > rightNumber:
			return 1, nil
		default:
			return 0, nil
		}
	}
	return compareRDFTermsForOrder(left, right), nil
}

func rdfNumericValue(term RDFTerm) (float64, bool) {
	if term.Kind != RDFTermLiteral {
		return 0, false
	}
	if term.Datatype != "" &&
		term.Datatype != builtinNamespaces["xsd"]+"integer" &&
		term.Datatype != builtinNamespaces["xsd"]+"decimal" &&
		term.Datatype != builtinNamespaces["xsd"]+"double" &&
		term.Datatype != builtinNamespaces["xsd"]+"float" {
		return 0, false
	}
	value, err := strconv.ParseFloat(term.Value, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func evalArithmeticTerms(op string, left, right RDFTerm) (RDFTerm, bool, error) {
	leftNumber, leftOK := rdfNumericValue(left)
	rightNumber, rightOK := rdfNumericValue(right)
	if !leftOK || !rightOK {
		return RDFTerm{}, false, fmt.Errorf("arithmetic operator %s requires numeric literals", op)
	}
	var result float64
	switch op {
	case "+":
		result = leftNumber + rightNumber
	case "-":
		result = leftNumber - rightNumber
	case "*":
		result = leftNumber * rightNumber
	case "/":
		if rightNumber == 0 {
			return RDFTerm{}, false, fmt.Errorf("division by zero")
		}
		result = leftNumber / rightNumber
	default:
		return RDFTerm{}, false, fmt.Errorf("unsupported arithmetic operator: %s", op)
	}
	return NewTypedLiteral(strconv.FormatFloat(result, 'f', -1, 64), builtinNamespaces["xsd"]+"decimal"), true, nil
}

func effectiveBooleanValue(term RDFTerm) (bool, error) {
	switch term.Kind {
	case RDFTermLiteral:
		if term.Datatype == builtinNamespaces["xsd"]+"boolean" {
			return strings.EqualFold(term.Value, "true") || term.Value == "1", nil
		}
		if value, ok := rdfNumericValue(term); ok {
			return value != 0, nil
		}
		return term.Value != "", nil
	case RDFTermIRI, RDFTermBlankNode:
		return term.Value != "", nil
	default:
		return false, nil
	}
}

func evalSPARQLFilter(ctx context.Context, store *GraphStore, opts sparqlExecOptions, filter sparqlFilter, binding map[string]RDFTerm) (bool, error) {
	if runtimeFilter, ok := filter.(sparqlRuntimeFilter); ok {
		return runtimeFilter.EvalRuntime(ctx, store, opts, binding)
	}
	return filter.Eval(binding)
}

func evalSPARQLFilterGroup(ctx context.Context, store *GraphStore, opts sparqlExecOptions, filter sparqlFilter, bindings []map[string]RDFTerm) (bool, error) {
	if runtimeFilter, ok := filter.(sparqlRuntimeFilter); ok {
		return runtimeFilter.EvalGroupRuntime(ctx, store, opts, bindings)
	}
	return filter.EvalGroup(bindings)
}

func (f sparqlBoundFilter) Eval(binding map[string]RDFTerm) (bool, error) {
	_, ok := binding[f.Variable]
	return ok, nil
}

func (f sparqlBoundFilter) EvalGroup(bindings []map[string]RDFTerm) (bool, error) {
	if len(bindings) == 0 {
		return false, nil
	}
	return f.Eval(bindings[0])
}

func (f sparqlCompareFilter) Eval(binding map[string]RDFTerm) (bool, error) {
	left, leftOK, err := f.Left.Eval(binding)
	if err != nil {
		return false, err
	}
	right, rightOK, err := f.Right.Eval(binding)
	if err != nil {
		return false, err
	}
	if !leftOK || !rightOK {
		return false, nil
	}
	switch f.Op {
	case "=":
		return termsEqual(left, right), nil
	case "!=":
		return !termsEqual(left, right), nil
	case "<", "<=", ">", ">=":
		cmp, err := compareRDFTerms(left, right)
		if err != nil {
			return false, err
		}
		switch f.Op {
		case "<":
			return cmp < 0, nil
		case "<=":
			return cmp <= 0, nil
		case ">":
			return cmp > 0, nil
		case ">=":
			return cmp >= 0, nil
		}
	default:
		return false, fmt.Errorf("unsupported filter operator: %s", f.Op)
	}
	return false, fmt.Errorf("unsupported filter operator: %s", f.Op)
}

func (f sparqlCompareFilter) EvalGroup(bindings []map[string]RDFTerm) (bool, error) {
	left, leftOK, err := f.Left.EvalGroup(bindings)
	if err != nil {
		return false, err
	}
	right, rightOK, err := f.Right.EvalGroup(bindings)
	if err != nil {
		return false, err
	}
	if !leftOK || !rightOK {
		return false, nil
	}
	switch f.Op {
	case "=":
		return termsEqual(left, right), nil
	case "!=":
		return !termsEqual(left, right), nil
	case "<", "<=", ">", ">=":
		cmp, err := compareRDFTerms(left, right)
		if err != nil {
			return false, err
		}
		switch f.Op {
		case "<":
			return cmp < 0, nil
		case "<=":
			return cmp <= 0, nil
		case ">":
			return cmp > 0, nil
		case ">=":
			return cmp >= 0, nil
		}
	}
	return false, fmt.Errorf("unsupported filter operator: %s", f.Op)
}

func (f sparqlContainsFilter) Eval(binding map[string]RDFTerm) (bool, error) {
	haystack, haystackOK, err := f.Haystack.Eval(binding)
	if err != nil {
		return false, err
	}
	needle, needleOK, err := f.Needle.Eval(binding)
	if err != nil {
		return false, err
	}
	if !haystackOK || !needleOK {
		return false, nil
	}
	return strings.Contains(haystack.Value, needle.Value), nil
}

func (f sparqlContainsFilter) EvalGroup(bindings []map[string]RDFTerm) (bool, error) {
	haystack, haystackOK, err := f.Haystack.EvalGroup(bindings)
	if err != nil {
		return false, err
	}
	needle, needleOK, err := f.Needle.EvalGroup(bindings)
	if err != nil {
		return false, err
	}
	if !haystackOK || !needleOK {
		return false, nil
	}
	return strings.Contains(haystack.Value, needle.Value), nil
}

func (f sparqlStrStartsFilter) Eval(binding map[string]RDFTerm) (bool, error) {
	haystack, haystackOK, err := f.Haystack.Eval(binding)
	if err != nil {
		return false, err
	}
	prefix, prefixOK, err := f.Prefix.Eval(binding)
	if err != nil {
		return false, err
	}
	if !haystackOK || !prefixOK {
		return false, nil
	}
	return strings.HasPrefix(haystack.Value, prefix.Value), nil
}

func (f sparqlStrStartsFilter) EvalGroup(bindings []map[string]RDFTerm) (bool, error) {
	haystack, haystackOK, err := f.Haystack.EvalGroup(bindings)
	if err != nil {
		return false, err
	}
	prefix, prefixOK, err := f.Prefix.EvalGroup(bindings)
	if err != nil {
		return false, err
	}
	if !haystackOK || !prefixOK {
		return false, nil
	}
	return strings.HasPrefix(haystack.Value, prefix.Value), nil
}

func (f sparqlRegexFilter) Eval(binding map[string]RDFTerm) (bool, error) {
	value, valueOK, err := f.Value.Eval(binding)
	if err != nil {
		return false, err
	}
	pattern, patternOK, err := f.Pattern.Eval(binding)
	if err != nil {
		return false, err
	}
	if !valueOK || !patternOK {
		return false, nil
	}

	flags := ""
	if f.Flags != nil {
		flagValue, flagOK, err := f.Flags.Eval(binding)
		if err != nil {
			return false, err
		}
		if flagOK {
			flags = flagValue.Value
		}
	}

	expr := pattern.Value
	if strings.Contains(flags, "i") {
		expr = "(?i)" + expr
	}
	matched, err := regexp.MatchString(expr, value.Value)
	if err != nil {
		return false, err
	}
	return matched, nil
}

func (f sparqlRegexFilter) EvalGroup(bindings []map[string]RDFTerm) (bool, error) {
	value, valueOK, err := f.Value.EvalGroup(bindings)
	if err != nil {
		return false, err
	}
	pattern, patternOK, err := f.Pattern.EvalGroup(bindings)
	if err != nil {
		return false, err
	}
	if !valueOK || !patternOK {
		return false, nil
	}
	flags := ""
	if f.Flags != nil {
		flagValue, flagOK, err := f.Flags.EvalGroup(bindings)
		if err != nil {
			return false, err
		}
		if flagOK {
			flags = flagValue.Value
		}
	}
	expr := pattern.Value
	if strings.Contains(flags, "i") {
		expr = "(?i)" + expr
	}
	matched, err := regexp.MatchString(expr, value.Value)
	if err != nil {
		return false, err
	}
	return matched, nil
}

func (f sparqlAndFilter) Eval(binding map[string]RDFTerm) (bool, error) {
	left, err := f.Left.Eval(binding)
	if err != nil || !left {
		return left, err
	}
	return f.Right.Eval(binding)
}

func (f sparqlAndFilter) EvalGroup(bindings []map[string]RDFTerm) (bool, error) {
	left, err := f.Left.EvalGroup(bindings)
	if err != nil || !left {
		return left, err
	}
	return f.Right.EvalGroup(bindings)
}

func (f sparqlOrFilter) Eval(binding map[string]RDFTerm) (bool, error) {
	left, err := f.Left.Eval(binding)
	if err != nil {
		return false, err
	}
	if left {
		return true, nil
	}
	return f.Right.Eval(binding)
}

func (f sparqlOrFilter) EvalGroup(bindings []map[string]RDFTerm) (bool, error) {
	left, err := f.Left.EvalGroup(bindings)
	if err != nil {
		return false, err
	}
	if left {
		return true, nil
	}
	return f.Right.EvalGroup(bindings)
}

func (f sparqlNotFilter) Eval(binding map[string]RDFTerm) (bool, error) {
	value, err := f.Inner.Eval(binding)
	if err != nil {
		return false, err
	}
	return !value, nil
}

func (f sparqlNotFilter) EvalGroup(bindings []map[string]RDFTerm) (bool, error) {
	value, err := f.Inner.EvalGroup(bindings)
	if err != nil {
		return false, err
	}
	return !value, nil
}

func (f sparqlExprFilter) Eval(binding map[string]RDFTerm) (bool, error) {
	value, ok, err := f.Expr.Eval(binding)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return effectiveBooleanValue(value)
}

func (f sparqlExprFilter) EvalGroup(bindings []map[string]RDFTerm) (bool, error) {
	value, ok, err := f.Expr.EvalGroup(bindings)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return effectiveBooleanValue(value)
}

func (f sparqlExistsFilter) Eval(_ map[string]RDFTerm) (bool, error) {
	return false, fmt.Errorf("EXISTS/NOT EXISTS requires SPARQL execution context")
}

func (f sparqlExistsFilter) EvalGroup(_ []map[string]RDFTerm) (bool, error) {
	return false, fmt.Errorf("EXISTS/NOT EXISTS requires SPARQL execution context")
}

func (f sparqlExistsFilter) EvalRuntime(ctx context.Context, store *GraphStore, opts sparqlExecOptions, binding map[string]RDFTerm) (bool, error) {
	matches, err := store.executeSPARQLGroup(ctx, f.Group, []map[string]RDFTerm{cloneBinding(binding)}, opts)
	if err != nil {
		return false, err
	}
	ok := len(matches) > 0
	if f.Negated {
		ok = !ok
	}
	return ok, nil
}

func (f sparqlExistsFilter) EvalGroupRuntime(ctx context.Context, store *GraphStore, opts sparqlExecOptions, bindings []map[string]RDFTerm) (bool, error) {
	if len(bindings) == 0 {
		return false, nil
	}
	return f.EvalRuntime(ctx, store, opts, bindings[0])
}

func (e sparqlVarExpr) Eval(binding map[string]RDFTerm) (RDFTerm, bool, error) {
	value, ok := binding[e.Variable]
	return value, ok, nil
}

func (e sparqlVarExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	if len(bindings) == 0 {
		return RDFTerm{}, false, nil
	}
	return e.Eval(bindings[0])
}

func (e sparqlVarExpr) IsAggregate() bool { return false }

func (e sparqlLiteralExpr) Eval(_ map[string]RDFTerm) (RDFTerm, bool, error) {
	return e.Term, true, nil
}

func (e sparqlLiteralExpr) EvalGroup(_ []map[string]RDFTerm) (RDFTerm, bool, error) {
	return e.Term, true, nil
}

func (e sparqlLiteralExpr) IsAggregate() bool { return false }

func (e sparqlStrFuncExpr) Eval(binding map[string]RDFTerm) (RDFTerm, bool, error) {
	value, ok, err := e.Inner.Eval(binding)
	if err != nil || !ok {
		return RDFTerm{}, ok, err
	}
	return NewLiteral(value.Value), true, nil
}

func (e sparqlStrFuncExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	value, ok, err := e.Inner.EvalGroup(bindings)
	if err != nil || !ok {
		return RDFTerm{}, ok, err
	}
	return NewLiteral(value.Value), true, nil
}

func (e sparqlStrFuncExpr) IsAggregate() bool { return e.Inner.IsAggregate() }

func (e sparqlLCaseFuncExpr) Eval(binding map[string]RDFTerm) (RDFTerm, bool, error) {
	value, ok, err := e.Inner.Eval(binding)
	if err != nil || !ok {
		return RDFTerm{}, ok, err
	}
	return NewLiteral(strings.ToLower(value.Value)), true, nil
}

func (e sparqlLCaseFuncExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	value, ok, err := e.Inner.EvalGroup(bindings)
	if err != nil || !ok {
		return RDFTerm{}, ok, err
	}
	return NewLiteral(strings.ToLower(value.Value)), true, nil
}

func (e sparqlLCaseFuncExpr) IsAggregate() bool { return e.Inner.IsAggregate() }

func (e sparqlLangFuncExpr) Eval(binding map[string]RDFTerm) (RDFTerm, bool, error) {
	value, ok, err := e.Inner.Eval(binding)
	if err != nil || !ok {
		return RDFTerm{}, ok, err
	}
	return NewLiteral(value.Language), true, nil
}

func (e sparqlLangFuncExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	value, ok, err := e.Inner.EvalGroup(bindings)
	if err != nil || !ok {
		return RDFTerm{}, ok, err
	}
	return NewLiteral(value.Language), true, nil
}

func (e sparqlLangFuncExpr) IsAggregate() bool { return e.Inner.IsAggregate() }

func (e sparqlDatatypeFuncExpr) Eval(binding map[string]RDFTerm) (RDFTerm, bool, error) {
	value, ok, err := e.Inner.Eval(binding)
	if err != nil || !ok {
		return RDFTerm{}, ok, err
	}
	if value.Kind != RDFTermLiteral || value.Datatype == "" {
		return RDFTerm{}, false, nil
	}
	return NewIRI(value.Datatype), true, nil
}

func (e sparqlDatatypeFuncExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	value, ok, err := e.Inner.EvalGroup(bindings)
	if err != nil || !ok {
		return RDFTerm{}, ok, err
	}
	if value.Kind != RDFTermLiteral || value.Datatype == "" {
		return RDFTerm{}, false, nil
	}
	return NewIRI(value.Datatype), true, nil
}

func (e sparqlDatatypeFuncExpr) IsAggregate() bool { return e.Inner.IsAggregate() }

func (e sparqlCountFuncExpr) Eval(_ map[string]RDFTerm) (RDFTerm, bool, error) {
	return RDFTerm{}, false, fmt.Errorf("COUNT cannot be evaluated outside a group")
}

func (e sparqlCountFuncExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	if e.Wildcard {
		return NewTypedLiteral(strconv.Itoa(len(bindings)), builtinNamespaces["xsd"]+"integer"), true, nil
	}
	if e.Inner == nil {
		return NewTypedLiteral("0", builtinNamespaces["xsd"]+"integer"), true, nil
	}
	if !e.Distinct {
		count := 0
		for _, binding := range bindings {
			if _, ok, err := e.Inner.Eval(binding); err != nil {
				return RDFTerm{}, false, err
			} else if ok {
				count++
			}
		}
		return NewTypedLiteral(strconv.Itoa(count), builtinNamespaces["xsd"]+"integer"), true, nil
	}
	seen := make(map[string]struct{})
	for _, binding := range bindings {
		value, ok, err := e.Inner.Eval(binding)
		if err != nil {
			return RDFTerm{}, false, err
		}
		if !ok {
			continue
		}
		key := value.Kind + "|" + value.Value + "|" + value.Language + "|" + value.Datatype
		seen[key] = struct{}{}
	}
	return NewTypedLiteral(strconv.Itoa(len(seen)), builtinNamespaces["xsd"]+"integer"), true, nil
}

func (e sparqlCountFuncExpr) IsAggregate() bool { return true }

func (e sparqlUnaryNumericExpr) Eval(binding map[string]RDFTerm) (RDFTerm, bool, error) {
	value, ok, err := e.Inner.Eval(binding)
	if err != nil || !ok {
		return RDFTerm{}, ok, err
	}
	number, ok := rdfNumericValue(value)
	if !ok {
		return RDFTerm{}, false, fmt.Errorf("numeric operator %s requires numeric value", e.Op)
	}
	if e.Op == "-" {
		number = -number
	}
	return NewTypedLiteral(strconv.FormatFloat(number, 'f', -1, 64), builtinNamespaces["xsd"]+"decimal"), true, nil
}

func (e sparqlUnaryNumericExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	value, ok, err := e.Inner.EvalGroup(bindings)
	if err != nil || !ok {
		return RDFTerm{}, ok, err
	}
	number, ok := rdfNumericValue(value)
	if !ok {
		return RDFTerm{}, false, fmt.Errorf("numeric operator %s requires numeric value", e.Op)
	}
	if e.Op == "-" {
		number = -number
	}
	return NewTypedLiteral(strconv.FormatFloat(number, 'f', -1, 64), builtinNamespaces["xsd"]+"decimal"), true, nil
}

func (e sparqlUnaryNumericExpr) IsAggregate() bool { return e.Inner.IsAggregate() }

func (e sparqlArithmeticExpr) Eval(binding map[string]RDFTerm) (RDFTerm, bool, error) {
	left, leftOK, err := e.Left.Eval(binding)
	if err != nil || !leftOK {
		return RDFTerm{}, leftOK, err
	}
	right, rightOK, err := e.Right.Eval(binding)
	if err != nil || !rightOK {
		return RDFTerm{}, rightOK, err
	}
	return evalArithmeticTerms(e.Op, left, right)
}

func (e sparqlArithmeticExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	left, leftOK, err := e.Left.EvalGroup(bindings)
	if err != nil || !leftOK {
		return RDFTerm{}, leftOK, err
	}
	right, rightOK, err := e.Right.EvalGroup(bindings)
	if err != nil || !rightOK {
		return RDFTerm{}, rightOK, err
	}
	return evalArithmeticTerms(e.Op, left, right)
}

func (e sparqlArithmeticExpr) IsAggregate() bool {
	return e.Left.IsAggregate() || e.Right.IsAggregate()
}

func (e sparqlCoalesceFuncExpr) Eval(binding map[string]RDFTerm) (RDFTerm, bool, error) {
	for _, arg := range e.Args {
		value, ok, err := arg.Eval(binding)
		if err != nil {
			return RDFTerm{}, false, err
		}
		if ok {
			return value, true, nil
		}
	}
	return RDFTerm{}, false, nil
}

func (e sparqlCoalesceFuncExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	for _, arg := range e.Args {
		value, ok, err := arg.EvalGroup(bindings)
		if err != nil {
			return RDFTerm{}, false, err
		}
		if ok {
			return value, true, nil
		}
	}
	return RDFTerm{}, false, nil
}

func (e sparqlCoalesceFuncExpr) IsAggregate() bool {
	for _, arg := range e.Args {
		if arg.IsAggregate() {
			return true
		}
	}
	return false
}

func (e sparqlIfFuncExpr) Eval(binding map[string]RDFTerm) (RDFTerm, bool, error) {
	cond, err := e.Cond.Eval(binding)
	if err != nil {
		return RDFTerm{}, false, err
	}
	if cond {
		return e.Then.Eval(binding)
	}
	return e.Else.Eval(binding)
}

func (e sparqlIfFuncExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	cond, err := e.Cond.EvalGroup(bindings)
	if err != nil {
		return RDFTerm{}, false, err
	}
	if cond {
		return e.Then.EvalGroup(bindings)
	}
	return e.Else.EvalGroup(bindings)
}

func (e sparqlIfFuncExpr) IsAggregate() bool {
	return e.Then.IsAggregate() || e.Else.IsAggregate() || filterUsesAggregate(e.Cond)
}

func (e sparqlAggregateFuncExpr) Eval(_ map[string]RDFTerm) (RDFTerm, bool, error) {
	return RDFTerm{}, false, fmt.Errorf("%s cannot be evaluated outside a group", strings.ToUpper(e.Name))
}

func (e sparqlAggregateFuncExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	values := make([]RDFTerm, 0, len(bindings))
	seen := make(map[string]struct{})
	for _, binding := range bindings {
		value, ok, err := e.Inner.Eval(binding)
		if err != nil {
			return RDFTerm{}, false, err
		}
		if !ok {
			continue
		}
		if e.Distinct {
			key := value.Kind + "|" + value.Value + "|" + value.Language + "|" + value.Datatype
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
		}
		values = append(values, value)
	}

	switch strings.ToUpper(e.Name) {
	case "SUM":
		total := 0.0
		for _, value := range values {
			n, ok := rdfNumericValue(value)
			if !ok {
				return RDFTerm{}, false, fmt.Errorf("SUM requires numeric literals")
			}
			total += n
		}
		return NewTypedLiteral(strconv.FormatFloat(total, 'f', -1, 64), builtinNamespaces["xsd"]+"decimal"), true, nil
	case "AVG":
		if len(values) == 0 {
			return RDFTerm{}, false, nil
		}
		total := 0.0
		for _, value := range values {
			n, ok := rdfNumericValue(value)
			if !ok {
				return RDFTerm{}, false, fmt.Errorf("AVG requires numeric literals")
			}
			total += n
		}
		avg := total / float64(len(values))
		return NewTypedLiteral(strconv.FormatFloat(avg, 'f', -1, 64), builtinNamespaces["xsd"]+"decimal"), true, nil
	case "MIN":
		if len(values) == 0 {
			return RDFTerm{}, false, nil
		}
		minValue := values[0]
		for _, value := range values[1:] {
			if cmp, err := compareRDFTerms(value, minValue); err == nil && cmp < 0 {
				minValue = value
			}
		}
		return minValue, true, nil
	case "MAX":
		if len(values) == 0 {
			return RDFTerm{}, false, nil
		}
		maxValue := values[0]
		for _, value := range values[1:] {
			if cmp, err := compareRDFTerms(value, maxValue); err == nil && cmp > 0 {
				maxValue = value
			}
		}
		return maxValue, true, nil
	case "SAMPLE":
		if len(values) == 0 {
			return RDFTerm{}, false, nil
		}
		return values[0], true, nil
	case "GROUP_CONCAT":
		if len(values) == 0 {
			return NewLiteral(""), true, nil
		}
		separator := e.Separator
		if separator == "" {
			separator = " "
		}
		parts := make([]string, 0, len(values))
		for _, value := range values {
			parts = append(parts, value.Value)
		}
		return NewLiteral(strings.Join(parts, separator)), true, nil
	default:
		return RDFTerm{}, false, fmt.Errorf("unsupported aggregate function: %s", e.Name)
	}
}

func (e sparqlAggregateFuncExpr) IsAggregate() bool { return true }

func (g *GraphStore) parseSPARQL(ctx context.Context, query string) (*sparqlQuery, error) {
	namespaces, err := g.ListNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	prefixes := make(map[string]string, len(namespaces))
	for _, ns := range namespaces {
		prefixes[ns.Prefix] = ns.URI
	}

	parser := newSPARQLParser(query, prefixes)
	return parser.parse()
}

type sparqlParser struct {
	tokens   []sparqlToken
	position int
	prefixes map[string]string
}

type sparqlTokenType string

const (
	sparqlTokenKeyword  sparqlTokenType = "keyword"
	sparqlTokenVar      sparqlTokenType = "var"
	sparqlTokenIRI      sparqlTokenType = "iri"
	sparqlTokenString   sparqlTokenType = "string"
	sparqlTokenNumber   sparqlTokenType = "number"
	sparqlTokenBoolean  sparqlTokenType = "boolean"
	sparqlTokenPunct    sparqlTokenType = "punct"
	sparqlTokenOperator sparqlTokenType = "operator"
	sparqlTokenQName    sparqlTokenType = "qname"
	sparqlTokenIdent    sparqlTokenType = "ident"
	sparqlTokenEOF      sparqlTokenType = "eof"
)

type sparqlToken struct {
	Type  sparqlTokenType
	Value string
}

func newSPARQLParser(query string, prefixes map[string]string) *sparqlParser {
	return &sparqlParser{
		tokens:   tokenizeSPARQL(query),
		prefixes: prefixes,
	}
}

func (p *sparqlParser) parse() (query *sparqlQuery, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok {
				err = recoveredErr
				return
			}
			err = fmt.Errorf("invalid SPARQL query")
		}
	}()

	query = &sparqlQuery{
		Prefixes: clonePrefixes(p.prefixes),
	}

	for p.matchKeyword("PREFIX") {
		prefixToken := p.expectType(sparqlTokenQName, "prefix label")
		iriToken := p.expectType(sparqlTokenIRI, "prefix iri")
		prefix := strings.TrimSuffix(prefixToken.Value, ":")
		query.Prefixes[prefix] = iriToken.Value
	}
	if p.matchKeyword("WITH") {
		withTerm, err := p.parseGraphResourceTerm(query.Prefixes)
		if err != nil {
			return nil, err
		}
		query.With = &withTerm
	}

	switch {
	case p.matchKeyword("SELECT"):
		err := p.parseSelectQueryBody(query)
		if err != nil {
			return nil, err
		}
		if p.peek().Type != sparqlTokenEOF {
			return nil, fmt.Errorf("unexpected trailing token %q", p.peek().Value)
		}
		return query, nil
	case p.matchKeyword("CONSTRUCT"):
		query.QueryType = SPARQLQueryConstruct
		template, err := p.parseConstructTemplate(query.Prefixes)
		if err != nil {
			return nil, err
		}
		query.Template = template
	case p.matchKeyword("DESCRIBE"):
		query.QueryType = SPARQLQueryDescribe
		for p.peek().Type == sparqlTokenVar || p.peek().Type == sparqlTokenIRI || p.peek().Type == sparqlTokenQName {
			describe, err := p.parseTermPattern(query.Prefixes, false)
			if err != nil {
				return nil, err
			}
			query.Describe = append(query.Describe, describe)
		}
		if len(query.Describe) == 0 {
			return nil, fmt.Errorf("DESCRIBE requires at least one iri or variable")
		}
	case p.matchKeyword("ASK"):
		query.QueryType = SPARQLQueryAsk
	case p.matchKeyword("INSERT"):
		if p.matchKeyword("DATA") {
			query.QueryType = SPARQLQueryInsertData
			templateGroup, err := p.parseEnclosedGroup(nil, query.Prefixes)
			if err != nil {
				return nil, err
			}
			template, err := flattenTemplatePatterns(templateGroup)
			if err != nil {
				return nil, err
			}
			query.Template = template
			break
		}
		query.QueryType = SPARQLQueryModify
		insertGroup, err := p.parseEnclosedGroup(nil, query.Prefixes)
		if err != nil {
			return nil, err
		}
		insertPatterns, err := flattenTemplatePatterns(insertGroup)
		if err != nil {
			return nil, err
		}
		query.Insert = insertPatterns
	case p.matchKeyword("DELETE"):
		switch {
		case p.matchKeyword("DATA"):
			query.QueryType = SPARQLQueryDeleteData
			templateGroup, err := p.parseEnclosedGroup(nil, query.Prefixes)
			if err != nil {
				return nil, err
			}
			template, err := flattenTemplatePatterns(templateGroup)
			if err != nil {
				return nil, err
			}
			query.Template = template
		case p.matchKeyword("WHERE"):
			query.QueryType = SPARQLQueryDeleteWhere
			group, err := p.parseEnclosedGroup(nil, query.Prefixes)
			if err != nil {
				return nil, err
			}
			query.Group = group
			template, err := flattenTemplatePatterns(group)
			if err != nil {
				return nil, err
			}
			query.Template = template
		default:
			query.QueryType = SPARQLQueryModify
			deleteGroup, err := p.parseEnclosedGroup(nil, query.Prefixes)
			if err != nil {
				return nil, err
			}
			deletePatterns, err := flattenTemplatePatterns(deleteGroup)
			if err != nil {
				return nil, err
			}
			query.Delete = deletePatterns
			if p.matchKeyword("INSERT") {
				insertGroup, err := p.parseEnclosedGroup(nil, query.Prefixes)
				if err != nil {
					return nil, err
				}
				insertPatterns, err := flattenTemplatePatterns(insertGroup)
				if err != nil {
					return nil, err
				}
				query.Insert = insertPatterns
			}
		}
	default:
		return nil, fmt.Errorf("expected SELECT, CONSTRUCT, DESCRIBE, ASK, INSERT DATA, or DELETE")
	}

	if query.QueryType == SPARQLQueryModify {
		for p.matchKeyword("USING") {
			if p.matchKeyword("NAMED") {
				namedTerm, err := p.parseGraphResourceTerm(query.Prefixes)
				if err != nil {
					return nil, err
				}
				query.UsingNamed = append(query.UsingNamed, namedTerm)
				continue
			}
			usingTerm, err := p.parseGraphResourceTerm(query.Prefixes)
			if err != nil {
				return nil, err
			}
			query.Using = append(query.Using, usingTerm)
		}
	}

	if p.matchKeyword("WHERE") {
		// explicit WHERE is optional after SELECT/ASK
	}
	if query.QueryType != SPARQLQueryInsertData &&
		query.QueryType != SPARQLQueryDeleteData &&
		query.QueryType != SPARQLQueryDeleteWhere &&
		p.peek().Type == sparqlTokenPunct && p.peek().Value == "{" {
		group, err := p.parseEnclosedGroup(nil, query.Prefixes)
		if err != nil {
			return nil, err
		}
		query.Group = group
	}
	if query.QueryType == SPARQLQueryModify && len(query.Group.Steps) == 0 {
		if p.matchKeyword("WHERE") {
			// optional keyword for modify forms
		}
		group, err := p.parseEnclosedGroup(nil, query.Prefixes)
		if err != nil {
			return nil, err
		}
		query.Group = group
	}

	p.parseSolutionModifiers(query)

	if p.peek().Type != sparqlTokenEOF {
		return nil, fmt.Errorf("unexpected trailing token %q", p.peek().Value)
	}
	return query, nil
}

func (p *sparqlParser) parseSelectQueryBody(query *sparqlQuery) error {
	query.QueryType = SPARQLQuerySelect
	if p.matchKeyword("DISTINCT") {
		query.Distinct = true
	}
	if p.matchOperator("*") {
		query.SelectAll = true
	} else {
		for p.peek().Type == sparqlTokenVar || (p.peek().Type == sparqlTokenPunct && p.peek().Value == "(") {
			item, err := p.parseSelectItem(query.Prefixes)
			if err != nil {
				return err
			}
			query.SelectItems = append(query.SelectItems, item)
			query.Vars = append(query.Vars, item.Alias)
		}
		if len(query.SelectItems) == 0 {
			return fmt.Errorf("SELECT requires variables, expressions, or *")
		}
	}

	if p.matchKeyword("WHERE") {
		// optional
	}
	group, err := p.parseEnclosedGroup(nil, query.Prefixes)
	if err != nil {
		return err
	}
	query.Group = group

	p.parseSolutionModifiers(query)
	return nil
}

func (p *sparqlParser) parseSolutionModifiers(query *sparqlQuery) {
	if p.matchKeyword("GROUP") {
		if !p.matchKeyword("BY") {
			panic(fmt.Errorf("expected BY after GROUP"))
		}
		for {
			if p.peek().Type == sparqlTokenEOF || p.peek().Value == "}" {
				break
			}
			if p.peek().Type == sparqlTokenKeyword &&
				(strings.EqualFold(p.peek().Value, "HAVING") ||
					strings.EqualFold(p.peek().Value, "ORDER") ||
					strings.EqualFold(p.peek().Value, "LIMIT") ||
					strings.EqualFold(p.peek().Value, "OFFSET")) {
				break
			}
			groupKey, err := p.parseGroupKey(query.Prefixes)
			if err != nil {
				panic(err)
			}
			query.GroupBy = append(query.GroupBy, groupKey)
		}
	}

	if p.matchKeyword("HAVING") {
		for {
			filter, err := p.parseFilter(nil, query.Prefixes)
			if err != nil {
				panic(err)
			}
			query.Having = append(query.Having, filter)
			if p.peek().Type == sparqlTokenKeyword &&
				(strings.EqualFold(p.peek().Value, "ORDER") ||
					strings.EqualFold(p.peek().Value, "LIMIT") ||
					strings.EqualFold(p.peek().Value, "OFFSET")) {
				break
			}
			if p.peek().Type == sparqlTokenEOF || p.peek().Value == "}" {
				break
			}
		}
	}

	if p.matchKeyword("ORDER") {
		if !p.matchKeyword("BY") {
			panic(fmt.Errorf("expected BY after ORDER"))
		}
		for {
			if p.peek().Type == sparqlTokenEOF || p.peek().Value == "}" {
				break
			}
			if p.peek().Type == sparqlTokenKeyword &&
				(strings.EqualFold(p.peek().Value, "LIMIT") || strings.EqualFold(p.peek().Value, "OFFSET")) {
				break
			}
			clause, err := p.parseOrderClause(query.Prefixes)
			if err != nil {
				panic(err)
			}
			query.OrderBy = append(query.OrderBy, clause)
		}
	}

	for {
		switch {
		case p.matchKeyword("LIMIT"):
			limitToken := p.expectType(sparqlTokenNumber, "limit value")
			limit, err := strconv.Atoi(limitToken.Value)
			if err != nil {
				panic(fmt.Errorf("invalid LIMIT value %q", limitToken.Value))
			}
			query.Limit = limit
		case p.matchKeyword("OFFSET"):
			offsetToken := p.expectType(sparqlTokenNumber, "offset value")
			offset, err := strconv.Atoi(offsetToken.Value)
			if err != nil {
				panic(fmt.Errorf("invalid OFFSET value %q", offsetToken.Value))
			}
			query.Offset = offset
		default:
			return
		}
	}
}

func (p *sparqlParser) parseSelectItem(prefixes map[string]string) (sparqlSelectItem, error) {
	if p.peek().Type == sparqlTokenVar {
		variable := strings.TrimPrefix(p.next().Value, "?")
		return sparqlSelectItem{
			Alias: variable,
			Expr:  sparqlVarExpr{Variable: variable},
		}, nil
	}
	p.expectPunct("(")
	expr, err := p.parseValueExpr(prefixes)
	if err != nil {
		return sparqlSelectItem{}, err
	}
	if !p.matchKeyword("AS") {
		return sparqlSelectItem{}, fmt.Errorf("SELECT expression requires AS")
	}
	alias := strings.TrimPrefix(p.expectType(sparqlTokenVar, "select alias").Value, "?")
	p.expectPunct(")")
	return sparqlSelectItem{Alias: alias, Expr: expr}, nil
}

func (p *sparqlParser) parseGroupKey(prefixes map[string]string) (sparqlGroupKey, error) {
	if p.peek().Type == sparqlTokenVar {
		variable := strings.TrimPrefix(p.next().Value, "?")
		return sparqlGroupKey{
			Alias: variable,
			Expr:  sparqlVarExpr{Variable: variable},
		}, nil
	}
	p.expectPunct("(")
	expr, err := p.parseValueExpr(prefixes)
	if err != nil {
		return sparqlGroupKey{}, err
	}
	alias := ""
	if p.matchKeyword("AS") {
		alias = strings.TrimPrefix(p.expectType(sparqlTokenVar, "group alias").Value, "?")
	}
	p.expectPunct(")")
	return sparqlGroupKey{Alias: alias, Expr: expr}, nil
}

func (p *sparqlParser) parseGraphResourceTerm(prefixes map[string]string) (RDFTerm, error) {
	term, err := p.parseTermPattern(prefixes, false)
	if err != nil {
		return RDFTerm{}, err
	}
	if term.Term == nil || (term.Term.Kind != RDFTermIRI && term.Term.Kind != RDFTermBlankNode) {
		return RDFTerm{}, fmt.Errorf("expected graph iri or blank node")
	}
	return *term.Term, nil
}

func (p *sparqlParser) parseConstructTemplate(prefixes map[string]string) ([]sparqlPattern, error) {
	group, err := p.parseEnclosedGroup(nil, prefixes)
	if err != nil {
		return nil, err
	}
	templates := make([]sparqlPattern, 0)
	if err := appendTemplatePatterns(&templates, group, false); err != nil {
		return nil, err
	}
	return templates, nil
}

func flattenTemplatePatterns(group sparqlGroup) ([]sparqlPattern, error) {
	out := make([]sparqlPattern, 0)
	if err := appendTemplatePatterns(&out, group, true); err != nil {
		return nil, err
	}
	return out, nil
}

func appendTemplatePatterns(out *[]sparqlPattern, group sparqlGroup, allowGraph bool) error {
	for _, rawStep := range group.Steps {
		switch step := rawStep.(type) {
		case sparqlPatternStep:
			if step.Pattern.Graph != nil && !allowGraph {
				return fmt.Errorf("CONSTRUCT templates do not support GRAPH blocks")
			}
			*out = append(*out, step.Pattern)
		case sparqlGroupStep:
			if err := appendTemplatePatterns(out, step.Group, allowGraph); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported step in template: %T", rawStep)
		}
	}
	return nil
}

func (p *sparqlParser) parseEnclosedGroup(activeGraph *sparqlTermPattern, prefixes map[string]string) (sparqlGroup, error) {
	p.expectPunct("{")
	return p.parseGroupBody(activeGraph, prefixes)
}

func (p *sparqlParser) parseGroupBody(activeGraph *sparqlTermPattern, prefixes map[string]string) (sparqlGroup, error) {
	group := sparqlGroup{Steps: make([]sparqlStep, 0)}
	for {
		if p.matchPunct("}") {
			break
		}
		step, err := p.parseGroupStep(activeGraph, prefixes)
		if err != nil {
			return sparqlGroup{}, err
		}
		group.Steps = append(group.Steps, step)
		p.matchPunct(".")
	}
	return group, nil
}

func (p *sparqlParser) parseGroupStep(activeGraph *sparqlTermPattern, prefixes map[string]string) (sparqlStep, error) {
	if p.matchKeyword("SELECT") {
		subQuery := &sparqlQuery{
			Prefixes: prefixes,
		}
		err := p.parseSelectQueryBody(subQuery)
		if err != nil {
			return nil, err
		}
		return sparqlSubQueryStep{Query: subQuery}, nil
	}
	if p.matchKeyword("FILTER") {
		filter, err := p.parseFilter(activeGraph, prefixes)
		if err != nil {
			return nil, err
		}
		return sparqlFilterStep{Filter: filter}, nil
	}
	if p.matchKeyword("BIND") {
		p.expectPunct("(")
		expr, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		if !p.matchKeyword("AS") {
			return nil, fmt.Errorf("expected AS in BIND")
		}
		variable := strings.TrimPrefix(p.expectType(sparqlTokenVar, "bind variable").Value, "?")
		p.expectPunct(")")
		return sparqlBindStep{Variable: variable, Expr: expr}, nil
	}
	if p.matchKeyword("OPTIONAL") {
		group, err := p.parseEnclosedGroup(activeGraph, prefixes)
		if err != nil {
			return nil, err
		}
		return sparqlOptionalStep{Group: group}, nil
	}
	if p.matchKeyword("GRAPH") {
		graphPattern, err := p.parseTermPattern(prefixes, false)
		if err != nil {
			return nil, err
		}
		group, err := p.parseEnclosedGroup(&graphPattern, prefixes)
		if err != nil {
			return nil, err
		}
		return sparqlGroupStep{Group: group}, nil
	}
	if p.matchKeyword("MINUS") {
		group, err := p.parseEnclosedGroup(activeGraph, prefixes)
		if err != nil {
			return nil, err
		}
		return sparqlMinusStep{Group: group}, nil
	}
	if p.matchKeyword("VALUES") {
		return p.parseValues(prefixes)
	}
	if p.peek().Type == sparqlTokenPunct && p.peek().Value == "{" {
		firstBranch, err := p.parseEnclosedGroup(activeGraph, prefixes)
		if err != nil {
			return nil, err
		}
		if !p.matchKeyword("UNION") {
			return sparqlGroupStep{Group: firstBranch}, nil
		}
		branches := []sparqlGroup{firstBranch}
		for {
			nextBranch, err := p.parseEnclosedGroup(activeGraph, prefixes)
			if err != nil {
				return nil, err
			}
			branches = append(branches, nextBranch)
			if !p.matchKeyword("UNION") {
				break
			}
		}
		return sparqlUnionStep{Branches: branches}, nil
	}

	statementPatterns, err := p.parseTriplePatternStatement(activeGraph, prefixes)
	if err != nil {
		return nil, err
	}
	if len(statementPatterns) == 1 {
		return sparqlPatternStep{Pattern: statementPatterns[0]}, nil
	}
	group := sparqlGroup{Steps: make([]sparqlStep, 0, len(statementPatterns))}
	for _, pattern := range statementPatterns {
		group.Steps = append(group.Steps, sparqlPatternStep{Pattern: pattern})
	}
	return sparqlGroupStep{Group: group}, nil
}

func (p *sparqlParser) parseValues(prefixes map[string]string) (sparqlStep, error) {
	var variables []string
	if p.peek().Type == sparqlTokenVar {
		variables = append(variables, strings.TrimPrefix(p.next().Value, "?"))
	} else {
		p.expectPunct("(")
		for p.peek().Type == sparqlTokenVar {
			variables = append(variables, strings.TrimPrefix(p.next().Value, "?"))
		}
		p.expectPunct(")")
	}
	if len(variables) == 0 {
		return nil, fmt.Errorf("VALUES requires variables")
	}

	p.expectPunct("{")
	rows := make([]map[string]RDFTerm, 0)
	for !p.matchPunct("}") {
		row := make(map[string]RDFTerm, len(variables))
		if len(variables) == 1 {
			term, err := p.parseTermPattern(prefixes, true)
			if err != nil {
				return nil, err
			}
			if term.Term == nil {
				return nil, fmt.Errorf("VALUES rows must contain concrete terms")
			}
			row[variables[0]] = *term.Term
		} else {
			p.expectPunct("(")
			for _, variable := range variables {
				term, err := p.parseTermPattern(prefixes, true)
				if err != nil {
					return nil, err
				}
				if term.Term == nil {
					return nil, fmt.Errorf("VALUES rows must contain concrete terms")
				}
				row[variable] = *term.Term
			}
			p.expectPunct(")")
		}
		rows = append(rows, row)
	}
	return sparqlValuesStep{Variables: variables, Rows: rows}, nil
}

func (p *sparqlParser) parseOrderClause(prefixes map[string]string) (sparqlOrderClause, error) {
	if p.matchKeyword("DESC") {
		p.expectPunct("(")
		expr, err := p.parseValueExpr(prefixes)
		if err != nil {
			return sparqlOrderClause{}, err
		}
		p.expectPunct(")")
		return sparqlOrderClause{Desc: true, Expr: expr}, nil
	}
	if p.matchKeyword("ASC") {
		p.expectPunct("(")
		expr, err := p.parseValueExpr(prefixes)
		if err != nil {
			return sparqlOrderClause{}, err
		}
		p.expectPunct(")")
		return sparqlOrderClause{Expr: expr}, nil
	}
	expr, err := p.parseValueExpr(prefixes)
	if err != nil {
		return sparqlOrderClause{}, err
	}
	return sparqlOrderClause{Expr: expr}, nil
}

func (p *sparqlParser) parsePredicatePattern(prefixes map[string]string) (sparqlTermPattern, *sparqlPropertyPath, error) {
	if p.matchOperator("^") {
		term, err := p.parsePropertyPathTerm(prefixes)
		if err != nil {
			return sparqlTermPattern{}, nil, err
		}
		return sparqlTermPattern{}, &sparqlPropertyPath{Kind: sparqlPathInverse, Terms: []RDFTerm{term}}, nil
	}

	predicate, err := p.parseTermPattern(prefixes, false)
	if err != nil {
		return sparqlTermPattern{}, nil, err
	}

	if p.matchOperator("|") {
		if predicate.Term == nil {
			return sparqlTermPattern{}, nil, fmt.Errorf("property path alternatives require concrete predicates")
		}
		terms := []RDFTerm{*predicate.Term}
		for {
			term, err := p.parsePropertyPathTerm(prefixes)
			if err != nil {
				return sparqlTermPattern{}, nil, err
			}
			terms = append(terms, term)
			if !p.matchOperator("|") {
				break
			}
		}
		return sparqlTermPattern{}, &sparqlPropertyPath{Kind: sparqlPathAlternative, Terms: terms}, nil
	}
	if p.matchOperator("*") {
		if predicate.Term == nil {
			return sparqlTermPattern{}, nil, fmt.Errorf("property path repetition requires a concrete predicate")
		}
		return sparqlTermPattern{}, &sparqlPropertyPath{Kind: sparqlPathZeroOrMore, Terms: []RDFTerm{*predicate.Term}}, nil
	}
	if p.matchOperator("+") {
		if predicate.Term == nil {
			return sparqlTermPattern{}, nil, fmt.Errorf("property path repetition requires a concrete predicate")
		}
		return sparqlTermPattern{}, &sparqlPropertyPath{Kind: sparqlPathOneOrMore, Terms: []RDFTerm{*predicate.Term}}, nil
	}
	return predicate, nil, nil
}

func (p *sparqlParser) parsePropertyPathTerm(prefixes map[string]string) (RDFTerm, error) {
	termPattern, err := p.parseTermPattern(prefixes, false)
	if err != nil {
		return RDFTerm{}, err
	}
	if termPattern.Term == nil || termPattern.Term.Kind != RDFTermIRI {
		return RDFTerm{}, fmt.Errorf("property paths require IRI predicates")
	}
	return *termPattern.Term, nil
}

func (p *sparqlParser) parseTriplePatternStatement(activeGraph *sparqlTermPattern, prefixes map[string]string) ([]sparqlPattern, error) {
	subject, err := p.parseTermPattern(prefixes, false)
	if err != nil {
		return nil, err
	}
	patterns := make([]sparqlPattern, 0)
	for {
		predicate, path, err := p.parsePredicatePattern(prefixes)
		if err != nil {
			return nil, err
		}
		for {
			object, err := p.parseTermPattern(prefixes, true)
			if err != nil {
				return nil, err
			}
			pattern := sparqlPattern{
				Subject:   subject,
				Predicate: predicate,
				Path:      path,
				Object:    object,
			}
			if activeGraph != nil {
				graphCopy := *activeGraph
				pattern.Graph = &graphCopy
			}
			patterns = append(patterns, pattern)
			if !p.matchPunct(",") {
				break
			}
		}
		if !p.matchPunct(";") {
			break
		}
		if p.peek().Value == "." || p.peek().Value == "}" {
			break
		}
	}
	return patterns, nil
}

func (p *sparqlParser) parseFilter(activeGraph *sparqlTermPattern, prefixes map[string]string) (sparqlFilter, error) {
	if isSPARQLExistsFilterStart(p.peek()) || isSPARQLNotExistsFilterStart(p.peek(), p.peekN(1)) {
		return p.parseFilterExpr(activeGraph, prefixes)
	}
	p.expectPunct("(")
	defer p.expectPunct(")")
	return p.parseFilterExpr(activeGraph, prefixes)
}

func (p *sparqlParser) parseFilterExpr(activeGraph *sparqlTermPattern, prefixes map[string]string) (sparqlFilter, error) {
	return p.parseFilterOr(activeGraph, prefixes)
}

func (p *sparqlParser) parseFilterOr(activeGraph *sparqlTermPattern, prefixes map[string]string) (sparqlFilter, error) {
	left, err := p.parseFilterAnd(activeGraph, prefixes)
	if err != nil {
		return nil, err
	}
	for p.matchOperator("||") {
		right, err := p.parseFilterAnd(activeGraph, prefixes)
		if err != nil {
			return nil, err
		}
		left = sparqlOrFilter{Left: left, Right: right}
	}
	return left, nil
}

func (p *sparqlParser) parseFilterAnd(activeGraph *sparqlTermPattern, prefixes map[string]string) (sparqlFilter, error) {
	left, err := p.parseFilterUnary(activeGraph, prefixes)
	if err != nil {
		return nil, err
	}
	for p.matchOperator("&&") {
		right, err := p.parseFilterUnary(activeGraph, prefixes)
		if err != nil {
			return nil, err
		}
		left = sparqlAndFilter{Left: left, Right: right}
	}
	return left, nil
}

func (p *sparqlParser) parseFilterUnary(activeGraph *sparqlTermPattern, prefixes map[string]string) (sparqlFilter, error) {
	if p.matchOperator("!") {
		inner, err := p.parseFilterUnary(activeGraph, prefixes)
		if err != nil {
			return nil, err
		}
		return sparqlNotFilter{Inner: inner}, nil
	}
	if p.matchPunct("(") {
		filter, err := p.parseFilterExpr(activeGraph, prefixes)
		if err != nil {
			return nil, err
		}
		p.expectPunct(")")
		return filter, nil
	}
	return p.parseFilterPrimary(activeGraph, prefixes)
}

func (p *sparqlParser) parseFilterPrimary(activeGraph *sparqlTermPattern, prefixes map[string]string) (sparqlFilter, error) {
	if p.matchKeyword("EXISTS") {
		group, err := p.parseEnclosedGroup(activeGraph, prefixes)
		if err != nil {
			return nil, err
		}
		return sparqlExistsFilter{Group: group}, nil
	}
	if p.matchKeyword("NOT") {
		if !p.matchKeyword("EXISTS") {
			return nil, fmt.Errorf("expected EXISTS after NOT")
		}
		group, err := p.parseEnclosedGroup(activeGraph, prefixes)
		if err != nil {
			return nil, err
		}
		return sparqlExistsFilter{Group: group, Negated: true}, nil
	}
	if p.matchKeyword("BOUND") {
		p.expectPunct("(")
		variable := strings.TrimPrefix(p.expectType(sparqlTokenVar, "variable").Value, "?")
		p.expectPunct(")")
		return sparqlBoundFilter{Variable: variable}, nil
	}
	if p.matchKeyword("REGEX") {
		p.expectPunct("(")
		valueExpr, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		p.expectPunct(",")
		patternExpr, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		var flagsExpr sparqlValueExpr
		if p.matchPunct(",") {
			flagsExpr, err = p.parseValueExpr(prefixes)
			if err != nil {
				return nil, err
			}
		}
		p.expectPunct(")")
		return sparqlRegexFilter{Value: valueExpr, Pattern: patternExpr, Flags: flagsExpr}, nil
	}
	if p.matchKeyword("CONTAINS") {
		p.expectPunct("(")
		haystack, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		p.expectPunct(",")
		needle, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		p.expectPunct(")")
		return sparqlContainsFilter{Haystack: haystack, Needle: needle}, nil
	}
	if p.matchKeyword("STRSTARTS") {
		p.expectPunct("(")
		haystack, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		p.expectPunct(",")
		prefix, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		p.expectPunct(")")
		return sparqlStrStartsFilter{Haystack: haystack, Prefix: prefix}, nil
	}

	left, err := p.parseValueExpr(prefixes)
	if err != nil {
		return nil, err
	}
	if p.matchOperator("=") {
		right, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		return sparqlCompareFilter{Op: "=", Left: left, Right: right}, nil
	}
	if p.matchOperator("!=") {
		right, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		return sparqlCompareFilter{Op: "!=", Left: left, Right: right}, nil
	}
	for _, op := range []string{"<=", ">=", "<", ">"} {
		if p.matchOperator(op) {
			right, err := p.parseValueExpr(prefixes)
			if err != nil {
				return nil, err
			}
			return sparqlCompareFilter{Op: op, Left: left, Right: right}, nil
		}
	}
	return sparqlExprFilter{Expr: left}, nil
}

func (p *sparqlParser) parseValueExpr(prefixes map[string]string) (sparqlValueExpr, error) {
	return p.parseAdditiveExpr(prefixes)
}

func (p *sparqlParser) parseAdditiveExpr(prefixes map[string]string) (sparqlValueExpr, error) {
	left, err := p.parseMultiplicativeExpr(prefixes)
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.matchOperator("+"):
			right, err := p.parseMultiplicativeExpr(prefixes)
			if err != nil {
				return nil, err
			}
			left = sparqlArithmeticExpr{Op: "+", Left: left, Right: right}
		case p.matchOperator("-"):
			right, err := p.parseMultiplicativeExpr(prefixes)
			if err != nil {
				return nil, err
			}
			left = sparqlArithmeticExpr{Op: "-", Left: left, Right: right}
		default:
			return left, nil
		}
	}
}

func (p *sparqlParser) parseMultiplicativeExpr(prefixes map[string]string) (sparqlValueExpr, error) {
	left, err := p.parseUnaryValueExpr(prefixes)
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.matchOperator("*"):
			right, err := p.parseUnaryValueExpr(prefixes)
			if err != nil {
				return nil, err
			}
			left = sparqlArithmeticExpr{Op: "*", Left: left, Right: right}
		case p.matchOperator("/"):
			right, err := p.parseUnaryValueExpr(prefixes)
			if err != nil {
				return nil, err
			}
			left = sparqlArithmeticExpr{Op: "/", Left: left, Right: right}
		default:
			return left, nil
		}
	}
}

func (p *sparqlParser) parseUnaryValueExpr(prefixes map[string]string) (sparqlValueExpr, error) {
	switch {
	case p.matchOperator("+"):
		return p.parseUnaryValueExpr(prefixes)
	case p.matchOperator("-"):
		inner, err := p.parseUnaryValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		return sparqlUnaryNumericExpr{Op: "-", Inner: inner}, nil
	default:
		return p.parsePrimaryValueExpr(prefixes)
	}
}

func (p *sparqlParser) parsePrimaryValueExpr(prefixes map[string]string) (sparqlValueExpr, error) {
	for _, name := range []string{"SUM", "AVG", "MIN", "MAX", "SAMPLE", "GROUP_CONCAT"} {
		if p.matchKeyword(name) {
			p.expectPunct("(")
			agg := sparqlAggregateFuncExpr{Name: name}
			if p.matchKeyword("DISTINCT") {
				agg.Distinct = true
			}
			inner, err := p.parseValueExpr(prefixes)
			if err != nil {
				return nil, err
			}
			agg.Inner = inner
			if strings.EqualFold(name, "GROUP_CONCAT") && p.matchPunct(";") {
				if !p.matchKeyword("SEPARATOR") {
					return nil, fmt.Errorf("expected SEPARATOR in GROUP_CONCAT")
				}
				if !p.matchOperator("=") {
					return nil, fmt.Errorf("expected = after SEPARATOR")
				}
				separatorTerm, err := p.parseTermPattern(prefixes, true)
				if err != nil {
					return nil, err
				}
				if separatorTerm.Term == nil {
					return nil, fmt.Errorf("GROUP_CONCAT separator must be a literal")
				}
				agg.Separator = separatorTerm.Term.Value
			}
			p.expectPunct(")")
			return agg, nil
		}
	}
	if p.matchKeyword("COUNT") {
		p.expectPunct("(")
		countExpr := sparqlCountFuncExpr{}
		if p.matchKeyword("DISTINCT") {
			countExpr.Distinct = true
		}
		if p.matchOperator("*") {
			countExpr.Wildcard = true
		} else {
			inner, err := p.parseValueExpr(prefixes)
			if err != nil {
				return nil, err
			}
			countExpr.Inner = inner
		}
		p.expectPunct(")")
		return countExpr, nil
	}
	if p.matchKeyword("COALESCE") {
		p.expectPunct("(")
		args := make([]sparqlValueExpr, 0)
		for {
			arg, err := p.parseValueExpr(prefixes)
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
			if !p.matchPunct(",") {
				break
			}
		}
		p.expectPunct(")")
		return sparqlCoalesceFuncExpr{Args: args}, nil
	}
	if p.matchKeyword("IF") {
		p.expectPunct("(")
		cond, err := p.parseFilterExpr(nil, prefixes)
		if err != nil {
			return nil, err
		}
		p.expectPunct(",")
		thenExpr, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		p.expectPunct(",")
		elseExpr, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		p.expectPunct(")")
		return sparqlIfFuncExpr{Cond: cond, Then: thenExpr, Else: elseExpr}, nil
	}
	if p.matchKeyword("STR") {
		p.expectPunct("(")
		inner, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		p.expectPunct(")")
		return sparqlStrFuncExpr{Inner: inner}, nil
	}
	if p.matchKeyword("LCASE") {
		p.expectPunct("(")
		inner, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		p.expectPunct(")")
		return sparqlLCaseFuncExpr{Inner: inner}, nil
	}
	if p.matchKeyword("LANG") {
		p.expectPunct("(")
		inner, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		p.expectPunct(")")
		return sparqlLangFuncExpr{Inner: inner}, nil
	}
	if p.matchKeyword("DATATYPE") {
		p.expectPunct("(")
		inner, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		p.expectPunct(")")
		return sparqlDatatypeFuncExpr{Inner: inner}, nil
	}
	if p.matchPunct("(") {
		expr, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		p.expectPunct(")")
		return expr, nil
	}

	termPattern, err := p.parseTermPattern(prefixes, true)
	if err != nil {
		return nil, err
	}
	if termPattern.Variable != "" {
		return sparqlVarExpr{Variable: termPattern.Variable}, nil
	}
	if termPattern.Term == nil {
		return nil, fmt.Errorf("expected value expression")
	}
	return sparqlLiteralExpr{Term: *termPattern.Term}, nil
}

func (p *sparqlParser) parseTermPattern(prefixes map[string]string, allowLiteral bool) (sparqlTermPattern, error) {
	token := p.peek()
	switch token.Type {
	case sparqlTokenVar:
		return sparqlTermPattern{Variable: strings.TrimPrefix(p.next().Value, "?")}, nil
	case sparqlTokenIRI:
		term := NewIRI(p.next().Value)
		return sparqlTermPattern{Term: &term}, nil
	case sparqlTokenQName:
		value := p.next().Value
		expanded, err := expandWithPrefixes(value, prefixes)
		if err != nil {
			return sparqlTermPattern{}, err
		}
		term := NewIRI(expanded)
		return sparqlTermPattern{Term: &term}, nil
	case sparqlTokenIdent:
		value := p.next().Value
		if strings.EqualFold(value, "a") {
			term := NewIRI(builtinNamespaces["rdf"] + "type")
			return sparqlTermPattern{Term: &term}, nil
		}
		if allowLiteral && (strings.EqualFold(value, "true") || strings.EqualFold(value, "false")) {
			term := NewTypedLiteral(strings.ToLower(value), builtinNamespaces["xsd"]+"boolean")
			return sparqlTermPattern{Term: &term}, nil
		}
		return sparqlTermPattern{}, fmt.Errorf("unexpected identifier %q", value)
	case sparqlTokenString:
		if !allowLiteral {
			return sparqlTermPattern{}, fmt.Errorf("literal not allowed in this position")
		}
		literal := NewLiteral(p.next().Value)
		if p.matchOperator("@") {
			lang := p.expectType(sparqlTokenIdent, "language tag")
			literal.Language = strings.ToLower(lang.Value)
			return sparqlTermPattern{Term: &literal}, nil
		}
		if p.matchOperator("^^") {
			datatype, err := p.parseTermPattern(prefixes, false)
			if err != nil {
				return sparqlTermPattern{}, err
			}
			if datatype.Term == nil || datatype.Term.Kind != RDFTermIRI {
				return sparqlTermPattern{}, fmt.Errorf("literal datatype must be an iri")
			}
			literal.Datatype = datatype.Term.Value
		}
		return sparqlTermPattern{Term: &literal}, nil
	case sparqlTokenNumber:
		if !allowLiteral {
			return sparqlTermPattern{}, fmt.Errorf("number literal not allowed in this position")
		}
		number := p.next().Value
		datatype := builtinNamespaces["xsd"] + "integer"
		if strings.Contains(number, ".") {
			datatype = builtinNamespaces["xsd"] + "decimal"
		}
		literal := NewTypedLiteral(number, datatype)
		return sparqlTermPattern{Term: &literal}, nil
	case sparqlTokenBoolean:
		if !allowLiteral {
			return sparqlTermPattern{}, fmt.Errorf("boolean literal not allowed in this position")
		}
		literal := NewTypedLiteral(strings.ToLower(p.next().Value), builtinNamespaces["xsd"]+"boolean")
		return sparqlTermPattern{Term: &literal}, nil
	default:
		return sparqlTermPattern{}, fmt.Errorf("unexpected token %q", token.Value)
	}
}

func expandWithPrefixes(value string, prefixes map[string]string) (string, error) {
	colon := strings.IndexByte(value, ':')
	if colon < 0 {
		return value, nil
	}
	prefix := value[:colon]
	local := value[colon+1:]
	uri, ok := prefixes[prefix]
	if !ok {
		return "", fmt.Errorf("unknown prefix %q", prefix)
	}
	return uri + local, nil
}

func tokenizeSPARQL(query string) []sparqlToken {
	tokens := make([]sparqlToken, 0)
	for i := 0; i < len(query); {
		switch ch := query[i]; {
		case unicode.IsSpace(rune(ch)):
			i++
		case ch == '#':
			for i < len(query) && query[i] != '\n' {
				i++
			}
		case ch == '?':
			j := i + 1
			for j < len(query) && isSPARQLIdentPart(query[j]) {
				j++
			}
			tokens = append(tokens, sparqlToken{Type: sparqlTokenVar, Value: query[i:j]})
			i = j
		case strings.HasPrefix(query[i:], ">="):
			tokens = append(tokens, sparqlToken{Type: sparqlTokenOperator, Value: ">="})
			i += 2
		case strings.HasPrefix(query[i:], "<="):
			tokens = append(tokens, sparqlToken{Type: sparqlTokenOperator, Value: "<="})
			i += 2
		case strings.HasPrefix(query[i:], "&&"):
			tokens = append(tokens, sparqlToken{Type: sparqlTokenOperator, Value: "&&"})
			i += 2
		case strings.HasPrefix(query[i:], "||"):
			tokens = append(tokens, sparqlToken{Type: sparqlTokenOperator, Value: "||"})
			i += 2
		case ch == '|':
			tokens = append(tokens, sparqlToken{Type: sparqlTokenOperator, Value: "|"})
			i++
		case ch == '<' && looksLikeSPARQLIRI(query, i):
			j := i + 1
			for j < len(query) && query[j] != '>' {
				j++
			}
			if j < len(query) {
				tokens = append(tokens, sparqlToken{Type: sparqlTokenIRI, Value: query[i+1 : j]})
				i = j + 1
			} else {
				tokens = append(tokens, sparqlToken{Type: sparqlTokenIRI, Value: query[i+1:]})
				i = len(query)
			}
		case ch == '"':
			value, next := readQuotedString(query, i)
			tokens = append(tokens, sparqlToken{Type: sparqlTokenString, Value: value})
			i = next
		case ch == '<' || ch == '>':
			tokens = append(tokens, sparqlToken{Type: sparqlTokenOperator, Value: string(ch)})
			i++
		case strings.HasPrefix(query[i:], "!="):
			tokens = append(tokens, sparqlToken{Type: sparqlTokenOperator, Value: "!="})
			i += 2
		case strings.HasPrefix(query[i:], "^^"):
			tokens = append(tokens, sparqlToken{Type: sparqlTokenOperator, Value: "^^"})
			i += 2
		case strings.ContainsRune("{}().,;=*", rune(ch)):
			tokenType := sparqlTokenPunct
			if ch == '=' || ch == '*' {
				tokenType = sparqlTokenOperator
			}
			tokens = append(tokens, sparqlToken{Type: tokenType, Value: string(ch)})
			i++
		case ch == '+' || ch == '-' || ch == '/' || ch == '!' || ch == '^':
			tokens = append(tokens, sparqlToken{Type: sparqlTokenOperator, Value: string(ch)})
			i++
		case ch == '@':
			tokens = append(tokens, sparqlToken{Type: sparqlTokenOperator, Value: "@"})
			i++
		case isSPARQLNumberStart(query, i):
			j := i + 1
			for j < len(query) && (unicode.IsDigit(rune(query[j])) || query[j] == '.') {
				j++
			}
			tokens = append(tokens, sparqlToken{Type: sparqlTokenNumber, Value: query[i:j]})
			i = j
		default:
			j := i + 1
			for j < len(query) && isSPARQLWordPart(query[j]) {
				j++
			}
			value := query[i:j]
			switch {
			case strings.Contains(value, ":") && !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://"):
				tokens = append(tokens, sparqlToken{Type: sparqlTokenQName, Value: value})
			case strings.EqualFold(value, "true") || strings.EqualFold(value, "false"):
				tokens = append(tokens, sparqlToken{Type: sparqlTokenBoolean, Value: strings.ToLower(value)})
			case isSPARQLKeyword(value):
				tokens = append(tokens, sparqlToken{Type: sparqlTokenKeyword, Value: value})
			default:
				tokens = append(tokens, sparqlToken{Type: sparqlTokenIdent, Value: value})
			}
			i = j
		}
	}
	tokens = append(tokens, sparqlToken{Type: sparqlTokenEOF, Value: ""})
	return tokens
}

func readQuotedString(input string, start int) (string, int) {
	var builder strings.Builder
	i := start + 1
	for i < len(input) {
		switch input[i] {
		case '\\':
			if i+1 >= len(input) {
				return builder.String(), len(input)
			}
			builder.WriteByte(input[i])
			builder.WriteByte(input[i+1])
			i += 2
		case '"':
			decoded, err := strconv.Unquote(`"` + builder.String() + `"`)
			if err != nil {
				return builder.String(), i + 1
			}
			return decoded, i + 1
		default:
			builder.WriteByte(input[i])
			i++
		}
	}
	return builder.String(), len(input)
}

func isSPARQLIdentPart(ch byte) bool {
	return unicode.IsLetter(rune(ch)) || unicode.IsDigit(rune(ch)) || ch == '_' || ch == '-'
}

func isSPARQLWordPart(ch byte) bool {
	return isSPARQLIdentPart(ch) || ch == ':' || ch == '/' || ch == '#' || ch == '.'
}

func isSPARQLNumberStart(query string, index int) bool {
	if !unicode.IsDigit(rune(query[index])) {
		return false
	}
	return true
}

func looksLikeSPARQLIRI(query string, start int) bool {
	for i := start + 1; i < len(query); i++ {
		switch query[i] {
		case '>':
			return true
		case '\n', '\r', '\t', ' ', '{', '}', '(', ')':
			return false
		}
	}
	return false
}

func isSPARQLKeyword(value string) bool {
	switch {
	case strings.EqualFold(value, "PREFIX"),
		strings.EqualFold(value, "SELECT"),
		strings.EqualFold(value, "CONSTRUCT"),
		strings.EqualFold(value, "DESCRIBE"),
		strings.EqualFold(value, "DISTINCT"),
		strings.EqualFold(value, "ASK"),
		strings.EqualFold(value, "INSERT"),
		strings.EqualFold(value, "DELETE"),
		strings.EqualFold(value, "DATA"),
		strings.EqualFold(value, "WITH"),
		strings.EqualFold(value, "USING"),
		strings.EqualFold(value, "NAMED"),
		strings.EqualFold(value, "WHERE"),
		strings.EqualFold(value, "FILTER"),
		strings.EqualFold(value, "GRAPH"),
		strings.EqualFold(value, "OPTIONAL"),
		strings.EqualFold(value, "EXISTS"),
		strings.EqualFold(value, "NOT"),
		strings.EqualFold(value, "MINUS"),
		strings.EqualFold(value, "UNION"),
		strings.EqualFold(value, "GROUP"),
		strings.EqualFold(value, "HAVING"),
		strings.EqualFold(value, "ORDER"),
		strings.EqualFold(value, "BY"),
		strings.EqualFold(value, "LIMIT"),
		strings.EqualFold(value, "OFFSET"),
		strings.EqualFold(value, "VALUES"),
		strings.EqualFold(value, "BIND"),
		strings.EqualFold(value, "AS"),
		strings.EqualFold(value, "BOUND"),
		strings.EqualFold(value, "SUM"),
		strings.EqualFold(value, "AVG"),
		strings.EqualFold(value, "MIN"),
		strings.EqualFold(value, "MAX"),
		strings.EqualFold(value, "SAMPLE"),
		strings.EqualFold(value, "GROUP_CONCAT"),
		strings.EqualFold(value, "SEPARATOR"),
		strings.EqualFold(value, "COUNT"),
		strings.EqualFold(value, "REGEX"),
		strings.EqualFold(value, "COALESCE"),
		strings.EqualFold(value, "IF"),
		strings.EqualFold(value, "STR"),
		strings.EqualFold(value, "LCASE"),
		strings.EqualFold(value, "LANG"),
		strings.EqualFold(value, "DATATYPE"),
		strings.EqualFold(value, "CONTAINS"),
		strings.EqualFold(value, "STRSTARTS"),
		strings.EqualFold(value, "ASC"),
		strings.EqualFold(value, "DESC"):
		return true
	default:
		return false
	}
}

func clonePrefixes(prefixes map[string]string) map[string]string {
	out := make(map[string]string, len(prefixes))
	for key, value := range prefixes {
		out[key] = value
	}
	return out
}

func (p *sparqlParser) peek() sparqlToken {
	return p.tokens[p.position]
}

func (p *sparqlParser) peekN(offset int) sparqlToken {
	index := p.position + offset
	if index >= len(p.tokens) {
		return sparqlToken{Type: sparqlTokenEOF}
	}
	return p.tokens[index]
}

func (p *sparqlParser) next() sparqlToken {
	token := p.tokens[p.position]
	p.position++
	return token
}

func isSPARQLExistsFilterStart(token sparqlToken) bool {
	return token.Type == sparqlTokenKeyword && strings.EqualFold(token.Value, "EXISTS")
}

func isSPARQLNotExistsFilterStart(first, second sparqlToken) bool {
	return first.Type == sparqlTokenKeyword &&
		second.Type == sparqlTokenKeyword &&
		strings.EqualFold(first.Value, "NOT") &&
		strings.EqualFold(second.Value, "EXISTS")
}

func (p *sparqlParser) matchKeyword(value string) bool {
	token := p.peek()
	if token.Type == sparqlTokenKeyword && strings.EqualFold(token.Value, value) {
		p.position++
		return true
	}
	return false
}

func (p *sparqlParser) matchPunct(value string) bool {
	token := p.peek()
	if token.Type == sparqlTokenPunct && token.Value == value {
		p.position++
		return true
	}
	return false
}

func (p *sparqlParser) matchOperator(value string) bool {
	token := p.peek()
	if token.Type == sparqlTokenOperator && token.Value == value {
		p.position++
		return true
	}
	return false
}

func (p *sparqlParser) expectPunct(value string) {
	if !p.matchPunct(value) {
		panicSPARQLParse(fmt.Errorf("expected %q, got %q", value, p.peek().Value))
	}
}

func (p *sparqlParser) expectType(tokenType sparqlTokenType, label string) sparqlToken {
	token := p.peek()
	if token.Type != tokenType {
		panicSPARQLParse(fmt.Errorf("expected %s, got %q", label, token.Value))
	}
	p.position++
	return token
}

func panicSPARQLParse(err error) {
	panic(err)
}
