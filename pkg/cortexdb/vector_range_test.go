package cortexdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// openRangeBrain opens a store with an explicit metric and dimension.
//
// The euclidean metric is what most of these tests want: with integer
// coordinates the distance between two points is exact in both float32 and
// float64, so "exactly at the radius" is a fact about the arithmetic and not a
// hope about rounding. Cosine distances of interesting vectors are irrational
// and could only be compared with a tolerance, which is the one thing a
// boundary test must not do.
func openRangeBrain(t *testing.T, name string, similarity core.SimilarityFunc, dimensions int, opts ...Option) *DB {
	t.Helper()
	dbPath := fmt.Sprintf("test_vector_range_%s_%d.db", name, testname.Nano())
	config := DefaultConfig(dbPath)
	config.Dimensions = dimensions
	config.SimilarityFn = similarity

	db, err := Open(config, opts...)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	})
	return db
}

func upsertRangeVectors(t *testing.T, db *DB, vectors map[string][]float32) {
	t.Helper()
	embeddings := make([]*core.Embedding, 0, len(vectors))
	for id, vector := range vectors {
		embeddings = append(embeddings, &core.Embedding{ID: id, Vector: vector, Content: id})
	}
	if err := db.Vector().UpsertBatch(context.Background(), embeddings); err != nil {
		t.Fatalf("upsert vectors: %v", err)
	}
}

func resultIDs(results []core.ScoredEmbedding) []string {
	ids := make([]string, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.ID)
	}
	return ids
}

// TestRangeSearchVectorKeepsOnlyWhatIsInsideTheRadius is the facade path: the
// answer is defined by the bar, not by a count, so the same query with the same
// data returns a different number of rows for a different radius — which is the
// whole reason to have this alongside Search.
func TestRangeSearchVectorKeepsOnlyWhatIsInsideTheRadius(t *testing.T) {
	db := openRangeBrain(t, "inside", core.EuclideanDist, 3)
	upsertRangeVectors(t, db, map[string][]float32{
		"at-0":  {0, 0, 0},
		"at-3":  {3, 0, 0},
		"at-5":  {3, 4, 0},
		"at-13": {5, 12, 0},
	})

	ctx := context.Background()
	origin := []float32{0, 0, 0}

	wide, err := db.RangeSearchVector(ctx, origin, VectorRangeOptions{Radius: 6})
	if err != nil {
		t.Fatalf("range search: %v", err)
	}
	if got, want := resultIDs(wide), []string{"at-0", "at-3", "at-5"}; !equalStrings(got, want) {
		t.Fatalf("radius 6 returned %v, want %v (closest first)", got, want)
	}

	narrow, err := db.RangeSearchVector(ctx, origin, VectorRangeOptions{Radius: 4})
	if err != nil {
		t.Fatalf("range search: %v", err)
	}
	if got, want := resultIDs(narrow), []string{"at-0", "at-3"}; !equalStrings(got, want) {
		t.Fatalf("radius 4 returned %v, want %v", got, want)
	}

	// Score is a similarity on the same scale Search reports, so a range hit
	// can be ranked next to a top-K hit without the caller converting
	// anything. Under euclidean that similarity is the negated distance, and
	// it has to fall as the rows get further away.
	for i, result := range wide {
		if i > 0 && result.Score > wide[i-1].Score {
			t.Fatalf("results are not sorted by descending similarity: %v then %v", wide[i-1].Score, result.Score)
		}
	}
	if wide[0].Score != 0 {
		t.Errorf("the vector sitting on the query scored %v, want 0 (euclidean similarity is the negated distance)", wide[0].Score)
	}
	if wide[2].Score != -5 {
		t.Errorf("the vector 5 away scored %v, want -5; a positive score here means the engine is still reporting distance as the score", wide[2].Score)
	}

	// MaxResults is a cap on the closest matches, not a second bar.
	capped, err := db.RangeSearchVector(ctx, origin, VectorRangeOptions{Radius: 6, MaxResults: 2})
	if err != nil {
		t.Fatalf("range search: %v", err)
	}
	if got, want := resultIDs(capped), []string{"at-0", "at-3"}; !equalStrings(got, want) {
		t.Fatalf("capped search returned %v, want %v", got, want)
	}
}

