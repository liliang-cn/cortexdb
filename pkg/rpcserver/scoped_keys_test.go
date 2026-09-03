package rpcserver

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// newPolicyConn runs a real server behind a key policy and returns a client
// connection to it. The end-to-end shape matters here: a scope check that only
// holds in a unit test of the interceptor is a scope check that has not been
// wired up.
func newPolicyConn(t *testing.T, opts Options) *grpc.ClientConn {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "scoped.db")
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	opts.DBPath = dbPath
	srv, err := NewWithPolicy(db, opts)
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufnet: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func asKey(secret string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+secret)
}

// hermesAndZeus is the shape the shared-brain docs describe: two agents against
// one brain, each confined to its own user_id, plus the operator's full key.
func hermesAndZeus(t *testing.T) *authz.KeySet {
	t.Helper()
	ks, err := authz.NewKeySet([]authz.Key{
		{ID: "operator", Secret: "op-secret", Clearance: authz.ReadWrite},
		{ID: "hermes", Secret: "hermes-secret", Clearance: authz.ReadWrite,
			Scope: authz.Scope{UserID: "hermes"}},
		{ID: "hermes-reader", Secret: "hermes-ro", Clearance: authz.ReadOnly,
			Scope: authz.Scope{UserID: "hermes"}},
	})
	if err != nil {
		t.Fatalf("build keys: %v", err)
	}
	return ks
}

func TestAKeyConfinedToOneUserCannotReadAnotherUsersMemory(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: hermesAndZeus(t)})
	client := rpcv1.NewMemoryServiceClient(conn)

	if _, err := client.SaveMemory(asKey("op-secret"), &rpcv1.SaveMemoryRequest{
		MemoryId: "z1", UserId: "zeus", Scope: "user",
		Content: "Zeus keeps the thunderbolts in the second drawer.",
	}); err != nil {
		t.Fatalf("operator save: %v", err)
	}

	_, err := client.SearchMemory(asKey("hermes-secret"), &rpcv1.SearchMemoryRequest{
		Query: "thunderbolts", UserId: "zeus", TopK: 5,
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("hermes read zeus's memory: %v", err)
	}

	// And its own rows are still reachable, or the confinement is useless.
	if _, err := client.SearchMemory(asKey("hermes-secret"), &rpcv1.SearchMemoryRequest{
		Query: "thunderbolts", UserId: "hermes", TopK: 5,
	}); err != nil {
		t.Fatalf("hermes was refused its own rows: %v", err)
	}
}

func TestAKeyConfinedToOneUserCannotWriteIntoAnotherUsersMemory(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: hermesAndZeus(t)})
	client := rpcv1.NewMemoryServiceClient(conn)

	_, err := client.SaveMemory(asKey("hermes-secret"), &rpcv1.SaveMemoryRequest{
		MemoryId: "z2", UserId: "zeus", Scope: "user",
		Content: "Planted by hermes.",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("hermes wrote into zeus's rows: %v", err)
	}

	if _, err := client.SaveMemory(asKey("hermes-secret"), &rpcv1.SaveMemoryRequest{
		MemoryId: "h1", UserId: "hermes", Scope: "user",
		Content: "Hermes prefers the fast route.",
	}); err != nil {
		t.Fatalf("hermes was refused its own write: %v", err)
	}
}

func TestAReadOnlyKeyCannotDeleteSomebodyElsesMemory(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: hermesAndZeus(t)})
	client := rpcv1.NewMemoryServiceClient(conn)

	if _, err := client.SaveMemory(asKey("op-secret"), &rpcv1.SaveMemoryRequest{
		MemoryId: "z3", UserId: "zeus", Scope: "user", Content: "Zeus's note.",
	}); err != nil {
		t.Fatalf("operator save: %v", err)
	}

	_, err := client.DeleteMemory(asKey("hermes-ro"), &rpcv1.DeleteMemoryRequest{MemoryId: "z3"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a read-only key deleted a memory: %v", err)
	}

	got, err := client.GetMemory(asKey("op-secret"), &rpcv1.GetMemoryRequest{MemoryId: "z3"})
	if err != nil || got.GetMemory().GetId() != "z3" {
		t.Fatalf("the memory did not survive the refused delete: %v", err)
	}
}

// TestAConfinedKeyCannotReachAMemoryByIdAlone documents a deliberate gap: the
// id-addressed RPCs carry no user_id, so an interceptor cannot tell whose row
// an id names without reading it first. Fail closed — a confined key is refused
// rather than allowed on a guess.
func TestAConfinedKeyCannotReachAMemoryByIdAlone(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: hermesAndZeus(t)})
	client := rpcv1.NewMemoryServiceClient(conn)

	if _, err := client.SaveMemory(asKey("hermes-secret"), &rpcv1.SaveMemoryRequest{
		MemoryId: "h2", UserId: "hermes", Scope: "user", Content: "Hermes's own note.",
	}); err != nil {
		t.Fatalf("hermes save: %v", err)
	}
	// Even its own row, because the request does not say whose it is.
	_, err := client.GetMemory(asKey("hermes-secret"), &rpcv1.GetMemoryRequest{MemoryId: "h2"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("want PERMISSION_DENIED on an id-only request, got %v", err)
	}
}

