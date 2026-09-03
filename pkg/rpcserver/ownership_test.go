package rpcserver

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// ownershipKeys is hermesAndZeus plus a collection-confined key, because
// knowledge documents carry a collection and none of the memory scope fields.
func ownershipKeys(t *testing.T) *authz.KeySet {
	t.Helper()
	ks, err := authz.NewKeySet([]authz.Key{
		{ID: "operator", Secret: "op-secret", Clearance: authz.ReadWrite},
		{ID: "hermes", Secret: "hermes-secret", Clearance: authz.ReadWrite,
			Scope: authz.Scope{UserID: "hermes"}},
		{ID: "notes", Secret: "notes-secret", Clearance: authz.ReadWrite,
			Scope: authz.Scope{Collection: "notes"}},
	})
	if err != nil {
		t.Fatalf("build keys: %v", err)
	}
	return ks
}

// sameRefusal asserts that two errors are indistinguishable to a caller, which
// is the whole of the "do not leak existence" requirement: if the answer for a
// row that exists but is somebody else's differs in any way from the answer for
// a row that does not exist, a confined key can enumerate ids to map the brain.
//
// The ids themselves are blanked before comparing, because each message quotes
// the id the caller just sent — its own input coming back is not a disclosure.
func sameRefusal(t *testing.T, what string, forbidden error, forbiddenID string, missing error, missingID string) {
	t.Helper()
	f, m := status.Convert(forbidden), status.Convert(missing)
	if f.Code() != codes.NotFound {
		t.Fatalf("%s: want NOT_FOUND for another's row, got %v", what, forbidden)
	}
	fMsg := strings.ReplaceAll(f.Message(), forbiddenID, "<id>")
	mMsg := strings.ReplaceAll(m.Message(), missingID, "<id>")
	if f.Code() != m.Code() || fMsg != mMsg {
		t.Fatalf("%s: a forbidden row is distinguishable from a missing one: %q vs %q",
			what, fMsg, mMsg)
	}
}

// TestAConfinedKeyGetsItsOwnMemoryButNotAnothersAndCannotTellThemApart replaces
// TestAConfinedKeyCannotReachAMemoryByIdAlone, which documented the opposite:
// that a confined key was refused GetMemory outright, including for rows it had
// written itself. That was safe and useless — an agent could not read back the
// memory it saved a moment earlier. The handler now checks the row it read.
func TestAConfinedKeyGetsItsOwnMemoryButNotAnothersAndCannotTellThemApart(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: ownershipKeys(t)})
	client := rpcv1.NewMemoryServiceClient(conn)

	if _, err := client.SaveMemory(asKey("hermes-secret"), &rpcv1.SaveMemoryRequest{
		MemoryId: "h1", UserId: "hermes", Scope: "user", Content: "Hermes takes the fast route.",
	}); err != nil {
		t.Fatalf("hermes save: %v", err)
	}
	if _, err := client.SaveMemory(asKey("op-secret"), &rpcv1.SaveMemoryRequest{
		MemoryId: "z1", UserId: "zeus", Scope: "user", Content: "Thunderbolts, second drawer.",
	}); err != nil {
		t.Fatalf("operator save: %v", err)
	}

	got, err := client.GetMemory(asKey("hermes-secret"), &rpcv1.GetMemoryRequest{MemoryId: "h1"})
	if err != nil {
		t.Fatalf("hermes was refused its own memory by id: %v", err)
	}
	if got.GetMemory().GetContent() != "Hermes takes the fast route." {
		t.Fatalf("wrong memory came back: %q", got.GetMemory().GetContent())
	}

	_, forbidden := client.GetMemory(asKey("hermes-secret"), &rpcv1.GetMemoryRequest{MemoryId: "z1"})
	_, missing := client.GetMemory(asKey("hermes-secret"), &rpcv1.GetMemoryRequest{MemoryId: "z1-does-not-exist"})
	sameRefusal(t, "GetMemory", forbidden, "z1", missing, "z1-does-not-exist")
}

