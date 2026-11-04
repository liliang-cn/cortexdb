package cortexdb

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeTranscriber struct {
	name     string
	segments []MediaSegment
	calls    atomic.Int32
}

func (f *fakeTranscriber) Name() string { return f.name }
func (f *fakeTranscriber) Transcribe(context.Context, MediaInput) ([]MediaSegment, error) {
	f.calls.Add(1)
	return f.segments, nil
}

type fakeDescriber struct {
	name  string
	calls atomic.Int32
	// describe answers by the bytes of the image.
	describe func(MediaInput) *MediaDescription
}

func (f *fakeDescriber) Name() string { return f.name }
func (f *fakeDescriber) DescribeImage(_ context.Context, in MediaInput) (*MediaDescription, error) {
	f.calls.Add(1)
	return f.describe(in), nil
}

type fakeFrames struct{ frames []MediaFrame }

func (f fakeFrames) Name() string { return "every-10s" }
func (f fakeFrames) SampleFrames(context.Context, MediaInput) ([]MediaFrame, error) {
	return f.frames, nil
}

func openMediaBrain(t *testing.T, opts ...Option) (*DB, *GraphRAGToolbox, context.Context) {
	t.Helper()
	db, err := Open(DefaultConfig(filepath.Join(t.TempDir(), "media.db")), opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, db.GraphRAGTools(), context.Background()
}

// A meeting of three stretches: the decision sits in the middle.
func meetingSegments() []MediaSegment {
	return []MediaSegment{
		{StartMS: 0, EndMS: 60_000, Speaker: "Ana", Text: "Welcome everyone, today we look at the storage options for the ledger service."},
		{StartMS: 760_000, EndMS: 790_000, Speaker: "Ben", Text: "We choose PostgreSQL for the ledger, because replication there is what operations already run."},
		{StartMS: 1_500_000, EndMS: 1_530_000, Speaker: "Ana", Text: "Next week the migration plan, and the budget for the new cluster."},
	}
}

func searchMedia(t *testing.T, db *DB, ctx context.Context, query string) []KnowledgeSearchHit {
	t.Helper()
	resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: query, TopK: 5, RetrievalMode: "lexical"})
	if err != nil {
		t.Fatal(err)
	}
	return resp.Results
}

func TestARecordingIsFoundAtTheMomentItSaysIt(t *testing.T) {
	transcriber := &fakeTranscriber{name: "whisper-test", segments: meetingSegments()}
	db, tools, ctx := openMediaBrain(t, WithTranscriber(transcriber))
	resp, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{
		DocumentID: "standup", MediaType: "audio/mpeg", Bytes: []byte("fake mp3 bytes"),
		Title: "Ledger standup", SourceURL: "file:///meetings/standup.mp3", ChunkSize: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.DerivedBy != "transcriber:whisper-test" || resp.Segments != 3 || resp.DurationMS != 1_530_000 || len(resp.ChunkNodeIDs) != 3 {
		t.Fatalf("response %+v", resp)
	}
	sum := sha256.Sum256([]byte("fake mp3 bytes"))
	if resp.ContentSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("content_sha256 %s is not the file's hash", resp.ContentSHA256)
	}
	hits := searchMedia(t, db, ctx, "why did we choose PostgreSQL replication")
	if len(hits) == 0 || hits[0].KnowledgeID != "standup" {
		t.Fatalf("hits %+v", hits)
	}
	media := hits[0].Media
	if media == nil || media.MediaType != "audio/mpeg" || media.DurationMS != 1_530_000 {
		t.Fatalf("media %+v", media)
	}
	found := false
	for _, span := range media.Spans {
		if span.ChunkID == "chunk:standup:001" && span.StartMS == 760_000 && span.EndMS == 790_000 {
			found = true
		}
	}
	if !found {
		t.Fatalf("spans %+v, want chunk 1 at 760000-790000", media.Spans)
	}
	if _, leaked := hits[0].Metadata[mediaSpansKey]; leaked {
		t.Fatal("the spans list is reported in the hit's metadata")
	}
	if hits[0].SourceURL != "file:///meetings/standup.mp3" || !strings.Contains(hits[0].Snippet, "Ben:") {
		t.Fatalf("hit %+v", hits[0])
	}
	// The chunk row and its node carry the span too.
	chunk, err := db.store.GetByID(ctx, "chunk:standup:001")
	if err != nil || chunk.Metadata[chunkStartKey] != "760000" || chunk.Metadata[chunkEndKey] != "790000" {
		t.Fatalf("chunk %+v %v", chunk, err)
	}
	node, err := db.graph.GetNode(ctx, "chunk:standup:001")
	if err != nil || node.Properties["start_ms"] == nil {
		t.Fatalf("chunk node %+v %v", node, err)
	}
}

