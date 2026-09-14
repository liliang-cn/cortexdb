package cortexdb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/internal/testname"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// openChunkWindowBrain opens a brain whose embedder is a bag of known words.
//
// Flat, because every test here asserts which chunk was the hit and which
// chunks were only carried along beside it, and an approximate index is
// allowed to be approximate about exactly that.
func openChunkWindowBrain(t *testing.T, vocabulary ...string) *DB {
	t.Helper()
	path := fmt.Sprintf("test_chunk_window_%d.db", testname.Nano())
	config := DefaultConfig(path)
	config.IndexType = core.IndexTypeFlat

	db, err := Open(config, WithEmbedder(newKeywordEmbedder(vocabulary...)))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(path + suffix)
		}
	})
	return db
}

// ingestSentencesAsChunks ingests one sentence per chunk.
//
// The chunker packs whole sentences up to a word budget, so a budget wider than
// any one of these sentences and narrower than any two of them puts each
// sentence in a chunk of its own. That is what makes a boundary predictable
// enough to write a test about: the statement under test can be made to
// straddle one on purpose.
func ingestSentencesAsChunks(t *testing.T, db *DB, documentID string, sentences ...string) {
	t.Helper()
	ingest, err := db.InsertGraphDocument(context.Background(), GraphRAGDocument{
		ID:      documentID,
		Title:   documentID,
		Content: strings.Join(sentences, " "),
	}, GraphRAGIngestOptions{ChunkSize: 9, Extractor: fixtureExtractor{}})
	if err != nil {
		t.Fatalf("insert %s: %v", documentID, err)
	}
	if len(ingest.ChunkNodeIDs) != len(sentences) {
		t.Fatalf("ingested %s as %d chunks, want one per sentence (%d)",
			documentID, len(ingest.ChunkNodeIDs), len(sentences))
	}
}

// The sentence that answers the question and the sentence that names its
// subject are in different chunks, which is the failure the window exists for:
// retrieval finds "ninety days" and hands back a passage whose subject is
// "she".
func TestAWindowRecoversAStatementSplitAcrossChunks(t *testing.T) {
	db := openChunkWindowBrain(t,
		"priya", "raman", "approved", "vault", "rotation", "policy",
		"interval", "ninety", "days", "coffee", "machine", "fridays",
	)
	ingestSentencesAsChunks(t, db, "rotation-doc",
		"Priya Raman approved the vault rotation policy.",
		"She set the interval to ninety days.",
		"Coffee machine refills happen on Fridays here.",
	)

	ctx := context.Background()
	ask := func(window int) *GraphRAGQueryResult {
		t.Helper()
		result, err := db.SearchGraphRAG(ctx, "interval ninety days", GraphRAGQueryOptions{
			TopK:         1,
			DisableGraph: true,
			ChunkWindow:  window,
		})
		if err != nil {
			t.Fatalf("search with window %d: %v", window, err)
		}
		if len(result.Chunks) != 1 {
			t.Fatalf("window %d returned %d chunks, want the single hit", window, len(result.Chunks))
		}
		return result
	}

	narrow := ask(0)
	if !strings.Contains(narrow.Context, "ninety days") {
		t.Fatalf("the hit itself is missing from the unwidened context: %q", narrow.Context)
	}
	if strings.Contains(narrow.Context, "Priya Raman") {
		t.Fatalf("unwidened context already spans the boundary, so the test proves nothing: %q", narrow.Context)
	}

	widened := ask(1)
	if !strings.Contains(widened.Context, "ninety days") {
		t.Fatalf("widening lost the hit: %q", widened.Context)
	}
	if !strings.Contains(widened.Context, "Priya Raman") {
		t.Fatalf("widening did not reach the chunk that names the subject: %q", widened.Context)
	}
	if widened.Chunks[0].ID != narrow.Chunks[0].ID {
		t.Fatalf("widening changed which chunk was the hit: %s then %s",
			narrow.Chunks[0].ID, widened.Chunks[0].ID)
	}

	if len(widened.Windows) != 1 {
		t.Fatalf("expected one span, got %d", len(widened.Windows))
	}
	span := widened.Windows[0]
	if len(span.Segments) != 3 {
		t.Fatalf("expected the hit plus both neighbours, got %d segments", len(span.Segments))
	}
	if span.Truncated {
		t.Fatal("span reported truncation with the default character budget in hand")
	}
	for i, segment := range span.Segments {
		if segment.ChunkIndex != i {
			t.Fatalf("segment %d carries chunk index %d — spans must be in document order", i, segment.ChunkIndex)
		}
		wantHit := i == 1
		if segment.Hit != wantHit {
			t.Fatalf("segment %d has Hit=%v, want %v", i, segment.Hit, wantHit)
		}
	}
}

