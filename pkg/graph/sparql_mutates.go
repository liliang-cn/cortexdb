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

// sparqlQueryTypeMutates is the parser's own notion of an update: the query
// types after which it accepts ';' and another operation, and that the
// executor dispatches as writes. One list, not two, so an update form added
// to the parser is a write here the moment it exists — graph management
// (CLEAR, DROP, ADD, COPY, MOVE, CREATE, LOAD) arrived after this file and
// would otherwise have been a read-only key's way to drop the graph.
func sparqlQueryTypeMutates(queryType string) bool {
	return isSPARQLUpdate(queryType)
}
