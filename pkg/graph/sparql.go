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
	From        []RDFTerm
	FromNamed   []RDFTerm
	// DatasetDeclared records that the query named its dataset with FROM /
	// FROM NAMED, which replaces the engine default entirely (see
	// sparqlExecOptions).
	DatasetDeclared bool
	runtime         *sparqlRuntime
	// Next is the update operation after this one, for a request that
	// chains several with ';'.
	Next *sparqlQuery
}

type sparqlGroup struct {
	Steps []sparqlStep
}

// sparqlExecOptions is the RDF dataset a query is evaluated against.
//
// With DatasetDeclared false this engine uses its own default: patterns
// outside GRAPH match the unnamed graph plus the property-graph projection
// (or the USING/WITH graphs of an update), and GRAPH can bind any named graph
// (or only USING NAMED ones).
// With DatasetDeclared true — a query that said FROM or FROM NAMED — the
// dataset is exactly what it declared, as SPARQL 1.1 §13.2 requires: FROM
// NAMED alone leaves the default graph empty, and FROM alone leaves no named
// graphs for GRAPH to match.
type sparqlExecOptions struct {
	DefaultGraphs   []RDFTerm
	NamedGraphs     []RDFTerm
	DatasetDeclared bool
}

type sparqlStep interface {
	sparqlStep()
}

type sparqlPatternStep struct {
	Pattern sparqlPattern
}

type sparqlFilterStep struct {
	Filter sparqlFilter
	// refs and outOfScope are set by scopeGroup (sparql_scope.go).
	refs, outOfScope []string
}

type sparqlOptionalStep struct {
	Group sparqlGroup
}

type sparqlUnionStep struct {
	Branches []sparqlGroup
}

type sparqlSubQueryStep struct {
	Query *sparqlQuery
	// Graph is the enclosing GRAPH's name. The subquery's patterns match in
	// that graph; when it is a variable they name it by Inner, a variable no
	// query can write, because the subquery's own ?g is a different one.
	Graph *sparqlTermPattern
	Inner string
}

func (sparqlSubQueryStep) sparqlStep() {}

type sparqlGroupStep struct {
	Group sparqlGroup
	// Graph is the graph name of GRAPH ... { }, nil for a plain group.
	Graph *sparqlTermPattern
}

type sparqlMinusStep struct {
	Group sparqlGroup
	// Graph and Inner are as for sparqlSubQueryStep: inside GRAPH ?g the
	// right-hand side matches in ?g's graph without binding ?g, so ?g alone
	// never makes the two sides share a variable.
	Graph *sparqlTermPattern
	Inner string
}

type sparqlValuesStep struct {
	Variables []string
	Rows      []map[string]RDFTerm
}

type sparqlBindStep struct {
	Variable string
	Expr     sparqlValueExpr
	// refs and outOfScope are set by scopeGroup (sparql_scope.go).
	refs, outOfScope []string
	rt               *sparqlRuntime
}

type sparqlPattern struct {
	Subject   sparqlTermPattern
	Predicate sparqlTermPattern
	Path      *sparqlPropertyPath
	Object    sparqlTermPattern
	Graph     *sparqlTermPattern
}

// sparqlPropertyPath is a predicate written as a property path; see
// sparql_paths.go.
type sparqlPropertyPath struct {
	Expr *sparqlPathExpr
}

type sparqlSelectItem struct {
	Alias string
	Expr  sparqlValueExpr
	// refs are the variables Expr reads outside aggregates.
	refs []string
}

type sparqlGroupKey struct {
	Alias string
	Expr  sparqlValueExpr
}

type sparqlOrderClause struct {
	Desc bool
	Expr sparqlValueExpr
}

// sparqlTermPattern is one position of a triple pattern: a variable, a
// constant term, a blank node, or a triple term pattern with something free
// in it. See sparql12.go for the last two.
type sparqlTermPattern struct {
	Variable string
	Term     *RDFTerm
	// Blank is a blank node written in the query, or one the RDF 1.2 sugar
	// introduced. In a pattern it matches like a variable no solution
	// projects; in a template it is a fresh blank node for each solution.
	Blank string
	// Triple is a triple term pattern <<( s p o )>> that is not constant.
	Triple *sparqlTriplePattern
}

type sparqlFilter interface {
	Eval(binding map[string]RDFTerm) (bool, error)
	EvalGroup(bindings []map[string]RDFTerm) (bool, error)
}

type sparqlValueExpr interface {
	Eval(binding map[string]RDFTerm) (RDFTerm, bool, error)
	EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error)
	IsAggregate() bool
}

type sparqlExprFilter struct {
	Expr sparqlValueExpr
}

type sparqlVarExpr struct {
	Variable string
}