// A neighbour is text, not evidence. Nothing scored it, so it must not appear
// with a score, must not appear among the hits, and must be tagged in the
// prompt so a reader can tell what matched from what was carried along.
func TestNeighboursAreMarkedAndDoNotBecomeHits(t *testing.T) {
	db := openChunkWindowBrain(t,
		"priya", "raman", "approved", "vault", "rotation", "policy",
		"interval", "ninety", "days", "coffee", "machine", "fridays",
	)
	ingestSentencesAsChunks(t, db, "rotation-doc",
		"Priya Raman approved the vault rotation policy.",
		"She set the interval to ninety days.",
		"Coffee machine refills happen on Fridays here.",
	)

	result, err := db.SearchGraphRAG(context.Background(), "interval ninety days", GraphRAGQueryOptions{
		TopK:         1,
		DisableGraph: true,
		ChunkWindow:  1,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	hitIDs := make(map[string]struct{}, len(result.Chunks))
	for _, chunk := range result.Chunks {
		hitIDs[chunk.ID] = struct{}{}
	}

	neighbours := 0
	for _, window := range result.Windows {
		for _, segment := range window.Segments {
			if segment.Hit {
				continue
			}
			neighbours++
			if _, promoted := hitIDs[segment.ID]; promoted {
				t.Fatalf("neighbour %s is also listed as a hit", segment.ID)
			}
			if segment.Score != 0 {
				t.Fatalf("neighbour %s carries score %v — it was never scored", segment.ID, segment.Score)
			}
			line := windowSegmentLine(segment)
			if !strings.Contains(line, windowNeighbourTag) {
				t.Fatalf("neighbour line is indistinguishable from a hit: %q", line)
			}
			if !strings.Contains(result.Context, line) {
				t.Fatalf("neighbour line missing from the assembled context: %q", line)
			}
		}
	}
	if neighbours != 2 {
		t.Fatalf("expected both neighbours of the middle chunk, got %d", neighbours)
	}

	// The caller asked for one chunk and gets one chunk. Continuity is not
	// allowed to spend the top-K it was not given.
	if len(result.Chunks) != 1 {
		t.Fatalf("widening displaced the hit list: %d chunks for TopK=1", len(result.Chunks))
	}
}

// Off is off: the chunks, their order and the assembled context must be what
// they were before this feature existed, because everything downstream of
// retrieval is built from them.
func TestNoWindowLeavesRetrievalExactlyAsItWas(t *testing.T) {
	db := openChunkWindowBrain(t,
		"priya", "raman", "approved", "vault", "rotation", "policy",
		"interval", "ninety", "days", "coffee", "machine", "fridays",
	)
	ingestSentencesAsChunks(t, db, "rotation-doc",
		"Priya Raman approved the vault rotation policy.",
		"She set the interval to ninety days.",
		"Coffee machine refills happen on Fridays here.",
	)

	result, err := db.SearchGraphRAG(context.Background(), "interval ninety days", GraphRAGQueryOptions{
		TopK:         2,
		DisableGraph: true,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if result.Windows != nil {
		t.Fatalf("no window was asked for, yet %d spans came back", len(result.Windows))
	}
	// The context the widening path would have replaced, rebuilt from the same
	// chunks by the same function the unwidened path uses. Equal means the new
	// code contributed no bytes.
	if want := buildGraphRAGContext(result.Chunks); result.Context != want {
		t.Fatalf("context differs from the unwidened assembly:\n got %q\nwant %q", result.Context, want)
	}
}

// Two hits close enough for their windows to touch are one passage. Emitting
// them as two overlapping windows would print the chunks between them twice,
// which wastes the budget and tells the model that the repeated text matters
// more than it does.
func TestOverlappingWindowsMergeIntoOneSpan(t *testing.T) {
	db := openChunkWindowBrain(t, "vault", "key", "ninety", "budget", "coffee", "filler")
	ingestSentencesAsChunks(t, db, "keys-doc",
		"Opening remarks about the annual budget review.",
		"The vault key expires after ninety days.",
		"Coffee is served in the morning.",
		"Some filler text sits in between them.",
		"A second vault key rotates every ninety days.",
		"More filler text closes the document out.",
	)

	result, err := db.SearchGraphRAG(context.Background(), "vault key ninety", GraphRAGQueryOptions{
		TopK:         2,
		DisableGraph: true,
		ChunkWindow:  2,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Chunks) != 2 {
		t.Fatalf("expected both matching chunks as hits, got %d", len(result.Chunks))
	}
	if len(result.Windows) != 1 {
		t.Fatalf("windows two apart did not merge: %d spans", len(result.Windows))
	}

	span := result.Windows[0]
	if len(span.Segments) != 6 {
		t.Fatalf("merged span covers %d chunks, want the whole run of 6", len(span.Segments))
	}
	seen := make(map[string]int, len(span.Segments))
	hits := 0
	for i, segment := range span.Segments {
		if segment.ChunkIndex != i {
			t.Fatalf("segment %d is chunk %d — a merged span must stay contiguous and ordered", i, segment.ChunkIndex)
		}
		seen[segment.ID]++
		if segment.Hit {
			hits++
		}
	}
	if hits != 2 {
		t.Fatalf("merged span holds %d hits, want the 2 that matched", hits)
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("chunk %s appears %d times in one span", id, count)
		}
	}
	for _, segment := range span.Segments {
		if occurrences := strings.Count(result.Context, segment.Content); occurrences != 1 {
			t.Fatalf("chunk %s appears %d times in the assembled context", segment.ID, occurrences)
		}
	}
}

// The caller's character cap is the caller's. When continuity will not fit
// inside it, the hit is what survives, and the span says it was cut rather
// than presenting a trimmed passage as a whole one.
func TestWideningStaysInsideTheContextBudget(t *testing.T) {
	db := openChunkWindowBrain(t,
		"priya", "raman", "approved", "vault", "rotation", "policy",
		"interval", "ninety", "days", "coffee", "machine", "fridays",
	)
	ingestSentencesAsChunks(t, db, "rotation-doc",
		"Priya Raman approved the vault rotation policy.",
		"She set the interval to ninety days.",
		"Coffee machine refills happen on Fridays here.",
	)

	ctx := context.Background()
	narrow, err := db.SearchGraphRAG(ctx, "interval ninety days", GraphRAGQueryOptions{
		TopK:         1,
		DisableGraph: true,
	})
	if err != nil {
		t.Fatalf("search without a window: %v", err)
	}

	// A budget with room for the hit and nothing else.
	budget := len(narrow.Context) + 4
	widened, err := db.SearchGraphRAG(ctx, "interval ninety days", GraphRAGQueryOptions{
		TopK:            1,
		DisableGraph:    true,
		ChunkWindow:     1,
		MaxContextChars: budget,
	})
	if err != nil {
		t.Fatalf("search with a window: %v", err)
	}

	if len(widened.Context) > budget {
		t.Fatalf("widened context is %d chars against a budget of %d", len(widened.Context), budget)
	}
	if len(widened.Chunks) != 1 {
		t.Fatalf("the budget cost a hit: %d chunks", len(widened.Chunks))
	}
	if len(widened.Windows) != 1 || len(widened.Windows[0].Segments) != 1 {
		t.Fatalf("expected the bare hit, got %+v", widened.Windows)
	}
	if !widened.Windows[0].Segments[0].Hit {
		t.Fatal("the one segment that fitted is not the hit")
	}
	if !widened.Windows[0].Truncated {
		t.Fatal("neighbours were dropped for budget and the span did not say so")
	}
}

// A chunk with no chunk_index cannot be widened — nothing says where it sits.
// It must still reach the context: a hit that the window code cannot place is
// a hit the window code must not lose.
func TestAChunkWithNoIndexIsKeptAndNotWidened(t *testing.T) {
	db := openChunkWindowBrain(t, "vault", "rotation", "policy", "orphan", "ledger", "entry")
	// Ingest one real document first, so the GraphRAG collection exists with
	// the right dimension before the hand-written row goes in beside it.
	ingestSentencesAsChunks(t, db, "rotation-doc",
		"The vault rotation policy is reviewed twice.",
		"Rotation happens under the vault policy rules.",
	)

	ctx := context.Background()
	vector, err := db.embedder.Embed(ctx, "orphan ledger entry")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if err := db.store.CreateDocument(ctx, &core.Document{
		ID:      "loose-doc",
		Title:   "loose",
		Content: "An orphan ledger entry with no position of its own.",
		Version: 1,
	}); err != nil {
		t.Fatalf("create loose document: %v", err)
	}
	if err := db.Vector().Upsert(ctx, &core.Embedding{
		ID:         "loose-row",
		Collection: defaultGraphRAGCollection,
		Vector:     vector,
		Content:    "An orphan ledger entry with no position of its own.",
		DocID:      "loose-doc",
		Metadata:   map[string]string{"graph_kind": "chunk", "document_id": "loose-doc"},
	}); err != nil {
		t.Fatalf("upsert loose row: %v", err)
	}

	result, err := db.SearchGraphRAG(ctx, "orphan ledger entry", GraphRAGQueryOptions{
		TopK:         1,
		DisableGraph: true,
		ChunkWindow:  2,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Chunks) != 1 || result.Chunks[0].ID != "loose-row" {
		t.Fatalf("expected the loose row as the only hit, got %+v", result.Chunks)
	}
	if len(result.Windows) != 1 || len(result.Windows[0].Segments) != 1 {
		t.Fatalf("an unplaceable chunk should be a span of one, got %+v", result.Windows)
	}
	segment := result.Windows[0].Segments[0]
	if !segment.Hit {
		t.Fatal("the loose row lost its hit marking")
	}
	if segment.ChunkIndex != -1 {
		t.Fatalf("chunk index %d invented for a row that carries none", segment.ChunkIndex)
	}
	if !strings.Contains(result.Context, "orphan ledger entry") {
		t.Fatalf("the unplaceable hit is missing from the context: %q", result.Context)
	}
}
