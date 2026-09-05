package rpcserver

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// The contract over gRPC.
//
// pkg/cortexdb already tests that ContractTally and NeedsAttention answer
// correctly; these tests are about the wire. What they are guarding is the
// half a library test cannot see: that the numbers a Rust client reads are the
// numbers the shelf holds, and specifically that the two the reader acts on —
// how much carries no grade at all, and how much a capped list is not showing
// — arrive rather than being flattened into a plausible-looking summary.

// contractShelf serves a store the test seeds directly through db.Graph(), so
// the records carry exactly the properties a producer writes. Going through
// the ingest RPCs instead would test the extractor, not the contract.
func contractShelf(t *testing.T, seed func(t *testing.T, db *cortexdb.DB)) rpcv1.ContractServiceClient {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "shelf.db")
	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if seed != nil {
		seed(t, db)
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
	return rpcv1.NewContractServiceClient(conn)
}

// contractProps builds the metadata a conformant producer writes.
func contractProps(grade, why string) map[string]any {
	p := map[string]any{
		cortexdb.KeyGrade:    grade,
		cortexdb.KeySource:   "川鑫家电",
		cortexdb.KeyProducer: cortexdb.ProducerLLMExtract,
		cortexdb.KeyAt:       "2026-09-05T00:00:00Z",
	}
	if why != "" {
		p[cortexdb.KeyWhy] = why
	}
	return p
}

func upsertNode(t *testing.T, db *cortexdb.DB, id, nodeType, content string, props map[string]any) {
	t.Helper()
	err := db.Graph().UpsertNode(context.Background(), &graph.GraphNode{
		ID: id, NodeType: nodeType, Content: content,
		Vector: []float32{1, 0, 0, 0}, Properties: props,
	})
	if err != nil {
		t.Fatalf("upsert node %s: %v", id, err)
	}
}

// TestContractTallyCarriesTheTwoNumbersAReaderActsOn is the point of the RPC.
//
// The five graded counts are easy and would survive almost any encoding. The
// untagged count and the undefined grade are the ones that get lost: a map
// keyed by grade drops the empty key on some encoders, and folding an
// unrecognised grade into untagged looks correct until a producer starts
// writing a vocabulary nobody defined and nothing says so.
func TestContractTallyCarriesTheTwoNumbersAReaderActsOn(t *testing.T) {
	client := contractShelf(t, func(t *testing.T, db *cortexdb.DB) {
		upsertNode(t, db, "precedent:p1", "Precedent", "铺货拉流水", contractProps(cortexdb.GradeVerified, ""))
		upsertNode(t, db, "claim:c1", "Claim", "Reuters said so", contractProps(cortexdb.GradeAsserted, ""))
		// A producer that does not write the contract at all. On a shared
		// shelf this is most of what is there.
		upsertNode(t, db, "note:old", "Note", "written before the contract", nil)
		upsertNode(t, db, "note:older", "Note", "also before", nil)
		// A grade nobody defined: a typo, a newer contract, or an invented
		// vocabulary. It has to surface, not fold into the unstamped.
		upsertNode(t, db, "note:odd", "Note", "probably true", contractProps("probably", ""))
		// An edge, because a graph's assertions are mostly edges and a tally
		// counting only nodes reports a shelf far better established than it is.
		err := db.Graph().UpsertEdge(context.Background(), &graph.GraphEdge{
			ID: "edge:held", FromNodeID: "claim:c1", ToNodeID: "precedent:p1",
			EdgeType: "about", Weight: 1,
			Properties: contractProps(cortexdb.GradeHeld, "two sources disagree"),
		})
		if err != nil {
			t.Fatalf("upsert edge: %v", err)
		}
	})

	got, err := client.ContractTally(context.Background(), &rpcv1.ContractTallyRequest{})
	if err != nil {
		t.Fatalf("tally: %v", err)
	}
	if n, e := got.GetVerified().GetNodes(), got.GetVerified().GetEdges(); n != 1 || e != 0 {
		t.Errorf("verified = %d nodes %d edges, want 1/0", n, e)
	}
	if n := got.GetAsserted().GetNodes(); n != 1 {
		t.Errorf("asserted nodes = %d, want 1", n)
	}
	// The held record is an edge. Reported as a node it would be invisible to
	// a wall that renders the two separately.
	if n, e := got.GetHeld().GetNodes(), got.GetHeld().GetEdges(); n != 0 || e != 1 {
		t.Errorf("held = %d nodes %d edges, want 0/1", n, e)
	}
	if n := got.GetUntagged().GetNodes(); n != 2 {
		t.Errorf("untagged nodes = %d, want 2 — the largest number on most shelves, and the one a five-bar chart drops", n)
	}
	unknown := got.GetUnknown()
	if len(unknown) != 1 || unknown["probably"].GetNodes() != 1 {
		t.Errorf("unknown = %v, want one entry {probably: 1 node}", unknown)
	}
	if _, ok := unknown[""]; ok {
		t.Error(`unknown carries the empty grade; untagged is its own field and reporting it twice double-counts the shelf`)
	}
}

