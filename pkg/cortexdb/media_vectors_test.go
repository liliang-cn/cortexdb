package cortexdb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// conceptEmbedder is a media embedder with one dimension per concept and a
// last one for anything else: an image "img:heron" and the text "a heron"
// meet at cosine 1, and a text that names no concept meets no image.
type conceptEmbedder struct {
	name       string
	concepts   []string
	mediaCalls atomic.Int32
	textCalls  atomic.Int32
	failText   bool
}

func newConceptEmbedder(name string, concepts ...string) *conceptEmbedder {
	return &conceptEmbedder{name: name, concepts: concepts}
}

func (e *conceptEmbedder) Name() string { return e.name }

func (e *conceptEmbedder) vector(words string) []float32 {
	v := make([]float32, len(e.concepts)+1)
	hit := false
	for i, c := range e.concepts {
		if strings.Contains(strings.ToLower(words), c) {
			v[i], hit = 1, true
		}
	}
	if !hit {
		v[len(e.concepts)] = 1
	}
	return v
}

func (e *conceptEmbedder) EmbedMedia(_ context.Context, in MediaInput) ([]MediaVector, error) {
	e.mediaCalls.Add(1)
	if strings.HasPrefix(in.MediaType, "audio/") {
		return nil, ErrUnsupportedMedia
	}
	return []MediaVector{{Vector: e.vector(string(in.Data))}}, nil
}

func (e *conceptEmbedder) EmbedText(_ context.Context, text string) ([]float32, error) {
	e.textCalls.Add(1)
	if e.failText {
		return nil, errors.New("media model is down")
	}
	return e.vector(text), nil
}

type countingFrames struct {
	frames []MediaFrame
	calls  atomic.Int32
}

func (f *countingFrames) Name() string { return "every-20s" }
func (f *countingFrames) SampleFrames(context.Context, MediaInput) ([]MediaFrame, error) {
	f.calls.Add(1)
	return f.frames, nil
}

func mediaVectorCount(t *testing.T, db *DB, docID string) int {
	t.Helper()
	if err := db.ensureMediaVectorTable(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.store.GetDB().QueryRow(db.Dialect().Rebind(`SELECT COUNT(*) FROM media_vectors WHERE doc_id = ?`), docID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func mediaHitIDs(hits []KnowledgeSearchHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.KnowledgeID)
	}
	return out
}

func TestAnImageIsFoundByWhatItShows(t *testing.T) {
	embedder := newConceptEmbedder("clip-test", "heron", "bicycle", "sunset")
	db, tools, ctx := openMediaBrain(t, WithMediaEmbedder(embedder))
	// No describer: no caption says what either photo shows.
	for id, data := range map[string]string{"bird": "img:heron", "bike": "img:bicycle"} {
		resp, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: id, MediaType: "image/jpeg", Bytes: []byte(data), Title: "IMG_" + id})
		if err != nil {
			t.Fatal(err)
		}
		if resp.DerivedBy != "" || len(resp.ChunkNodeIDs) != 1 {
			t.Fatalf("response %+v", resp)
		}
	}
	resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: "a grey heron standing in shallow water", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if ids := mediaHitIDs(resp.Results); len(ids) != 1 || ids[0] != "bird" {
		t.Fatalf("hits %v, want the heron photo alone", ids)
	}
	hit := resp.Results[0]
	if hit.MediaScore < 0.99 || hit.Media == nil || hit.Media.MediaType != "image/jpeg" {
		t.Fatalf("hit %+v", hit)
	}
	if !strings.Contains(resp.Context, "IMG_bird") {
		t.Fatalf("context %q does not carry the hit", resp.Context)
	}
	// A keyword search asked for is left a keyword search.
	lexical, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: "a grey heron standing in shallow water", RetrievalMode: RetrievalModeLexical})
	if err != nil {
		t.Fatal(err)
	}
	if len(lexical.Results) != 0 {
		t.Fatalf("a lexical search found %v", mediaHitIDs(lexical.Results))
	}
}

