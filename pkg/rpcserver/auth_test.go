package rpcserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

func passthrough(ctx context.Context, req any) (any, error) { return "ok", nil }

func bearer(secret string) context.Context {
	return metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("authorization", "Bearer "+secret))
}

func methodInfo(full string) *grpc.UnaryServerInfo {
	return &grpc.UnaryServerInfo{FullMethod: full}
}

const (
	healthMethod = "/cortexdb.v1.AdminService/Health"
	searchMethod = "/cortexdb.v1.MemoryService/SearchMemory"
	deleteMethod = "/cortexdb.v1.MemoryService/DeleteMemory"
)

func TestNoKeysConfiguredLeavesTheServerOpen(t *testing.T) {
	ic := authInterceptor(nil)
	if _, err := ic(context.Background(), nil, methodInfo(healthMethod), passthrough); err != nil {
		t.Fatalf("expected open access, got %v", err)
	}
}

func TestTheLegacyTokenStillGrantsEverything(t *testing.T) {
	ic := authInterceptor(authz.LegacyToken("s3cret"))
	ctx := bearer("s3cret")
	for _, method := range authz.ClassifiedMethods() {
		// CallTool is the one method whose request the policy has to read: it
		// is authorized by the tool it names, so a nil request is refused for
		// naming nothing. Every other method is decided from the table alone,
		// which is why nil is enough for them.
		var req any
		if method == authz.CallToolMethod {
			req = &rpcv1.CallToolRequest{Name: "ingest_document"}
		}
		if _, err := ic(ctx, req, methodInfo(method), passthrough); err != nil {
			t.Errorf("legacy token refused %s: %v", method, err)
		}
	}
}

func TestMissingAuthorizationMetadataIsUnauthenticated(t *testing.T) {
	ic := authInterceptor(authz.LegacyToken("s3cret"))
	_, err := ic(context.Background(), nil, methodInfo(healthMethod), passthrough)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want UNAUTHENTICATED, got %v", err)
	}
}

func TestAnAuthorizationHeaderWithoutTheBearerPrefixIsUnauthenticated(t *testing.T) {
	ic := authInterceptor(authz.LegacyToken("s3cret"))
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("authorization", "s3cret"))
	_, err := ic(ctx, nil, methodInfo(healthMethod), passthrough)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want UNAUTHENTICATED, got %v", err)
	}
}

func TestAWrongTokenIsUnauthenticated(t *testing.T) {
	ic := authInterceptor(authz.LegacyToken("s3cret"))
	_, err := ic(bearer("nope"), nil, methodInfo(healthMethod), passthrough)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want UNAUTHENTICATED, got %v", err)
	}
}

func TestAnUnclassifiedMethodIsDeniedRatherThanAllowed(t *testing.T) {
	keys, err := authz.NewKeySet([]authz.Key{
		{ID: "root", Secret: "s", Clearance: authz.ReadWrite},
	})
	if err != nil {
		t.Fatal(err)
	}
	ic := authInterceptor(keys)
	_, err = ic(bearer("s"), nil, methodInfo("/cortexdb.v1.FutureService/DropEverything"), passthrough)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("want PERMISSION_DENIED for an unclassified method, got %v", err)
	}
	// The refusal has to say why, or an operator adding an RPC sees only a
	// permission error and goes looking for the wrong thing.
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "not classified") {
		t.Fatalf("denial should say the method is unclassified, got %q", msg)
	}
}

func TestTheKeyPolicysOwnRefusalsCarryTheDeniedSentinel(t *testing.T) {
	key := authz.Key{ID: "reader", Secret: "ro", Clearance: authz.ReadOnly}
	if err := key.AuthorizeMethod(deleteMethod); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("expected an authz.ErrDenied, got %v", err)
	}
}

func TestAReadOnlyKeyIsRefusedAWriteAndAllowedARead(t *testing.T) {
	keys, err := authz.NewKeySet([]authz.Key{
		{ID: "reader", Secret: "ro", Clearance: authz.ReadOnly},
	})
	if err != nil {
		t.Fatal(err)
	}
	ic := authInterceptor(keys)

	req := &rpcv1.SearchMemoryRequest{Query: "coffee", UserId: "hermes"}
	if _, err := ic(bearer("ro"), req, methodInfo(searchMethod), passthrough); err != nil {
		t.Fatalf("a read-only key was refused a read: %v", err)
	}
	del := &rpcv1.DeleteMemoryRequest{MemoryId: "m1"}
	_, err = ic(bearer("ro"), del, methodInfo(deleteMethod), passthrough)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("want PERMISSION_DENIED on a write, got %v", err)
	}
}

func TestAConfinedKeyIsDeniedARequestThatIsNotAProtoMessage(t *testing.T) {
	// The interceptor's contract is that it can read the request. Anything it
	// cannot read is a denial, not a pass.
	keys, err := authz.NewKeySet([]authz.Key{
		{ID: "hermes", Secret: "h", Clearance: authz.ReadWrite,
			Scope: authz.Scope{UserID: "hermes"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ic := authInterceptor(keys)
	_, err = ic(bearer("h"), "not a proto message", methodInfo(searchMethod), passthrough)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("want PERMISSION_DENIED, got %v", err)
	}
}

func TestTheScopeCheckReadsNestedRetrievalPlanFilters(t *testing.T) {
	keys, err := authz.NewKeySet([]authz.Key{
		{ID: "hermes", Secret: "h", Clearance: authz.ReadOnly,
			Scope: authz.Scope{UserID: "hermes"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ic := authInterceptor(keys)

	// Top-level user_id says hermes, but the plan the handler also consults
	// names somebody else. Which of the two wins downstream is not something
	// an interceptor should have to know, so a disagreement is refused.
	req := &rpcv1.SearchMemoryRequest{
		Query:  "coffee",
		UserId: "hermes",
		Plan: &rpcv1.RetrievalPlan{
			Filters: &rpcv1.RetrievalFilters{UserId: "zeus"},
		},
	}
	_, err = ic(bearer("h"), req, methodInfo(searchMethod), passthrough)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("want PERMISSION_DENIED for a contradicting nested filter, got %v", err)
	}

	req.Plan.Filters.UserId = "hermes"
	if _, err := ic(bearer("h"), req, methodInfo(searchMethod), passthrough); err != nil {
		t.Fatalf("an agreeing nested filter was refused: %v", err)
	}
}

func TestAKeyFileThatCannotBeLoadedProducesAServerThatRefusesEverything(t *testing.T) {
	srv := New(nil, Options{KeyFile: "/nonexistent/keys.json"})
	if srv == nil {
		t.Fatal("New returned nil")
	}
	if _, err := NewWithPolicy(nil, Options{KeyFile: "/nonexistent/keys.json"}); err == nil {
		t.Fatal("NewWithPolicy hid a key-file load failure")
	}
	srv.Stop()
}
