package authz

import (
	"errors"
	"strings"
	"testing"
)

func namedTool(name string) ToolNameLookup {
	return func() (string, bool) { return name, true }
}

func unreadableTool() ToolNameLookup {
	return func() (string, bool) { return "", false }
}

func TestAReadOnlyKeyCanSearchButCannotIngest(t *testing.T) {
	key := Key{ID: "reader", Secret: "ro", Clearance: ReadOnly}

	// The whole point of the refinement: before it, both of these were refused
	// because CallTool was classified as a write for every tool it carried.
	for _, tool := range []string{"knowledge_search", "knowledge_memory_recall", "expand_graph"} {
		if err := key.AuthorizeCall(CallToolMethod, namedTool(tool)); err != nil {
			t.Errorf("a read-only key was refused %s: %v", tool, err)
		}
	}

	err := key.AuthorizeCall(CallToolMethod, namedTool("ingest_document"))
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("a read-only key ingested a document: %v", err)
	}
	// Naming the tool matters: "permission denied" on the one RPC that reaches
	// sixty-one tools tells an operator nothing about which one it was.
	if !strings.Contains(err.Error(), "ingest_document") {
		t.Fatalf("the denial should name the tool, got %q", err)
	}
}

func TestTheToolNamedInACallDecidesTheClearanceItNeeds(t *testing.T) {
	reader := Key{ID: "reader", Secret: "ro", Clearance: ReadOnly}
	writer := Key{ID: "writer", Secret: "rw", Clearance: ReadWrite}

	for _, name := range ClassifiedTools() {
		access, ok := LookupTool(name)
		if !ok {
			t.Fatalf("%s vanished from the catalogue between listing and lookup", name)
		}
		if err := writer.AuthorizeCall(CallToolMethod, namedTool(name)); err != nil {
			t.Errorf("a read-write key was refused %s: %v", name, err)
		}
		err := reader.AuthorizeCall(CallToolMethod, namedTool(name))
		if access == Write && err == nil {
			t.Errorf("a read-only key was allowed %s, which writes", name)
		}
		if access == Read && err != nil {
			t.Errorf("a read-only key was refused %s, which only reads: %v", name, err)
		}
	}
}

func TestACallNamingAToolThatDoesNotExistIsDenied(t *testing.T) {
	// Fail closed. The handler would reject the name a moment later with
	// "unknown tool", but letting the request through means the handler is
	// doing the security, and a tool added there and not here would then be
	// reachable by any clearance.
	key := Key{ID: "root", Secret: "s", Clearance: ReadWrite}
	err := key.AuthorizeCall(CallToolMethod, namedTool("drop_everything"))
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("an unknown tool was allowed: %v", err)
	}
	if !strings.Contains(err.Error(), "drop_everything") {
		t.Fatalf("the denial should name the tool, got %q", err)
	}
}

func TestACallWhoseToolNameCannotBeReadIsDenied(t *testing.T) {
	key := Key{ID: "root", Secret: "s", Clearance: ReadWrite}
	for what, lookup := range map[string]ToolNameLookup{
		"an unreadable request": unreadableTool(),
		"no lookup at all":      nil,
		"an empty name":         namedTool(""),
	} {
		if err := key.AuthorizeCall(CallToolMethod, lookup); !errors.Is(err, ErrDenied) {
			t.Errorf("%s was allowed through: %v", what, err)
		}
	}
}

func TestAConfinedKeyCannotCallToolsAtAll(t *testing.T) {
	// A CallTool request carries a name and a string of JSON. The row checks
	// read proto fields, so they see the string and not the user_id inside it
	// — and every tool, memory_search included, is reachable through here. A
	// confinement that cannot be checked is refused rather than dropped.
	key := Key{ID: "hermes", Secret: "h", Clearance: ReadWrite, Scope: Scope{UserID: "hermes"}}
	err := key.AuthorizeRows(CallToolMethod, func(string) (string, []string, bool) {
		// Deliberately generous: even a lookup that claims the request carries
		// exactly the right user_id must not unlock this method.
		return "hermes", nil, true
	})
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("a confined key reached the whole toolbox: %v", err)
	}
	if !strings.Contains(err.Error(), "opaque JSON") {
		t.Fatalf("the denial should say why the scope cannot be checked, got %q", err)
	}

	// An unconfined key is unaffected — this is a scope rule, not a ban.
	unconfined := Key{ID: "root", Secret: "s", Clearance: ReadWrite}
	if err := unconfined.AuthorizeRows(CallToolMethod, nil); err != nil {
		t.Fatalf("an unconfined key was refused CallTool: %v", err)
	}
}

func TestEveryOtherMethodIsStillDecidedByTheMethodTable(t *testing.T) {
	// AuthorizeCall must be a drop-in for AuthorizeMethod everywhere else, or
	// routing the interceptor through it would quietly change unrelated RPCs.
	for _, clearance := range []Clearance{ReadOnly, ReadWrite} {
		key := Key{ID: "k", Secret: "s", Clearance: clearance}
		for _, method := range ClassifiedMethods() {
			if method == CallToolMethod {
				continue
			}
			viaCall := key.AuthorizeCall(method, namedTool("ingest_document"))
			viaMethod := key.AuthorizeMethod(method)
			if (viaCall == nil) != (viaMethod == nil) {
				t.Errorf("%s for a %s key: AuthorizeCall says %v, AuthorizeMethod says %v",
					method, clearance, viaCall, viaMethod)
			}
		}
	}
}

func TestTheToolCatalogueIsNotEmpty(t *testing.T) {
	// Without this the tests above pass vacuously if the catalogue ever fails
	// to build — every loop over it would simply have nothing to check.
	if len(ClassifiedTools()) == 0 {
		t.Fatal("the tool catalogue is empty; every per-tool test above would pass on nothing")
	}
}

func TestSparqlCountsAsAWriteBecauseItCanInsertAndDelete(t *testing.T) {
	// Both doors onto the same executor: the tool and the typed RPC. Leaving
	// either as a read would make the other one pointless, because a read-only
	// key would just use whichever still let INSERT DATA through.
	key := Key{ID: "reader", Secret: "ro", Clearance: ReadOnly}
	if err := key.AuthorizeCall(CallToolMethod, namedTool("knowledge_graph_query")); !errors.Is(err, ErrDenied) {
		t.Errorf("a read-only key was handed SPARQL through the toolbox: %v", err)
	}
	if err := key.AuthorizeMethod("/cortexdb.v1.KnowledgeGraphService/QuerySparql"); !errors.Is(err, ErrDenied) {
		t.Errorf("a read-only key was handed SPARQL through the typed RPC: %v", err)
	}
}