// TestRangeSearchVectorIncludesAVectorExactlyAtTheRadius pins the comparison as
// inclusive. A radius is quoted to callers as "within this distance", and an
// exclusive boundary would silently drop the row a threshold was chosen to
// catch — the failure is one missing result, which looks like the data being
// absent rather than the comparison being wrong by one operator.
func TestRangeSearchVectorIncludesAVectorExactlyAtTheRadius(t *testing.T) {
	db := openRangeBrain(t, "boundary", core.EuclideanDist, 3)
	upsertRangeVectors(t, db, map[string][]float32{
		"on-the-line": {3, 4, 0},  // exactly 5 from the origin
		"just-past":   {3, 4, 1},  // sqrt(26), the nearest thing outside
		"far":         {5, 12, 0}, // 13
	})

	results, err := db.RangeSearchVector(context.Background(), []float32{0, 0, 0}, VectorRangeOptions{Radius: 5})
	if err != nil {
		t.Fatalf("range search: %v", err)
	}
	if got, want := resultIDs(results), []string{"on-the-line"}; !equalStrings(got, want) {
		t.Fatalf("radius 5 returned %v, want %v: distance == radius must be kept and anything past it dropped", got, want)
	}
}

// TestRangeSearchRejectsANonPositiveRadius refuses the argument rather than
// answering it. Zero is the shape an unset field takes, and "nothing is within
// zero distance" is an empty result set that reads like a real answer.
func TestRangeSearchRejectsANonPositiveRadius(t *testing.T) {
	db := openRangeBrain(t, "radius", core.EuclideanDist, 3,
		WithEmbedder(newKeywordEmbedder("alpha", "beta", "gamma")))
	upsertRangeVectors(t, db, map[string][]float32{"at-0": {0, 0, 0}})
	ctx := context.Background()

	for _, radius := range []float32{0, -1} {
		if _, err := db.RangeSearchVector(ctx, []float32{0, 0, 0}, VectorRangeOptions{Radius: radius}); !errors.Is(err, ErrNonPositiveRadius) {
			t.Errorf("radius %v returned %v, want ErrNonPositiveRadius", radius, err)
		}
	}

	// The tool surface has to refuse it too: a model that omits radius must be
	// told, not handed an empty match list it will read as "nothing similar".
	if _, err := db.GraphRAGTools().SearchVectorRange(ctx, ToolSearchVectorRangeRequest{Query: "alpha"}); !errors.Is(err, ErrNonPositiveRadius) {
		t.Errorf("tool with no radius returned %v, want ErrNonPositiveRadius", err)
	}
}

// TestRangeSearchEmptyResultIsNotAnError guards the answer this API exists to
// give. "There is nothing in the store like this" is the useful reply to a
// near-duplicate check, and returning an error for it would push callers into
// treating a real finding as a failure.
func TestRangeSearchEmptyResultIsNotAnError(t *testing.T) {
	db := openRangeBrain(t, "empty", core.EuclideanDist, 3)
	upsertRangeVectors(t, db, map[string][]float32{
		"at-0": {0, 0, 0},
		"at-3": {3, 0, 0},
	})

	results, err := db.RangeSearchVector(context.Background(), []float32{100, 100, 100}, VectorRangeOptions{Radius: 1})
	if err != nil {
		t.Fatalf("range search over an empty neighbourhood: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no matches, got %v", resultIDs(results))
	}
}

// TestRangeSearchReturnsEveryMatchPastAThousand checks that no cap is applied
// behind the caller's back. A range query that quietly stopped at a round
// number would answer "all the near-duplicates" with some of them, and the
// caller has no way to see the difference between a store holding 1000 matches
// and a store holding 5000.
func TestRangeSearchReturnsEveryMatchPastAThousand(t *testing.T) {
	const total = 1200

	db := openRangeBrain(t, "bulk", core.EuclideanDist, 3)
	vectors := make(map[string][]float32, total)
	for i := 0; i < total; i++ {
		// Spread over a small lattice so every point is well inside the radius
		// while the ids stay distinct.
		vectors[fmt.Sprintf("v-%04d", i)] = []float32{float32(i % 7), float32(i % 11), float32(i % 13)}
	}
	upsertRangeVectors(t, db, vectors)

	results, err := db.RangeSearchVector(context.Background(), []float32{0, 0, 0}, VectorRangeOptions{Radius: 100})
	if err != nil {
		t.Fatalf("range search: %v", err)
	}
	if len(results) != total {
		t.Fatalf("got %d matches, want %d: MaxResults 0 must mean every match, with no cap of the engine's own", len(results), total)
	}
}