func TestAnImageFindsTheImagesThatLookLikeIt(t *testing.T) {
	embedder := newConceptEmbedder("clip-test", "heron", "lake", "reeds", "bicycle")
	_, tools, ctx := openMediaBrain(t, WithMediaEmbedder(embedder))
	// Against the query: 1 for bird, 0.87 for both, 0 for bike; the third
	// sits under the floor, and would sit under the cut at 85% of the best.
	for id, data := range map[string]string{"bird": "img:heron by the lake in the reeds", "bike": "img:bicycle", "both": "img:heron by the lake in the reeds, a bicycle"} {
		if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: id, MediaType: "image/png", Bytes: []byte(data)}); err != nil {
			t.Fatal(err)
		}
	}
	args, _ := json.Marshal(map[string]any{"media_type": "image/png", "data": base64.StdEncoding.EncodeToString([]byte("img:a heron, a lake, reeds"))})
	out, err := tools.Call(ctx, "search_media", args)
	if err != nil {
		t.Fatal(err)
	}
	resp := out.(*ToolSearchMediaResponse)
	if ids := mediaHitIDs(resp.Results); len(ids) != 2 || ids[0] != "bird" || ids[1] != "both" {
		t.Fatalf("hits %v, want the heron photo, then the one with a bicycle in it", ids)
	}
	if resp.Results[0].MediaScore <= resp.Results[1].MediaScore {
		t.Fatalf("scores %v then %v", resp.Results[0].MediaScore, resp.Results[1].MediaScore)
	}
	if _, err := tools.SearchMedia(ctx, ToolSearchMediaRequest{}); err == nil {
		t.Fatal("search_media took neither a query nor a file")
	}
	if _, err := tools.SearchMedia(ctx, ToolSearchMediaRequest{Query: "heron", Bytes: []byte("img:heron"), MediaType: "image/png"}); err == nil {
		t.Fatal("search_media took a query and a file")
	}
}

func TestSearchMediaWithoutAMediaEmbedderSaysSo(t *testing.T) {
	_, tools, ctx := openMediaBrain(t)
	if _, err := tools.SearchMedia(ctx, ToolSearchMediaRequest{Query: "heron"}); !errors.Is(err, ErrNoMediaEmbedder) {
		t.Fatalf("err %v", err)
	}
}

// The media vectors are not the text index's: the chunks of a media
// document are what they would be without a media embedder, and a search
// over a brain with no media answers as it did.
func TestTheTextIndexIsUntouched(t *testing.T) {
	describer := &fakeDescriber{name: "v", describe: func(MediaInput) *MediaDescription {
		return &MediaDescription{Caption: "A bird by the lake"}
	}}
	plain, plainTools, ctx := openMediaBrain(t, WithImageDescriber(describer), WithEmbedder(NewMockEmbedder(8)))
	withMedia, mediaTools, _ := openMediaBrain(t, WithImageDescriber(describer), WithEmbedder(NewMockEmbedder(8)), WithMediaEmbedder(newConceptEmbedder("clip-test", "heron")))
	for _, tools := range []*GraphRAGToolbox{plainTools, mediaTools} {
		if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "notes", Content: "The heron nests by the lake every spring."}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := withMedia.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: "where does the heron nest", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	want, err := plain.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: "where does the heron nest", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Chunks) != len(want.Chunks) || before.Chunks[0].Score != want.Chunks[0].Score || before.Chunks[0].MediaRank != 0 {
		t.Fatalf("with no media the answer changed: %+v, want %+v", before.Chunks, want.Chunks)
	}

	for _, tools := range []*GraphRAGToolbox{plainTools, mediaTools} {
		if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "bird", MediaType: "image/png", Bytes: []byte("img:heron")}); err != nil {
			t.Fatal(err)
		}
	}
	a, err := plain.store.GetByDocID(ctx, "bird")
	if err != nil {
		t.Fatal(err)
	}
	b, err := withMedia.store.GetByDocID(ctx, "bird")
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || len(b) != 1 || len(b[0].Vector) != 8 || b[0].Content != a[0].Content {
		t.Fatalf("chunks %+v and %+v", a, b)
	}
	if n := mediaVectorCount(t, withMedia, "bird"); n != 1 {
		t.Fatalf("%d media vectors, want 1", n)
	}
	if n := mediaVectorCount(t, plain, "bird"); n != 0 {
		t.Fatalf("a brain with no media embedder wrote %d media vectors", n)
	}
}

