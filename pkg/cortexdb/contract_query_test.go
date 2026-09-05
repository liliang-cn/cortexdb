package cortexdb

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Reading the contract back. The tests write records the way the three
// producers write them — the shapes contract_test.go pins — and then ask the
// questions a wall and an agent ask, because a contract only producers honour
// is one nobody can check.

// shelf writes a small store holding one of everything a reader has to tell
// apart: each grade, an edge as well as nodes, a record from a producer that
// does not write the contract at all, and a grade nobody defined.
func shelf(t *testing.T) *DB {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "shelf.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}

	nodes := []*graph.GraphNode{
		{ID: "metric:revenue", Vector: []float32{1, 0, 0, 0}, NodeType: "Metric", Content: "revenue",
			Properties: props(GradeSelfConsistent, "", "", "shop", ProducerCompiled)},
		{ID: "precedent:p1", Vector: []float32{0, 1, 0, 0}, NodeType: "Precedent", Content: "铺货拉流水",
			Properties: props(GradeVerified, "met_but_costly", "", "川鑫家电", ProducerMeasured)},
		{ID: "precedent:p2", Vector: []float32{0, 0, 1, 0}, NodeType: "Precedent", Content: "换季前动库存",
			Properties: signed(props(GradeRefused, "", "换季前不动库存", "川鑫家电", ProducerHuman), "客户")},
		{ID: "claim:reuters", Vector: []float32{0, 0, 0, 1}, NodeType: "Claim", Content: "Reuters said so",
			Properties: props(GradeAsserted, "", "", "https://reuters.com/x", ProducerLLMExtract)},
		// A producer that does not write the contract. On a shared shelf this
		// is most of what is there, and a reader that cannot see it is
		// describing a fraction while looking like it describes everything.
		{ID: "note:old", Vector: []float32{1, 1, 0, 0}, NodeType: "Note", Content: "written before the contract"},
		// A grade nobody defined: a typo, a newer contract, or an invented
		// vocabulary. It has to surface, not fold into the unstamped.
		{ID: "note:odd", Vector: []float32{0, 1, 1, 0}, NodeType: "Note", Content: "probably true",
			Properties: props("probably", "", "", "somewhere", ProducerLLMExtract)},
	}
	for _, n := range nodes {
		if err := db.graph.UpsertNode(ctx, n); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.ID, err)
		}
	}
	// An edge held for review. A graph's assertions are mostly edges, so a
	// reader that only sees nodes misses the half that matters.
	edge := &graph.GraphEdge{
		ID: "edge:held", FromNodeID: "claim:reuters", ToNodeID: "metric:revenue",
		EdgeType: "about", Weight: 1,
		Properties: props(GradeHeld, "needs_review:conflict", "two sources disagree", "https://reuters.com/x", ProducerLLMExtract),
	}
	if err := db.graph.UpsertEdge(ctx, edge); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}
	return db
}

// signed adds the asserter a human-produced record must name. Without it the
// record fails ValidateContract, which is what the round-trip test below is
// for: a fixture that could not have been written is not evidence about a
// reader.
func signed(p map[string]interface{}, by string) map[string]interface{} {
	p[KeyBy] = by
	return p
}

func props(grade, state, why, source, producer string) map[string]interface{} {
	p := map[string]interface{}{
		KeyGrade: grade, KeySource: source, KeyProducer: producer,
		KeyAt: "2026-09-05T00:00:00Z",
	}
	if state != "" {
		p[KeyState] = state
	}
	if why != "" {
		p[KeyWhy] = why
	}
	return p
}

// The tally's two uncomfortable numbers are the ones it exists for: how much
// of the shelf nobody graded, and what grades nobody defined.
func TestTheTallyCountsWhatWasNeverGradedAndWhatNobodyDefined(t *testing.T) {
	got, err := shelf(t).ContractTally(context.Background())
	if err != nil {
		t.Fatalf("ContractTally: %v", err)
	}
	for _, c := range []struct {
		name string
		got  graph.PropertyCount
		want graph.PropertyCount
	}{
		{"verified", got.Verified, graph.PropertyCount{Nodes: 1}},
		{"self_consistent", got.SelfConsistent, graph.PropertyCount{Nodes: 1}},
		{"asserted", got.Asserted, graph.PropertyCount{Nodes: 1}},
		{"refused", got.Refused, graph.PropertyCount{Nodes: 1}},
		// Only on the edge, which is the point of counting both tables.
		{"held", got.Held, graph.PropertyCount{Edges: 1}},
		{"untagged", got.Untagged, graph.PropertyCount{Nodes: 1}},
	} {
		if c.got != c.want {
			t.Errorf("%s = %+v, want %+v", c.name, c.got, c.want)
		}
	}
	if got.Unknown["probably"] != (graph.PropertyCount{Nodes: 1}) {
		t.Errorf("an undefined grade did not surface: %+v", got.Unknown)
	}
	// An undefined grade must not be filed as ungraded: one is a producer
	// writing something wrong, the other is a producer writing nothing, and
	// only the first is somebody's bug to fix.
	if got.Untagged.Nodes != 1 {
		t.Errorf("untagged = %+v; the odd grade was folded in", got.Untagged)
	}
}