func TestTheSameFileIsNotDerivedTwice(t *testing.T) {
	transcriber := &fakeTranscriber{name: "whisper-test", segments: meetingSegments()}
	db, tools, ctx := openMediaBrain(t, WithTranscriber(transcriber))
	req := ToolIngestMediaRequest{DocumentID: "standup", MediaType: "audio/mpeg", Bytes: []byte("bytes"), ChunkSize: 20}
	if _, err := tools.IngestMedia(ctx, req); err != nil {
		t.Fatal(err)
	}
	again, err := tools.IngestMedia(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Unchanged || !again.Reused || transcriber.calls.Load() != 1 {
		t.Fatalf("a second ingest of the same file: %+v, %d transcriptions", again, transcriber.calls.Load())
	}
	// A new title keeps the text and calls no model.
	req.Title = "Renamed"
	renamed, err := tools.IngestMedia(ctx, req)
	if err != nil || renamed.Unchanged || !renamed.Reused || transcriber.calls.Load() != 1 {
		t.Fatalf("a renamed ingest: %+v %v, %d transcriptions", renamed, err, transcriber.calls.Load())
	}
	if hits := searchMedia(t, db, ctx, "PostgreSQL replication"); len(hits) == 0 || hits[0].Title != "Renamed" || hits[0].Media == nil || len(hits[0].Media.Spans) == 0 {
		t.Fatalf("hits after the rename %+v", hits)
	}
}

func TestAnotherModelDerivesTheFileAgain(t *testing.T) {
	first := &fakeTranscriber{name: "whisper-small", segments: meetingSegments()}
	db, tools, ctx := openMediaBrain(t, WithTranscriber(first))
	req := ToolIngestMediaRequest{DocumentID: "standup", MediaType: "audio/mpeg", Bytes: []byte("bytes"), ChunkSize: 20}
	if _, err := tools.IngestMedia(ctx, req); err != nil {
		t.Fatal(err)
	}
	better := &fakeTranscriber{name: "whisper-large", segments: []MediaSegment{
		{StartMS: 760_000, EndMS: 790_000, Text: "We choose PostgreSQL for the ledger."},
	}}
	db.media.transcriber = better
	resp, err := tools.IngestMedia(ctx, req)
	if err != nil || resp.Reused || resp.DerivedBy != "transcriber:whisper-large" || len(resp.ChunkNodeIDs) != 1 {
		t.Fatalf("re-derived: %+v %v", resp, err)
	}
	if _, err := db.store.GetByID(ctx, "chunk:standup:002"); err == nil {
		t.Fatal("a chunk of the old transcript is still stored")
	}
	if hits := searchMedia(t, db, ctx, "budget new cluster"); len(hits) != 0 {
		t.Fatalf("the old transcript is still found: %+v", hits)
	}
}

func TestAnImageIsFoundByItsCaptionAndItsText(t *testing.T) {
	describer := &fakeDescriber{name: "vision-test", describe: func(MediaInput) *MediaDescription {
		return &MediaDescription{Caption: "A whiteboard with a database schema drawn in blue marker.", Text: "orders customers invoices"}
	}}
	db, tools, ctx := openMediaBrain(t, WithImageDescriber(describer))
	if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "board", MediaType: "image/jpeg", Bytes: []byte("jpeg")}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"whiteboard database schema", "invoices customers"} {
		hits := searchMedia(t, db, ctx, q)
		if len(hits) == 0 || hits[0].KnowledgeID != "board" || hits[0].Media == nil || hits[0].Media.MediaType != "image/jpeg" || len(hits[0].Media.Spans) != 0 {
			t.Fatalf("%q: %+v", q, hits)
		}
	}
}