// TestSearchVectorRangeToolAnswersAndSaysWhenItTrimmed runs the agent-callable
// path end to end, through the dispatch switch a host reaches it by.
func TestSearchVectorRangeToolAnswersAndSaysWhenItTrimmed(t *testing.T) {
	db := openRangeBrain(t, "tool", core.CosineSimilarity, 4,
		WithEmbedder(newKeywordEmbedder("alpha", "beta", "gamma", "delta")))
	ctx := context.Background()
	tools := db.GraphRAGTools()

	// Cosine distance from the query "alpha" ([1,0,0,0]) is 1 - cos, so these
	// sit at 0, ~0.11, ~0.29 and ~0.68.
	texts := map[string]string{
		"near-exact":  "alpha",
		"near-mostly": "alpha alpha beta",
		"near-mixed":  "alpha beta",
		"far":         "alpha beta beta beta",
	}
	for id, text := range texts {
		if err := db.InsertText(ctx, id, text, nil); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	input, err := json.Marshal(ToolSearchVectorRangeRequest{Query: "alpha", Radius: 0.5})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	raw, err := tools.Call(ctx, "search_vector_range", input)
	if err != nil {
		t.Fatalf("call search_vector_range: %v", err)
	}
	resp, ok := raw.(*ToolSearchVectorRangeResponse)
	if !ok {
		t.Fatalf("dispatch returned %T, want *ToolSearchVectorRangeResponse", raw)
	}
	got := make([]string, 0, len(resp.Matches))
	for _, match := range resp.Matches {
		got = append(got, match.ID)
	}
	if want := []string{"near-exact", "near-mostly", "near-mixed"}; !equalStrings(got, want) {
		t.Fatalf("radius 0.5 returned %v, want %v", got, want)
	}
	if resp.Count != len(resp.Matches) {
		t.Errorf("count %d does not match %d returned rows", resp.Count, len(resp.Matches))
	}
	if resp.Truncated {
		t.Error("nothing was cut, so truncated must be false")
	}

	trimmed, err := tools.SearchVectorRange(ctx, ToolSearchVectorRangeRequest{Query: "alpha", Radius: 0.5, MaxResults: 2})
	if err != nil {
		t.Fatalf("capped call: %v", err)
	}
	if len(trimmed.Matches) != 2 {
		t.Fatalf("max_results 2 returned %d rows", len(trimmed.Matches))
	}
	if !trimmed.Truncated {
		t.Error("the radius matched three rows and only two were returned; truncated must say so, " +
			"or the caller reads a trimmed list as the complete neighbourhood")
	}
}

// TestRangeSearchTextNeedsAnEmbedder refuses rather than falling back to
// lexical retrieval, because BM25 scores are not distances in the vector space
// the radius describes.
func TestRangeSearchTextNeedsAnEmbedder(t *testing.T) {
	db := openRangeBrain(t, "noembedder", core.CosineSimilarity, 4)

	if _, err := db.RangeSearchText(context.Background(), "alpha", VectorRangeOptions{Radius: 0.5}); !errors.Is(err, ErrEmbedderNotConfigured) {
		t.Fatalf("got %v, want ErrEmbedderNotConfigured", err)
	}
}

// countingEmbedder records how many times it was asked to embed, so a test can
// assert that a call never reached it.
type countingEmbedder struct {
	dim   int
	calls int
}

func (e *countingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	e.calls++
	v := make([]float32, e.dim)
	for i := range v {
		v[i] = 1
	}
	return v, nil
}

func (e *countingEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		v, err := e.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (e *countingEmbedder) Dim() int { return e.dim }

// A radius the caller got wrong must be refused before the embedder is paid.
//
// RangeSearchText used to validate the query and the embedder's presence, embed,
// and only then delegate to RangeSearchVector, which is where the radius check
// lived. The refusal was correct and the bill was already run up: for most
// deployments Embed is a network round trip against a metered model, and
// nothing about a non-positive radius needs a vector to detect.
func TestABadRadiusIsRefusedBeforeTheEmbedderIsCalled(t *testing.T) {
	emb := &countingEmbedder{dim: 3}
	db := openRangeBrain(t, "unpaid", core.EuclideanDist, 3, WithEmbedder(emb))

	for _, radius := range []float32{0, -1} {
		if _, err := db.RangeSearchText(context.Background(), "anything", VectorRangeOptions{Radius: radius}); !errors.Is(err, ErrNonPositiveRadius) {
			t.Fatalf("radius %v: got %v, want ErrNonPositiveRadius", radius, err)
		}
	}
	if emb.calls != 0 {
		t.Fatalf("the embedder was called %d times for arguments that were refused; a caller pays for its own mistake", emb.calls)
	}
}
