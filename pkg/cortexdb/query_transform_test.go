package cortexdb

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// fakeQueryTransformer returns canned rewrite signals, or an error when errOut
// is set, so the tests can exercise both the merge and the swallow paths.
type fakeQueryTransformer struct {
	out    *QueryTransform
	errOut error
	calls  int
}

func (f *fakeQueryTransformer) TransformQuery(ctx context.Context, query string) (*QueryTransform, error) {
	f.calls++
	if f.errOut != nil {
		return nil, f.errOut
	}
	return f.out, nil
}

// TestApplyQueryTransformMerges verifies that transformer AlternateQueries and
// Keywords are folded into the request without dropping caller-provided values
// (and deduped), and that the HyDE document is returned for the embedding step.
func TestApplyQueryTransformMerges(t *testing.T) {
	ctx := context.Background()
	db := &DB{queryTransformer: &fakeQueryTransformer{
		out: &QueryTransform{
			AlternateQueries:     []string{"who is the Apollo owner", "Apollo lead"},
			Keywords:             []string{"Alice", "Apollo", "owner"}, // "Apollo" duplicates caller
			HypotheticalDocument: "Alice owns and leads the Apollo project.",
		},
	}}

	req := &KnowledgeSearchRequest{
		Query:            "Who owns Apollo?",
		Keywords:         []string{"Apollo"},          // caller value must survive and stay first
		AlternateQueries: []string{"Apollo ownership"}, // caller value must survive and stay first
	}

	hyde := db.applyQueryTransform(ctx, req)

	if hyde != "Alice owns and leads the Apollo project." {
		t.Fatalf("expected hypothetical document returned, got %q", hyde)
	}
	// Caller keyword first, then merged transformer keywords, deduped.
	wantKeywords := []string{"Apollo", "Alice", "owner"}
	if !equalStrings(req.Keywords, wantKeywords) {
		t.Fatalf("keywords merge = %v, want %v", req.Keywords, wantKeywords)
	}
	wantAlternates := []string{"Apollo ownership", "who is the Apollo owner", "Apollo lead"}
	if !equalStrings(req.AlternateQueries, wantAlternates) {
		t.Fatalf("alternate queries merge = %v, want %v", req.AlternateQueries, wantAlternates)
	}
}

// TestApplyQueryTransformSwallowsError verifies a transformer error never
// mutates the request or surfaces — retrieval must proceed on the raw query.
func TestApplyQueryTransformSwallowsError(t *testing.T) {
	ctx := context.Background()
	ft := &fakeQueryTransformer{errOut: errors.New("boom")}
	db := &DB{queryTransformer: ft}

	req := &KnowledgeSearchRequest{
		Query:            "Who owns Apollo?",
		Keywords:         []string{"Apollo"},
		AlternateQueries: []string{"Apollo ownership"},
	}

	hyde := db.applyQueryTransform(ctx, req)

	if hyde != "" {
		t.Fatalf("expected empty hyde on error, got %q", hyde)
	}
	if ft.calls != 1 {
		t.Fatalf("expected transformer called once, got %d", ft.calls)
	}
	if !equalStrings(req.Keywords, []string{"Apollo"}) {
		t.Fatalf("keywords must be untouched on error, got %v", req.Keywords)
	}
	if !equalStrings(req.AlternateQueries, []string{"Apollo ownership"}) {
		t.Fatalf("alternate queries must be untouched on error, got %v", req.AlternateQueries)
	}
}

// TestApplyQueryTransformNilTransformer verifies the no-transformer path is a
// no-op returning "".
func TestApplyQueryTransformNilTransformer(t *testing.T) {
	db := &DB{}
	req := &KnowledgeSearchRequest{Query: "hello", Keywords: []string{"a"}}
	if hyde := db.applyQueryTransform(context.Background(), req); hyde != "" {
		t.Fatalf("expected empty hyde, got %q", hyde)
	}
	if !equalStrings(req.Keywords, []string{"a"}) {
		t.Fatalf("keywords must be untouched, got %v", req.Keywords)
	}
}

// TestSearchKnowledgeWithTransformerNoRegression confirms that setting a
// transformer on the no-embedder lexical path does not break retrieval: the
// merged keywords still recall the saved knowledge item.
func TestSearchKnowledgeWithTransformerNoRegression(t *testing.T) {
	ctx := context.Background()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "qt.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	db.queryTransformer = &fakeQueryTransformer{out: &QueryTransform{
		AlternateQueries:     []string{"Apollo project owner"},
		Keywords:             []string{"Alice", "owns"},
		HypotheticalDocument: "Alice owns the Apollo project.",
	}}

	const knowledgeID = "apollo"
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{
		KnowledgeID: knowledgeID,
		Title:       "Apollo",
		Content:     "Alice owns the Apollo project. Apollo ships on Friday.",
		ChunkSize:   40,
	}); err != nil {
		t.Fatalf("save knowledge: %v", err)
	}

	resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{
		Query:         "Who owns Apollo?",
		RetrievalMode: RetrievalModeLexical,
		TopK:          3,
	})
	if err != nil {
		t.Fatalf("search knowledge: %v", err)
	}
	found := false
	for _, hit := range resp.Results {
		if hit.KnowledgeID == knowledgeID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected hit for %q with transformer set, results=%+v", knowledgeID, resp.Results)
	}
}