func TestAVideoPutsWhatIsSaidAndWhatIsShownOnOneTimeline(t *testing.T) {
	transcriber := &fakeTranscriber{name: "whisper-test", segments: []MediaSegment{
		{StartMS: 0, EndMS: 9_000, Text: "Here is the deploy pipeline we run every night."},
		{StartMS: 20_000, EndMS: 29_000, Text: "And this is where the canary fails over."},
	}}
	describer := &fakeDescriber{name: "vision-test", describe: func(in MediaInput) *MediaDescription {
		return &MediaDescription{Caption: "Slide titled " + string(in.Data)}
	}}
	frames := fakeFrames{frames: []MediaFrame{
		{AtMS: 10_000, Image: MediaInput{Data: []byte("Grafana latency dashboard"), MediaType: "image/jpeg"}},
		{AtMS: 0, Image: MediaInput{Data: []byte("Nightly pipeline overview"), MediaType: "image/jpeg"}},
	}}
	db, tools, ctx := openMediaBrain(t, WithTranscriber(transcriber), WithImageDescriber(describer), WithFrameSampler(frames))
	resp, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "demo", MediaType: "video/mp4", Bytes: []byte("mp4"), ChunkSize: 12})
	if err != nil {
		t.Fatal(err)
	}
	if resp.DerivedBy != "transcriber:whisper-test frames:every-10s describer:vision-test" || resp.Segments != 4 {
		t.Fatalf("response %+v", resp)
	}
	hits := searchMedia(t, db, ctx, "Grafana latency dashboard")
	if len(hits) == 0 || hits[0].Media == nil || len(hits[0].Media.Spans) == 0 {
		t.Fatalf("hits %+v", hits)
	}
	// The frame at 10 s is shown until the next frame or the end of what
	// is said: 29 s, since it is the last one.
	span := hits[0].Media.Spans[0]
	if span.StartMS > 10_000 || span.EndMS < 29_000 {
		t.Fatalf("span %+v, want it to cover 10000-29000", span)
	}
}

func TestTheCallerMayDeriveTheTextItself(t *testing.T) {
	db, tools, ctx := openMediaBrain(t)
	file := []byte("png bytes the brain never sees")
	sum := sha256.Sum256(file)
	sha := hex.EncodeToString(sum[:])
	if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "shot", MediaType: "image/png", Caption: "A login form"}); err == nil {
		t.Fatal("a caption with neither data nor sha256 was taken")
	}
	resp, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{
		DocumentID: "shot", MediaType: "image/png", SHA256: sha, SizeBytes: 31,
		Caption: "A login form with a red error banner", DerivedBy: "claude",
	})
	if err != nil || resp.ContentSHA256 != sha || resp.DerivedBy != "claude" {
		t.Fatalf("%+v %v", resp, err)
	}
	if hits := searchMedia(t, db, ctx, "login form error banner"); len(hits) == 0 || hits[0].Metadata[mediaSizeKey] != "31" {
		t.Fatalf("hits %+v", hits)
	}
	if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "x", MediaType: "image/png", Bytes: file, SHA256: strings.Repeat("0", 64), Caption: "c"}); !errors.Is(err, ErrMediaHashMismatch) {
		t.Fatalf("a wrong sha256: %v", err)
	}
}

