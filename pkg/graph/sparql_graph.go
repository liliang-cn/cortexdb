package graph

// GRAPH ?g { P }: P evaluated in each named graph, with ?g bound to it.
//
// The engine states GRAPH by pushing the graph name into every triple pattern
// inside it, so ?g is bound by whichever triple a pattern matches. That is the
// whole definition when P begins with a triple pattern — every later step then
// runs with ?g already bound, inside that one graph — and it is fast. It is
// not the definition when P binds ?g some other way or not at all: GRAPH ?g
// { VALUES ... }, GRAPH ?g { { SELECT ... } }, GRAPH ?g { } and an aggregate
// over an empty group inside GRAPH all have no triple to name the graph, so ?g
// was never bound and they answered nothing. For those, and only those, the
// group is evaluated once per named graph with ?g bound first, as the algebra
// says (SPARQL 1.1 §18.6, Graph).

import (
	"context"
	"fmt"
)

func (g *GraphStore) executeSPARQLGraphGroup(ctx context.Context, step sparqlGroupStep, bindings []map[string]RDFTerm, opts sparqlExecOptions) ([]map[string]RDFTerm, error) {
	if step.Graph == nil || step.Graph.Variable == "" || groupStartsWithPattern(step.Group) {
		return g.executeSPARQLGroup(ctx, step.Group, bindings, opts)
	}
	variable := step.Graph.Variable
	var bound, unbound []map[string]RDFTerm
	for _, b := range bindings {
		if b[variable].Kind != "" {
			bound = append(bound, b)
		} else {
			unbound = append(unbound, b)
		}
	}
	out, err := g.executeSPARQLGroup(ctx, step.Group, bound, opts)
	if err != nil {
		return nil, err
	}
	if len(unbound) == 0 {
		return out, nil
	}
	graphs, err := g.sparqlNamedGraphs(ctx, opts)
	if err != nil {
		return nil, err
	}
	for _, name := range graphs {
		rows := make([]map[string]RDFTerm, len(unbound))
		for i, b := range unbound {
			row := cloneBinding(b)
			row[variable] = name
			rows[i] = row
		}
		res, err := g.executeSPARQLGroup(ctx, step.Group, rows, opts)
		if err != nil {
			return nil, err
		}
		out = append(out, res...)
	}
	return out, nil
}

func groupStartsWithPattern(group sparqlGroup) bool {
	if len(group.Steps) == 0 {
		return false
	}
	_, ok := group.Steps[0].(sparqlPatternStep)
	return ok
}

// sparqlNamedGraphs lists the named graphs of the query's dataset: the ones
// FROM NAMED or USING NAMED declared, or else every graph the store holds a
// triple in, the property-graph projection among them while it is on.
func (g *GraphStore) sparqlNamedGraphs(ctx context.Context, opts sparqlExecOptions) ([]RDFTerm, error) {
	if opts.DatasetDeclared || len(opts.NamedGraphs) > 0 {
		return append([]RDFTerm(nil), opts.NamedGraphs...), nil
	}
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := g.query(ctx, `SELECT DISTINCT graph_kind, graph_value FROM kg_triples
		WHERE COALESCE(graph_value, '') <> '' ORDER BY graph_kind, graph_value`)
	if err != nil {
		return nil, fmt.Errorf("list named graphs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []RDFTerm
	for rows.Next() {
		var kind, value string
		if err := rows.Scan(&kind, &value); err != nil {
			return nil, err
		}
		if kind == "" {
			kind = RDFTermIRI
		}
		out = append(out, RDFTerm{Kind: kind, Value: value})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if g.PropertyGraphProjectionEnabled() {
		out = append(out, NewIRI(PropertyGraphIRI))
	}
	return out, nil
}

// executeSPARQLSubQuery joins a subquery's solutions into the outer rows. A
// subquery is evaluated on its own, not per outer row — except inside GRAPH
// ?g, where it is evaluated once for each graph the outer rows are in, with
// its hidden graph variable bound to it, so its patterns match that graph and
// only that graph.
func (g *GraphStore) executeSPARQLSubQuery(ctx context.Context, step sparqlSubQueryStep, current []map[string]RDFTerm, opts sparqlExecOptions) ([]map[string]RDFTerm, error) {
	var out []map[string]RDFTerm
	err := forEachGraph(current, step.Graph, step.Inner, func(rows []map[string]RDFTerm, seed map[string]RDFTerm) error {
		subBindings, err := g.executeSPARQLGroup(ctx, step.Query.Group, []map[string]RDFTerm{seed}, opts)
		if err != nil {
			return err
		}
		subResult, err := g.executeSPARQLSelect(ctx, step.Query, subBindings, opts)
		if err != nil {
			return err
		}
		for _, o := range rows {
			for _, i := range subResult.Bindings {
				delete(i, step.Inner)
				if merged, ok := mergeValueRow(o, i); ok {
					out = append(out, merged)
				}
			}
		}
		return nil
	})
	return out, err
}

// executeSPARQLMinus removes the rows that are compatible with, and share a
// variable with, some solution of the right-hand side.
func (g *GraphStore) executeSPARQLMinus(ctx context.Context, step sparqlMinusStep, current []map[string]RDFTerm, opts sparqlExecOptions) ([]map[string]RDFTerm, error) {
	out := make([]map[string]RDFTerm, 0, len(current))
	err := forEachGraph(current, step.Graph, step.Inner, func(rows []map[string]RDFTerm, seed map[string]RDFTerm) error {
		minus, err := g.executeSPARQLGroup(ctx, step.Group, []map[string]RDFTerm{seed}, opts)
		if err != nil {
			return err
		}
		for _, m := range minus {
			delete(m, step.Inner)
		}
		for _, row := range rows {
			removed := false
			for _, m := range minus {
				if bindingsCompatibleAndShared(row, m) {
					removed = true
					break
				}
			}
			if !removed {
				out = append(out, row)
			}
		}
		return nil
	})
	return out, err
}

// forEachGraph calls run once for all of current with an empty seed or,
// inside GRAPH ?g, once per graph the rows are in, with inner bound to it.
func forEachGraph(current []map[string]RDFTerm, graph *sparqlTermPattern, inner string, run func(rows []map[string]RDFTerm, seed map[string]RDFTerm) error) error {
	if graph == nil || graph.Variable == "" || inner == "" {
		return run(current, map[string]RDFTerm{})
	}
	byGraph := map[string][]map[string]RDFTerm{}
	var order []string
	names := map[string]RDFTerm{}
	for _, row := range current {
		name := row[graph.Variable]
		key := sparqlGraphKey(&name)
		if _, seen := byGraph[key]; !seen {
			order = append(order, key)
			names[key] = name
		}
		byGraph[key] = append(byGraph[key], row)
	}
	for _, key := range order {
		seed := map[string]RDFTerm{}
		if name := names[key]; name.Kind != "" {
			seed[inner] = name
		}
		if err := run(byGraph[key], seed); err != nil {
			return err
		}
	}
	return nil
}