func TestAConfinedKeyCanStillAskWhetherTheServerIsUp(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: hermesAndZeus(t)})
	admin := rpcv1.NewAdminServiceClient(conn)
	resp, err := admin.Health(asKey("hermes-ro"), &rpcv1.HealthRequest{})
	if err != nil || !resp.GetOk() {
		t.Fatalf("health probe refused to a confined key: %v", err)
	}
}

func TestTheLegacySingleTokenDeploymentStillHasFullAccess(t *testing.T) {
	// The regression that would break every deployment in the wild: only
	// Options.Token set, no key file, everything still permitted.
	conn := newPolicyConn(t, Options{Token: "s3cret"})
	memory := rpcv1.NewMemoryServiceClient(conn)
	ctx := asKey("s3cret")

	if _, err := memory.SaveMemory(ctx, &rpcv1.SaveMemoryRequest{
		MemoryId: "m1", UserId: "anyone", Scope: "user", Content: "Anything at all.",
	}); err != nil {
		t.Fatalf("legacy token save: %v", err)
	}
	if _, err := memory.GetMemory(ctx, &rpcv1.GetMemoryRequest{MemoryId: "m1"}); err != nil {
		t.Fatalf("legacy token get: %v", err)
	}
	if _, err := memory.SearchMemory(ctx, &rpcv1.SearchMemoryRequest{
		Query: "anything", TopK: 3,
	}); err != nil {
		t.Fatalf("legacy token search with no user_id: %v", err)
	}
	del, err := memory.DeleteMemory(ctx, &rpcv1.DeleteMemoryRequest{MemoryId: "m1"})
	if err != nil || !del.GetDeleted() {
		t.Fatalf("legacy token delete: %v", err)
	}

	graph := rpcv1.NewKnowledgeGraphServiceClient(conn)
	if _, err := graph.ListNamespaces(ctx, &rpcv1.ListNamespacesRequest{}); err != nil {
		t.Fatalf("legacy token graph read: %v", err)
	}
}

func TestARevokedKeyIsRefused(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: hermesAndZeus(t)})
	client := rpcv1.NewMemoryServiceClient(conn)
	_, err := client.SearchMemory(asKey("a-secret-nobody-issued"), &rpcv1.SearchMemoryRequest{
		Query: "anything", UserId: "hermes",
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want UNAUTHENTICATED, got %v", err)
	}
}

func TestAKeyConfinedToACollectionCannotSearchAnother(t *testing.T) {
	ks, err := authz.NewKeySet([]authz.Key{
		{ID: "notes", Secret: "notes-secret", Clearance: authz.ReadWrite,
			Scope: authz.Scope{Collection: "notes"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	conn := newPolicyConn(t, Options{Keys: ks})
	client := rpcv1.NewKnowledgeServiceClient(conn)

	if _, err := client.SaveKnowledge(asKey("notes-secret"), &rpcv1.SaveKnowledgeRequest{
		KnowledgeId: "k1", Title: "A note", Content: "Something worth keeping.",
		Collection: "notes",
	}); err != nil {
		t.Fatalf("save into the permitted collection: %v", err)
	}
	_, err = client.SaveKnowledge(asKey("notes-secret"), &rpcv1.SaveKnowledgeRequest{
		KnowledgeId: "k2", Title: "Elsewhere", Content: "Not yours.",
		Collection: "secrets",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("wrote into another collection: %v", err)
	}
	_, err = client.SearchKnowledge(asKey("notes-secret"), &rpcv1.SearchKnowledgeRequest{
		Query: "note", Collection: "secrets", TopK: 3,
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("searched another collection: %v", err)
	}
}

func TestAConfinedKeyCannotReachTheRdfGraphAtAll(t *testing.T) {
	// The RDF store is one global graph with no user_id, scope, namespace or
	// collection on its requests, so there is nothing for a scope to check
	// against. SPARQL in particular can read everything in one query. Fail
	// closed: a confined key is refused rather than handed the whole graph.
	conn := newPolicyConn(t, Options{Keys: hermesAndZeus(t)})
	graph := rpcv1.NewKnowledgeGraphServiceClient(conn)
	_, err := graph.QuerySparql(asKey("hermes-secret"), &rpcv1.QuerySparqlRequest{
		Query: "SELECT ?s WHERE { ?s ?p ?o }",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a confined key reached the shared RDF graph: %v", err)
	}
	// The operator's unconfined key is unaffected.
	if _, err := graph.QuerySparql(asKey("op-secret"), &rpcv1.QuerySparqlRequest{
		Query: "SELECT ?s WHERE { ?s ?p ?o }",
	}); err != nil {
		t.Fatalf("operator sparql: %v", err)
	}
}

func TestAKeyFileOnDiskDrivesTheRunningServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")
	body := `{"keys":[
	  {"id":"hermes","secret":"hermes-secret","clearance":"read-only","scope":{"user_id":"hermes"}}
	]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	conn := newPolicyConn(t, Options{KeyFile: path, Token: "ignored-legacy-token"})
	client := rpcv1.NewMemoryServiceClient(conn)

	if _, err := client.SearchMemory(asKey("hermes-secret"), &rpcv1.SearchMemoryRequest{
		Query: "anything", UserId: "hermes", TopK: 3,
	}); err != nil {
		t.Fatalf("the key from the file was refused: %v", err)
	}
	_, err := client.SearchMemory(asKey("ignored-legacy-token"), &rpcv1.SearchMemoryRequest{
		Query: "anything", UserId: "hermes", TopK: 3,
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("the environment token outranked the key file: %v", err)
	}
}