func TestMediaIsRefusedWhenItCannotBeTaken(t *testing.T) {
	_, tools, ctx := openMediaBrain(t, WithMediaLimits(8, 2))
	cases := []struct {
		name string
		req  ToolIngestMediaRequest
		want error
	}{
		{"not media", ToolIngestMediaRequest{DocumentID: "a", MediaType: "text/plain", Bytes: []byte("x")}, ErrUnsupportedMedia},
		{"too large", ToolIngestMediaRequest{DocumentID: "a", MediaType: "audio/wav", Bytes: []byte("123456789")}, ErrMediaTooLarge},
		{"too many segments", ToolIngestMediaRequest{DocumentID: "a", MediaType: "audio/wav", Bytes: []byte("x"),
			Segments: []MediaSegment{{Text: "a"}, {Text: "b"}, {Text: "c"}}}, ErrMediaTooLarge},
		{"no deriver", ToolIngestMediaRequest{DocumentID: "a", MediaType: "audio/wav", Bytes: []byte("x")}, ErrNoMediaDeriver},
		{"too large as base64", ToolIngestMediaRequest{DocumentID: "a", MediaType: "audio/wav", Data: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))}, ErrMediaTooLarge},
	}
	for _, c := range cases {
		if _, err := tools.IngestMedia(ctx, c.req); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "a", MediaType: "audio/wav", Data: "not base64!"}); err == nil || !strings.Contains(err.Error(), "base64") {
		t.Errorf("data that is not base64: %v", err)
	}
}

func TestDedupeRefusesAFileTheCollectionHolds(t *testing.T) {
	transcriber := &fakeTranscriber{name: "w", segments: meetingSegments()}
	_, tools, ctx := openMediaBrain(t, WithTranscriber(transcriber))
	if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "first", MediaType: "audio/mpeg", Bytes: []byte("same")}); err != nil {
		t.Fatal(err)
	}
	resp, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "copy", MediaType: "audio/mpeg", Bytes: []byte("same"), Dedupe: true})
	if err != nil || resp.DuplicateOf != "first" {
		t.Fatalf("%+v %v", resp, err)
	}
	// if_sha256 compares the hash of the file.
	sum := sha256.Sum256([]byte("same"))
	if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "first", MediaType: "audio/mpeg", Bytes: []byte("newer"), IfSHA256: hex.EncodeToString(sum[:])}); err != nil {
		t.Fatalf("a write over the file read: %v", err)
	}
	if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "first", MediaType: "audio/mpeg", Bytes: []byte("newest"), IfSHA256: hex.EncodeToString(sum[:])}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("a write over a stale file hash: %v", err)
	}
}

func TestTheMediaMarksAreTheEngines(t *testing.T) {
	transcriber := &fakeTranscriber{name: "w", segments: meetingSegments()}
	db, tools, ctx := openMediaBrain(t, WithTranscriber(transcriber))
	// A caller's copy on a text document is dropped.
	if _, err := tools.IngestDocument(ctx, ToolIngestDocumentRequest{DocumentID: "note", Content: "PostgreSQL replication notes",
		Metadata: map[string]string{mediaTypeKey: "video/mp4", mediaSpansKey: "[[0,1]]"}}); err != nil {
		t.Fatal(err)
	}
	if hits := searchMedia(t, db, ctx, "PostgreSQL replication notes"); len(hits) == 0 || hits[0].Media != nil {
		t.Fatalf("a text document reported as media: %+v", hits)
	}
	// A media document rewritten as text is text: its spans no longer
	// describe its chunks.
	if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "standup", MediaType: "audio/mpeg", Bytes: []byte("b")}); err != nil {
		t.Fatal(err)
	}
	title := "Standup notes"
	if _, err := db.UpdateKnowledge(ctx, KnowledgeUpdateRequest{KnowledgeID: "standup", Title: &title}); err != nil {
		t.Fatal(err)
	}
	doc, err := db.store.GetDocument(ctx, "standup")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range mediaKeys {
		if _, ok := doc.Metadata[key]; ok {
			t.Errorf("%s kept after the document was rewritten as text", key)
		}
	}
}

