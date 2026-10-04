package graph

// Variable scope inside a group graph pattern.
//
// The engine evaluates a group left to right, feeding each step the solutions
// so far — a nested group, a UNION branch or an OPTIONAL starts from the outer
// rows rather than from nothing and a join afterwards. That is far cheaper,
// and for patterns it is the same answer. For expressions it is not: SPARQL
// evaluates every group bottom-up (§18.2), so a FILTER or BIND only sees the
// variables its own group binds, and a FILTER applies to its whole group
// wherever it is written in it. Fed the outer rows, a nested group's
// FILTER(?v = ?z) read an outer ?z it cannot see, { BIND(?o+1 AS ?z) } in a
// UNION branch computed from an outer ?o, and a FILTER written before the
// BIND that binds its variable ran before it.
//
// scopeGroup fixes both statically, after a group is parsed: a FILTER that
// reads a variable a later step binds moves to the end of the group, and each
// FILTER and BIND records the variables it reads that are out of scope, which
// it then evaluates as unbound. An OPTIONAL's own filters are the LeftJoin
// condition and see the outer solution too; EXISTS substitutes the outer
// solution into its whole pattern, so nothing inside it is scoped.

import (
	"fmt"
	"sort"
	"strconv"
)

// scopeGroup also refuses a BIND to a variable already in scope (§18.2.1),
// which is a syntax error, inside EXISTS as anywhere; scoped is false there.
func scopeGroup(group *sparqlGroup, activeGraph *sparqlTermPattern, optional, scoped bool) error {
	bound := make([]*varSet, len(group.Steps)+1) // bound[i]: steps before i
	bound[0] = newVarSet()
	if activeGraph != nil && activeGraph.Variable != "" {
		bound[0].add(activeGraph.Variable)
	}
	for i, step := range group.Steps {
		next := bound[i].clone()
		stepVars(step, next)
		bound[i+1] = next
	}
	all := bound[len(group.Steps)].m
	for i, raw := range group.Steps {
		if step, ok := raw.(sparqlBindStep); ok && bound[i].m[step.Variable] {
			return fmt.Errorf("BIND assigns ?%s, which is already in scope", step.Variable)
		}
	}
	if !scoped {
		return nil
	}

	var kept, deferred []sparqlStep
	for i, raw := range group.Steps {
		switch step := raw.(type) {
		case sparqlFilterStep:
			if !optional {
				step.outOfScope = missing(step.refs, all)
			}
			if len(missing(step.refs, bound[i].m)) > len(step.outOfScope) {
				deferred = append(deferred, step)
				continue
			}
			kept = append(kept, step)
		case sparqlBindStep:
			step.outOfScope = missing(step.refs, bound[i].m)
			kept = append(kept, step)
		default:
			kept = append(kept, raw)
		}
	}
	group.Steps = append(kept, deferred...)
	return nil
}

// validateSelectScope applies the SELECT clause's scope rules (§18.2.4.1):
// an alias may not name a variable already in scope, and in a grouped query
// — GROUP BY, or an aggregate anywhere in SELECT or HAVING — SELECT * is not
// allowed and every projected variable must be a group key, an earlier
// alias, or appear only inside aggregates.
func validateSelectScope(q *sparqlQuery) error {
	grouped := len(q.GroupBy) > 0 || len(q.Having) > 0
	for _, item := range q.SelectItems {
		if item.Expr.IsAggregate() {
			grouped = true
		}
	}
	// What is in scope for SELECT: the WHERE clause's variables, or in a
	// grouped query only the group keys.
	inScope := map[string]bool{}
	if grouped {
		for _, key := range q.GroupBy {
			if v, ok := key.Expr.(sparqlVarExpr); ok {
				inScope[v.Variable] = true
			} else if key.Alias != "" {
				inScope[key.Alias] = true
			}
		}
	} else {
		where := newVarSet()
		groupVars(q.Group, where)
		inScope = where.m
	}
	aliases := map[string]bool{}
	for _, item := range q.SelectItems {
		if isPlainVariable(item) {
			continue
		}
		if inScope[item.Alias] || aliases[item.Alias] {
			return fmt.Errorf("SELECT alias ?%s is already in scope", item.Alias)
		}
		aliases[item.Alias] = true
	}
	if !grouped {
		return nil
	}
	if q.SelectAll {
		return fmt.Errorf("SELECT * is not allowed with GROUP BY or aggregates")
	}
	visible := inScope
	for _, item := range q.SelectItems {
		refs := item.refs
		if isPlainVariable(item) {
			refs = []string{item.Alias}
		}
		for _, v := range refs {
			if !visible[v] {
				return fmt.Errorf("?%s is projected from a grouped query but is not a GROUP BY key", v)
			}
		}
		visible[item.Alias] = true
	}
	return nil
}

