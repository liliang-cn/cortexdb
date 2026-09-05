package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
)

// hermesAndZeus is the shape the shared-brain docs describe, and the same one
// pkg/rpcserver's scoped-key tests use: two agents against one brain, each
// confined to its own user_id, plus the operator's full key. Keeping the two
// fixtures identical is deliberate — when the ports disagree, these tests
// should be comparable line for line.
func hermesAndZeus(t *testing.T) *authz.KeySet {
	t.Helper()
	ks, err := authz.NewKeySet([]authz.Key{
		{ID: "operator", Secret: "op-secret", Clearance: authz.ReadWrite},
		{ID: "hermes", Secret: "hermes-secret", Clearance: authz.ReadWrite,
			Scope: authz.Scope{UserID: "hermes"}},
		{ID: "hermes-reader", Secret: "hermes-ro", Clearance: authz.ReadOnly,
			Scope: authz.Scope{UserID: "hermes"}},
		{ID: "librarian", Secret: "librarian-ro", Clearance: authz.ReadOnly},
	})
	if err != nil {
		t.Fatalf("build keys: %v", err)
	}
	return ks
}

// wantStatus sends one request as a key and insists on a status, returning the
// error message so a test can check the refusal says which rule was broken.
func wantStatus(t *testing.T, srv *httptest.Server, method, path, secret, body string, want int) string {
	t.Helper()
	status, payload := do(t, srv, method, path, secret, body)
	if status != want {
		t.Fatalf("%s %s as %q = %d, want %d: %s", method, path, secret, status, want, payload)
	}
	if want >= 400 {
		return errorMessage(t, payload)
	}
	return ""
}

func TestAReadOnlyKeyMaySearchKnowledgeButNotSaveIt(t *testing.T) {
	srv := newPolicyServer(t, Options{Keys: hermesAndZeus(t)})

	msg := wantStatus(t, srv, http.MethodPost, "/v1/knowledge", "librarian-ro",
		`{"knowledge_id":"k1","content":"Anything at all."}`, http.StatusForbidden)
	if !strings.Contains(msg, "read-only") || !strings.Contains(msg, "is a write") {
		t.Fatalf("refusal %q does not say the key is read-only and the route is a write", msg)
	}
	if !strings.Contains(msg, "POST /v1/knowledge") {
		t.Fatalf("refusal %q does not name the route it refused", msg)
	}

	// And the read it is entitled to still works, or the clearance is just a
	// more elaborate way of switching the key off.
	wantStatus(t, srv, http.MethodPost, "/v1/knowledge/search", "librarian-ro",
		`{"query":"anything","top_k":3}`, http.StatusOK)
}

// A tool call is classified by the tool actually named, from the tool's own
// declaration (cortexdb.ToolDefinition.Mutates), the same way the gRPC side
// does it. This replaced TestEveryToolCallIsAWriteUntilToolsSayOtherwise, which
// pinned the conservative placeholder: every tool a write, so a read-only
// auditor could not even read the contract tally — found the first time the
// product's own doors were run with a read-only key.
func TestAReadOnlyKeyMayCallTheToolsThatOnlyRead(t *testing.T) {
	srv := newPolicyServer(t, Options{Keys: hermesAndZeus(t)})

	wantStatus(t, srv, http.MethodPost, "/v1/tools/contract_tally", "librarian-ro", `{}`, http.StatusOK)
	wantStatus(t, srv, http.MethodPost, "/v1/tools/find_nodes", "librarian-ro", `{"query":"anything"}`, http.StatusOK)

	msg := wantStatus(t, srv, http.MethodPost, "/v1/tools/ingest_document", "librarian-ro",
		`{"document_id":"d","content":"x"}`, http.StatusForbidden)
	if !strings.Contains(msg, "ingest_document") || !strings.Contains(msg, "write") {
		t.Fatalf("refusal %q should name the tool and say it writes", msg)
	}

	// Unknown name: denied by the policy, before the handler could 404 it,
	// and naming what was asked for.
	msg = wantStatus(t, srv, http.MethodPost, "/v1/tools/search_knowledge", "librarian-ro", `{}`, http.StatusForbidden)
	if !strings.Contains(msg, "search_knowledge") {
		t.Fatalf("refusal %q should name the unknown tool", msg)
	}

	// The catalogue is not a tool call and reads no row, so it stays open.
	wantStatus(t, srv, http.MethodGet, "/v1/tools", "librarian-ro", "", http.StatusOK)
}