func TestIngestMediaOverToolCallTakesBase64(t *testing.T) {
	transcriber := &fakeTranscriber{name: "w", segments: meetingSegments()}
	db, tools, ctx := openMediaBrain(t, WithTranscriber(transcriber))
	args, _ := json.Marshal(map[string]any{
		"document_id": "standup", "media_type": "audio/ogg", "data": base64.StdEncoding.EncodeToString([]byte("ogg")),
	})
	out, err := tools.Call(ctx, "ingest_media", args)
	if err != nil {
		t.Fatal(err)
	}
	resp := out.(*ToolIngestMediaResponse)
	if resp.MediaType != "audio/ogg" || resp.Segments != 3 {
		t.Fatalf("%+v", resp)
	}
	if hits := searchMedia(t, db, ctx, "PostgreSQL ledger"); len(hits) == 0 || hits[0].Media == nil {
		t.Fatalf("hits %+v", hits)
	}
	found := false
	for _, d := range tools.Definitions() {
		found = found || d.Name == "ingest_media"
	}
	if !found {
		t.Fatal("ingest_media is not declared")
	}
}

func TestAChunkOfARecordingCoversAtMostThirtySeconds(t *testing.T) {
	var segs []MediaSegment
	for i := int64(0); i < 10; i++ {
		segs = append(segs, MediaSegment{StartMS: i * 10_000, EndMS: i*10_000 + 9_000, Text: "short line"})
	}
	chunks := groupMediaSegments(segs, true, 120)
	if len(chunks) < 3 {
		t.Fatalf("%d chunks for 100 s of short lines, want one per 30 s or so", len(chunks))
	}
	for _, c := range chunks {
		if c.endMS-c.startMS > maxChunkSpanMS {
			t.Errorf("chunk %d-%d spans more than %d ms", c.startMS, c.endMS, maxChunkSpanMS)
		}
	}
	// An image has no time: its text groups by words alone.
	if got := groupMediaSegments(segs, false, 120); len(got) != 1 {
		t.Fatalf("%d chunks for an image's short text, want 1", len(got))
	}
}

func TestAFrameThatHoldsIsDescribedOnce(t *testing.T) {
	describer := &fakeDescriber{name: "v", describe: func(in MediaInput) *MediaDescription {
		return &MediaDescription{Caption: "Slide " + string(in.Data)}
	}}
	frames := fakeFrames{frames: []MediaFrame{
		{AtMS: 0, Image: MediaInput{Data: []byte("one")}},
		{AtMS: 5_000, Image: MediaInput{Data: []byte("one")}},
		{AtMS: 10_000, Image: MediaInput{Data: []byte("two")}},
	}}
	db, _, ctx := openMediaBrain(t, WithImageDescriber(describer), WithFrameSampler(frames))
	in := MediaInput{Data: []byte("mp4")}
	segs, err := db.media.derive(ctx, mediaVideo, in, func() ([]MediaFrame, error) { return frames.SampleFrames(ctx, in) })
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 2 || segs[0].Text != "Slide one" || segs[0].EndMS != 10_000 || segs[1].StartMS != 10_000 {
		t.Fatalf("%+v", segs)
	}
}

