package rpcserver

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// Over CallTool, under the review gate, include_proposed is served to any
// key that may read, confined or not, read-only included, on
// knowledge_search and on aggregate_metadata. The collection a confined
// key's scope forces still bounds what it shows: a proposal in another
// collection is neither served nor counted.
func TestIncludeProposedOverCallToolServesWhatWaitsToAnyReader(t *testing.T) {
	ks, err := authz.NewKeySet([]authz.Key{
		{ID: "curator", Secret: "curator-secret", Clearance: authz.ReadWrite},
		{ID: "notes", Secret: "notes-secret", Clearance: authz.ReadWrite, Scope: authz.Scope{Collection: "notes"}},
		{ID: "reader", Secret: "reader-secret", Clearance: authz.ReadOnly},
		{ID: "notes-reader", Secret: "notes-ro", Clearance: authz.ReadOnly, Scope: authz.Scope{Collection: "notes"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tools := rpcv1.NewToolsServiceClient(newPolicyConn(t, Options{Keys: ks}, cortexdb.WithReviewGate()))
	try := func(secret, name string, args map[string]any) (string, error) {
		raw, _ := json.Marshal(args)
		resp, err := tools.CallTool(asKey(secret), &rpcv1.CallToolRequest{Name: name, ArgsJson: string(raw)})
		return resp.GetResultJson(), err
	}
	call := func(secret, name string, args map[string]any) string {
		t.Helper()
		got, err := try(secret, name, args)
		if err != nil {
			t.Fatalf("%s %v as %s: %v", name, args, secret, err)
		}
		return got
	}
	for _, doc := range []struct{ id, collection string }{{"n1", "notes"}, {"o1", "other"}} {
		call("curator-secret", "knowledge_save", map[string]any{"knowledge_id": doc.id, "collection": doc.collection,
			"content": "The heron protocol governs the ledger.", "review_status": "proposed"})
	}
	for _, key := range []struct {
		secret, collection, count string
		other                     bool
	}{{"curator-secret", "", `"count":2`, true}, {"reader-secret", "", `"count":2`, true}, {"notes-secret", "notes", `"count":1`, false}, {"notes-ro", "notes", `"count":1`, false}} {
		search := map[string]any{"query": "heron ledger", "include_proposed": true}
		count := map[string]any{"type": "count", "include_proposed": true}
		if key.collection != "" {
			search["collection"], count["collection"] = key.collection, key.collection
		}
		got := call(key.secret, "knowledge_search", search)
		if !strings.Contains(got, `"knowledge_id":"n1"`) || !strings.Contains(got, `"_review_status":"proposed"`) || strings.Contains(got, `"knowledge_id":"o1"`) != key.other {
			t.Fatalf("knowledge_search as %s: %s, want n1 and its status, o1 %v", key.secret, got, key.other)
		}
		if got := call(key.secret, "aggregate_metadata", count); !strings.Contains(got, key.count) {
			t.Fatalf("aggregate_metadata as %s: %s, want %s", key.secret, got, key.count)
		}
	}
	if got := call("reader-secret", "knowledge_search", map[string]any{"query": "heron ledger"}); strings.Contains(got, `"knowledge_id"`) {
		t.Fatalf("knowledge_search without include_proposed served a proposal: %s", got)
	}
	for _, name := range []string{"knowledge_search", "aggregate_metadata"} {
		args := map[string]any{"query": "heron ledger", "type": "count", "collection": "other", "include_proposed": true}
		if name == "knowledge_search" {
			delete(args, "type")
		} else {
			delete(args, "query")
		}
		if got, err := try("notes-ro", name, args); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s on another collection as a key confined to notes: %s %v, want PermissionDenied", name, got, err)
		}
	}
}
