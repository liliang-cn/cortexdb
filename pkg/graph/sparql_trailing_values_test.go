package graph

import (
	"strings"
	"testing"
)

// The ValuesClause follows the solution modifiers in SPARQL 1.1's grammar
// (SelectQuery ::= ... WhereClause SolutionModifier ValuesClause), so it can
// come after GROUP BY, HAVING, ORDER BY, LIMIT and OFFSET. Each of those
// loops used to stop only at the next modifier keyword, read VALUES as one
// more sort or group key, and fail the whole query.
func TestTrailingValuesFollowsEverySolutionModifier(t *testing.T) {
	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			loadSPARQLSpecFixture(t, b.store)
			for _, tc := range []struct {
				name, query, want string
			}{
				{"bare", `SELECT ?p ?n WHERE { ?p ex:name ?n } VALUES ?p { ex:alice }`, `"Alice"`},
				{"order by", `SELECT ?p ?n WHERE { ?p ex:name ?n } ORDER BY ?n VALUES ?p { ex:alice ex:bob }`, `"Alice","Bob"`},
				{"order by desc", `SELECT ?p ?n WHERE { ?p ex:name ?n } ORDER BY DESC(?n) VALUES ?p { ex:alice ex:bob }`, `"Bob","Alice"`},
				{"group by", `SELECT ?p (COUNT(?n) AS ?c) WHERE { ?p ex:name ?n } GROUP BY ?p VALUES ?p { ex:bob }`, ``},
				{"having", `SELECT ?p (COUNT(?n) AS ?c) WHERE { ?p ex:name ?n } GROUP BY ?p HAVING (COUNT(?n) > 0) VALUES ?p { ex:bob }`, ``},
				{"limit", `SELECT ?p ?n WHERE { ?p ex:name ?n } ORDER BY ?n LIMIT 1 VALUES ?p { ex:alice ex:bob }`, `"Alice"`},
				{"offset", `SELECT ?p ?n WHERE { ?p ex:name ?n } ORDER BY ?n OFFSET 1 VALUES ?p { ex:alice ex:bob }`, `"Bob"`},
			} {
				t.Run(tc.name, func(t *testing.T) {
					result := runSPARQL(t, b.store, tc.query)
					if tc.want == "" {
						if len(result.Bindings) != 1 || result.Bindings[0]["p"].String() != "<"+specEx+"bob>" {
							t.Fatalf("got %v, want one group for ex:bob", result.Bindings)
						}
						return
					}
					if got := strings.Join(column(result, "n"), ","); got != tc.want {
						t.Fatalf("got %s, want %s", got, tc.want)
					}
				})
			}
		})
	}
}