func TestAVideoFrameIsFoundAtItsMoment(t *testing.T) {
	embedder := newConceptEmbedder("clip-test", "heron", "bicycle")
	frames := &countingFrames{frames: []MediaFrame{
		{AtMS: 0, Image: MediaInput{Data: []byte("img:bicycle"), MediaType: "image/jpeg"}},
		{AtMS: 40_000, Image: MediaInput{Data: []byte("img:heron"), MediaType: "image/jpeg"}},
	}}
	describer := &fakeDescriber{name: "v", describe: func(MediaInput) *MediaDescription { return &MediaDescription{Caption: "outdoor scene"} }}
	transcriber := &fakeTranscriber{name: "w", segments: []MediaSegment{
		{StartMS: 0, EndMS: 20_000, Text: "We ride along the river path."},
		{StartMS: 40_000, EndMS: 60_000, Text: "Quiet now, look over there."},
	}}
	db, tools, ctx := openMediaBrain(t, WithMediaEmbedder(embedder), WithFrameSampler(frames), WithImageDescriber(describer), WithTranscriber(transcriber))
	resp, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "walk", MediaType: "video/mp4", Bytes: []byte("mp4"), DurationMS: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	if frames.calls.Load() != 1 {
		t.Fatalf("the frames were sampled %d times, want once for the describer and the embedder", frames.calls.Load())
	}
	if n := mediaVectorCount(t, db, "walk"); n != 2 {
		t.Fatalf("%d media vectors, want one per frame", n)
	}
	got, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: "a heron", TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Results) == 0 || got.Results[0].KnowledgeID != "walk" || got.Results[0].Media == nil {
		t.Fatalf("hits %+v", got.Results)
	}
	spans := got.Results[0].Media.Spans
	if len(spans) == 0 || spans[0].StartMS != 40_000 {
		t.Fatalf("spans %+v (chunks %v), want the moment the heron is shown", spans, resp.ChunkNodeIDs)
	}
}