// The one question the contract exists to make answerable, and the reason held
// and refused come back together: "nobody has looked" and "somebody looked and
// said no" are both work, told apart by Grade rather than by which call the
// reader remembered to make.
func TestNeedsAttentionReturnsBothGradesWithTheirReasons(t *testing.T) {
	got, err := shelf(t).NeedsAttention(context.Background(), 0)
	if err != nil {
		t.Fatalf("NeedsAttention: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2 (one held edge, one refused node): %+v", len(got), got)
	}
	seen := map[string]GradedRecord{}
	for _, r := range got {
		seen[r.Grade] = r
		if r.Why == "" {
			t.Errorf("%s record %s has no reason — a reader can only delete that", r.Grade, r.ID)
		}
	}
	held, ok := seen[GradeHeld]
	if !ok {
		t.Fatalf("no held record: %+v", got)
	}
	if !held.Edge || held.From == "" || held.To == "" {
		t.Errorf("the held record is an edge and must arrive with its ends: %+v", held)
	}
	// The producer's own finer word, carried through untouched — the contract
	// never interprets it, and a reader shows it as detail.
	if held.State != "needs_review:conflict" {
		t.Errorf("held.State = %q, want the producer's own word", held.State)
	}
	refused := seen[GradeRefused]
	// A refusal a person made is signed, and the signature has to survive the
	// read — it is the one thing that makes the record chaseable.
	if refused.Producer != ProducerHuman || refused.By != "客户" {
		t.Errorf("the signature did not survive the read: %+v", refused)
	}
	if refused.Why != "换季前不动库存" {
		t.Errorf("refused.Why = %q", refused.Why)
	}
}

// A shelf is shared. A reader asking about one engagement must not be handed
// another's records, and the narrowing is the same collection discipline the
// producers write under.
func TestGradedRecordsNarrowsToOneSource(t *testing.T) {
	db := shelf(t)
	got, err := db.GradedRecords(context.Background(), GradedQuery{
		Grades:  []string{GradeVerified, GradeRefused, GradeAsserted},
		Sources: []string{"川鑫家电"},
	})
	if err != nil {
		t.Fatalf("GradedRecords: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want the engagement's 2: %+v", len(got), got)
	}
	for _, r := range got {
		if r.Source != "川鑫家电" {
			t.Errorf("%s leaked from %q", r.ID, r.Source)
		}
	}
}

// Asking for everything on a shared shelf is asking for other people's
// records. Every useful question names a grade.
func TestAQueryNamingNoGradeIsRefused(t *testing.T) {
	if _, err := shelf(t).GradedRecords(context.Background(), GradedQuery{}); err == nil {
		t.Fatal("a query with no grades returned records")
	}
}

// An empty shelf answers zeroes rather than failing: a wall drawn before
// anything has been written should say "nothing yet", not show an error.
func TestAnEmptyShelfTalliesToZero(t *testing.T) {
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "empty.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.graph.InitGraphSchema(context.Background()); err != nil {
		t.Fatalf("schema: %v", err)
	}
	got, err := db.ContractTally(context.Background())
	if err != nil {
		t.Fatalf("ContractTally: %v", err)
	}
	if got.Verified != (graph.PropertyCount{}) || got.Untagged != (graph.PropertyCount{}) || len(got.Unknown) != 0 {
		t.Errorf("an empty shelf tallied to %+v", got)
	}
}

// The reader must not drop a key the contract needs. Rebuilding a record's
// metadata from what came back and running it through ValidateContract catches
// the failure that is otherwise invisible: a Fetch list missing _by returns
// records that look complete and are not, and the wall renders an unsigned
// refusal without anything saying a signature was lost on the way out.
func TestWhatTheReaderReturnsIsWhatTheValidatorAccepts(t *testing.T) {
	got, err := shelf(t).NeedsAttention(context.Background(), 0)
	if err != nil {
		t.Fatalf("NeedsAttention: %v", err)
	}
	for _, r := range got {
		meta := map[string]string{
			KeySource: r.Source, KeyProducer: r.Producer,
			KeyGrade: r.Grade, KeyAt: r.At, KeyWhy: r.Why,
		}
		if r.By != "" {
			meta[KeyBy] = r.By
		}
		if err := ValidateContract(meta); err != nil {
			t.Errorf("record %s came back failing the contract it was stored under: %v", r.ID, err)
		}
	}
}