// SPARQL is classified by what the query does, on this port exactly as on
// gRPC: a read-only key keeps SELECT and is refused INSERT. Both doors ask the
// executor's own parser, so they cannot disagree.
func TestSPARQLOverHTTPIsClassifiedByWhatTheQueryDoes(t *testing.T) {
	srv := newPolicyServer(t, Options{Keys: hermesAndZeus(t)})
	wantStatus(t, srv, http.MethodPost, "/v1/tools/knowledge_graph_query", "librarian-ro",
		`{"query":"SELECT ?s WHERE { ?s ?p ?o }"}`, http.StatusOK)
	wantStatus(t, srv, http.MethodPost, "/v1/tools/knowledge_graph_query", "librarian-ro",
		`{"query":"INSERT DATA { <http://x/a> <http://x/b> \"c\" }"}`, http.StatusForbidden)
	wantStatus(t, srv, http.MethodPost, "/v1/tools/knowledge_graph_query", "librarian-ro",
		`{"query":"not sparql"}`, http.StatusForbidden)
}

func TestAConfinedKeyIsRefusedAnotherUsersRowsOverHTTP(t *testing.T) {
	srv := newPolicyServer(t, Options{Keys: hermesAndZeus(t)})

	wantStatus(t, srv, http.MethodPost, "/v1/memory", "op-secret",
		`{"memory_id":"z1","user_id":"zeus","scope":"user","content":"Zeus keeps the thunderbolts in the second drawer."}`,
		http.StatusOK)

	msg := wantStatus(t, srv, http.MethodPost, "/v1/memory/search", "hermes-secret",
		`{"query":"thunderbolts","user_id":"zeus","top_k":5}`, http.StatusForbidden)
	if !strings.Contains(msg, `confined to user_id="hermes"`) || !strings.Contains(msg, `asks for "zeus"`) {
		t.Fatalf("refusal %q does not name the confinement and the value asked for", msg)
	}

	// Writing into another user's rows is refused the same way, and this is
	// the one that matters: a REST port that let hermes plant a memory under
	// zeus would undo the confinement the gRPC port enforces on the same DB.
	wantStatus(t, srv, http.MethodPost, "/v1/memory", "hermes-secret",
		`{"memory_id":"z2","user_id":"zeus","scope":"user","content":"Planted by hermes."}`,
		http.StatusForbidden)

	// Its own rows are still reachable, or the confinement is useless.
	wantStatus(t, srv, http.MethodPost, "/v1/memory", "hermes-secret",
		`{"memory_id":"h1","user_id":"hermes","scope":"user","content":"Hermes prefers the fast route."}`,
		http.StatusOK)
	wantStatus(t, srv, http.MethodPost, "/v1/memory/search", "hermes-secret",
		`{"query":"route","user_id":"hermes","top_k":5}`, http.StatusOK)
}

// A body that omits the confined field is refused rather than narrowed, which
// is authz.AuthorizeRows' rule and not one this package gets to soften. An
// omitted user_id means every user, and a search that quietly answered with
// only hermes's rows would be a different answer than the one asked for, with
// nothing anywhere saying so.
func TestAConfinedKeyIsRefusedARequestThatOmitsTheConfinedField(t *testing.T) {
	srv := newPolicyServer(t, Options{Keys: hermesAndZeus(t)})

	msg := wantStatus(t, srv, http.MethodPost, "/v1/memory/search", "hermes-secret",
		`{"query":"thunderbolts","top_k":5}`, http.StatusForbidden)
	if !strings.Contains(msg, "leaves user_id unset") {
		t.Fatalf("refusal %q does not say the field was left unset", msg)
	}

	// An empty string is the same request written out longhand.
	wantStatus(t, srv, http.MethodPost, "/v1/memory/search", "hermes-secret",
		`{"query":"thunderbolts","user_id":"","top_k":5}`, http.StatusForbidden)

	// And a route that carries no scope field at all — the id-only GET — is
	// refused for the same reason, exactly as GetMemory is over gRPC.
	wantStatus(t, srv, http.MethodGet, "/v1/memory?memory_id=z1", "hermes-secret", "",
		http.StatusForbidden)
}

// A nested copy of the field that disagrees is refused even when the top-level
// one is right: the plan's filter may be what the facade ends up using, and
// rather than reason about precedence per endpoint the policy refuses the
// disagreement.
func TestANestedScopeFieldMayNotDisagreeWithTheConfinement(t *testing.T) {
	srv := newPolicyServer(t, Options{Keys: hermesAndZeus(t)})

	msg := wantStatus(t, srv, http.MethodPost, "/v1/memory/search", "hermes-secret",
		`{"query":"thunderbolts","user_id":"hermes","plan":{"user_id":"zeus"}}`,
		http.StatusForbidden)
	if !strings.Contains(msg, "nested user_id") {
		t.Fatalf("refusal %q does not say a nested copy disagreed", msg)
	}
}

// Liveness and identity read no row, so a confined key may still probe them.
// Refusing them would break the health check of every scoped deployment, and
// they expose nothing a scope could protect.
func TestAConfinedKeyMayStillProbeHealthAndInfo(t *testing.T) {
	srv := newPolicyServer(t, Options{Keys: hermesAndZeus(t)})
	wantStatus(t, srv, http.MethodGet, "/v1/health", "hermes-secret", "", http.StatusOK)
	wantStatus(t, srv, http.MethodGet, "/v1/info", "hermes-ro", "", http.StatusOK)
}