type sparqlLiteralExpr struct {
	Term RDFTerm
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
	Cond sparqlValueExpr
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

// ExecuteSPARQL runs a practical SPARQL 1.1 subset (queries and updates)
// against the embedded RDF layer.
func (g *GraphStore) ExecuteSPARQL(ctx context.Context, query string) (*SPARQLResult, error) {
	parsed, err := g.parseSPARQL(ctx, query)
	if err != nil {
		return nil, err
	}
	if parsed.Next == nil {
		return g.executeParsedSPARQL(ctx, parsed)
	}
	// A request of several update operations runs them in order and
	// reports the total count; the first failure stops it.
	total := &SPARQLResult{QueryType: parsed.QueryType}
	for op := parsed; op != nil; op = op.Next {
		result, err := g.executeParsedSPARQL(ctx, op)
		if err != nil {
			return nil, err
		}
		total.Count += result.Count
	}
	return total, nil
}

func (g *GraphStore) executeParsedSPARQL(ctx context.Context, parsed *sparqlQuery) (*SPARQLResult, error) {
	execOptions := buildSPARQLExecOptions(parsed)
	if parsed.runtime != nil {
		parsed.runtime.ctx = ctx
		parsed.runtime.store = g
		parsed.runtime.opts = execOptions
	}

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

	if parsed.QueryType == SPARQLQueryConstruct || parsed.QueryType == SPARQLQueryDescribe {
		if len(parsed.OrderBy) > 0 {
			sortSPARQLBindings(bindings, parsed.OrderBy)
		}
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
		triples, err := g.materializeDescribeTriples(ctx, parsed.Describe, bindings, execOptions)
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
	if parsed.DatasetDeclared {
		opts.DatasetDeclared = true
		opts.DefaultGraphs = append(opts.DefaultGraphs, parsed.From...)
		opts.NamedGraphs = append(opts.NamedGraphs, parsed.FromNamed...)
		return opts
	}
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

// executeSPARQLSelect applies the solution modifiers in the order SPARQL 1.1
// §18.2.4 defines: grouping and aggregation, HAVING, the SELECT expressions,
// ORDER BY, projection, DISTINCT/REDUCED, then OFFSET/LIMIT. The order is
// observable: ORDER BY may name a SELECT alias, and DISTINCT must run before
// LIMIT or a limited page can come back short.
func (g *GraphStore) executeSPARQLSelect(ctx context.Context, parsed *sparqlQuery, bindings []map[string]RDFTerm, opts sparqlExecOptions) (*SPARQLResult, error) {
	result := &SPARQLResult{}
	isGrouped := len(parsed.GroupBy) > 0 || sparqlQueryUsesGrouping(parsed)

	var (
		vars      []string
		projected []map[string]RDFTerm
	)
	if !isGrouped {
		extended, err := extendSPARQLBindings(parsed, bindings)
		if err != nil {
			return nil, err
		}
		if len(parsed.OrderBy) > 0 {
			sortSPARQLBindings(extended, parsed.OrderBy)
		}
		vars, projected = projectSPARQLBindings(parsed, extended)
	} else {
		groups, err := buildSPARQLGroups(parsed, bindings)
		if err != nil {
			return nil, err
		}
		if len(parsed.Having) > 0 {
			filteredGroups := make([][]map[string]RDFTerm, 0, len(groups))
			for _, group := range groups {
				keep := true
				for _, filter := range parsed.Having {
					ok, err := evalSPARQLFilterGroup(filter, group)
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
		vars, projected, err = projectSPARQLGroups(parsed, groups)
		if err != nil {
			return nil, err
		}
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

// extendSPARQLBindings evaluates the SELECT expressions onto each solution,
// in order, so a later expression and ORDER BY can both see an earlier alias.
// An expression error leaves its alias unbound for that solution.
func extendSPARQLBindings(parsed *sparqlQuery, bindings []map[string]RDFTerm) ([]map[string]RDFTerm, error) {
	if parsed.SelectAll {
		return bindings, nil
	}
	extended := make([]map[string]RDFTerm, 0, len(bindings))
	for _, binding := range bindings {
		parsed.runtime.beginSolution()
		row := binding
		cloned := false
		for _, item := range parsed.SelectItems {
			if varExpr, ok := item.Expr.(sparqlVarExpr); ok && varExpr.Variable == item.Alias {
				continue
			}
			value, ok, err := item.Expr.Eval(row)
			if err != nil {
				if isSPARQLExprError(err) {
					continue
				}
				return nil, err
			}
			if !ok {
				continue
			}
			if !cloned {
				row = cloneBinding(binding)
				cloned = true
			}
			row[item.Alias] = value
		}
		extended = append(extended, row)
	}
	return extended, nil
}

func projectSPARQLBindings(parsed *sparqlQuery, bindings []map[string]RDFTerm) ([]string, []map[string]RDFTerm) {
	vars := make([]string, 0, len(parsed.SelectItems))
	if parsed.SelectAll {
		vars = collectBindingVars(bindings)
	} else {
		for _, item := range parsed.SelectItems {
			vars = append(vars, item.Alias)
		}
	}
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
	return vars, projected
}

// projectSPARQLGroups aggregates each group into one row and orders the rows.
// Every alias computed for a group is also written into every solution of a
// copy of the group, so a later SELECT expression or ORDER BY can name it:
// the value is constant across the group, so COUNT(*) and COUNT(DISTINCT *)
// over the copy still see what they would have seen over the original. An
// empty group (an aggregate over no solutions) stays empty, so COUNT(*) is 0.
func projectSPARQLGroups(parsed *sparqlQuery, groups [][]map[string]RDFTerm) ([]string, []map[string]RDFTerm, error) {
	if parsed.SelectAll {
		if len(groups) == 0 {
			return nil, nil, nil
		}
		if len(parsed.OrderBy) > 0 {
			sortSPARQLGroups(groups, parsed.OrderBy)
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
	type groupRow struct {
		group []map[string]RDFTerm
		row   map[string]RDFTerm
	}
	rows := make([]groupRow, 0, len(groups))
	for _, group := range groups {
		row := make(map[string]RDFTerm, len(parsed.SelectItems))
		augment := len(group) > 0 && (len(parsed.OrderBy) > 0 || len(parsed.SelectItems) > 1)
		if augment {
			group = cloneBindingSlice(group)
		}
		for _, item := range parsed.SelectItems {
			value, ok, err := item.Expr.EvalGroup(group)
			if err != nil {
				if isSPARQLExprError(err) {
					continue
				}
				return nil, nil, err
			}
			if !ok {
				continue
			}
			row[item.Alias] = value
			if augment {
				for _, binding := range group {
					binding[item.Alias] = value
				}
			}
		}
		rows = append(rows, groupRow{group: group, row: row})
	}
	if len(parsed.OrderBy) > 0 {
		sort.SliceStable(rows, func(i, j int) bool {
			return sparqlOrderLess(parsed.OrderBy, func(e sparqlValueExpr) (RDFTerm, bool, error) {
				return e.EvalGroup(rows[i].group)
			}, func(e sparqlValueExpr) (RDFTerm, bool, error) {
				return e.EvalGroup(rows[j].group)
			})
		})
	}
	projected := make([]map[string]RDFTerm, 0, len(rows))
	for _, r := range rows {
		projected = append(projected, r.row)
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
	if f, ok := filter.(sparqlExprFilter); ok {
		return f.Expr.IsAggregate()
	}
	return false
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
			if err != nil && !isSPARQLExprError(err) {
				return nil, err
			}
			if err != nil || !ok {
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
			if err != nil && !isSPARQLExprError(err) {
				return nil, err
			}
			if err == nil && ok {
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
		return sparqlOrderLess(clauses, evalGrouped(groups[i]), evalGrouped(groups[j]))
	})
}

func (g *GraphStore) executeSPARQLInsertData(ctx context.Context, templates []sparqlPattern) (int, error) {
	triples := materializeTemplateTriples(templates, []map[string]RDFTerm{{}})
	if err := refuseProjectedInserts(triples); err != nil {
		return 0, err
	}
	for _, triple := range triples {
		tripleCopy := triple
		if err := g.UpsertTriple(ctx, &tripleCopy); err != nil {
			return 0, err
		}
	}
	return len(triples), nil
}

// The delete paths count what was removed, not what was asked for: a triple
// that was not stored removes nothing and counts nothing. One the
// property-graph projection supplies fails the whole statement before any
// triple is removed.
func (g *GraphStore) executeSPARQLDeleteData(ctx context.Context, templates []sparqlPattern) (int, error) {
	triples := materializeTemplateTriples(templates, []map[string]RDFTerm{{}})
	if err := g.refuseProjectedDeletes(ctx, triples); err != nil {
		return 0, err
	}
	return g.deleteTriples(ctx, triples)
}

func (g *GraphStore) executeSPARQLDeleteWhere(ctx context.Context, templates []sparqlPattern, bindings []map[string]RDFTerm, defaultGraph *RDFTerm) (int, error) {
	triples := materializeTemplateTriplesWithDefaultGraph(templates, bindings, defaultGraph)
	if err := g.refuseProjectedDeletes(ctx, triples); err != nil {
		return 0, err
	}
	return g.deleteTriples(ctx, triples)
}

func (g *GraphStore) executeSPARQLModify(ctx context.Context, deletes, inserts []sparqlPattern, bindings []map[string]RDFTerm, defaultGraph *RDFTerm) (int, error) {
	deleteTriples := materializeTemplateTriplesWithDefaultGraph(deletes, bindings, defaultGraph)
	insertTriples := materializeTemplateTriplesWithDefaultGraph(inserts, bindings, defaultGraph)
	if err := refuseProjectedInserts(insertTriples); err != nil {
		return 0, err
	}
	if err := g.refuseProjectedDeletes(ctx, deleteTriples); err != nil {
		return 0, err
	}
	changed, err := g.deleteTriples(ctx, deleteTriples)
	if err != nil {
		return changed, err
	}
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
				keep, err := evalSPARQLFilter(step.Filter, inScope(binding, step.outOfScope))
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
			nextBindings, err := g.executeSPARQLGraphGroup(ctx, step, current, opts)
			if err != nil {
				return nil, err
			}
			current = nextBindings
		case sparqlSubQueryStep:
			nextBindings, err := g.executeSPARQLSubQuery(ctx, step, current, opts)
			if err != nil {
				return nil, err
			}
			current = nextBindings
		case sparqlMinusStep:
			nextBindings, err := g.executeSPARQLMinus(ctx, step, current, opts)
			if err != nil {
				return nil, err
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
			// A BIND whose expression errs or is unbound keeps the solution
			// and leaves the variable unbound (SPARQL 1.1 §18.6, Extend).
			nextBindings := make([]map[string]RDFTerm, 0, len(current))
			for _, binding := range current {
				step.rt.beginSolution()
				value, ok, err := step.Expr.Eval(inScope(binding, step.outOfScope))
				if err != nil && !isSPARQLExprError(err) {
					return nil, err
				}
				if err != nil || !ok {
					nextBindings = append(nextBindings, binding)
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

// A pattern outside GRAPH reads the default graph, which here is the unnamed
// triples plus the property-graph projection: the projection is the only RDF
// most brains have, and a query that had to know to ask for it by name would
// find nothing for everyone who did not.
func sparqlTripleAllowedForGraph(pattern sparqlPattern, triple RDFTriple, opts sparqlExecOptions) bool {
	if opts.DatasetDeclared {
		if triple.Graph == nil {
			return false
		}
		if pattern.Graph == nil {
			return containsTerm(opts.DefaultGraphs, *triple.Graph)
		}
		return containsTerm(opts.NamedGraphs, *triple.Graph)
	}
	if pattern.Graph == nil && triple.Graph != nil && len(opts.DefaultGraphs) == 0 && !isPropertyGraphTerm(triple.Graph) {
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
	// RDF 1.2 has no triple with a triple term as subject or predicate, so
	// such a pattern — legal SPARQL syntax — matches nothing.
	if pattern.Subject.Triple != nil || pattern.Predicate.Triple != nil ||
		(pattern.Subject.Term != nil && pattern.Subject.Term.Kind == RDFTermTriple) {
		return nil, nil
	}
	basePattern, err := resolveTriplePattern(pattern, binding)
	if err != nil {
		return nil, err
	}
	if !sparqlPatternCanMatch(basePattern) {
		return nil, nil
	}
	if opts.DatasetDeclared {
		if pattern.Graph == nil && len(opts.DefaultGraphs) == 0 {
			return nil, nil
		}
		if pattern.Graph != nil && len(opts.NamedGraphs) == 0 {
			return nil, nil
		}
	}
	if pattern.Graph != nil || len(opts.DefaultGraphs) == 0 {
		return g.FindTriples(ctx, basePattern)
	}
	// The default graph is the RDF merge of the listed graphs, so a triple
	// asserted in two of them is one triple, not two solutions.
	out := make([]RDFTriple, 0)
	seen := make(map[string]struct{})
	for _, defaultGraph := range opts.DefaultGraphs {
		graphCopy := defaultGraph
		patternWithGraph := basePattern
		patternWithGraph.Graph = &graphCopy
		triples, err := g.FindTriples(ctx, patternWithGraph)
		if err != nil {
			return nil, err
		}
		for _, triple := range triples {
			if len(opts.DefaultGraphs) > 1 {
				key := triple.Subject.String() + " " + triple.Predicate.String() + " " + triple.Object.String()
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
			}
			out = append(out, triple)
		}
	}
	return out, nil
}

// sparqlPatternCanMatch reports whether a resolved pattern could match any
// triple at all. A variable is bound by whatever an earlier pattern matched, so
// a join can put a literal where only an IRI or blank node can stand — ?rel
// ranging over every predicate binds ?y to a name as often as to a node. No
// triple has a literal subject, so that row simply has no solutions here.
// Passing it on to FindTriples would instead fail the whole query, because
// FindTriples is right to refuse such a pattern from a caller who wrote it.
func sparqlPatternCanMatch(pattern TriplePattern) bool {
	if pattern.Subject != nil && pattern.Subject.Kind != RDFTermIRI && pattern.Subject.Kind != RDFTermBlankNode {
		return false
	}
	if pattern.Object != nil && pattern.Object.Kind == sparqlNoMatchKind {
		return false
	}
	if pattern.Predicate != nil && pattern.Predicate.Kind != "" && pattern.Predicate.Kind != RDFTermIRI {
		return false
	}
	if pattern.Graph != nil && pattern.Graph.Kind != RDFTermIRI && pattern.Graph.Kind != RDFTermBlankNode {
		return false
	}
	return true
}

func resolveOptionalPatternTerm(pattern *sparqlTermPattern, binding map[string]RDFTerm) (*RDFTerm, error) {
	if pattern == nil {
		return nil, nil
	}
	return resolvePatternTerm(*pattern, binding)
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
	if object == nil && pattern.Object.Triple != nil {
		// A partly bound <<( ?s :p ?o )>>: the store narrows to triple
		// terms with these parts, and unification binds the rest.
		out.objectTriple = tripleTermFilterFor(pattern.Object.Triple, binding)
		if noMatch(out.objectTriple.Subject) || noMatch(out.objectTriple.Predicate) || noMatch(out.objectTriple.Object) {
			out.Object = &RDFTerm{Kind: sparqlNoMatchKind}
			out.objectTriple = nil
		}
	}

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
	if pattern.Triple != nil {
		return resolveTripleTermPattern(pattern.Triple, binding), nil
	}
	name := pattern.varName()
	if name == "" {
		return nil, nil
	}
	if value, ok := binding[name]; ok {
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
	if pattern.Triple != nil {
		return bindTripleTermPattern(binding, pattern.Triple, value)
	}
	name := pattern.varName()
	if name == "" {
		return true
	}
	if existing, ok := binding[name]; ok {
		return termsEqual(existing, value)
	}
	binding[name] = value
	return true
}

// termsEqual is RDF term equality. A simple literal and the same literal
// typed xsd:string are one term since RDF 1.1, which the store does not
// normalize on write, so the comparison does.
func termsEqual(a, b RDFTerm) bool {
	return a.Kind == b.Kind &&
		a.Value == b.Value &&
		(a.Datatype == b.Datatype || (a.Kind == RDFTermLiteral && plainStringDatatype(a.Datatype) && plainStringDatatype(b.Datatype))) &&
		a.Language == b.Language
}

func plainStringDatatype(datatype string) bool {
	return datatype == "" || datatype == rdf12XSDStringIRI
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
			// Blank nodes in a pattern bind hidden variables; SELECT *
			// projects the variables the query named and no others.
			if isHiddenVariable(variable) {
				continue
			}
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

func materializeTemplateTriples(templates []sparqlPattern, bindings []map[string]RDFTerm) []RDFTriple {
	return materializeTemplateTriplesWithDefaultGraph(templates, bindings, nil)
}

func materializeTemplateTriplesWithDefaultGraph(templates []sparqlPattern, bindings []map[string]RDFTerm, defaultGraph *RDFTerm) []RDFTriple {
	out := make([]RDFTriple, 0)
	seen := make(map[string]struct{})
	blanks := newSPARQLTemplateBlanks()
	for _, binding := range bindings {
		blanks.nextSolution()
		for _, template := range templates {
			subject, ok := instantiateTemplateTerm(template.Subject, binding, blanks)
			if !ok || (subject.Kind != RDFTermIRI && subject.Kind != RDFTermBlankNode) {
				continue
			}
			predicate, ok := instantiateTemplateTerm(template.Predicate, binding, blanks)
			if !ok || predicate.Kind != RDFTermIRI {
				continue
			}
			object, ok := instantiateTemplateTerm(template.Object, binding, blanks)
			if !ok {
				continue
			}
			triple := RDFTriple{
				Subject:   subject,
				Predicate: predicate,
				Object:    object,
			}
			if template.Graph != nil {
				graphTerm, ok := instantiateTemplateTerm(*template.Graph, binding, blanks)
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

// materializeDescribeTriples returns each resource's incoming and outgoing
// triples. Without a dataset clause that is every graph; with FROM it is the
// declared default graph only.
func (g *GraphStore) materializeDescribeTriples(ctx context.Context, describes []sparqlTermPattern, bindings []map[string]RDFTerm, opts sparqlExecOptions) ([]RDFTriple, error) {
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
			if opts.DatasetDeclared && !sparqlTripleAllowedForGraph(sparqlPattern{}, triple, opts) {
				continue
			}
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
			if opts.DatasetDeclared && !sparqlTripleAllowedForGraph(sparqlPattern{}, triple, opts) {
				continue
			}
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
		return sparqlOrderLess(clauses, evalSingle(bindings[i]), evalSingle(bindings[j]))
	})
}

// sparqlOrderLess compares two solutions under ORDER BY. A key that fails to
// evaluate sorts as unbound, first in ascending order — an error never
// aborts a sort.
func sparqlOrderLess(clauses []sparqlOrderClause, left, right sparqlEvalFn) bool {
	for _, clause := range clauses {
		leftValue, leftOK, leftErr := left(clause.Expr)
		rightValue, rightOK, rightErr := right(clause.Expr)
		cmp := sparqlOrderCompare(leftValue, leftOK && leftErr == nil, rightValue, rightOK && rightErr == nil)
		if cmp == 0 {
			continue
		}
		if clause.Desc {
			return cmp > 0
		}
		return cmp < 0
	}
	return false
}

func evalArithmeticTerms(op string, left, right RDFTerm) (RDFTerm, bool, error) {
	value, err := sparqlArithmetic(op, left, right)
	if err != nil {
		return RDFTerm{}, false, err
	}
	return value, true, nil
}

// evalSPARQLFilter applies a FILTER/HAVING constraint to one solution (or
// group). An expression error is the spec's "false": the solution is dropped
// and the query carries on.
func evalSPARQLFilter(filter sparqlFilter, binding map[string]RDFTerm) (bool, error) {
	return filter.Eval(binding)
}

func evalSPARQLFilterGroup(filter sparqlFilter, bindings []map[string]RDFTerm) (bool, error) {
	return filter.EvalGroup(bindings)
}

func (f sparqlExprFilter) Eval(binding map[string]RDFTerm) (bool, error) {
	return filterOutcome(evalBoolean(evalSingle(binding), f.Expr))
}

func (f sparqlExprFilter) EvalGroup(bindings []map[string]RDFTerm) (bool, error) {
	return filterOutcome(evalBoolean(evalGrouped(bindings), f.Expr))
}

func filterOutcome(keep bool, err error) (bool, error) {
	if err != nil {
		if isSPARQLExprError(err) {
			return false, nil
		}
		return false, err
	}
	return keep, nil
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

func (e sparqlCountFuncExpr) Eval(_ map[string]RDFTerm) (RDFTerm, bool, error) {
	return RDFTerm{}, false, fmt.Errorf("COUNT cannot be evaluated outside a group")
}

// EvalGroup counts the solutions for which the expression has a bound,
// error-free value — an expression error excludes the solution from the
// count rather than failing the query.
func (e sparqlCountFuncExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	count := func(n int) (RDFTerm, bool, error) {
		return NewTypedLiteral(strconv.Itoa(n), xsdIntegerIRI), true, nil
	}
	if e.Wildcard {
		if e.Distinct {
			return count(len(distinctBindings(bindings, collectBindingVars(bindings))))
		}
		return count(len(bindings))
	}
	if e.Inner == nil {
		return count(0)
	}
	seen := make(map[string]struct{})
	n := 0
	for _, binding := range bindings {
		value, ok, err := e.Inner.Eval(binding)
		if err != nil {
			if isSPARQLExprError(err) {
				continue
			}
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
		n++
	}
	return count(n)
}

func (e sparqlCountFuncExpr) IsAggregate() bool { return true }

func (e sparqlUnaryNumericExpr) Eval(binding map[string]RDFTerm) (RDFTerm, bool, error) {
	return e.eval(evalSingle(binding))
}

// eval negates while keeping the operand's numeric type: -3 is an integer.
func (e sparqlUnaryNumericExpr) eval(eval sparqlEvalFn) (RDFTerm, bool, error) {
	value, err := evalValue(eval, e.Inner)
	if err != nil {
		return RDFTerm{}, false, err
	}
	number, err := sparqlNumericArg("numeric operator "+e.Op, value)
	if err != nil {
		return RDFTerm{}, false, err
	}
	if e.Op == "-" {
		number.value = -number.value
	}
	return number.term(), true, nil
}

func (e sparqlUnaryNumericExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	return e.eval(evalGrouped(bindings))
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
	return e.eval(evalSingle(binding))
}

// eval returns the first argument that evaluates without error to a bound
// value; errors in earlier arguments are exactly what COALESCE skips.
func (e sparqlCoalesceFuncExpr) eval(eval sparqlEvalFn) (RDFTerm, bool, error) {
	for _, arg := range e.Args {
		value, ok, err := eval(arg)
		if err != nil {
			if isSPARQLExprError(err) {
				continue
			}
			return RDFTerm{}, false, err
		}
		if ok {
			return value, true, nil
		}
	}
	return RDFTerm{}, false, sparqlTypeErrorf("COALESCE found no bound argument")
}

func (e sparqlCoalesceFuncExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	return e.eval(evalGrouped(bindings))
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
	return e.eval(evalSingle(binding))
}

// eval evaluates only the chosen branch; an error in the condition is an
// error of the whole IF.
func (e sparqlIfFuncExpr) eval(eval sparqlEvalFn) (RDFTerm, bool, error) {
	cond, err := evalBoolean(eval, e.Cond)
	if err != nil {
		return RDFTerm{}, false, err
	}
	if cond {
		return eval(e.Then)
	}
	return eval(e.Else)
}

func (e sparqlIfFuncExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	return e.eval(evalGrouped(bindings))
}

func (e sparqlIfFuncExpr) IsAggregate() bool {
	return e.Then.IsAggregate() || e.Else.IsAggregate() || e.Cond.IsAggregate()
}

func (e sparqlAggregateFuncExpr) Eval(_ map[string]RDFTerm) (RDFTerm, bool, error) {
	return RDFTerm{}, false, fmt.Errorf("%s cannot be evaluated outside a group", strings.ToUpper(e.Name))
}

// EvalGroup aggregates the group's values. Unbound values are skipped; an
// expression error, or a value the aggregate cannot use (SUM of a string),
// makes the aggregate itself an error, which leaves its alias unbound for
// this group only (SPARQL 1.1 §18.5.1). SAMPLE skips errors, since any one
// value will do.
func (e sparqlAggregateFuncExpr) EvalGroup(bindings []map[string]RDFTerm) (RDFTerm, bool, error) {
	name := strings.ToUpper(e.Name)
	values := make([]RDFTerm, 0, len(bindings))
	seen := make(map[string]struct{})
	for _, binding := range bindings {
		value, ok, err := e.Inner.Eval(binding)
		if err != nil {
			if isSPARQLExprError(err) && name == "SAMPLE" {
				continue
			}
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

	sum := func() (sparqlNumber, error) {
		total := sparqlNumber{kind: sparqlNumInteger}
		for _, value := range values {
			n, ok := lenientNumber(value)
			if !ok {
				return sparqlNumber{}, sparqlTypeErrorf("%s requires numeric literals", name)
			}
			total.kind = max(total.kind, n.kind)
			total.value += n.value
		}
		return total, nil
	}

	switch name {
	case "SUM":
		total, err := sum()
		if err != nil {
			return RDFTerm{}, false, err
		}
		return total.term(), true, nil
	case "AVG":
		// The average of nothing is 0, as the spec defines it.
		total, err := sum()
		if err != nil {
			return RDFTerm{}, false, err
		}
		if len(values) == 0 {
			return total.term(), true, nil
		}
		total.kind = max(total.kind, sparqlNumDecimal)
		total.value /= float64(len(values))
		return total.term(), true, nil
	case "MIN", "MAX":
		if len(values) == 0 {
			return RDFTerm{}, false, nil
		}
		best := values[0]
		for _, value := range values[1:] {
			cmp := sparqlOrderCompare(value, true, best, true)
			if (name == "MIN" && cmp < 0) || (name == "MAX" && cmp > 0) {
				best = value
			}
		}
		return best, true, nil
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
	// rt is shared by every expression node of one query, subqueries
	// included, so NOW(), BNODE(str) and the regex cache are per query.
	rt *sparqlRuntime
	// exprGraph is the GRAPH block enclosing the FILTER/BIND being parsed,
	// so an EXISTS inside it inherits the active graph.
	exprGraph *sparqlTermPattern
	// selectGraph hands the enclosing GRAPH to a subquery's WHERE.
	selectGraph *sparqlTermPattern
	// refVars, while non-nil, collects every variable the parser reads: the
	// references of the FILTER or BIND being parsed. optionalGroup marks the
	// next group as an OPTIONAL's, existsDepth counts enclosing EXISTS; both
	// steer scopeGroup.
	refVars       map[string]bool
	optionalGroup bool
	existsDepth   int
	// aggDepth counts enclosing aggregates, whose variables are not
	// references of the expression around them.
	aggDepth int

	// blankSeq numbers the blank nodes the parser invents for [], reified
	// triples and annotations; depth bounds recursion; inTemplate is set
	// while a CONSTRUCT, INSERT or DELETE template is parsed, where paths
	// are not allowed.
	blankSeq   int
	depth      int
	inTemplate bool
	// base resolves relative IRIs once a BASE has been declared.
	base string
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
	sparqlTokenBlank    sparqlTokenType = "blank"
	sparqlTokenIdent    sparqlTokenType = "ident"
	sparqlTokenEOF      sparqlTokenType = "eof"
)

type sparqlToken struct {
	Type  sparqlTokenType
	Value string
	// Long marks a string written with tripled quotes, which VERSION does
	// not accept.
	Long bool
}

func newSPARQLParser(query string, prefixes map[string]string) *sparqlParser {
	return &sparqlParser{
		tokens:   tokenizeSPARQL(query),
		prefixes: prefixes,
		rt:       newSPARQLRuntime(),
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
	return p.parseOperation(clonePrefixes(p.prefixes))
}

// parsePrologue reads PREFIX, BASE and VERSION declarations. VERSION is
// recorded nowhere: SPARQL 1.2 makes it an announcement a processor may warn
// about, and this one reads 1.1 and 1.2 alike.
func (p *sparqlParser) parsePrologue(prefixes map[string]string) {
	for {
		switch {
		case p.matchKeyword("PREFIX"):
			prefixToken := p.peek()
			if prefixToken.Type != sparqlTokenQName || !strings.HasSuffix(prefixToken.Value, ":") || strings.Count(prefixToken.Value, ":") != 1 {
				panicSPARQLParse(fmt.Errorf("expected prefix label, got %q", prefixToken.Value))
			}
			p.next()
			iriToken := p.expectType(sparqlTokenIRI, "prefix iri")
			value := iriToken.Value
			if p.base != "" && !hasIRIScheme(value) {
				value = resolveIRIReference(p.base, value)
			}
			prefixes[strings.TrimSuffix(prefixToken.Value, ":")] = value
		case p.matchKeyword("BASE"):
			iriToken := p.expectType(sparqlTokenIRI, "base iri")
			value := iriToken.Value
			if p.base != "" && !hasIRIScheme(value) {
				value = resolveIRIReference(p.base, value)
			}
			p.base = value
			p.rt.base = value
		case p.matchKeyword("VERSION"):
			// VersionSpecifier is STRING_LITERAL1 | STRING_LITERAL2 only.
			if version := p.expectType(sparqlTokenString, "version string"); version.Long {
				panicSPARQLParse(fmt.Errorf("VERSION takes a short string, not %q", version.Value))
			}
		default:
			return
		}
	}
}

// parseOperation parses one query, or one update operation and any that
// follow it after ';' — SPARQL Update runs a request's operations in order,
// each seeing what the one before it did.
func (p *sparqlParser) parseOperation(prefixes map[string]string) (query *sparqlQuery, err error) {
	query = &sparqlQuery{
		Prefixes: prefixes,
		runtime:  p.rt,
	}

	p.parsePrologue(query.Prefixes)
	if p.matchKeyword("WITH") {
		withTerm, err := p.parseGraphResourceTerm(query.Prefixes)
		if err != nil {
			return nil, err
		}
		query.With = &withTerm
	}

	switch {
	case p.matchKeyword("SELECT"):
		err := p.parseSelectQueryBody(query, true)
		if err != nil {
			return nil, err
		}
		if p.peek().Type != sparqlTokenEOF {
			return nil, fmt.Errorf("unexpected trailing token %q", p.peek().Value)
		}
		return query, nil
	case p.matchKeyword("CONSTRUCT"):
		query.QueryType = SPARQLQueryConstruct
		if p.peek().Type == sparqlTokenPunct && p.peek().Value == "{" {
			p.inTemplate = true
			template, err := p.parseConstructTemplate(query.Prefixes)
			p.inTemplate = false
			if err != nil {
				return nil, err
			}
			query.Template = template
			break
		}
		// CONSTRUCT WHERE { ... }: the pattern is its own template, which
		// is why it may hold only triple patterns.
		if err := p.parseDatasetClauses(query); err != nil {
			return nil, err
		}
		if !p.matchKeyword("WHERE") {
			return nil, fmt.Errorf("expected a CONSTRUCT template or WHERE")
		}
		p.inTemplate = true
		group, err := p.parseEnclosedGroup(nil, query.Prefixes)
		p.inTemplate = false
		if err != nil {
			return nil, err
		}
		if !onlyTriplePatterns(group) {
			return nil, fmt.Errorf("CONSTRUCT WHERE takes triple patterns only")
		}
		template, err := flattenTemplatePatterns(group)
		if err != nil {
			return nil, err
		}
		query.Group = group
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
			p.inTemplate = true
			templateGroup, err := p.parseEnclosedGroup(nil, query.Prefixes)
			p.inTemplate = false
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
		p.inTemplate = true
		insertGroup, err := p.parseEnclosedGroup(nil, query.Prefixes)
		p.inTemplate = false
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
			p.inTemplate = true
			templateGroup, err := p.parseEnclosedGroup(nil, query.Prefixes)
			p.inTemplate = false
			if err != nil {
				return nil, err
			}
			template, err := flattenTemplatePatterns(templateGroup)
			if err != nil {
				return nil, err
			}
			if templateHasBlankNode(template) {
				return nil, fmt.Errorf("DELETE DATA cannot contain blank nodes")
			}
			query.Template = template
		case p.matchKeyword("WHERE"):
			query.QueryType = SPARQLQueryDeleteWhere
			p.inTemplate = true
			group, err := p.parseEnclosedGroup(nil, query.Prefixes)
			p.inTemplate = false
			if err != nil {
				return nil, err
			}
			query.Group = group
			template, err := flattenTemplatePatterns(group)
			if err != nil {
				return nil, err
			}
			if templateHasBlankNode(template) {
				return nil, fmt.Errorf("DELETE WHERE cannot contain blank nodes")
			}
			query.Template = template
		default:
			query.QueryType = SPARQLQueryModify
			p.inTemplate = true
			deleteGroup, err := p.parseEnclosedGroup(nil, query.Prefixes)
			p.inTemplate = false
			if err != nil {
				return nil, err
			}
			deletePatterns, err := flattenTemplatePatterns(deleteGroup)
			if err != nil {
				return nil, err
			}
			if templateHasBlankNode(deletePatterns) {
				return nil, fmt.Errorf("a DELETE template cannot contain blank nodes")
			}
			query.Delete = deletePatterns
			if p.matchKeyword("INSERT") {
				p.inTemplate = true
				insertGroup, err := p.parseEnclosedGroup(nil, query.Prefixes)
				p.inTemplate = false
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

	switch query.QueryType {
	case SPARQLQueryConstruct, SPARQLQueryDescribe, SPARQLQueryAsk:
		if err := p.parseDatasetClauses(query); err != nil {
			return nil, err
		}
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
	if !isSPARQLUpdate(query.QueryType) {
		if err := p.parseTrailingValues(query); err != nil {
			return nil, err
		}
	}

	if isSPARQLUpdate(query.QueryType) && p.matchPunct(";") {
		p.parsePrologue(query.Prefixes)
		if p.peek().Type != sparqlTokenEOF {
			next, err := p.parseOperation(clonePrefixes(query.Prefixes))
			if err != nil {
				return nil, err
			}
			query.Next = next
		}
	}

	if p.peek().Type != sparqlTokenEOF {
		return nil, fmt.Errorf("unexpected trailing token %q", p.peek().Value)
	}
	return query, nil
}

// onlyTriplePatterns reports whether a group is a basic graph pattern. A
// statement with several objects or predicates parses as a plain group of its
// patterns, which counts.
func onlyTriplePatterns(group sparqlGroup) bool {
	for _, raw := range group.Steps {
		switch step := raw.(type) {
		case sparqlPatternStep:
		case sparqlGroupStep:
			if step.Graph != nil || !onlyTriplePatterns(step.Group) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func isSPARQLUpdate(queryType string) bool {
	switch queryType {
	case SPARQLQueryInsertData, SPARQLQueryDeleteData, SPARQLQueryDeleteWhere, SPARQLQueryModify:
		return true
	}
	return false
}

func (p *sparqlParser) parseSelectQueryBody(query *sparqlQuery, allowDataset bool) error {
	query.QueryType = SPARQLQuerySelect
	// REDUCED permits, but does not require, dropping duplicates; doing
	// exactly what DISTINCT does is a conforming choice.
	if p.matchKeyword("DISTINCT") || p.matchKeyword("REDUCED") {
		query.Distinct = true
	}
	if p.matchOperator("*") {
		query.SelectAll = true
	} else {
		for p.peek().Type == sparqlTokenVar || (p.peek().Type == sparqlTokenPunct && p.peek().Value == "(") {
			var item sparqlSelectItem
			refs, err := p.recordRefs(func() (err error) {
				item, err = p.parseSelectItem(query.Prefixes)
				return err
			})
			if err != nil {
				return err
			}
			item.refs = refs
			query.SelectItems = append(query.SelectItems, item)
			query.Vars = append(query.Vars, item.Alias)
		}
		if len(query.SelectItems) == 0 {
			return fmt.Errorf("SELECT requires variables, expressions, or *")
		}
	}

	if allowDataset {
		if err := p.parseDatasetClauses(query); err != nil {
			return err
		}
	}
	if p.matchKeyword("WHERE") {
		// optional
	}
	activeGraph := p.selectGraph
	p.selectGraph = nil
	group, err := p.parseEnclosedGroup(activeGraph, query.Prefixes)
	if err != nil {
		return err
	}
	query.Group = group

	p.parseSolutionModifiers(query)
	if err := validateSelectScope(query); err != nil {
		return err
	}
	if err := p.parseTrailingValues(query); err != nil {
		return err
	}
	return nil
}

// parseDatasetClauses reads FROM / FROM NAMED. Subqueries and updates never
// call it: SPARQL gives a dataset to the whole query only, and updates name
// theirs with USING.
func (p *sparqlParser) parseDatasetClauses(query *sparqlQuery) error {
	for p.matchKeyword("FROM") {
		named := p.matchKeyword("NAMED")
		term, err := p.parseGraphResourceTerm(query.Prefixes)
		if err != nil {
			return err
		}
		if term.Kind != RDFTermIRI {
			return fmt.Errorf("FROM requires a graph IRI")
		}
		query.DatasetDeclared = true
		if named {
			query.FromNamed = append(query.FromNamed, term)
		} else {
			query.From = append(query.From, term)
		}
	}
	return nil
}

// parseTrailingValues reads the ValuesClause after a query's solution
// modifiers. The algebra joins it with the WHERE pattern before grouping and
// projection, which is what appending it to the group as its last step does.
func (p *sparqlParser) parseTrailingValues(query *sparqlQuery) error {
	if !p.matchKeyword("VALUES") {
		return nil
	}
	step, err := p.parseValues(query.Prefixes)
	if err != nil {
		return err
	}
	query.Group.Steps = append(query.Group.Steps, step)
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

// parseConstructTemplate accepts GRAPH blocks, so a CONSTRUCT can produce
// quads. That is an extension beyond SPARQL 1.1 (whose templates are
// triples only), matching what quad stores such as Jena and RDF4J offer.
func (p *sparqlParser) parseConstructTemplate(prefixes map[string]string) ([]sparqlPattern, error) {
	group, err := p.parseEnclosedGroup(nil, prefixes)
	if err != nil {
		return nil, err
	}
	return flattenTemplatePatterns(group)
}

func flattenTemplatePatterns(group sparqlGroup) ([]sparqlPattern, error) {
	out := make([]sparqlPattern, 0)
	if err := appendTemplatePatterns(&out, group); err != nil {
		return nil, err
	}
	return out, nil
}

func appendTemplatePatterns(out *[]sparqlPattern, group sparqlGroup) error {
	for _, rawStep := range group.Steps {
		switch step := rawStep.(type) {
		case sparqlPatternStep:
			*out = append(*out, step.Pattern)
		case sparqlGroupStep:
			if err := appendTemplatePatterns(out, step.Group); err != nil {
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
	optional := p.optionalGroup
	p.optionalGroup = false
	group := sparqlGroup{Steps: make([]sparqlStep, 0)}
	for {
		if p.matchPunct("}") {
			break
		}
		first := len(group.Steps) == 0
		step, err := p.parseGroupStep(activeGraph, prefixes)
		if err != nil {
			return sparqlGroup{}, err
		}
		// A subquery is a group of its own: { SELECT ... }, nothing else.
		if _, sub := step.(sparqlSubQueryStep); sub && (!first || !p.peekPunct("}")) {
			return sparqlGroup{}, fmt.Errorf("a subquery must be the only content of its group: { SELECT ... }")
		}
		group.Steps = append(group.Steps, step)
		p.matchPunct(".")
	}
	if err := scopeGroup(&group, activeGraph, optional, p.existsDepth == 0); err != nil {
		return sparqlGroup{}, err
	}
	return group, nil
}

func (p *sparqlParser) parseGroupStep(activeGraph *sparqlTermPattern, prefixes map[string]string) (sparqlStep, error) {
	if p.matchKeyword("SELECT") {
		subQuery := &sparqlQuery{
			Prefixes: prefixes,
			runtime:  p.rt,
		}
		step := sparqlSubQueryStep{Query: subQuery, Graph: activeGraph}
		p.selectGraph = activeGraph
		if activeGraph != nil && activeGraph.Variable != "" {
			step.Inner = p.hiddenGraphVariable()
			p.selectGraph = &sparqlTermPattern{Variable: step.Inner}
		}
		if err := p.parseSelectQueryBody(subQuery, false); err != nil {
			return nil, err
		}
		return step, nil
	}
	if p.matchKeyword("FILTER") {
		var filter sparqlFilter
		refs, err := p.recordRefs(func() (err error) {
			filter, err = p.parseFilter(activeGraph, prefixes)
			return err
		})
		if err != nil {
			return nil, err
		}
		return sparqlFilterStep{Filter: filter, refs: refs}, nil
	}
	if p.matchKeyword("BIND") {
		p.expectPunct("(")
		savedGraph := p.exprGraph
		p.exprGraph = activeGraph
		var expr sparqlValueExpr
		refs, err := p.recordRefs(func() (err error) {
			expr, err = p.parseValueExpr(prefixes)
			return err
		})
		p.exprGraph = savedGraph
		if err != nil {
			return nil, err
		}
		if !p.matchKeyword("AS") {
			return nil, fmt.Errorf("expected AS in BIND")
		}
		variable := strings.TrimPrefix(p.expectType(sparqlTokenVar, "bind variable").Value, "?")
		p.expectPunct(")")
		return sparqlBindStep{Variable: variable, Expr: expr, refs: refs, rt: p.rt}, nil
	}
	if p.matchKeyword("OPTIONAL") {
		p.optionalGroup = true
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
		return sparqlGroupStep{Group: group, Graph: &graphPattern}, nil
	}
	if p.matchKeyword("MINUS") {
		step := sparqlMinusStep{Graph: activeGraph}
		inner := activeGraph
		if activeGraph != nil && activeGraph.Variable != "" {
			step.Inner = p.hiddenGraphVariable()
			inner = &sparqlTermPattern{Variable: step.Inner}
		}
		group, err := p.parseEnclosedGroup(inner, prefixes)
		if err != nil {
			return nil, err
		}
		step.Group = group
		return step, nil
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
	// A single variable written bare takes bare values; written in
	// parentheses, (?o), its rows are parenthesised too.
	parenthesised := p.peek().Type != sparqlTokenVar
	if !parenthesised {
		variables = append(variables, strings.TrimPrefix(p.next().Value, "?"))
	} else {
		p.expectPunct("(")
		for p.peek().Type == sparqlTokenVar {
			variables = append(variables, strings.TrimPrefix(p.next().Value, "?"))
		}
		p.expectPunct(")")
	}

	p.expectPunct("{")
	rows := make([]map[string]RDFTerm, 0)
	// value reads one DataBlockValue into the row: a constant term, a
	// TripleTermData, or UNDEF, which leaves the variable unbound.
	value := func(row map[string]RDFTerm, variable string) error {
		if p.matchKeyword("UNDEF") {
			return nil
		}
		if p.peekPunct("<<(") {
			term, err := p.parseTripleTermData(prefixes)
			if err != nil {
				return err
			}
			row[variable] = term
			return nil
		}
		term, err := p.parseTermPattern(prefixes, true)
		if err != nil {
			return err
		}
		if term.Term == nil || term.Term.Kind == RDFTermBlankNode {
			return fmt.Errorf("VALUES rows must contain concrete terms")
		}
		row[variable] = *term.Term
		return nil
	}
	for !p.matchPunct("}") {
		row := make(map[string]RDFTerm, len(variables))
		if !parenthesised {
			if err := value(row, variables[0]); err != nil {
				return nil, err
			}
		} else {
			p.expectPunct("(")
			for _, variable := range variables {
				if err := value(row, variable); err != nil {
					return nil, err
				}
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

// parseFilter reads a FILTER or HAVING constraint: a bracketed expression, or
// a bare built-in call such as FILTER regex(...) or FILTER NOT EXISTS {...}.
func (p *sparqlParser) parseFilter(activeGraph *sparqlTermPattern, prefixes map[string]string) (sparqlFilter, error) {
	savedGraph := p.exprGraph
	p.exprGraph = activeGraph
	defer func() { p.exprGraph = savedGraph }()

	var (
		expr sparqlValueExpr
		err  error
	)
	if p.matchPunct("(") {
		expr, err = p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		p.expectPunct(")")
	} else {
		expr, err = p.parsePrimaryValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
	}
	return sparqlExprFilter{Expr: expr}, nil
}

// parseValueExpr parses a full SPARQL expression. One grammar serves FILTER,
// BIND, SELECT, ORDER BY, GROUP BY and HAVING, so a comparison or a boolean
// function is legal wherever an expression is, as in the spec:
//
//	Or  := And ('||' And)*
//	And := Rel ('&&' Rel)*
//	Rel := Add (op Add | [NOT] IN '(' list ')')?
//	Add := Mul (('+'|'-') Mul)*,  Mul := Unary (('*'|'/') Unary)*
//	Unary := ('!'|'+'|'-') Unary | Primary
func (p *sparqlParser) parseValueExpr(prefixes map[string]string) (sparqlValueExpr, error) {
	return p.parseOrExpr(prefixes)
}

func (p *sparqlParser) parseOrExpr(prefixes map[string]string) (sparqlValueExpr, error) {
	left, err := p.parseAndExpr(prefixes)
	if err != nil {
		return nil, err
	}
	for p.matchOperator("||") {
		right, err := p.parseAndExpr(prefixes)
		if err != nil {
			return nil, err
		}
		left = sparqlLogicalExpr{Op: "||", Left: left, Right: right}
	}
	return left, nil
}

func (p *sparqlParser) parseAndExpr(prefixes map[string]string) (sparqlValueExpr, error) {
	left, err := p.parseRelationalExpr(prefixes)
	if err != nil {
		return nil, err
	}
	for p.matchOperator("&&") {
		right, err := p.parseRelationalExpr(prefixes)
		if err != nil {
			return nil, err
		}
		left = sparqlLogicalExpr{Op: "&&", Left: left, Right: right}
	}
	return left, nil
}

func (p *sparqlParser) parseRelationalExpr(prefixes map[string]string) (sparqlValueExpr, error) {
	left, err := p.parseAdditiveExpr(prefixes)
	if err != nil {
		return nil, err
	}
	for _, op := range []string{"=", "!=", "<=", ">=", "<", ">"} {
		if p.matchOperator(op) {
			right, err := p.parseAdditiveExpr(prefixes)
			if err != nil {
				return nil, err
			}
			return sparqlCompareExpr{Op: op, Left: left, Right: right}, nil
		}
	}
	isIn := func(token sparqlToken) bool {
		return token.Type == sparqlTokenIdent && strings.EqualFold(token.Value, "IN")
	}
	negated := false
	if p.peek().Type == sparqlTokenKeyword && strings.EqualFold(p.peek().Value, "NOT") && isIn(p.peekN(1)) {
		p.next()
		negated = true
	}
	if isIn(p.peek()) {
		p.next()
		list, err := p.parseExpressionList(prefixes)
		if err != nil {
			return nil, err
		}
		return sparqlInExpr{Value: left, List: list, Negated: negated}, nil
	}
	return left, nil
}

// parseExpressionList reads '(' expr (',' expr)* ')', allowing '()'.
func (p *sparqlParser) parseExpressionList(prefixes map[string]string) ([]sparqlValueExpr, error) {
	p.expectPunct("(")
	list := make([]sparqlValueExpr, 0)
	if p.matchPunct(")") {
		return list, nil
	}
	for {
		expr, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		list = append(list, expr)
		if !p.matchPunct(",") {
			break
		}
	}
	p.expectPunct(")")
	return list, nil
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
	case p.matchOperator("!"):
		inner, err := p.parseUnaryValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		return sparqlNotExpr{Inner: inner}, nil
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
			p.aggDepth++
			inner, err := p.parseValueExpr(prefixes)
			p.aggDepth--
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
			p.aggDepth++
			inner, err := p.parseValueExpr(prefixes)
			p.aggDepth--
			if err != nil {
				return nil, err
			}
			countExpr.Inner = inner
		}
		p.expectPunct(")")
		return countExpr, nil
	}
	if p.matchKeyword("EXISTS") {
		p.existsDepth++
		group, err := p.parseEnclosedGroup(p.exprGraph, prefixes)
		p.existsDepth--
		if err != nil {
			return nil, err
		}
		return sparqlExistsExpr{Group: group, rt: p.rt}, nil
	}
	if p.peek().Type == sparqlTokenKeyword && strings.EqualFold(p.peek().Value, "NOT") {
		p.next()
		if !p.matchKeyword("EXISTS") {
			return nil, fmt.Errorf("expected EXISTS after NOT")
		}
		p.existsDepth++
		group, err := p.parseEnclosedGroup(p.exprGraph, prefixes)
		p.existsDepth--
		if err != nil {
			return nil, err
		}
		return sparqlExistsExpr{Group: group, Negated: true, rt: p.rt}, nil
	}
	if p.matchKeyword("BOUND") {
		p.expectPunct("(")
		variable := strings.TrimPrefix(p.expectType(sparqlTokenVar, "variable").Value, "?")
		p.expectPunct(")")
		return sparqlBoundExpr{Variable: variable}, nil
	}
	if p.matchKeyword("COALESCE") {
		args, err := p.parseExpressionList(prefixes)
		if err != nil {
			return nil, err
		}
		return sparqlCoalesceFuncExpr{Args: args}, nil
	}
	if p.matchKeyword("IF") {
		args, err := p.parseExpressionList(prefixes)
		if err != nil {
			return nil, err
		}
		if len(args) != 3 {
			return nil, fmt.Errorf("IF takes 3 arguments, got %d", len(args))
		}
		return sparqlIfFuncExpr{Cond: args[0], Then: args[1], Else: args[2]}, nil
	}
	if token := p.peek(); (token.Type == sparqlTokenKeyword || token.Type == sparqlTokenIdent) &&
		p.peekN(1).Type == sparqlTokenPunct && p.peekN(1).Value == "(" {
		name := strings.ToUpper(token.Value)
		fn, ok := sparqlFunctions[name]
		if !ok {
			return nil, fmt.Errorf("unsupported function %s", token.Value)
		}
		p.next()
		args, err := p.parseExpressionList(prefixes)
		if err != nil {
			return nil, err
		}
		if len(args) < fn.minArgs || (fn.maxArgs >= 0 && len(args) > fn.maxArgs) {
			return nil, fmt.Errorf("%s: wrong number of arguments (%d)", name, len(args))
		}
		return sparqlFuncExpr{Name: name, Args: args, fn: fn, rt: p.rt}, nil
	}
	if token := p.peek(); (token.Type == sparqlTokenIRI || token.Type == sparqlTokenQName) &&
		p.peekN(1).Type == sparqlTokenPunct && p.peekN(1).Value == "(" {
		return p.parseIRIFunctionCall(prefixes)
	}
	if p.matchPunct("(") {
		expr, err := p.parseValueExpr(prefixes)
		if err != nil {
			return nil, err
		}
		p.expectPunct(")")
		return expr, nil
	}

	if p.peekPunct("<<(") {
		return p.parseExprTripleTerm(prefixes)
	}
	if p.peekPunct("<<") {
		return nil, fmt.Errorf("a reified triple << >> is a pattern, not an expression; use <<( )>> or TRIPLE()")
	}
	termPattern, err := p.parseTermPattern(prefixes, true)
	if err != nil {
		return nil, err
	}
	if termPattern.Variable != "" {
		return sparqlVarExpr{Variable: termPattern.Variable}, nil
	}
	if termPattern.Term == nil || termPattern.Term.Kind == RDFTermBlankNode {
		return nil, fmt.Errorf("expected value expression")
	}
	return sparqlLiteralExpr{Term: *termPattern.Term}, nil
}

// parseIRIFunctionCall reads iriOrFunction when it is a call: a cast, or an
// extension function this engine evaluates to an error (sparql_casts.go).
func (p *sparqlParser) parseIRIFunctionCall(prefixes map[string]string) (sparqlValueExpr, error) {
	head, err := p.parseTermPattern(prefixes, false)
	if err != nil {
		return nil, err
	}
	iri := head.Term.Value
	var args []sparqlValueExpr
	p.expectPunct("(")
	p.matchKeyword("DISTINCT") // allowed by ArgList; meaningless for a scalar call
	if !p.matchPunct(")") {
		for {
			arg, err := p.parseValueExpr(prefixes)
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
			if p.matchPunct(")") {
				break
			}
			p.expectPunct(",")
		}
	}
	fn, ok := sparqlIRIFunctions[iri]
	if !ok {
		fn = unknownIRIFunction(iri)
	} else if len(args) < fn.minArgs || (fn.maxArgs >= 0 && len(args) > fn.maxArgs) {
		return nil, fmt.Errorf("<%s>: wrong number of arguments (%d)", iri, len(args))
	}
	return sparqlFuncExpr{Name: iri, Args: args, fn: fn, rt: p.rt}, nil
}

func (p *sparqlParser) parseTermPattern(prefixes map[string]string, allowLiteral bool) (sparqlTermPattern, error) {
	token := p.peek()
	switch token.Type {
	case sparqlTokenVar:
		name := strings.TrimPrefix(p.next().Value, "?")
		if p.refVars != nil && p.aggDepth == 0 {
			p.refVars[name] = true
		}
		return sparqlTermPattern{Variable: name}, nil
	case sparqlTokenIRI:
		value := p.next().Value
		if p.base != "" && !hasIRIScheme(value) {
			value = resolveIRIReference(p.base, value)
		}
		term := NewIRI(value)
		return sparqlTermPattern{Term: &term}, nil
	case sparqlTokenBlank:
		label := p.next().Value
		if label == "" {
			return sparqlTermPattern{}, fmt.Errorf("empty blank node label")
		}
		return sparqlTermPattern{Blank: label}, nil
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
			lang := p.peek()
			if lang.Type != sparqlTokenIdent && lang.Type != sparqlTokenKeyword && lang.Type != sparqlTokenBoolean {
				return sparqlTermPattern{}, fmt.Errorf("expected a language tag, got %q", lang.Value)
			}
			p.next()
			literal.Language = strings.ToLower(lang.Value)
			// LANG_DIR: a base direction after "--" must be ltr or rtl.
			if strings.Contains(literal.Language, "--") {
				if err := validateLangDir(literal.Language); err != nil {
					return sparqlTermPattern{}, err
				}
			}
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
		switch {
		case strings.ContainsAny(number, "eE"):
			datatype = builtinNamespaces["xsd"] + "double"
		case strings.Contains(number, "."):
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
	case sparqlTokenOperator:
		// A signed number is one literal in data positions (VALUES rows,
		// triple objects); in expressions the sign is parsed as an operator
		// before this is reached.
		if allowLiteral && (token.Value == "-" || token.Value == "+") && p.peekN(1).Type == sparqlTokenNumber {
			sign := p.next().Value
			term, err := p.parseTermPattern(prefixes, allowLiteral)
			if err != nil {
				return sparqlTermPattern{}, err
			}
			if sign == "-" {
				term.Term.Value = "-" + term.Term.Value
			}
			return term, nil
		}
		return sparqlTermPattern{}, fmt.Errorf("unexpected token %q", token.Value)
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

// sparqlTokenInvalid marks text the tokenizer could not read, such as an
// unterminated string, so the parser fails on it instead of guessing.
const sparqlTokenInvalid sparqlTokenType = "invalid"

func tokenizeSPARQL(query string) []sparqlToken {
	tokens := make([]sparqlToken, 0)
	emit := func(t sparqlTokenType, v string) { tokens = append(tokens, sparqlToken{Type: t, Value: v}) }
	for i := 0; i < len(query); {
		switch ch := query[i]; {
		case unicode.IsSpace(rune(ch)):
			i++
		case ch == '#':
			for i < len(query) && query[i] != '\n' {
				i++
			}
		case ch == '?' || ch == '$':
			j := i + 1
			for j < len(query) && isSPARQLIdentPart(query[j]) {
				j++
			}
			if j == i+1 && ch == '?' {
				// '?' alone is the zero-or-one path modifier.
				emit(sparqlTokenOperator, "?")
				i++
				continue
			}
			emit(sparqlTokenVar, "?"+query[i+1:j])
			i = j
		// SPARQL 1.2 delimiters. Longest match first: <<( before <<, and
		// both before the IRI and comparison readings of '<'.
		case strings.HasPrefix(query[i:], "<<("):
			emit(sparqlTokenPunct, "<<(")
			i += 3
		case strings.HasPrefix(query[i:], "<<"):
			emit(sparqlTokenPunct, "<<")
			i += 2
		case strings.HasPrefix(query[i:], ")>>"):
			emit(sparqlTokenPunct, ")>>")
			i += 3
		case strings.HasPrefix(query[i:], ">>"):
			emit(sparqlTokenPunct, ">>")
			i += 2
		case strings.HasPrefix(query[i:], "{|"):
			emit(sparqlTokenPunct, "{|")
			i += 2
		case ch == '~':
			emit(sparqlTokenPunct, "~")
			i++
		case strings.HasPrefix(query[i:], ">="):
			emit(sparqlTokenOperator, ">=")
			i += 2
		case strings.HasPrefix(query[i:], "<="):
			emit(sparqlTokenOperator, "<=")
			i += 2
		case strings.HasPrefix(query[i:], "&&"):
			emit(sparqlTokenOperator, "&&")
			i += 2
		case strings.HasPrefix(query[i:], "||"):
			emit(sparqlTokenOperator, "||")
			i += 2
		case strings.HasPrefix(query[i:], "|}"):
			emit(sparqlTokenPunct, "|}")
			i += 2
		case ch == '|':
			emit(sparqlTokenOperator, "|")
			i++
		case ch == '<' && looksLikeSPARQLIRI(query, i):
			j := i + 1
			for j < len(query) && query[j] != '>' {
				j++
			}
			raw, next := query[i+1:], len(query)
			if j < len(query) {
				raw, next = query[i+1:j], j+1
			}
			// SPARQL 1.2 processes \u escapes inside IRIs (and strings) only,
			// and never into a surrogate.
			if iri, ok := decodeSPARQLIRIEscapes(raw); ok {
				emit(sparqlTokenIRI, iri)
			} else {
				emit(sparqlTokenInvalid, raw)
			}
			i = next
		case ch == '"' || ch == '\'':
			value, next, ok := readSPARQLString(query, i)
			if !ok {
				emit(sparqlTokenInvalid, query[i:next])
			} else {
				tokens = append(tokens, sparqlToken{Type: sparqlTokenString, Value: value,
					Long: strings.HasPrefix(query[i:], `"""`) || strings.HasPrefix(query[i:], `'''`)})
			}
			i = next
		case ch == '<' || ch == '>':
			emit(sparqlTokenOperator, string(ch))
			i++
		case strings.HasPrefix(query[i:], "!="):
			emit(sparqlTokenOperator, "!=")
			i += 2
		case strings.HasPrefix(query[i:], "^^"):
			emit(sparqlTokenOperator, "^^")
			i += 2
		case strings.ContainsRune("{}().,;=*[]", rune(ch)):
			tokenType := sparqlTokenPunct
			if ch == '=' || ch == '*' {
				tokenType = sparqlTokenOperator
			}
			emit(tokenType, string(ch))
			i++
		case ch == '+' || ch == '-' || ch == '/' || ch == '!' || ch == '^':
			emit(sparqlTokenOperator, string(ch))
			i++
		case ch == '@':
			emit(sparqlTokenOperator, "@")
			i++
		case isSPARQLNumberStart(query, i):
			j := i + 1
			for j < len(query) && (unicode.IsDigit(rune(query[j])) || (query[j] == '.' && j+1 < len(query) && unicode.IsDigit(rune(query[j+1])))) {
				j++
			}
			// An exponent makes the number a double: 1e3, 2.5E-2.
			if j < len(query) && (query[j] == 'e' || query[j] == 'E') {
				k := j + 1
				if k < len(query) && (query[k] == '+' || query[k] == '-') {
					k++
				}
				if k < len(query) && unicode.IsDigit(rune(query[k])) {
					for k < len(query) && unicode.IsDigit(rune(query[k])) {
						k++
					}
					j = k
				}
			}
			emit(sparqlTokenNumber, query[i:j])
			i = j
		default:
			j := i + 1
			for j < len(query) {
				if isSPARQLWordPart(query[j]) {
					j++
					continue
				}
				// PN_LOCAL_ESC: a backslash escapes a punctuation character
				// into the local part of a prefixed name.
				if query[j] == '\\' && j+1 < len(query) && strings.IndexByte(sparqlLocalEscapes, query[j+1]) >= 0 &&
					strings.Contains(query[i:j], ":") {
					j += 2
					continue
				}
				break
			}
			// A name never ends in '.': the dot ends the triple instead
			// (PN_LOCAL and BLANK_NODE_LABEL both forbid a trailing dot),
			// unless the dot is escaped.
			for j > i+1 && query[j-1] == '.' && query[j-2] != '\\' {
				j--
			}
			if cut := sequencePathCut(query[i:j]); cut > 0 {
				j = i + cut
			}
			value := query[i:j]
			switch {
			case strings.HasPrefix(value, "_:"):
				// BLANK_NODE_LABEL has no colon after the prefix and no
				// escapes; an empty label is refused by the parser.
				if strings.ContainsAny(value[2:], ":\\") {
					emit(sparqlTokenInvalid, value)
				} else {
					emit(sparqlTokenBlank, value[2:])
				}
			case strings.Contains(value, ":") && !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://"):
				emit(sparqlTokenQName, unescapeSPARQLLocal(value))
			case strings.EqualFold(value, "true") || strings.EqualFold(value, "false"):
				emit(sparqlTokenBoolean, strings.ToLower(value))
			case isSPARQLKeyword(value):
				emit(sparqlTokenKeyword, value)
			default:
				emit(sparqlTokenIdent, value)
			}
			i = j
		}
	}
	tokens = append(tokens, sparqlToken{Type: sparqlTokenEOF, Value: ""})
	return tokens
}

// decodeSPARQLIRIEscapes decodes \uXXXX and \UXXXXXXXX in an IRIREF.
func decodeSPARQLIRIEscapes(raw string) (string, bool) {
	if !strings.Contains(raw, `\`) {
		return raw, true
	}
	var b strings.Builder
	for i := 0; i < len(raw); {
		if raw[i] != '\\' {
			b.WriteByte(raw[i])
			i++
			continue
		}
		if i+1 >= len(raw) || (raw[i+1] != 'u' && raw[i+1] != 'U') {
			return "", false
		}
		width := 4
		if raw[i+1] == 'U' {
			width = 8
		}
		if i+2+width > len(raw) {
			return "", false
		}
		code, err := strconv.ParseUint(raw[i+2:i+2+width], 16, 32)
		if err != nil || (code >= 0xD800 && code <= 0xDFFF) || code > 0x10FFFF {
			return "", false
		}
		b.WriteRune(rune(code))
		i += 2 + width
	}
	return b.String(), true
}

var sequencePathNext = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_.\-]*)?:`)

// sequencePathCut finds where a prefixed name ends and a sequence path
// continues: in ex:p/ex:q the '/' is the path operator. The tokenizer lets
// '/' into names, which users of this store rely on for IRIs like ex:a/b, so
// only a '/' followed by another prefixed name is taken as the operator — the
// reading SPARQL gives it, since a local name cannot hold a raw '/' at all.
func sequencePathCut(word string) int {
	if !strings.Contains(word, ":") || strings.HasPrefix(word, "http://") || strings.HasPrefix(word, "https://") || strings.HasPrefix(word, "_:") {
		return 0
	}
	for k := strings.IndexByte(word, ':'); k < len(word); k++ {
		if word[k] == '/' && word[k-1] != '\\' && sequencePathNext.MatchString(word[k+1:]) {
			return k
		}
	}
	return 0
}

// readSPARQLString reads a string literal in any of SPARQL's four quotings
// (double or single quotes, each also tripled for a long string), decoding
// ECHAR and UCHAR escapes.
// ok is false for an unterminated string or an invalid escape.
func readSPARQLString(input string, start int) (string, int, bool) {
	quote := input[start]
	delim := string(quote)
	if strings.HasPrefix(input[start:], strings.Repeat(delim, 3)) {
		delim = strings.Repeat(delim, 3)
	}
	long := len(delim) == 3
	var b strings.Builder
	i := start + len(delim)
	for i < len(input) {
		if strings.HasPrefix(input[i:], delim) {
			return b.String(), i + len(delim), true
		}
		c := input[i]
		switch {
		case c == '\\':
			if i+1 >= len(input) {
				return "", len(input), false
			}
			switch e := input[i+1]; e {
			case 't':
				b.WriteByte('\t')
			case 'b':
				b.WriteByte('\b')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 'f':
				b.WriteByte('\f')
			case '"', '\'', '\\':
				b.WriteByte(e)
			case 'u', 'U':
				width := 4
				if e == 'U' {
					width = 8
				}
				if i+2+width > len(input) {
					return "", len(input), false
				}
				code, err := strconv.ParseUint(input[i+2:i+2+width], 16, 32)
				if err != nil || (code >= 0xD800 && code <= 0xDFFF) || code > 0x10FFFF {
					return "", i + 2 + width, false
				}
				b.WriteRune(rune(code))
				i += 2 + width
				continue
			default:
				return "", i + 2, false
			}
			i += 2
		case !long && (c == '\n' || c == '\r'):
			return "", i, false
		default:
			b.WriteByte(c)
			i++
		}
	}
	return "", len(input), false
}

func isSPARQLIdentPart(ch byte) bool {
	return unicode.IsLetter(rune(ch)) || unicode.IsDigit(rune(ch)) || ch == '_' || ch == '-'
}

// '%' is allowed because SPARQL allows a percent-escape in a prefixed name's
// local part, and the property-graph projection's IRIs are full of them.
// sparqlLocalEscapes are the characters PN_LOCAL_ESC may escape.
const sparqlLocalEscapes = "_~.-!$&'()*+,;=/?#@%"

// unescapeSPARQLLocal drops the backslash of each PN_LOCAL_ESC.
func unescapeSPARQLLocal(name string) string {
	if !strings.Contains(name, "\\") {
		return name
	}
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		if name[i] == '\\' && i+1 < len(name) {
			i++
		}
		b.WriteByte(name[i])
	}
	return b.String()
}

func isSPARQLWordPart(ch byte) bool {
	return isSPARQLIdentPart(ch) || ch == ':' || ch == '/' || ch == '#' || ch == '.' || ch == '%'
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
		strings.EqualFold(value, "BASE"),
		strings.EqualFold(value, "VERSION"),
		strings.EqualFold(value, "UNDEF"),
		strings.EqualFold(value, "SELECT"),
		strings.EqualFold(value, "CONSTRUCT"),
		strings.EqualFold(value, "DESCRIBE"),
		strings.EqualFold(value, "DISTINCT"),
		strings.EqualFold(value, "REDUCED"),
		strings.EqualFold(value, "FROM"),
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
