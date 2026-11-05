package rpcserver

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

func historyKeys(t *testing.T) *authz.KeySet {
	t.Helper()
	ks, err := authz.NewKeySet([]authz.Key{
		{ID: "operator", Secret: "op-secret", Clearance: authz.ReadWrite},
		{ID: "reader", Secret: "ro-secret", Clearance: authz.ReadOnly},
		{ID: "alpha", Secret: "alpha-secret", Clearance: authz.ReadOnly, Scope: authz.Scope{Collection: "alpha"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

// The history tools are reachable through the generic CallTool, which is the
// gRPC and REST surface for tools: nothing in the method table had to change.
// A read-only key calls all three and gets the history the operator wrote.
func TestAReadOnlyKeyReadsTheHistoryOverCallTool(t *testing.T) {
	tools := rpcv1.NewToolsServiceClient(newPolicyConn(t, Options{Keys: historyKeys(t)}))
	op := func(name, args string) {
		t.Helper()
		if _, err := tools.CallTool(asKey("op-secret"), &rpcv1.CallToolRequest{Name: name, ArgsJson: args}); err != nil {
			t.Fatalf("operator %s: %v", name, err)
		}
	}
	op("upsert_entities", `{"entities":[{"name":"Leo","type":"Person"},{"name":"Beijing","type":"City"},{"name":"Chengdu","type":"City"}]}`)
	op("upsert_relations", `{"relations":[{"from":"Leo","to":"Beijing","type":"lives_in"}]}`)
	op("upsert_relations", `{"relations":[{"from":"Leo","to":"Chengdu","type":"lives_in"}]}`)
	op("knowledge_graph_query", `{"query":"INSERT DATA { <urn:leo> <urn:city> <urn:beijing> }"}`)
	op("knowledge_graph_query", `{"query":"DELETE DATA { <urn:leo> <urn:city> <urn:beijing> }"}`)
	op("ontology_save", `{"schema":{"schema_id":"people","name":"People","object_types":[{"api_name":"Person","primary_key":"personId","properties":[{"api_name":"personId","data_type":{"kind":"string"},"required":true}]}]},"activate":true}`)

	call := func(secret, name, args string) (string, error) {
		resp, err := tools.CallTool(asKey(secret), &rpcv1.CallToolRequest{Name: name, ArgsJson: args})
		return resp.GetResultJson(), err
	}
	out, err := call("ro-secret", "graph_history", `{"entity":"Leo","relation":"lives_in"}`)
	if err != nil || !strings.Contains(out, "Chengdu") || !strings.Contains(out, "Beijing") || !strings.Contains(out, `"in_effect"`) {
		t.Fatalf("graph_history for a read-only key: %v: %s", err, out)
	}
	out, err = call("ro-secret", "knowledge_graph_history", `{"subject":{"kind":"iri","value":"urn:leo"}}`)
	if err != nil || !strings.Contains(out, "urn:beijing") {
		t.Fatalf("knowledge_graph_history for a read-only key should return the deleted triple: %v: %s", err, out)
	}
	out, err = call("ro-secret", "ontology_history", `{"schema_id":"people"}`)
	if err != nil || !strings.Contains(out, `"version":1`) || !strings.Contains(out, `"active":true`) {
		t.Fatalf("ontology_history for a read-only key: %v: %s", err, out)
	}
	// And the as_of reads.
	if _, err := call("ro-secret", "knowledge_graph_query", `{"query":"SELECT ?o WHERE { <urn:leo> <urn:city> ?o }","as_of":"2030-01-01T00:00:00Z"}`); err != nil {
		t.Errorf("a read-only key was refused a SELECT as of an instant: %v", err)
	}
}

// A write does not become a read by being asked about the past. A SPARQL update
// with as_of is refused to a read-only key by the policy, and to the operator by
// the tool, and in neither case does it run.
func TestAWriteUnderAsOfIsRefusedAndDoesNotRun(t *testing.T) {
	tools := rpcv1.NewToolsServiceClient(newPolicyConn(t, Options{Keys: historyKeys(t)}))
	const update = `INSERT DATA { <urn:ghost> <urn:was> <urn:here> }`
	args := `{"query":"` + update + `","as_of":"2030-01-01T00:00:00Z"}`

	_, err := tools.CallTool(asKey("ro-secret"), &rpcv1.CallToolRequest{Name: "knowledge_graph_query", ArgsJson: args})
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("a read-only key ran a SPARQL update under as_of: %v", err)
	}
	_, err = tools.CallTool(asKey("op-secret"), &rpcv1.CallToolRequest{Name: "knowledge_graph_query", ArgsJson: args})
	if err == nil {
		t.Fatal("the operator's SPARQL update under as_of ran")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "as_of") && !strings.Contains(strings.ToLower(err.Error()), "as of") {
		t.Errorf("the refusal should say why: %v", err)
	}
	resp, err := tools.CallTool(asKey("op-secret"), &rpcv1.CallToolRequest{Name: "knowledge_graph_query", ArgsJson: `{"query":"SELECT ?s WHERE { ?s <urn:was> ?o }"}`})
	if err != nil || strings.Contains(resp.GetResultJson(), "urn:ghost") {
		t.Errorf("the refused update left a triple behind: %v %s", err, resp.GetResultJson())
	}
}

// A key confined to a collection is refused the three history tools: they read
// the history tables, which no scope field confines.
func TestAConfinedKeyIsRefusedTheHistoryToolsOverCallTool(t *testing.T) {
	tools := rpcv1.NewToolsServiceClient(newPolicyConn(t, Options{Keys: historyKeys(t)}))
	for name, args := range map[string]string{
		"graph_history":           `{"entity":"Leo"}`,
		"knowledge_graph_history": `{"subject":{"kind":"iri","value":"urn:leo"}}`,
		"ontology_history":        `{"schema_id":"people"}`,
	} {
		_, err := tools.CallTool(asKey("alpha-secret"), &rpcv1.CallToolRequest{Name: name, ArgsJson: args})
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("a confined key called %s: %v", name, err)
		}
	}
}
