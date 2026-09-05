package rpcserver

// The ledger over gRPC.
//
// pkg/cortexdb already proves the ledger answers correctly; these are about
// the wire and the policy. Two things a library test cannot see: that the
// fields a reader acts on survive the encoding — a premise's grade, and
// whether the premise is a fact or a thing — and that recording is a write
// while asking why is a read, on both entry points a client has.

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// decisionLedger serves a store seeded with two services and the relation
// between them, so a decision has both a thing and a fact to rest on.
func decisionLedger(t *testing.T) rpcv1.DecisionServiceClient {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "ledger.db")
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	upsertNode(t, db, "svc:ledger", "Service", "ledger-svc", map[string]any{
		cortexdb.KeyGrade: cortexdb.GradeVerified, cortexdb.KeySource: "runbook",
	})
	upsertNode(t, db, "svc:riskd", "Service", "riskd", map[string]any{
		cortexdb.KeyGrade: cortexdb.GradeAsserted, cortexdb.KeySource: "runbook",
	})
	if err := db.Graph().UpsertEdge(ctx, &graph.GraphEdge{
		ID: "fact:depends", FromNodeID: "svc:ledger", ToNodeID: "svc:riskd",
		EdgeType: "DEPENDS_ON", Weight: 1,
		Properties: map[string]any{
			cortexdb.KeyGrade: cortexdb.GradeAsserted, cortexdb.KeySource: "runbook",
		},
	}); err != nil {
		t.Fatalf("upsert edge: %v", err)
	}

	lis := bufconn.Listen(1 << 20)
	srv := New(db, Options{DBPath: dbPath})
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
	return rpcv1.NewDecisionServiceClient(conn)
}

// TestAChainOverTheWireCarriesEachPremisesGrade is the point of the service.
// A list of premise ids is a list of ids; a list with each premise's grade on
// it is why the decision was reasonable.
func TestAChainOverTheWireCarriesEachPremisesGrade(t *testing.T) {
	client := decisionLedger(t)
	ctx := context.Background()

	rec, err := client.RecordDecision(ctx, &rpcv1.RecordDecisionRequest{
		Id: "hold", Kind: "review", Actor: "liliang",
		Note:     "Held the ledger-svc release: riskd's rule source is unverified.",
		Verdict:  "hold",
		Subject:  "svc:ledger",
		Premises: []string{"svc:riskd", "fact:depends"},
		At:       "2026-03-01T09:00:00Z",
	})
	if err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	if got := rec.GetDecision().GetId(); got != "decision:hold" {
		t.Errorf("id = %q, want the prefixed form", got)
	}
	// The contract keys the ledger stamps by default. Without them the entry
	// is invisible to contract_tally, which is most of the reason a decision
	// is a graph record rather than a side table.
	if d := rec.GetDecision(); d.GetGrade() != cortexdb.GradeVerified || d.GetProducer() != cortexdb.ProducerHuman {
		t.Errorf("decision contract = grade %q producer %q, want verified/human", d.GetGrade(), d.GetProducer())
	}

	chain, err := client.DecisionChain(ctx, &rpcv1.DecisionChainRequest{Id: "hold"})
	if err != nil {
		t.Fatalf("DecisionChain: %v", err)
	}
	if chain.GetRoot() != "decision:hold" || len(chain.GetDecisions()) != 1 {
		t.Fatalf("chain = %+v", chain)
	}
	byID := map[string]*rpcv1.DecisionPremise{}
	for _, p := range chain.GetDecisions()[0].GetPremises() {
		byID[p.GetId()] = p
	}
	if len(byID) != 2 {
		t.Fatalf("premises = %v, want two", byID)
	}
	thing := byID["svc:riskd"]
	if thing.GetEdge() || thing.GetGrade() != cortexdb.GradeAsserted {
		t.Errorf("node premise = %+v, want a node graded asserted", thing)
	}
	// The premise that is a fact. Its grade must be the edge's own, not that
	// of the node its based_on edge had to be anchored on: svc:ledger is
	// verified and the fact is only asserted, so reading the wrong one shows
	// up here rather than being plausible.
	fact := byID["fact:depends"]
	if !fact.GetEdge() {
		t.Fatalf("the fact premise arrived flagged as a node: %+v", fact)
	}
	if fact.GetGrade() != cortexdb.GradeAsserted || fact.GetType() != "DEPENDS_ON" {
		t.Errorf("fact premise = %+v, want the edge's own grade and type", fact)
	}
	if fact.GetFrom() != "svc:ledger" || fact.GetTo() != "svc:riskd" {
		t.Errorf("fact premise ends = %q -> %q", fact.GetFrom(), fact.GetTo())
	}
}

