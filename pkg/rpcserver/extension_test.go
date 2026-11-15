package rpcserver

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// extensionTools is a pair a workflow package might register: one tool that
// only computes and one that writes through the facade.
func extensionTools(db func() *cortexdb.DB) []Tool {
	return []Tool{
		{
			Definition: cortexdb.ToolDefinition{Name: "ext_echo", Description: "echo", InputSchema: map[string]any{"type": "object"}},
			Call: func(_ context.Context, args json.RawMessage) (any, error) {
				return map[string]any{"args": json.RawMessage(args)}, nil
			},
		},
		{
			Definition:      cortexdb.ToolDefinition{Name: "ext_save", Description: "save", InputSchema: map[string]any{"type": "object"}, Mutates: true},
			ConfinedRefusal: "writes a document in a collection of its own choosing",
			Call: func(ctx context.Context, args json.RawMessage) (any, error) {
				var in struct {
					Fail string `json:"fail"`
				}
				_ = json.Unmarshal(args, &in)
				switch in.Fail {
				case "status":
					return nil, status.Error(codes.FailedPrecondition, "not now")
				case "plain":
					return nil, errors.New("boom")
				}
				return db().SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{KnowledgeID: "ext-doc", Content: "written by an extension tool"})
			},
		},
	}
}

func extensionKeys(t *testing.T) *authz.KeySet {
	t.Helper()
	ks, err := authz.NewKeySet([]authz.Key{
		{ID: "reader", Secret: "ro-secret", Clearance: authz.ReadOnly},
		{ID: "operator", Secret: "op-secret", Clearance: authz.ReadWrite},
		{ID: "tenant", Secret: "tenant-secret", Clearance: authz.ReadWrite, Scope: authz.Scope{Collection: "acme"}},
		{ID: "hermes", Secret: "hermes-secret", Clearance: authz.ReadWrite, Scope: authz.Scope{UserID: "hermes"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

func TestExtraToolsAreListedAndCalledBesideTheToolbox(t *testing.T) {
	ctx := context.Background()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "ext.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	extras, err := newExtraTools(extensionTools(func() *cortexdb.DB { return db }))
	if err != nil {
		t.Fatal(err)
	}
	svc := &toolsService{db: db, extras: extras}

	list, err := svc.ListTools(ctx, &rpcv1.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	// The catalogue is the toolbox's, in its order, then the extras in theirs.
	core := len(db.GraphRAGTools().Definitions())
	if got := len(list.GetTools()); got != core+2 {
		t.Fatalf("listed %d tools, want the toolbox's %d and 2 extras", got, core)
	}
	if a, b := list.GetTools()[core].GetName(), list.GetTools()[core+1].GetName(); a != "ext_echo" || b != "ext_save" {
		t.Fatalf("extras listed as %s, %s", a, b)
	}

	resp, err := svc.CallTool(ctx, &rpcv1.CallToolRequest{Name: "ext_echo", ArgsJson: `{"x":1}`})
	if err != nil || resp.GetResultJson() != `{"args":{"x":1}}` {
		t.Fatalf("ext_echo = %q, %v", resp.GetResultJson(), err)
	}
	// No arguments reach the tool as an empty object, as they do a toolbox tool.
	resp, err = svc.CallTool(ctx, &rpcv1.CallToolRequest{Name: "ext_echo"})
	if err != nil || resp.GetResultJson() != `{"args":{}}` {
		t.Fatalf("ext_echo with no arguments = %q, %v", resp.GetResultJson(), err)
	}
	if _, err := svc.CallTool(ctx, &rpcv1.CallToolRequest{Name: "ext_save"}); err != nil {
		t.Fatal(err)
	}
	if got, err := db.GetKnowledge(ctx, cortexdb.KnowledgeGetRequest{KnowledgeID: "ext-doc"}); err != nil || got == nil {
		t.Fatalf("the extension's write did not land: %v", err)
	}

	// A status the tool chose is kept; any other error is mapped as a facade
	// error is.
	_, err = svc.CallTool(ctx, &rpcv1.CallToolRequest{Name: "ext_save", ArgsJson: `{"fail":"status"}`})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("a tool's own status: %v", err)
	}
	_, err = svc.CallTool(ctx, &rpcv1.CallToolRequest{Name: "ext_save", ArgsJson: `{"fail":"plain"}`})
	if status.Code(err) != codes.Internal {
		t.Fatalf("a plain error: %v", err)
	}
	// The toolbox still answers for its own names and for unknown ones.
	if _, err := svc.CallTool(ctx, &rpcv1.CallToolRequest{Name: "ext_missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("an unknown tool: %v", err)
	}
}

func TestExtraToolsAreAuthorizedByTheirOwnLine(t *testing.T) {
	// The tools write to a database of their own: what is under test is who
	// may call them.
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "side.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tools := extensionTools(func() *cortexdb.DB { return db })
	conn := newPolicyConn(t, Options{Keys: extensionKeys(t), ExtraTools: tools})
	client := rpcv1.NewToolsServiceClient(conn)

	// Full key: both. Read-only key: the read, not the write.
	for _, name := range []string{"ext_echo", "ext_save"} {
		if _, err := client.CallTool(asKey("op-secret"), &rpcv1.CallToolRequest{Name: name}); err != nil {
			t.Errorf("the operator was refused %s: %v", name, err)
		}
	}
	if _, err := client.CallTool(asKey("ro-secret"), &rpcv1.CallToolRequest{Name: "ext_echo"}); err != nil {
		t.Errorf("a read-only key was refused a reading extra tool: %v", err)
	}
	_, err = client.CallTool(asKey("ro-secret"), &rpcv1.CallToolRequest{Name: "ext_save"})
	if status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), "ext_save writes") {
		t.Errorf("a read-only key and a writing extra tool: %v", err)
	}

	// Confined keys, on either store: refused both, with the tool's reason
	// or the default.
	for _, secret := range []string{"tenant-secret", "hermes-secret"} {
		_, err = client.CallTool(asKey(secret), &rpcv1.CallToolRequest{Name: "ext_save", ArgsJson: `{"collection":"acme","user_id":"hermes"}`})
		if status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), "writes a document in a collection of its own choosing") {
			t.Errorf("%s and a writing extra tool: %v", secret, err)
		}
		_, err = client.CallTool(asKey(secret), &rpcv1.CallToolRequest{Name: "ext_echo"})
		if status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), "no scope field confines") {
			t.Errorf("%s and a reading extra tool: %v", secret, err)
		}
	}

	// No key at all is still no access.
	if _, err := client.CallTool(context.Background(), &rpcv1.CallToolRequest{Name: "ext_echo"}); status.Code(err) != codes.Unauthenticated {
		t.Errorf("no key: %v", err)
	}
	// Every key sees the extras listed.
	list, err := client.ListTools(asKey("ro-secret"), &rpcv1.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, tool := range list.GetTools() {
		listed[tool.GetName()] = true
	}
	if !listed["ext_echo"] || !listed["ext_save"] || !listed["knowledge_search"] {
		t.Errorf("the catalogue is missing tools: ext_echo=%v ext_save=%v knowledge_search=%v", listed["ext_echo"], listed["ext_save"], listed["knowledge_search"])
	}
}

// An interceptor that was not given the extras refuses them: the policy that
// does not know a name does not let it through.
func TestAnInterceptorWithoutTheExtrasRefusesThem(t *testing.T) {
	ks := extensionKeys(t)
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "ext.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tools := extensionTools(func() *cortexdb.DB { return db })
	served := func(context.Context, any) (any, error) { return "served", nil }
	info := &grpc.UnaryServerInfo{FullMethod: authz.CallToolMethod}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer op-secret"))
	req := &rpcv1.CallToolRequest{Name: "ext_echo"}

	_, err = AuthInterceptor(ks, db)(ctx, req, info, served)
	if status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), "not a tool this server defines") {
		t.Fatalf("without the extras: %v", err)
	}
	with, err := AuthInterceptorWithTools(authz.StaticKeySource(ks), db, tools)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := with(ctx, req, info, served); err != nil || out != "served" {
		t.Fatalf("with the extras: %v, %v", out, err)
	}
}

