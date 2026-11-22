package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
	"github.com/liliang-cn/cortexdb/v2/pkg/rpcserver"
)

// globalSearchTestBrain is this binary's server, with every extra tool main
// serves, over a brain holding two groups of three-entity cliques.
func globalSearchTestBrain(t *testing.T) rpcv1.ToolsServiceClient {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "brain.db")
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var ents []cortexdb.ToolEntityInput
	var rels []cortexdb.ToolRelationInput
	for _, group := range []string{"Storage", "Network"} {
		for i := 0; i < 4; i++ {
			ents = append(ents, cortexdb.ToolEntityInput{Name: fmt.Sprintf("%s%d", group, i), Type: "concept"})
			for j := 0; j < i; j++ {
				rels = append(rels, cortexdb.ToolRelationInput{From: fmt.Sprintf("%s%d", group, j), To: fmt.Sprintf("%s%d", group, i), Type: "related_to"})
			}
		}
	}
	if _, err := db.GraphRAGTools().UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: ents}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GraphRAGTools().UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: rels}); err != nil {
		t.Fatal(err)
	}
	keys, err := authz.NewKeySet([]authz.Key{
		{ID: "operator", Secret: "op-secret", Clearance: authz.ReadWrite},
		{ID: "reader", Secret: "ro-secret", Clearance: authz.ReadOnly},
		{ID: "tenant", Secret: "tenant-secret", Clearance: authz.ReadWrite, Scope: authz.Scope{Collection: "acme"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := rpcserver.NewWithPolicy(db, rpcserver.Options{Keys: keys, DBPath: dbPath,
		ExtraTools: append(importTools(db), globalSearchTools(db)...)})
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
		_ = db.Close()
	})
	return rpcv1.NewToolsServiceClient(conn)
}

func callGlobal(client rpcv1.ToolsServiceClient, secret, name, args string) (*rpcv1.CallToolResponse, error) {
	return client.CallTool(asImportKey(secret), &rpcv1.CallToolRequest{Name: name, ArgsJson: args})
}

// Both tools are listed, each with a reason for a confined key, and the build
// is the write.
func TestGlobalSearchToolsAreServedAndClassified(t *testing.T) {
	client := globalSearchTestBrain(t)
	defs := graphflow.GlobalSearchToolDefinitions()
	if len(defs) != len(globalSearchToolRefusals) {
		t.Errorf("graphflow defines %d global-search tools and %d have a refusal", len(defs), len(globalSearchToolRefusals))
	}
	for _, d := range defs {
		if globalSearchToolRefusals[d.Name] == "" {
			t.Errorf("%s has no reason to give a confined key", d.Name)
		}
		if d.Mutates != (d.Name == "build_community_hierarchy") {
			t.Errorf("%s: Mutates = %v", d.Name, d.Mutates)
		}
	}
	list, err := client.ListTools(asImportKey("ro-secret"), &rpcv1.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, tool := range list.GetTools() {
		listed[tool.GetName()] = true
	}
	for _, d := range defs {
		if !listed[d.Name] {
			t.Errorf("%s is not listed", d.Name)
		}
	}
}

// A read-only key searches but never builds: before a build it is told to
// build, the build itself is refused to it, and after an operator builds it
// gets the reports.
func TestGlobalSearchIsAReadAndTheBuildAWrite(t *testing.T) {
	client := globalSearchTestBrain(t)

	_, err := callGlobal(client, "ro-secret", "global_search", `{"query":"storage","build_if_empty":true}`)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("global_search before any build = %v, want FailedPrecondition and no build", err)
	}
	if _, err := callGlobal(client, "ro-secret", "build_community_hierarchy", `{}`); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a read-only key's build = %v, want PermissionDenied", err)
	}
	if _, err := callGlobal(client, "op-secret", "build_community_hierarchy", `{"min_size":2}`); err != nil {
		t.Fatalf("operator build: %v", err)
	}
	resp, err := callGlobal(client, "ro-secret", "global_search", `{"query":"storage"}`)
	if err != nil {
		t.Fatalf("read-only global_search after the build: %v", err)
	}
	var out graphflow.GlobalSearchResult
	if err := json.Unmarshal([]byte(resp.GetResultJson()), &out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != "no_model" || len(out.SupportingPoints) == 0 {
		t.Fatalf("global_search answered mode %q with %d points", out.Mode, len(out.SupportingPoints))
	}
	if _, err := callGlobal(client, "ro-secret", "global_search", `{"query":`); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("malformed arguments = %v, want InvalidArgument", err)
	}
}

// A confined key is refused both: the reports span every collection.
func TestGlobalSearchToolsAreRefusedToAConfinedKey(t *testing.T) {
	client := globalSearchTestBrain(t)
	for name, args := range map[string]string{"global_search": `{"query":"storage"}`, "build_community_hierarchy": `{}`} {
		if _, err := callGlobal(client, "tenant-secret", name, args); status.Code(err) != codes.PermissionDenied {
			t.Errorf("%s for a confined key = %v, want PermissionDenied", name, err)
		}
	}
}