func TestAConfinedKeyGetsItsOwnKnowledgeButNotAnothersAndCannotTellThemApart(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: ownershipKeys(t)})
	client := rpcv1.NewKnowledgeServiceClient(conn)

	if _, err := client.SaveKnowledge(asKey("notes-secret"), &rpcv1.SaveKnowledgeRequest{
		KnowledgeId: "k-notes", Title: "A note", Content: "Something worth keeping.",
		Collection: "notes",
	}); err != nil {
		t.Fatalf("save into the permitted collection: %v", err)
	}
	if _, err := client.SaveKnowledge(asKey("op-secret"), &rpcv1.SaveKnowledgeRequest{
		KnowledgeId: "k-secrets", Title: "Elsewhere", Content: "Not yours.",
		Collection: "secrets",
	}); err != nil {
		t.Fatalf("operator save: %v", err)
	}

	got, err := client.GetKnowledge(asKey("notes-secret"), &rpcv1.GetKnowledgeRequest{KnowledgeId: "k-notes"})
	if err != nil {
		t.Fatalf("the notes key was refused its own document by id: %v", err)
	}
	if got.GetKnowledge().GetCollection() != "notes" {
		t.Fatalf("wrong document came back: %q", got.GetKnowledge().GetId())
	}

	_, forbidden := client.GetKnowledge(asKey("notes-secret"), &rpcv1.GetKnowledgeRequest{KnowledgeId: "k-secrets"})
	_, missing := client.GetKnowledge(asKey("notes-secret"), &rpcv1.GetKnowledgeRequest{KnowledgeId: "k-secrets-does-not-exist"})
	sameRefusal(t, "GetKnowledge", forbidden, "k-secrets", missing, "k-secrets-does-not-exist")
}

func TestARefusedDeleteOrUpdateLeavesTheOtherUsersRowExactlyWhereItWas(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: ownershipKeys(t)})
	memory := rpcv1.NewMemoryServiceClient(conn)
	knowledge := rpcv1.NewKnowledgeServiceClient(conn)

	if _, err := memory.SaveMemory(asKey("op-secret"), &rpcv1.SaveMemoryRequest{
		MemoryId: "z2", UserId: "zeus", Scope: "user", Content: "Zeus's note.",
	}); err != nil {
		t.Fatalf("operator save memory: %v", err)
	}
	if _, err := knowledge.SaveKnowledge(asKey("op-secret"), &rpcv1.SaveKnowledgeRequest{
		KnowledgeId: "k-secrets", Content: "Not yours.", Collection: "secrets",
	}); err != nil {
		t.Fatalf("operator save knowledge: %v", err)
	}

	planted := "Planted by hermes."
	if _, err := memory.UpdateMemory(asKey("hermes-secret"), &rpcv1.UpdateMemoryRequest{
		MemoryId: "z2", Content: &planted,
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("hermes updated zeus's memory: %v", err)
	}
	if _, err := memory.DeleteMemory(asKey("hermes-secret"), &rpcv1.DeleteMemoryRequest{MemoryId: "z2"}); status.Code(err) != codes.NotFound {
		t.Fatalf("hermes deleted zeus's memory: %v", err)
	}
	survivor, err := memory.GetMemory(asKey("op-secret"), &rpcv1.GetMemoryRequest{MemoryId: "z2"})
	if err != nil {
		t.Fatalf("the memory did not survive the refused calls: %v", err)
	}
	if survivor.GetMemory().GetContent() != "Zeus's note." {
		t.Fatalf("the refused update went through anyway: %q", survivor.GetMemory().GetContent())
	}

	if _, err := knowledge.DeleteKnowledge(asKey("notes-secret"), &rpcv1.DeleteKnowledgeRequest{
		KnowledgeId: "k-secrets",
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("the notes key deleted another collection's document: %v", err)
	}
	if _, err := knowledge.GetKnowledge(asKey("op-secret"), &rpcv1.GetKnowledgeRequest{
		KnowledgeId: "k-secrets",
	}); err != nil {
		t.Fatalf("the document did not survive the refused delete: %v", err)
	}
}

// TestAConfinedKeyIsRefusedARowNobodyOwnsBecauseUnownedMeansTheSharedPool
// pins the fail-closed call on rows that predate scoping. A global-scope memory
// hangs off a bucket with no user_id, and a knowledge document saved without a
// collection has none: nothing on either row says it is the confined key's.
// Those rows belong to whoever had the one token before scopes existed, which
// is the operator's unconfined key, and a confined key cannot even create one —
// its own writes are forced to carry its user_id by the interceptor. So letting
// it read one could only ever hand it rows it did not write.
func TestAConfinedKeyIsRefusedARowNobodyOwnsBecauseUnownedMeansTheSharedPool(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: ownershipKeys(t)})
	memory := rpcv1.NewMemoryServiceClient(conn)
	knowledge := rpcv1.NewKnowledgeServiceClient(conn)

	if _, err := memory.SaveMemory(asKey("op-secret"), &rpcv1.SaveMemoryRequest{
		MemoryId: "g1", Scope: "global", Content: "Olympus closes at dusk.",
	}); err != nil {
		t.Fatalf("operator save of an unowned memory: %v", err)
	}
	if _, err := knowledge.SaveKnowledge(asKey("op-secret"), &rpcv1.SaveKnowledgeRequest{
		KnowledgeId: "k-loose", Content: "Filed before collections existed.",
	}); err != nil {
		t.Fatalf("operator save of an uncollected document: %v", err)
	}

	if _, err := memory.GetMemory(asKey("hermes-secret"), &rpcv1.GetMemoryRequest{MemoryId: "g1"}); status.Code(err) != codes.NotFound {
		t.Fatalf("a confined key read a memory nobody owns: %v", err)
	}
	if _, err := knowledge.GetKnowledge(asKey("notes-secret"), &rpcv1.GetKnowledgeRequest{KnowledgeId: "k-loose"}); status.Code(err) != codes.NotFound {
		t.Fatalf("a confined key read a document in no collection: %v", err)
	}

	// And the operator still reaches them, or the pre-scoping rows would be
	// unreachable to everybody, which is data loss dressed up as security.
	if _, err := memory.GetMemory(asKey("op-secret"), &rpcv1.GetMemoryRequest{MemoryId: "g1"}); err != nil {
		t.Fatalf("the operator lost the unowned memory: %v", err)
	}
	if _, err := knowledge.GetKnowledge(asKey("op-secret"), &rpcv1.GetKnowledgeRequest{KnowledgeId: "k-loose"}); err != nil {
		t.Fatalf("the operator lost the uncollected document: %v", err)
	}
}

