package httpapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Over REST, under the review gate, the tool routes and the typed search
// route serve include_proposed to any key that may read, read-only
// included: the proposal is served and counted, and without the argument
// it stays hidden. A key confined to a collection is shown the proposals of
// that collection and no other.
func TestIncludeProposedOverRESTServesWhatWaitsToAnyReader(t *testing.T) {
	ks, err := authz.NewKeySet([]authz.Key{
		{ID: "curator", Secret: "curator-secret", Clearance: authz.ReadWrite},
		{ID: "reader", Secret: "reader-secret", Clearance: authz.ReadOnly},
		{ID: "notes-reader", Secret: "notes-secret", Clearance: authz.ReadOnly, Scope: authz.Scope{Collection: "notes"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "gated.db")
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath), cortexdb.WithReviewGate())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	handler, err := NewWithPolicy(db, Options{Keys: ks, DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	for _, doc := range []string{`"knowledge_id":"n1","collection":"notes"`, `"knowledge_id":"o1","collection":"other"`} {
		wantStatus(t, srv, http.MethodPost, "/v1/tools/knowledge_save", "curator-secret",
			`{`+doc+`,"content":"The heron protocol governs the ledger.","review_status":"proposed"}`, http.StatusOK)
	}
	post := func(path, secret, body string) string {
		t.Helper()
		status, payload := do(t, srv, http.MethodPost, path, secret, body)
		if status != http.StatusOK {
			t.Fatalf("%s %s as %s = %d: %s", path, body, secret, status, payload)
		}
		return string(payload)
	}
	for _, secret := range []string{"reader-secret", "curator-secret"} {
		for _, path := range []string{"/v1/tools/knowledge_search", "/v1/knowledge/search"} {
			if got := post(path, secret, `{"query":"heron ledger"}`); strings.Contains(got, `"n1"`) {
				t.Fatalf("%s as %s without include_proposed served the proposal: %s", path, secret, got)
			}
			got := post(path, secret, `{"query":"heron ledger","include_proposed":true}`)
			if !strings.Contains(got, `"n1"`) || !strings.Contains(got, `"o1"`) || !strings.Contains(got, `"_review_status":"proposed"`) {
				t.Fatalf("%s as %s with include_proposed: %s, want n1 and o1 with their status", path, secret, got)
			}
		}
		if got := post("/v1/tools/aggregate_metadata", secret, `{"type":"count","include_proposed":true}`); !strings.Contains(got, `"count":2`) {
			t.Fatalf("aggregate_metadata as %s with include_proposed: %s, want both proposals counted", secret, got)
		}
	}
	for _, path := range []string{"/v1/tools/knowledge_search", "/v1/knowledge/search"} {
		got := post(path, "notes-secret", `{"query":"heron ledger","collection":"notes","include_proposed":true}`)
		if !strings.Contains(got, `"n1"`) || strings.Contains(got, `"o1"`) {
			t.Fatalf("%s as a key confined to notes with include_proposed: %s, want n1 and not o1", path, got)
		}
		wantStatus(t, srv, http.MethodPost, path, "notes-secret", `{"query":"heron ledger","collection":"other","include_proposed":true}`, http.StatusForbidden)
	}
	if got := post("/v1/tools/aggregate_metadata", "notes-secret", `{"type":"count","collection":"notes","include_proposed":true}`); !strings.Contains(got, `"count":1`) {
		t.Fatalf("a key confined to notes counted %s, want its own proposal alone", got)
	}
	wantStatus(t, srv, http.MethodPost, "/v1/tools/aggregate_metadata", "notes-secret", `{"type":"count","collection":"other","include_proposed":true}`, http.StatusForbidden)
}
