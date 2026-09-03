package graph

import "context"

// SPARQLMutates reports whether a query would change the graph.
//
// It exists so authorization can tell a SPARQL read from a SPARQL write, and it
// is deliberately a thin wrapper over the executor's own parser rather than a
// second one. The alternative — a policy that recognises "INSERT" and "DELETE"
// by itself — has to agree with ExecuteSPARQL forever, and the failure when it
// stops agreeing is silent in the dangerous direction: an update the policy
// read as a query.
//
// Without this the only safe classification is "every SPARQL call is a write",
// which costs a read-only key the whole query language, SELECT included.
//
// A query that does not parse is reported as mutating. It is about to fail
// anyway, and the caller learns nothing from which error it gets.
func (g *GraphStore) SPARQLMutates(ctx context.Context, query string) bool {
	parsed, err := g.parseSPARQL(ctx, query)
	if err != nil {
		return true
	}
	return sparqlQueryTypeMutates(parsed.QueryType)
}

// sparqlQueryTypeMutates lists the update forms ExecuteSPARQL dispatches on.
// Kept beside them, and asserted against them by a test, because a new update
// form added there without a line here is exactly the drift this file exists to
// prevent.
func sparqlQueryTypeMutates(queryType string) bool {
	switch queryType {
	case SPARQLQueryInsertData, SPARQLQueryDeleteData, SPARQLQueryDeleteWhere, SPARQLQueryModify:
		return true
	default:
		return false
	}
}