// TestAConfinedKeyIsRefusedARowWhoseKindCannotExpressItsConfinement covers the
// cross-kind case: a user_id-confined key against knowledge, which carries no
// user_id at all. An uncheckable constraint is a refusal, not a pass.
func TestAConfinedKeyIsRefusedARowWhoseKindCannotExpressItsConfinement(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: ownershipKeys(t)})
	knowledge := rpcv1.NewKnowledgeServiceClient(conn)

	if _, err := knowledge.SaveKnowledge(asKey("op-secret"), &rpcv1.SaveKnowledgeRequest{
		KnowledgeId: "k-any", Content: "Any document at all.", Collection: "notes",
	}); err != nil {
		t.Fatalf("operator save: %v", err)
	}
	if _, err := knowledge.GetKnowledge(asKey("hermes-secret"), &rpcv1.GetKnowledgeRequest{
		KnowledgeId: "k-any",
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("a user_id-confined key read a knowledge document: %v", err)
	}
}

// TestAnUnconfinedKeyReachesEveryRowByIdExactlyAsBefore is the regression that
// matters most: every deployment in the wild is one unconfined key, either from
// a key file or synthesised from the legacy token, and none of them should
// notice that this file exists.
func TestAnUnconfinedKeyReachesEveryRowByIdExactlyAsBefore(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"a key file with an unconfined key", Options{Keys: ownershipKeys(t)}},
		{"the legacy single token", Options{Token: "op-secret"}},
		{"no authentication at all", Options{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := newPolicyConn(t, tc.opts)
			memory := rpcv1.NewMemoryServiceClient(conn)
			knowledge := rpcv1.NewKnowledgeServiceClient(conn)
			ctx := asKey("op-secret")

			if _, err := memory.SaveMemory(ctx, &rpcv1.SaveMemoryRequest{
				MemoryId: "z3", UserId: "zeus", Scope: "user", Content: "Zeus's note.",
			}); err != nil {
				t.Fatalf("save: %v", err)
			}
			if _, err := memory.GetMemory(ctx, &rpcv1.GetMemoryRequest{MemoryId: "z3"}); err != nil {
				t.Fatalf("get somebody else's memory by id: %v", err)
			}
			amended := "Zeus's amended note."
			if _, err := memory.UpdateMemory(ctx, &rpcv1.UpdateMemoryRequest{
				MemoryId: "z3", Content: &amended,
			}); err != nil {
				t.Fatalf("update: %v", err)
			}
			if del, err := memory.DeleteMemory(ctx, &rpcv1.DeleteMemoryRequest{MemoryId: "z3"}); err != nil || !del.GetDeleted() {
				t.Fatalf("delete: %v", err)
			}
			// A missing row still reports itself as missing rather than being
			// dressed up in the withheld answer, which is only for confined keys.
			if _, err := memory.GetMemory(ctx, &rpcv1.GetMemoryRequest{MemoryId: "z3"}); status.Code(err) != codes.NotFound {
				t.Fatalf("a deleted memory: %v", err)
			}

			if _, err := knowledge.SaveKnowledge(ctx, &rpcv1.SaveKnowledgeRequest{
				KnowledgeId: "k-any", Content: "Anywhere at all.", Collection: "secrets",
			}); err != nil {
				t.Fatalf("save knowledge: %v", err)
			}
			if _, err := knowledge.GetKnowledge(ctx, &rpcv1.GetKnowledgeRequest{KnowledgeId: "k-any"}); err != nil {
				t.Fatalf("get another collection's document by id: %v", err)
			}
			if _, err := knowledge.DeleteKnowledge(ctx, &rpcv1.DeleteKnowledgeRequest{KnowledgeId: "k-any"}); err != nil {
				t.Fatalf("delete knowledge: %v", err)
			}
		})
	}
}