// TestNeedsAttentionCarriesTheReasonAndTheEnds: a refusal without a reason is
// noise the reader deletes, and a held edge whose ends did not travel cannot
// be shown as the assertion it is.
func TestNeedsAttentionCarriesTheReasonAndTheEnds(t *testing.T) {
	client := contractShelf(t, func(t *testing.T, db *cortexdb.DB) {
		upsertNode(t, db, "metric:revenue", "Metric", "revenue", contractProps(cortexdb.GradeSelfConsistent, ""))
		upsertNode(t, db, "claim:c1", "Claim", "Reuters said so", contractProps(cortexdb.GradeAsserted, ""))
		upsertNode(t, db, "precedent:p2", "Precedent", "换季前动库存",
			contractProps(cortexdb.GradeRefused, "换季前不动库存"))
		err := db.Graph().UpsertEdge(context.Background(), &graph.GraphEdge{
			ID: "edge:held", FromNodeID: "claim:c1", ToNodeID: "metric:revenue",
			EdgeType: "about", Weight: 1,
			Properties: contractProps(cortexdb.GradeHeld, "two sources disagree"),
		})
		if err != nil {
			t.Fatalf("upsert edge: %v", err)
		}
	})

	got, err := client.NeedsAttention(context.Background(), &rpcv1.NeedsAttentionRequest{})
	if err != nil {
		t.Fatalf("needs attention: %v", err)
	}
	if got.GetTruncated() {
		t.Error("truncated on a shelf of two records")
	}
	if got.GetTotal() != 2 {
		t.Errorf("total = %d, want 2", got.GetTotal())
	}
	byID := map[string]*rpcv1.GradedRecord{}
	for _, r := range got.GetRecords() {
		byID[r.GetId()] = r
	}
	if len(byID) != 2 {
		t.Fatalf("records = %v, want the held edge and the refused node", byID)
	}

	// Held and refused arrive in one list, told apart by grade. Splitting them
	// into two calls is how one of them stops being rendered.
	refused := byID["precedent:p2"]
	if refused.GetGrade() != cortexdb.GradeRefused || refused.GetWhy() != "换季前不动库存" {
		t.Errorf("refused record = %+v, want grade=refused with its reason", refused)
	}
	if refused.GetEdge() {
		t.Error("refused node arrived flagged as an edge")
	}
	if refused.GetContent() != "换季前动库存" {
		t.Errorf("refused content = %q, want the node's label", refused.GetContent())
	}

	held := byID["edge:held"]
	if held.GetGrade() != cortexdb.GradeHeld || held.GetWhy() != "two sources disagree" {
		t.Errorf("held record = %+v, want grade=held with its reason", held)
	}
	if !held.GetEdge() {
		t.Error("held edge arrived flagged as a node")
	}
	if held.GetFrom() != "claim:c1" || held.GetTo() != "metric:revenue" {
		t.Errorf("held edge ends = %q -> %q, want claim:c1 -> metric:revenue", held.GetFrom(), held.GetTo())
	}
	if held.GetSource() != "川鑫家电" || held.GetProducer() != cortexdb.ProducerLLMExtract {
		t.Errorf("held provenance = source %q producer %q, want both carried", held.GetSource(), held.GetProducer())
	}
	if held.GetAt() != "2026-09-05T00:00:00Z" {
		t.Errorf("held _at = %q, want the producer's stamp verbatim", held.GetAt())
	}
}