// TestTheServiceRefusesADecisionThatRestsOnNothing. Fail closed, over the wire
// too — a Rust client must get the same refusal a Go caller does, rather than
// a second copy of the rule that drifted.
func TestTheServiceRefusesADecisionThatRestsOnNothing(t *testing.T) {
	client := decisionLedger(t)
	ctx := context.Background()

	for name, req := range map[string]*rpcv1.RecordDecisionRequest{
		"unknown premise": {Id: "x", Actor: "liliang", Note: "n", Premises: []string{"svc:ghost"}},
		"empty actor":     {Id: "x", Note: "n"},
		"empty note":      {Id: "x", Actor: "liliang"},
		"bad timestamp":   {Id: "x", Actor: "liliang", Note: "n", At: "March"},
	} {
		if _, err := client.RecordDecision(ctx, req); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// Nothing written, so the chain has no root to walk.
	if _, err := client.DecisionChain(ctx, &rpcv1.DecisionChainRequest{Id: "x"}); err == nil {
		t.Error("a refused decision left something behind")
	}
}

// TestPrecedentsOverTheWireIsOrderedAndSaysWhenItTruncates.
func TestPrecedentsOverTheWireIsOrderedAndSaysWhenItTruncates(t *testing.T) {
	client := decisionLedger(t)
	ctx := context.Background()

	for _, d := range []struct{ id, at string }{
		{"r1", "2026-01-01T00:00:00Z"},
		{"r2", "2026-01-02T00:00:00Z"},
		{"r3", "2026-01-03T00:00:00Z"},
	} {
		if _, err := client.RecordDecision(ctx, &rpcv1.RecordDecisionRequest{
			Id: d.id, Kind: "review", Actor: "liliang", Note: "decision " + d.id,
			Subject: "svc:ledger", At: d.at,
		}); err != nil {
			t.Fatalf("record %s: %v", d.id, err)
		}
	}

	all, err := client.Precedents(ctx, &rpcv1.PrecedentsRequest{Kind: "review"})
	if err != nil {
		t.Fatalf("Precedents: %v", err)
	}
	if all.GetTruncated() || all.GetCount() != 3 {
		t.Errorf("precedents = count %d truncated %v, want 3/false", all.GetCount(), all.GetTruncated())
	}
	if got := all.GetDecisions()[0].GetId(); got != "decision:r3" {
		t.Errorf("first precedent = %q, want the newest", got)
	}

	capped, err := client.Precedents(ctx, &rpcv1.PrecedentsRequest{Subject: "svc:ledger", Limit: 2})
	if err != nil {
		t.Fatalf("Precedents capped: %v", err)
	}
	if !capped.GetTruncated() {
		t.Error("truncated = false on a capped list; the caller cannot tell it is not seeing everything")
	}
	if len(capped.GetDecisions()) != 2 || capped.GetDecisions()[0].GetId() != "decision:r3" {
		t.Errorf("capped = %+v, want the two newest", capped.GetDecisions())
	}

	// Neither kind nor subject is refused rather than meaning the whole
	// ledger, which on a shared brain is other people's.
	if _, err := client.Precedents(ctx, &rpcv1.PrecedentsRequest{}); err == nil {
		t.Error("an empty precedents query returned the whole ledger")
	}
}

// TestAReadOnlyKeyCanAskWhyButCannotDecide is the classification these RPCs
// exist to make possible: an auditor reads the ledger and cannot append to it.
// A ledger a reader could write is not a ledger.
func TestAReadOnlyKeyCanAskWhyButCannotDecide(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: readerAndWriter(t)})
	client := rpcv1.NewDecisionServiceClient(conn)

	if _, err := client.RecordDecision(asKey("op-secret"), &rpcv1.RecordDecisionRequest{
		Id: "d", Kind: "review", Actor: "liliang", Note: "Held it.",
	}); err != nil {
		t.Fatalf("the operator could not record a decision: %v", err)
	}

	// Reads, allowed.
	if _, err := client.DecisionChain(asKey("ro-secret"), &rpcv1.DecisionChainRequest{Id: "d"}); err != nil {
		t.Fatalf("a read-only key was refused a decision chain: %v", err)
	}
	if _, err := client.Precedents(asKey("ro-secret"), &rpcv1.PrecedentsRequest{Kind: "review"}); err != nil {
		t.Fatalf("a read-only key was refused precedents: %v", err)
	}

	// The write, refused.
	_, err := client.RecordDecision(asKey("ro-secret"), &rpcv1.RecordDecisionRequest{
		Id: "forged", Actor: "nobody", Note: "not yours",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a read-only key recorded a decision: %v", err)
	}
}

// TestTheDecisionToolsAreClassifiedThroughCallToolToo. Refining only the typed
// RPC would be theatre: a read-only key refused RecordDecision would call the
// decision_record tool instead.
func TestTheDecisionToolsAreClassifiedThroughCallToolToo(t *testing.T) {
	conn := newPolicyConn(t, Options{Keys: readerAndWriter(t)})
	tools := rpcv1.NewToolsServiceClient(conn)

	if _, err := tools.CallTool(asKey("op-secret"), &rpcv1.CallToolRequest{
		Name:     "decision_record",
		ArgsJson: `{"id":"d","kind":"review","actor":"liliang","note":"Held it."}`,
	}); err != nil {
		t.Fatalf("the operator could not call decision_record: %v", err)
	}
	if _, err := tools.CallTool(asKey("ro-secret"), &rpcv1.CallToolRequest{
		Name:     "decision_chain",
		ArgsJson: `{"id":"d"}`,
	}); err != nil {
		t.Fatalf("a read-only key was refused decision_chain: %v", err)
	}
	if _, err := tools.CallTool(asKey("ro-secret"), &rpcv1.CallToolRequest{
		Name:     "decision_precedents",
		ArgsJson: `{"kind":"review"}`,
	}); err != nil {
		t.Fatalf("a read-only key was refused decision_precedents: %v", err)
	}
	_, err := tools.CallTool(asKey("ro-secret"), &rpcv1.CallToolRequest{
		Name:     "decision_record",
		ArgsJson: `{"id":"forged","actor":"nobody","note":"not yours"}`,
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a read-only key recorded a decision through CallTool: %v", err)
	}
}