func TestAnExtraToolSetThatCannotBeServedIsRefusedAtStartup(t *testing.T) {
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "ext.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ok := func(context.Context, json.RawMessage) (any, error) { return nil, nil }
	for want, tools := range map[string][]Tool{
		"no name":                    {{Call: ok}},
		"no handler":                 {{Definition: cortexdb.ToolDefinition{Name: "ext_a"}}},
		"registered twice":           {{Definition: cortexdb.ToolDefinition{Name: "ext_a"}, Call: ok}, {Definition: cortexdb.ToolDefinition{Name: "ext_a"}, Call: ok}},
		"the name of a toolbox tool": {{Definition: cortexdb.ToolDefinition{Name: "ingest_document"}, Call: ok}},
	} {
		if _, err := NewWithPolicy(db, Options{ExtraTools: tools}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
		if _, err := AuthInterceptorWithTools(authz.StaticKeySource(nil), db, tools); err == nil {
			t.Errorf("%s: the interceptor took the set", want)
		}
	}
	// New, which reports nothing, comes up refusing every call.
	srv := New(db, Options{ExtraTools: []Tool{{Call: ok}}})
	defer srv.Stop()
	if len(srv.GetServiceInfo()) != 0 {
		t.Errorf("a server with an unservable extra tool registered services: %v", srv.GetServiceInfo())
	}
}