func TestTheSameFileIsNotEmbeddedTwice(t *testing.T) {
	embedder := newConceptEmbedder("clip-test", "heron")
	db, tools, ctx := openMediaBrain(t, WithMediaEmbedder(embedder))
	req := ToolIngestMediaRequest{DocumentID: "bird", MediaType: "image/png", Bytes: []byte("img:heron"), Title: "first"}
	if _, err := tools.IngestMedia(ctx, req); err != nil {
		t.Fatal(err)
	}
	again, err := tools.IngestMedia(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Unchanged || embedder.mediaCalls.Load() != 1 {
		t.Fatalf("unchanged %v, %d embeddings", again.Unchanged, embedder.mediaCalls.Load())
	}
	// Written again for a new title, with the vectors it holds.
	req.Title = "second"
	retitled, err := tools.IngestMedia(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if retitled.Unchanged || !retitled.Reused || embedder.mediaCalls.Load() != 1 || mediaVectorCount(t, db, "bird") != 1 {
		t.Fatalf("%+v, %d embeddings, %d vectors", retitled, embedder.mediaCalls.Load(), mediaVectorCount(t, db, "bird"))
	}
	// Another file under the id is embedded.
	req.Bytes = []byte("img:heron again")
	if _, err := tools.IngestMedia(ctx, req); err != nil {
		t.Fatal(err)
	}
	if embedder.mediaCalls.Load() != 2 || mediaVectorCount(t, db, "bird") != 1 {
		t.Fatalf("%d embeddings, %d vectors", embedder.mediaCalls.Load(), mediaVectorCount(t, db, "bird"))
	}
}

func TestAnotherMediaModelEmbedsTheFileAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "media.db")
	first := newConceptEmbedder("clip-a", "heron")
	db, err := Open(DefaultConfig(path), WithMediaEmbedder(first))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	req := ToolIngestMediaRequest{DocumentID: "bird", MediaType: "image/png", Bytes: []byte("img:heron")}
	if _, err := db.GraphRAGTools().IngestMedia(ctx, req); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	second := newConceptEmbedder("clip-b", "heron", "lake")
	db, err = Open(DefaultConfig(path), WithMediaEmbedder(second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	resp, err := db.GraphRAGTools().IngestMedia(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Unchanged || second.mediaCalls.Load() != 1 {
		t.Fatalf("%+v, %d embeddings by the new model", resp, second.mediaCalls.Load())
	}
	var spaces []string
	rows, err := db.store.GetDB().Query(`SELECT space FROM media_vectors WHERE doc_id = 'bird'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		spaces = append(spaces, s)
	}
	_ = rows.Close()
	if len(spaces) != 1 || spaces[0] != "clip-b:3" {
		t.Fatalf("spaces %v, want the new model's alone", spaces)
	}
	if hits, err := db.GraphRAGTools().SearchMedia(ctx, ToolSearchMediaRequest{Query: "heron"}); err != nil || len(hits.Results) != 1 {
		t.Fatalf("hits %+v, %v", hits, err)
	}
}

func TestTheMediaVectorsGoWithTheirDocument(t *testing.T) {
	embedder := newConceptEmbedder("clip-test", "heron")
	db, tools, ctx := openMediaBrain(t, WithMediaEmbedder(embedder))
	ingest := func(id string) {
		t.Helper()
		if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: id, MediaType: "image/png", Bytes: []byte("img:heron " + id)}); err != nil {
			t.Fatal(err)
		}
	}
	ingest("deleted")
	ingest("saved-over")
	ingest("ingested-over")
	if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "deleted"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "saved-over", Content: "Now a note about herons."}); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "ingested-over", Content: "Now a page about herons."}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"deleted", "saved-over", "ingested-over"} {
		if n := mediaVectorCount(t, db, id); n != 0 {
			t.Errorf("%s keeps %d media vectors", id, n)
		}
	}
	if hits, err := tools.SearchMedia(ctx, ToolSearchMediaRequest{Query: "heron"}); err != nil || len(hits.Results) != 0 {
		t.Fatalf("hits %v, %v", mediaHitIDs(hits.Results), err)
	}
}

func TestSearchMediaLeavesHiddenDocumentsOut(t *testing.T) {
	embedder := newConceptEmbedder("clip-test", "heron")
	db, tools, ctx := openMediaBrain(t, WithMediaEmbedder(embedder), WithReviewGate())
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	for _, req := range []ToolIngestMediaRequest{
		{DocumentID: "seen", MediaType: "image/png", Bytes: []byte("img:heron 1"), ReviewStatus: ReviewAccepted, ReviewedBy: "me"},
		{DocumentID: "expired", MediaType: "image/png", Bytes: []byte("img:heron 2"), ExpiresAt: &past},
		{DocumentID: "proposed", MediaType: "image/png", Bytes: []byte("img:heron 3"), ReviewStatus: ReviewProposed},
	} {
		if _, err := tools.IngestMedia(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	media, err := tools.SearchMedia(ctx, ToolSearchMediaRequest{Query: "heron"})
	if err != nil {
		t.Fatal(err)
	}
	if ids := mediaHitIDs(media.Results); len(ids) != 1 || ids[0] != "seen" {
		t.Fatalf("search_media found %v", ids)
	}
	fused, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: "heron"})
	if err != nil {
		t.Fatal(err)
	}
	if ids := mediaHitIDs(fused.Results); len(ids) != 1 || ids[0] != "seen" {
		t.Fatalf("knowledge_search found %v", ids)
	}
}

func TestAFailingMediaEmbedderLeavesTheTextAnswer(t *testing.T) {
	embedder := newConceptEmbedder("clip-test", "heron")
	db, tools, ctx := openMediaBrain(t, WithMediaEmbedder(embedder))
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "notes", Content: "The heron nests by the lake."}); err != nil {
		t.Fatal(err)
	}
	embedder.failText = true
	resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: "heron nests"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 || !strings.Contains(strings.Join(resp.Degraded, ","), DegradedMediaSearchFailed) {
		t.Fatalf("results %v, degraded %v", mediaHitIDs(resp.Results), resp.Degraded)
	}
}

func TestRecallFindsAnImageByWhatItShows(t *testing.T) {
	embedder := newConceptEmbedder("clip-test", "heron")
	_, tools, ctx := openMediaBrain(t, WithMediaEmbedder(embedder))
	if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "bird", MediaType: "image/png", Bytes: []byte("img:heron"), Title: "IMG_0042"}); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"query": "the heron photo from the trip"})
	out, err := tools.Call(ctx, "knowledge_memory_recall", args)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), `"media_type":"image/png"`) {
		t.Fatalf("recall: %s", raw)
	}
}

func TestMediaHitsJoinTheTextAnswerWithoutReorderingIt(t *testing.T) {
	text := []GraphRAGChunkResult{{ID: "t1", Score: 9}, {ID: "both", Score: 6}, {ID: "t3", Score: 3}}
	media := []GraphRAGChunkResult{{ID: "both", MediaScore: 0.3}, {ID: "m1", MediaScore: 0.28}, {ID: "m2", MediaScore: 0.27}}
	got := fuseMediaChunks(text, media, 0)
	var order []string
	for _, c := range got {
		order = append(order, c.ID)
	}
	if strings.Join(order, ",") != "t1,both,m1,t3,m2" {
		t.Fatalf("order %v", order)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Score >= got[i-1].Score {
			t.Fatalf("scores %v do not fall with the order", got)
		}
	}
	if got[1].Score != 6 || got[1].MediaScore != 0.3 || got[1].MediaRank != 1 || got[2].MediaRank != 2 {
		t.Fatalf("%+v", got)
	}
	if only := fuseMediaChunks(nil, media[1:], 0); len(only) != 2 || only[0].Score <= only[1].Score {
		t.Fatalf("media alone %+v", only)
	}
}

func TestAMediaHitFarBelowTheBestIsCut(t *testing.T) {
	embedder := newConceptEmbedder("clip-test", "heron", "bicycle")
	_, tools, ctx := openMediaBrain(t, WithMediaEmbedder(embedder))
	for id, data := range map[string]string{"bird": "img:heron", "both": "img:heron on a bicycle"} {
		if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: id, MediaType: "image/png", Bytes: []byte(data)}); err != nil {
			t.Fatal(err)
		}
	}
	// 0.71 of the best: over the floor, under the cut.
	resp, err := tools.SearchMedia(ctx, ToolSearchMediaRequest{Query: "a heron"})
	if err != nil {
		t.Fatal(err)
	}
	if ids := mediaHitIDs(resp.Results); len(ids) != 1 || ids[0] != "bird" {
		t.Fatalf("hits %v", ids)
	}
}

func TestMediaVectorsOnPostgreSQL(t *testing.T) {
	db := openPostgresBrain(t, 8)
	embedder := newConceptEmbedder("clip-test", "heron", "bicycle")
	WithMediaEmbedder(embedder)(db)
	tools, ctx := db.GraphRAGTools(), context.Background()
	for id, data := range map[string]string{"bird": "img:heron", "bike": "img:bicycle"} {
		if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: id, MediaType: "image/png", Bytes: []byte(data), Title: "IMG_" + id}); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: "a heron in the water"})
	if err != nil {
		t.Fatal(err)
	}
	if ids := mediaHitIDs(resp.Results); len(ids) != 1 || ids[0] != "bird" {
		t.Fatalf("knowledge_search found %v", ids)
	}
	like, err := tools.SearchMedia(ctx, ToolSearchMediaRequest{Bytes: []byte("img:bicycle"), MediaType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	if ids := mediaHitIDs(like.Results); len(ids) != 1 || ids[0] != "bike" {
		t.Fatalf("search_media found %v", ids)
	}
	retitled, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "bird", MediaType: "image/png", Bytes: []byte("img:heron"), Title: "renamed"})
	if err != nil {
		t.Fatal(err)
	}
	if !retitled.Reused || embedder.mediaCalls.Load() != 3 || mediaVectorCount(t, db, "bird") != 1 { // two ingests and the image query
		t.Fatalf("%+v, %d embeddings, %d vectors", retitled, embedder.mediaCalls.Load(), mediaVectorCount(t, db, "bird"))
	}
	if _, err := db.SaveKnowledge(ctx, KnowledgeSaveRequest{KnowledgeID: "bike", Content: "Now a note."}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DeleteKnowledge(ctx, KnowledgeDeleteRequest{KnowledgeID: "bird"}); err != nil {
		t.Fatal(err)
	}
	if mediaVectorCount(t, db, "bird") != 0 || mediaVectorCount(t, db, "bike") != 0 {
		t.Fatal("media vectors outlive their documents")
	}
}

func TestAMediaHitKeepsToTheStatusTheSearchAsksFor(t *testing.T) {
	embedder := newConceptEmbedder("clip-test", "heron")
	db, tools, ctx := openMediaBrain(t, WithMediaEmbedder(embedder))
	for _, req := range []ToolIngestMediaRequest{
		{DocumentID: "accepted", MediaType: "image/png", Bytes: []byte("img:heron 1"), ReviewStatus: ReviewAccepted, ReviewedBy: "me"},
		// No gate: a rejected document is served, unless the search asks
		// for accepted ones (a document with no status counts as accepted).
		{DocumentID: "rejected", MediaType: "image/png", Bytes: []byte("img:heron 2"), ReviewStatus: ReviewRejected, ReviewedBy: "me"},
	} {
		if _, err := tools.IngestMedia(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: "heron", Plan: &RetrievalPlan{
		Query: "heron", Filters: &RetrievalFilters{ReviewStatus: ReviewAccepted},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if ids := mediaHitIDs(resp.Results); len(ids) != 1 || ids[0] != "accepted" {
		t.Fatalf("hits %v, want the accepted image alone", ids)
	}
}
