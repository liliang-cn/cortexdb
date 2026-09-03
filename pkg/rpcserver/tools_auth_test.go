package rpcserver

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// readerAndWriter is the pair the tool-call policy is about: one key that may
// only read, one that may do anything, neither confined to a row scope.
func readerAndWriter(t *testing.T) *authz.KeySet {
	t.Helper()
	ks, err := authz.NewKeySet([]authz.Key{
		{ID: "reader", Secret: "ro-secret", Clearance: authz.ReadOnly},
		{ID: "operator", Secret: "op-secret", Clearance: authz.ReadWrite},
	})
	if err != nil {
		t.Fatalf("build keys: %v", err)
	}
	return ks
}

func TestAReadOnlyKeyCanCallAReadToolButNotAWriteOne(t *testing.T) {
	// The failure this fixes, end to end. cmd/cortexdb-mcp-stdio in remote
	// mode proxies every tool call through CallTool, so while CallTool was a
	// blanket write a read-only key could not search the shared brain — it
	// could not call anything at all.
	conn := newPolicyConn(t, Options{Keys: readerAndWriter(t)})
	client := rpcv1.NewToolsServiceClient(conn)

	if _, err := client.CallTool(asKey("op-secret"), &rpcv1.CallToolRequest{
		Name:     "ingest_document",
		ArgsJson: `{"document_id":"d1","content":"the brain remembers"}`,
	}); err != nil {
		t.Fatalf("the operator could not ingest: %v", err)
	}

	if _, err := client.CallTool(asKey("ro-secret"), &rpcv1.CallToolRequest{
		Name:     "knowledge_search",
		ArgsJson: `{"query":"brain","top_k":3}`,
	}); err != nil {
		t.Fatalf("a read-only key was refused a read tool: %v", err)
	}

	_, err := client.CallTool(asKey("ro-secret"), &rpcv1.CallToolRequest{
		Name:     "ingest_document",
		ArgsJson: `{"document_id":"d2","content":"not yours"}`,
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a read-only key ingested a document: %v", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "ingest_document") {
		t.Fatalf("the denial should name the tool, got %q", msg)
	}
}

func TestAToolThatOnlyLooksLikeAQueryIsStillRefusedToAReadOnlyKey(t *testing.T) {
	// knowledge_graph_query runs INSERT DATA and the DELETE forms as well as
	// SELECT, and apply_inference materialises edges. Both read as questions
	// and both write, which is exactly the pair a name heuristic gets wrong.
	conn := newPolicyConn(t, Options{Keys: readerAndWriter(t)})
	client := rpcv1.NewToolsServiceClient(conn)

	for name, args := range map[string]string{
		"knowledge_graph_query": `{"query":"SELECT ?s WHERE { ?s ?p ?o }"}`,
		"apply_inference":       `{"rules":[]}`,
	} {
		_, err := client.CallTool(asKey("ro-secret"), &rpcv1.CallToolRequest{Name: name, ArgsJson: args})
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("a read-only key was allowed %s: %v", name, err)
		}
	}
}

func TestAnUnknownToolNameIsDeniedRatherThanLeftToTheHandler(t *testing.T) {
	// Without a policy the handler answers NOT_FOUND, which is right. With one,
	// the refusal has to come first: allowing an unclassified name through
	// would make the handler's tool switch the thing deciding what a read-only
	// key may run.
	conn := newPolicyConn(t, Options{Keys: readerAndWriter(t)})
	client := rpcv1.NewToolsServiceClient(conn)

	_, err := client.CallTool(asKey("op-secret"), &rpcv1.CallToolRequest{
		Name: "drop_everything", ArgsJson: "{}",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("an unknown tool reached the handler: %v", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "drop_everything") {
		t.Fatalf("the denial should name the tool, got %q", msg)
	}

	// A CallTool with no name at all is the same denial, not a pass.
	if _, err := client.CallTool(asKey("op-secret"), &rpcv1.CallToolRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("an unnamed tool call was allowed: %v", err)
	}
}

func TestAConfinedKeyIsRefusedEveryToolCall(t *testing.T) {
	// hermes is confined to its own user_id. Tool arguments are a JSON string,
	// so the scope check cannot see the user_id inside them, and every tool —
	// memory_search included — is reachable through this one RPC. Confinement
	// must not lapse here of all places.
	conn := newPolicyConn(t, Options{Keys: hermesAndZeus(t)})
	client := rpcv1.NewToolsServiceClient(conn)

	for _, name := range []string{"memory_search", "knowledge_search", "memory_save"} {
		_, err := client.CallTool(asKey("hermes-secret"), &rpcv1.CallToolRequest{
			Name: name, ArgsJson: `{"query":"anything","user_id":"hermes"}`,
		})
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("a confined key called %s: %v", name, err)
		}
	}

	// ListTools stays open to it: the catalogue is static and names no rows.
	if _, err := client.ListTools(asKey("hermes-secret"), &rpcv1.ListToolsRequest{}); err != nil {
		t.Fatalf("a confined key could not list tools: %v", err)
	}
}

func TestACallToolRequestTheInterceptorCannotReadIsDenied(t *testing.T) {
	// The interceptor's contract is that it can read the request. A CallTool it
	// cannot read is a tool call nobody can classify, so it is refused rather
	// than handed to the toolbox to sort out.
	ic := authInterceptor(readerAndWriter(t))
	_, err := ic(bearer("op-secret"), "not a proto message",
		methodInfo(authz.CallToolMethod), passthrough)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("want PERMISSION_DENIED for an unreadable CallTool, got %v", err)
	}
}