func isPlainVariable(item sparqlSelectItem) bool {
	v, ok := item.Expr.(sparqlVarExpr)
	return ok && v.Variable == item.Alias
}

// varSet is a set of variables that remembers the order they were added in,
// which is the order SELECT * projects them in.
type varSet struct {
	m     map[string]bool
	order []string
}

func newVarSet() *varSet { return &varSet{m: map[string]bool{}} }

func (s *varSet) add(v string) {
	if !s.m[v] {
		s.m[v] = true
		s.order = append(s.order, v)
	}
}

func (s *varSet) del(v string) {
	if !s.m[v] {
		return
	}
	delete(s.m, v)
	for i, o := range s.order {
		if o == v {
			s.order = append(s.order[:i:i], s.order[i+1:]...)
			break
		}
	}
}

func (s *varSet) clone() *varSet {
	c := &varSet{m: make(map[string]bool, len(s.m)), order: append([]string(nil), s.order...)}
	for v := range s.m {
		c.m[v] = true
	}
	return c
}

// selectAllVars are the variables SELECT * projects: those in scope in the
// WHERE clause, in the order they first appear (§18.2.1), then any other the
// solutions bind, sorted. Hidden variables are never projected.
func selectAllVars(group sparqlGroup, bindings []map[string]RDFTerm) []string {
	set := newVarSet()
	groupVars(group, set)
	var vars []string
	for _, v := range set.order {
		if !isHiddenVariable(v) {
			vars = append(vars, v)
		}
	}
	for _, v := range collectBindingVars(bindings) {
		if !set.m[v] {
			vars = append(vars, v)
		}
	}
	return vars
}

// missing lists the refs not in scope, sorted.
func missing(refs []string, scope map[string]bool) []string {
	var out []string
	for _, v := range refs {
		if !scope[v] {
			out = append(out, v)
		}
	}
	return out
}

// stepVars adds the variables a step may bind.
func stepVars(raw sparqlStep, into *varSet) {
	switch step := raw.(type) {
	case sparqlPatternStep:
		pt := step.Pattern
		if pt.Graph != nil {
			termVars(*pt.Graph, into)
		}
		termVars(pt.Subject, into)
		if pt.Path == nil {
			termVars(pt.Predicate, into)
		}
		termVars(pt.Object, into)
	case sparqlOptionalStep:
		groupVars(step.Group, into)
	case sparqlGroupStep:
		if step.Graph != nil {
			termVars(*step.Graph, into)
		}
		groupVars(step.Group, into)
	case sparqlUnionStep:
		for _, b := range step.Branches {
			groupVars(b, into)
		}
	case sparqlSubQueryStep:
		q := step.Query
		if q.SelectAll {
			groupVars(q.Group, into)
			into.del(step.Inner)
		}
		for _, v := range q.Vars {
			into.add(v)
		}
		for _, item := range q.SelectItems {
			if item.Alias != "" {
				into.add(item.Alias)
			}
		}
	case sparqlServiceStep:
		groupVars(step.Group, into)
		termVars(step.Endpoint, into)
	case sparqlValuesStep:
		for _, v := range step.Variables {
			into.add(v)
		}
	case sparqlBindStep:
		into.add(step.Variable)
	}
}

func groupVars(group sparqlGroup, into *varSet) {
	for _, step := range group.Steps {
		stepVars(step, into)
	}
}

func termVars(t sparqlTermPattern, into *varSet) {
	if t.Variable != "" {
		into.add(t.Variable)
	}
	if t.Triple != nil {
		termVars(t.Triple.Subject, into)
		termVars(t.Triple.Predicate, into)
		termVars(t.Triple.Object, into)
	}
}

// inScope is binding without the out-of-scope variables.
func inScope(binding map[string]RDFTerm, outOfScope []string) map[string]RDFTerm {
	if len(outOfScope) == 0 {
		return binding
	}
	out := cloneBinding(binding)
	for _, v := range outOfScope {
		delete(out, v)
	}
	return out
}

// recordRefs runs parse and returns the variables it read, sorted. Nested
// recordings add to the enclosing one.
func (p *sparqlParser) recordRefs(parse func() error) ([]string, error) {
	saved := p.refVars
	p.refVars = map[string]bool{}
	err := parse()
	refs := make([]string, 0, len(p.refVars))
	for v := range p.refVars {
		refs = append(refs, v)
		if saved != nil {
			saved[v] = true
		}
	}
	p.refVars = saved
	sort.Strings(refs)
	return refs, err
}

// hiddenGraphVariable names the graph of an enclosing GRAPH ?g inside a
// subquery or MINUS: a variable no query can write, so it is neither ?g
// itself nor projected by a SELECT *.
func (p *sparqlParser) hiddenGraphVariable() string {
	p.blankSeq++
	return hiddenVariablePrefix + " graph" + strconv.Itoa(p.blankSeq)
}