// 401 and 403 answer two different questions — "who are you" and "is this
// yours" — and an operator debugging a key needs to know which one was asked.
// Collapsing them would make a typo in a secret and a too-narrow scope look
// identical from the outside.
func TestAnUnknownKeyIs401AndAKnownKeyOutOfItsScopeIs403(t *testing.T) {
	srv := newPolicyServer(t, Options{Keys: hermesAndZeus(t)})

	wantStatus(t, srv, http.MethodPost, "/v1/memory/search", "not-a-key",
		`{"query":"anything","user_id":"hermes"}`, http.StatusUnauthorized)
	// No credential at all is the same answer: which of the two it was is not
	// a prober's business.
	wantStatus(t, srv, http.MethodPost, "/v1/memory/search", "",
		`{"query":"anything","user_id":"hermes"}`, http.StatusUnauthorized)

	wantStatus(t, srv, http.MethodPost, "/v1/memory/search", "hermes-secret",
		`{"query":"anything","user_id":"zeus"}`, http.StatusForbidden)
}

// The single-token deployment is every deployment in the wild. It maps onto one
// unconfined read-write key and must keep behaving exactly as it did before
// this package knew what a key was.
func TestTheLegacySingleTokenStillGrantsEverything(t *testing.T) {
	srv := newPolicyServer(t, Options{Token: "s3cret"})

	wantStatus(t, srv, http.MethodGet, "/v1/health", "s3cret", "", http.StatusOK)
	wantStatus(t, srv, http.MethodPost, "/v1/knowledge", "s3cret",
		`{"knowledge_id":"k1","content":"The legacy token writes."}`, http.StatusOK)
	wantStatus(t, srv, http.MethodPost, "/v1/knowledge/search", "s3cret",
		`{"query":"legacy","top_k":3}`, http.StatusOK)
	// No user_id anywhere, which a confined key would be refused for. An
	// unconfined key is not confined, so nothing is checked.
	wantStatus(t, srv, http.MethodPost, "/v1/memory", "s3cret",
		`{"memory_id":"m1","content":"Unscoped, as it always was."}`, http.StatusOK)
	wantStatus(t, srv, http.MethodDelete, "/v1/memory?memory_id=m1", "s3cret", "", http.StatusOK)
	wantStatus(t, srv, http.MethodGet, "/v1/tools", "s3cret", "", http.StatusOK)
	wantStatus(t, srv, http.MethodPost, "/v1/knowledge", "wrong", "{}", http.StatusUnauthorized)
}

// A key file is the entire policy. Honouring the token beside it would leave the
// environment variable as a master key outranking every scope in the file,
// which is the hole the file exists to close — and the REST port must not be
// where that hole reopens.
func TestAKeyFileMakesThePlainTokenStopWorking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte(`{"keys":[
		{"id":"hermes","secret":"hermes-secret","clearance":"read-write","scope":{"user_id":"hermes"}}
	]}`), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	srv := newPolicyServer(t, Options{KeyFile: path, Token: "s3cret"})

	wantStatus(t, srv, http.MethodGet, "/v1/health", "s3cret", "", http.StatusUnauthorized)
	wantStatus(t, srv, http.MethodGet, "/v1/health", "hermes-secret", "", http.StatusOK)
	wantStatus(t, srv, http.MethodPost, "/v1/memory/search", "hermes-secret",
		`{"query":"anything","user_id":"zeus"}`, http.StatusForbidden)
}

// A key file that cannot be loaded must not degrade into an open server. New
// has no error to return, so the refusal has to be the handler itself.
func TestAnUnloadableKeyFileServesNothing(t *testing.T) {
	db, _ := newTestDB(t)
	srv := httptest.NewServer(New(db, Options{KeyFile: filepath.Join(t.TempDir(), "missing.json")}))
	t.Cleanup(srv.Close)

	msg := wantStatus(t, srv, http.MethodGet, "/v1/health", "", "", http.StatusServiceUnavailable)
	if !strings.Contains(msg, "policy unavailable") {
		t.Fatalf("refusal %q does not say the policy could not be loaded", msg)
	}
}

// A route the classification table does not know about is refused, and refused
// even to the most privileged key there is. The coverage test above is what
// stops such a route from existing; this is what the server does if one ever
// does, and it is the half of the design that makes the table safe to rely on.
func TestAnUnclassifiedRouteIsDeniedRatherThanServed(t *testing.T) {
	rt := newRouter()
	rt.handle(http.MethodPost, "/v1/unclassified", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"served": true})
	})
	rt.wrapEach(authorize)
	srv := httptest.NewServer(authenticate(hermesAndZeus(t), rt))
	t.Cleanup(srv.Close)

	msg := wantStatus(t, srv, http.MethodPost, "/v1/unclassified", "op-secret", "{}",
		http.StatusForbidden)
	if !strings.Contains(msg, "not classified") {
		t.Fatalf("refusal %q does not say the route was never classified", msg)
	}
}
