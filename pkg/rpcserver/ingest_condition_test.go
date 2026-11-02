package rpcserver

import (
	"encoding/json"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// A conditional ingest that is refused reaches the caller as
// FailedPrecondition, not as an internal fault, through the tool route and
// for a key confined to a collection, which may send if_absent and
// if_sha256.
func TestAConditionalIngestIsAFailedPrecondition(t *testing.T) {
	ks, err := authz.NewKeySet([]authz.Key{
		{ID: "notes", Secret: "notes-secret", Clearance: authz.ReadWrite, Scope: authz.Scope{Collection: "notes"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tools := rpcv1.NewToolsServiceClient(newPolicyConn(t, Options{Keys: ks}))
	ingest := func(extra map[string]any) error {
		args := map[string]any{"document_id": "n1", "collection": "notes", "content": "Orpheus plays."}
		for k, v := range extra {
			args[k] = v
		}
		raw, _ := json.Marshal(args)
		_, err := tools.CallTool(asKey("notes-secret"), &rpcv1.CallToolRequest{Name: "ingest_document", ArgsJson: string(raw)})
		return err
	}
	if err := ingest(map[string]any{"if_absent": true}); err != nil {
		t.Fatalf("a new id: %v", err)
	}
	if err := ingest(map[string]any{"if_absent": true}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("an id the brain has: %v, want FailedPrecondition", err)
	}
	if err := ingest(map[string]any{"if_sha256": "0000"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("another content hash: %v, want FailedPrecondition", err)
	}
}

// The sentinel decides, not the message: a refusal whose text would read as
// "not found" or "invalid" is still a failed precondition.
func TestToStatusMapsAFailedPreconditionByItsSentinel(t *testing.T) {
	err := toStatus(fmt.Errorf("%w: document %q not found, invalid", cortexdb.ErrPreconditionFailed, "d1"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("got %s (%v), want FailedPrecondition", status.Code(err), err)
	}
}

// Both conditions together are an invalid argument by their sentinel.
func TestToStatusMapsConflictingConditionsByTheirSentinel(t *testing.T) {
	if code := status.Code(toStatus(conflictingConditions{})); code != codes.InvalidArgument {
		t.Fatalf("got %s, want InvalidArgument", code)
	}
}

// conflictingConditions wraps the sentinel without the word "invalid", so only
// the errors.Is arm can make it InvalidArgument.
type conflictingConditions struct{}

func (conflictingConditions) Error() string { return "two conditions" }
func (conflictingConditions) Unwrap() error { return cortexdb.ErrConflictingConditions }