func TestIngestMediaOverMCPAndRecallTheMoment(t *testing.T) {
	transcriber := &fakeTranscriber{name: "w", segments: meetingSegments()}
	db, _, c := openMediaBrain(t, WithTranscriber(transcriber))
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := db.NewMCPServer(MCPServerOptions{}).Connect(c, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "media-test"}, nil).Connect(c, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	call := func(name string, args map[string]any) string {
		t.Helper()
		result, err := session.CallTool(c, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || result.IsError {
			msg := ""
			if result != nil && len(result.Content) > 0 {
				raw, _ := json.Marshal(result.Content[0])
				msg = string(raw)
			}
			t.Fatalf("%s over MCP: %v %s", name, err, msg)
		}
		payload, _ := json.Marshal(result.StructuredContent)
		return string(payload)
	}
	call("ingest_media", map[string]any{"document_id": "standup", "media_type": "audio/mpeg", "chunk_size": 20,
		"data": base64.StdEncoding.EncodeToString([]byte("mp3"))})
	sum := sha256.Sum256([]byte("a screenshot"))
	call("ingest_media", map[string]any{"document_id": "shot", "media_type": "image/png", "sha256": hex.EncodeToString(sum[:]),
		"caption": "Grafana panel of checkout latency", "segments": []map[string]any{{"text": "p99 820 ms"}}, "derived_by": "claude"})
	got := call("knowledge_memory_recall", map[string]any{"query": "why PostgreSQL for the ledger replication", "retrieval_mode": "lexical"})
	if !strings.Contains(got, `"media_type":"audio/mpeg"`) || !strings.Contains(got, `"start_ms":760000`) {
		t.Fatalf("recall over MCP: %s, want the recording and the moment", got)
	}
	if got := call("knowledge_search", map[string]any{"query": "checkout latency Grafana"}); !strings.Contains(got, `"media_type":"image/png"`) {
		t.Fatalf("search over MCP: %s", got)
	}
}

func TestARecordingIsFoundWithAnEmbedderToo(t *testing.T) {
	transcriber := &fakeTranscriber{name: "w", segments: meetingSegments()}
	db, tools, ctx := openMediaBrain(t, WithTranscriber(transcriber), WithEmbedder(NewMockEmbedder(8)))
	if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "standup", MediaType: "audio/mpeg", Bytes: []byte("b"), ChunkSize: 20}); err != nil {
		t.Fatal(err)
	}
	resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: "PostgreSQL ledger replication", TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 || resp.Results[0].Media == nil || len(resp.Results[0].Media.Spans) == 0 {
		t.Fatalf("%+v", resp.Results)
	}
	doc, err := db.store.GetDocument(ctx, "standup")
	if err != nil || doc.Metadata[vectorsKey] != "embedder:8" {
		t.Fatalf("vectors mark %v %v", doc.Metadata[vectorsKey], err)
	}
}

func TestTheEntitiesARecordingNamesAreInTheGraph(t *testing.T) {
	transcriber := &fakeTranscriber{name: "whisper-small", segments: meetingSegments()}
	db, tools, ctx := openMediaBrain(t, WithTranscriber(transcriber))
	if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "standup", MediaType: "audio/mpeg", Bytes: []byte("b"), ChunkSize: 20}); err != nil {
		t.Fatal(err)
	}
	node, err := db.graph.GetNode(ctx, graphEntityNodeID("PostgreSQL"))
	if err != nil {
		t.Fatalf("PostgreSQL, named in the transcript, is not an entity: %v", err)
	}
	if got := toStringSlice(node.Properties["source_document_ids"]); !slices.Contains(got, "standup") {
		t.Fatalf("PostgreSQL sources %v, want the recording", got)
	}
	edges, err := db.graph.GetEdges(ctx, "chunk:standup:001", "out")
	if err != nil {
		t.Fatal(err)
	}
	mentioned := false
	for _, e := range edges {
		mentioned = mentioned || e.ToNodeID == node.ID
	}
	if !mentioned {
		t.Fatalf("the chunk that says PostgreSQL does not mention it: %+v", edges)
	}
	// The graph walk finds the recording from the entity, and says when.
	resp, err := db.SearchKnowledge(ctx, KnowledgeSearchRequest{Query: "what was decided about it", RetrievalMode: "ppr", EntityNames: []string{"PostgreSQL"}, TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 || resp.Results[0].KnowledgeID != "standup" || resp.Results[0].Media == nil {
		t.Fatalf("ppr from PostgreSQL: %+v", resp.Results)
	}

	// Derived again without the name: the entity only the old transcript
	// named goes with it.
	db.media.transcriber = &fakeTranscriber{name: "whisper-large", segments: []MediaSegment{
		{StartMS: 0, EndMS: 9_000, Text: "Ana opens the meeting about the ledger service."},
	}}
	if _, err := tools.IngestMedia(ctx, ToolIngestMediaRequest{DocumentID: "standup", MediaType: "audio/mpeg", Bytes: []byte("b"), ChunkSize: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.graph.GetNode(ctx, graphEntityNodeID("PostgreSQL")); err == nil {
		t.Fatal("PostgreSQL outlived the transcript that named it")
	}
}