// TestEveryIdAddressedMethodIsGuardedByAHandler walks idAddressedMethods and
// proves each one refuses a row the calling key does not own.
//
// The interceptor waives its row check for exactly this set, so a method listed
// here whose handler forgets to call guardMemory/guardKnowledge is wide open to
// every confined key on the server. The table is the waiver; this is the proof
// that something closed behind it.
func TestEveryIdAddressedMethodIsGuardedByAHandler(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: ownershipKeys(t)})
	memory := rpcv1.NewMemoryServiceClient(conn)
	knowledge := rpcv1.NewKnowledgeServiceClient(conn)

	if _, err := memory.SaveMemory(asKey("op-secret"), &rpcv1.SaveMemoryRequest{
		MemoryId: "z4", UserId: "zeus", Scope: "user", Content: "Zeus's note.",
	}); err != nil {
		t.Fatalf("operator save memory: %v", err)
	}
	if _, err := knowledge.SaveKnowledge(asKey("op-secret"), &rpcv1.SaveKnowledgeRequest{
		KnowledgeId: "k-secrets", Content: "Not yours.", Collection: "secrets",
	}); err != nil {
		t.Fatalf("operator save knowledge: %v", err)
	}

	planted := "Planted."
	probes := map[string]func() error{
		"/cortexdb.v1.MemoryService/GetMemory": func() error {
			_, err := memory.GetMemory(asKey("hermes-secret"), &rpcv1.GetMemoryRequest{MemoryId: "z4"})
			return err
		},
		"/cortexdb.v1.MemoryService/UpdateMemory": func() error {
			_, err := memory.UpdateMemory(asKey("hermes-secret"), &rpcv1.UpdateMemoryRequest{
				MemoryId: "z4", Content: &planted,
			})
			return err
		},
		"/cortexdb.v1.MemoryService/DeleteMemory": func() error {
			_, err := memory.DeleteMemory(asKey("hermes-secret"), &rpcv1.DeleteMemoryRequest{MemoryId: "z4"})
			return err
		},
		"/cortexdb.v1.KnowledgeService/GetKnowledge": func() error {
			_, err := knowledge.GetKnowledge(asKey("notes-secret"), &rpcv1.GetKnowledgeRequest{KnowledgeId: "k-secrets"})
			return err
		},
		"/cortexdb.v1.KnowledgeService/DeleteKnowledge": func() error {
			_, err := knowledge.DeleteKnowledge(asKey("notes-secret"), &rpcv1.DeleteKnowledgeRequest{KnowledgeId: "k-secrets"})
			return err
		},
	}

	for method := range idAddressedMethods {
		probe, ok := probes[method]
		if !ok {
			t.Fatalf("%s is waived by the interceptor and no test here proves a handler guards it", method)
		}
		if code := status.Code(probe()); code != codes.NotFound {
			t.Errorf("%s let a confined key at another key's row: got %v", method, code)
		}
	}
	for method := range probes {
		if _, ok := idAddressedMethods[method]; !ok {
			t.Errorf("%s is probed here but is not in idAddressedMethods", method)
		}
	}
}

// TestAConfinedKeyStillCannotReachRowsItNeverNamedById guards the boundary the
// waiver runs along: only the id-addressed RPCs defer to a handler, and every
// other RPC is still decided by the interceptor from the request alone.
func TestAConfinedKeyStillCannotReachRowsItNeverNamedById(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: ownershipKeys(t)})
	memory := rpcv1.NewMemoryServiceClient(conn)

	_, err := memory.SearchMemory(asKey("hermes-secret"), &rpcv1.SearchMemoryRequest{
		Query: "anything", UserId: "zeus", TopK: 3,
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("want PERMISSION_DENIED from the interceptor, got %v", err)
	}
	_, err = memory.SearchMemory(asKey("hermes-secret"), &rpcv1.SearchMemoryRequest{
		Query: "anything", TopK: 3,
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("an unset user_id is still every user: %v", err)
	}
}