// TestNeedsAttentionCapReportsTheTrueTotal: "3 shown of 3" and "3 shown of 9"
// are different situations, and the cap alone cannot tell them apart. A list
// that quietly stopped reads as the whole of the work.
func TestNeedsAttentionCapReportsTheTrueTotal(t *testing.T) {
	client := contractShelf(t, func(t *testing.T, db *cortexdb.DB) {
		for i := range 6 {
			upsertNode(t, db, fmt.Sprintf("held:%d", i), "Note", "waiting",
				contractProps(cortexdb.GradeHeld, "nobody has looked"))
		}
		for i := range 3 {
			upsertNode(t, db, fmt.Sprintf("refused:%d", i), "Note", "declined",
				contractProps(cortexdb.GradeRefused, "out of scope"))
		}
		// Not attention-worthy, so it must not inflate the total.
		upsertNode(t, db, "fine:1", "Note", "checked", contractProps(cortexdb.GradeVerified, ""))
	})

	got, err := client.NeedsAttention(context.Background(), &rpcv1.NeedsAttentionRequest{Limit: 4})
	if err != nil {
		t.Fatalf("needs attention: %v", err)
	}
	if len(got.GetRecords()) != 4 {
		t.Errorf("records = %d, want the 4 asked for", len(got.GetRecords()))
	}
	if !got.GetTruncated() {
		t.Error("truncated = false on a capped list; the caller cannot tell it is not seeing everything")
	}
	if got.GetTotal() != 9 {
		t.Errorf("total = %d, want 9 — the work waiting, not the rows returned", got.GetTotal())
	}
}

// TestContractOnAnEmptyStore: a brain nobody has written to is a legitimate
// answer, not a failure. A reader that has to special-case an error here will
// special-case it wrong.
func TestContractOnAnEmptyStore(t *testing.T) {
	client := contractShelf(t, nil)
	ctx := context.Background()

	tally, err := client.ContractTally(ctx, &rpcv1.ContractTallyRequest{})
	if err != nil {
		t.Fatalf("tally on empty store: %v", err)
	}
	for name, c := range map[string]*rpcv1.GradeCount{
		"verified": tally.GetVerified(), "self_consistent": tally.GetSelfConsistent(),
		"asserted": tally.GetAsserted(), "held": tally.GetHeld(),
		"refused": tally.GetRefused(), "untagged": tally.GetUntagged(),
	} {
		if c.GetNodes() != 0 || c.GetEdges() != 0 {
			t.Errorf("%s = %d nodes %d edges on an empty store", name, c.GetNodes(), c.GetEdges())
		}
	}
	if len(tally.GetUnknown()) != 0 {
		t.Errorf("unknown = %v on an empty store", tally.GetUnknown())
	}

	att, err := client.NeedsAttention(ctx, &rpcv1.NeedsAttentionRequest{})
	if err != nil {
		t.Fatalf("needs attention on empty store: %v", err)
	}
	if len(att.GetRecords()) != 0 || att.GetTruncated() || att.GetTotal() != 0 {
		t.Errorf("needs attention on empty store = %d records truncated=%v total=%d, want 0/false/0",
			len(att.GetRecords()), att.GetTruncated(), att.GetTotal())
	}
}
