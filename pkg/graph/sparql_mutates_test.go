package graph

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// mutatesTestStore is a graph on a throwaway file. SPARQLMutates parses through
// the store because prefixes are namespace rows, so the parser needs one.
func mutatesTestStore(t *testing.T) *GraphStore {
	t.Helper()
	dbPath := fmt.Sprintf("test_sparql_mutates_%d.db", testname.Nano())
	t.Cleanup(func() { _ = os.Remove(dbPath) })
	store, err := core.New(dbPath, 16)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}
	return NewGraphStore(store)
}

func TestSPARQLMutatesSeparatesUpdatesFromQueries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  bool
	}{
		{"select", "SELECT ?s WHERE { ?s ?p ?o }", false},
		{"ask", "ASK { ?s ?p ?o }", false},
		{"construct", "CONSTRUCT { ?s ?p ?o } WHERE { ?s ?p ?o }", false},
		{"describe", "DESCRIBE <http://example.org/a>", false},
		{"insert data", `INSERT DATA { <http://example.org/a> <http://example.org/b> "c" }`, true},
		{"delete data", `DELETE DATA { <http://example.org/a> <http://example.org/b> "c" }`, true},
		{"delete where", "DELETE WHERE { ?s ?p ?o }", true},
		{"modify", "DELETE { ?s ?p ?o } INSERT { ?s ?p 1 } WHERE { ?s ?p ?o }", true},
		// A query that will not parse is a write, so a caller cannot smuggle an
		// update past the policy by making it unparseable to the policy alone.
		{"nonsense", "this is not sparql", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := mutatesTestStore(t)
			if got := g.SPARQLMutates(context.Background(), tc.query); got != tc.want {
				t.Fatalf("SPARQLMutates(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

// TestEverySPARQLUpdateFormIsCountedAsMutating is the anti-drift guard: every
// query type ExecuteSPARQL handles by writing must be in sparqlQueryTypeMutates.
// If a new update form is added to the executor without a line there, a
// read-only key would be allowed to run it.
func TestEverySPARQLUpdateFormIsCountedAsMutating(t *testing.T) {
	writing := []string{SPARQLQueryInsertData, SPARQLQueryDeleteData, SPARQLQueryDeleteWhere, SPARQLQueryModify}
	reading := []string{SPARQLQuerySelect, SPARQLQueryAsk, SPARQLQueryConstruct, SPARQLQueryDescribe}
	for _, qt := range writing {
		if !sparqlQueryTypeMutates(qt) {
			t.Errorf("%q writes but is not counted as mutating", qt)
		}
	}
	for _, qt := range reading {
		if sparqlQueryTypeMutates(qt) {
			t.Errorf("%q only reads but is counted as mutating", qt)
		}
	}
}
