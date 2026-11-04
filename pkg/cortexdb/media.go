package cortexdb

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// Images, audio and video are stored as documents whose text is derived
// from the file: a caption and the text visible in an image, the transcript
// of a recording, the transcript and the captions of sampled frames of a
// video. The derived text goes through the path every document takes
// (chunks, FTS5, vectors, entities, floors), so a media document is found by
// a text question with or without an embedder. CortexDB calls no model: the
// derivers are interfaces, given with WithImageDescriber, WithTranscriber and
// WithFrameSampler (pkg/media has HTTP and ffmpeg ones), or the caller sends
// the text it derived itself.

// MediaInput is a file handed to a deriver.
type MediaInput struct {
	Data      []byte
	MediaType string
}

// MediaSegment is a stretch of derived text. StartMS and EndMS place it in a
// recording or video; they are ignored for an image.
type MediaSegment struct {
	StartMS int64  `json:"start_ms,omitempty"`
	EndMS   int64  `json:"end_ms,omitempty"`
	Text    string `json:"text"`
	Speaker string `json:"speaker,omitempty"`
}

// MediaDescription is what an ImageDescriber says about an image: a caption,
// and the text that can be read in it.
type MediaDescription struct {
	Caption string `json:"caption"`
	Text    string `json:"text,omitempty"`
}

// MediaFrame is a still taken from a video at AtMS.
type MediaFrame struct {
	AtMS  int64
	Image MediaInput
}

// ImageDescriber captions an image and reads the text in it. Name names the
// model, and is recorded on every document whose text it derived.
type ImageDescriber interface {
	Name() string
	DescribeImage(ctx context.Context, image MediaInput) (*MediaDescription, error)
}

// Transcriber turns a recording, or the sound of a video, into timed
// segments.
type Transcriber interface {
	Name() string
	Transcribe(ctx context.Context, media MediaInput) ([]MediaSegment, error)
}

// FrameSampler takes the stills of a video that the ImageDescriber then
// captions: which frames, and how many, is the sampler's choice.
type FrameSampler interface {
	Name() string
	SampleFrames(ctx context.Context, video MediaInput) ([]MediaFrame, error)
}

// Defaults of WithMediaLimits.
const (
	DefaultMediaMaxBytes    = 32 << 20
	DefaultMediaMaxSegments = 20000
)

var (
	// ErrUnsupportedMedia is returned for a media_type that is not an
	// image, audio or video type.
	ErrUnsupportedMedia = errors.New("cortexdb: invalid request: media_type must be image/*, audio/* or video/*")
	// ErrMediaTooLarge is returned for a file or a derivation past the
	// limits of WithMediaLimits.
	ErrMediaTooLarge = errors.New("cortexdb: invalid request: media past the size limit")
	// ErrNoMediaDeriver is returned when the file has to be described or
	// transcribed and the brain has no deriver for its kind, and the caller
	// sent no text of its own.
	ErrNoMediaDeriver = errors.New("cortexdb: no deriver for this media: configure one or send caption, text or segments")
	// ErrMediaHashMismatch is returned when sha256 is not the hash of data.
	ErrMediaHashMismatch = errors.New("cortexdb: invalid request: sha256 is not the hash of data")
)

type mediaRuntime struct {
	describer   ImageDescriber
	transcriber Transcriber
	frames      FrameSampler
	embedder    MediaEmbedder
	scoreFloor  float64
	maxBytes    int
	maxSegments int
}

// WithImageDescriber sets the model that captions images and video frames.
func WithImageDescriber(d ImageDescriber) Option { return func(db *DB) { db.media.describer = d } }

// WithTranscriber sets the model that transcribes audio and the sound of
// videos.
func WithTranscriber(t Transcriber) Option { return func(db *DB) { db.media.transcriber = t } }

// WithFrameSampler sets what takes the frames of a video to caption.
func WithFrameSampler(f FrameSampler) Option { return func(db *DB) { db.media.frames = f } }

// WithMediaLimits caps the bytes of one file and the segments derived from
// it; zero keeps a default (DefaultMediaMaxBytes, DefaultMediaMaxSegments).
func WithMediaLimits(maxBytes, maxSegments int) Option {
	return func(db *DB) {
		db.media.maxBytes = maxBytes
		db.media.maxSegments = maxSegments
	}
}

func (m mediaRuntime) byteLimit() int {
	if m.maxBytes > 0 {
		return m.maxBytes
	}
	return DefaultMediaMaxBytes
}

func (m mediaRuntime) segmentLimit() int {
	if m.maxSegments > 0 {
		return m.maxSegments
	}
	return DefaultMediaMaxSegments
}

// The engine's marks on a media document. _content_sha256 holds the hash of
// the file. _media_spans, on the record only, holds the span of every chunk.
const (
	mediaTypeKey      = "_media_type"
	mediaSizeKey      = "_media_size"
	mediaDurationKey  = "_media_duration_ms"
	mediaDerivedByKey = "_media_derived_by"
	mediaSpansKey     = "_media_spans"
	chunkStartKey     = "_start_ms"
	chunkEndKey       = "_end_ms"
)

var mediaKeys = []string{mediaTypeKey, mediaSizeKey, mediaDurationKey, mediaDerivedByKey, mediaEmbeddedByKey, mediaSpansKey}

// documentOnlyKey reports a mark kept on the document record and copied
// neither to its node nor to its chunks.
func documentOnlyKey(key string) bool { return key == mediaSpansKey }

// ToolIngestMediaRequest stores an image, a recording or a video as a
// document. The file is Bytes from Go, or Data, its base64, over a tool
// call; the brain's derivers turn it into text. A caller that derived the
// text itself sends Caption, Text or Segments instead, with SHA256 naming
// the file when it sends no file.
type ToolIngestMediaRequest struct {
	DocumentID string `json:"document_id"`
	MediaType  string `json:"media_type"`
	Bytes      []byte `json:"-"`
	Data       string `json:"data,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`

	Caption   string         `json:"caption,omitempty"`
	Text      string         `json:"text,omitempty"`
	Segments  []MediaSegment `json:"segments,omitempty"`
	DerivedBy string         `json:"derived_by,omitempty"`

	Title      string            `json:"title,omitempty"`
	SourceURL  string            `json:"source_url,omitempty"`
	Author     string            `json:"author,omitempty"`
	Collection string            `json:"collection,omitempty"`
	ChunkSize  int               `json:"chunk_size,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`

	Dedupe       bool    `json:"dedupe,omitempty"`
	IfAbsent     bool    `json:"if_absent,omitempty"`
	IfSHA256     string  `json:"if_sha256,omitempty"`
	ExpiresAt    *string `json:"expires_at,omitempty"`
	ReviewStatus string  `json:"review_status,omitempty"`
	ReviewedBy   string  `json:"reviewed_by,omitempty"`
	ReviewedAt   string  `json:"reviewed_at,omitempty"`
}

// ToolIngestMediaResponse is ingest_document's answer, with what the text
// was derived with. ContentSHA256 is the hash of the file.
type ToolIngestMediaResponse struct {
	ToolIngestDocumentResponse
	MediaType  string `json:"media_type"`
	DerivedBy  string `json:"derived_by"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Segments   int    `json:"segments"`
	// Reused reports that the stored text and vectors were kept: the same
	// file, derived and embedded with the same models, so none was called.
	Reused bool `json:"reused,omitempty"`
}

// MediaSpan places a chunk of a search hit in its recording or video.
type MediaSpan struct {
	ChunkID string `json:"chunk_id"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
}

// MediaHit says that a search hit is a media document, and where its
// matching chunks sit in the file.
type MediaHit struct {
	MediaType  string      `json:"media_type"`
	DurationMS int64       `json:"duration_ms,omitempty"`
	Spans      []MediaSpan `json:"spans,omitempty"`
}

type mediaKind int

const (
	mediaImage mediaKind = iota + 1
	mediaAudio
	mediaVideo
)

func mediaKindOf(mediaType string) (mediaKind, bool) {
	major, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(mediaType)), "/")
	switch major {
	case "image":
		return mediaImage, true
	case "audio":
		return mediaAudio, true
	case "video":
		return mediaVideo, true
	}
	return 0, false
}

// IngestMedia stores a media document. The file's hash stands for its
// content: an identical re-ingest writes nothing, dedupe refuses a file the
// collection already holds, and if_sha256 compares file hashes. A file
// stored with the same derivers keeps its text and calls none of them; a
// change of model derives it again and replaces its chunks.
func (t *GraphRAGToolbox) IngestMedia(ctx context.Context, req ToolIngestMediaRequest) (*ToolIngestMediaResponse, error) {
	if req.DocumentID == "" {
		return nil, fmt.Errorf("document_id is required")
	}
	kind, ok := mediaKindOf(req.MediaType)
	if !ok {
		return nil, ErrUnsupportedMedia
	}
	limits := t.db.media
	file, err := mediaBytes(req, limits.byteLimit())
	if err != nil {
		return nil, err
	}
	if len(req.Segments) > limits.segmentLimit() {
		return nil, fmt.Errorf("%w: %d segments, the limit is %d", ErrMediaTooLarge, len(req.Segments), limits.segmentLimit())
	}
	sha := strings.ToLower(strings.TrimSpace(req.SHA256))
	size := req.SizeBytes
	if len(file) > 0 {
		sum := sha256.Sum256(file)
		computed := hex.EncodeToString(sum[:])
		if sha != "" && sha != computed {
			return nil, ErrMediaHashMismatch
		}
		sha, size = computed, int64(len(file))
	} else if !validSHA256(sha) {
		return nil, fmt.Errorf("cortexdb: invalid request: data, or the sha256 of the file the text stands for, is required")
	}

	in := MediaInput{Data: file, MediaType: req.MediaType}
	// A video's frames are sampled once, for the describer and the media
	// embedder both.
	var sampled []MediaFrame
	var sampleErr error
	sampledOnce := false
	frames := func() ([]MediaFrame, error) {
		if !sampledOnce {
			sampledOnce = true
			sampled, sampleErr = limits.frames.SampleFrames(ctx, in)
			if sampleErr != nil {
				sampleErr = fmt.Errorf("sample frames: %w", sampleErr)
			}
		}
		return sampled, sampleErr
	}

	// The media vectors: those stored for this file in the embedder's
	// space when there are, so no model is called, else the embedder's.
	var vectors []MediaVector
	space := ""
	vectorsReused, modelCalled := false, false
	if limits.embedder != nil && len(file) > 0 {
		if vectors, space, err = t.db.reusableMediaVectors(ctx, req.DocumentID, sha); err != nil {
			return nil, err
		}
		if vectors != nil {
			vectorsReused = true
		} else {
			modelCalled = true
			if vectors, space, err = limits.embedMediaFile(ctx, kind, in, frames, req.DurationMS); err != nil {
				return nil, err
			}
		}
	}

	given := callerSegments(kind, req)
	var segments []MediaSegment
	derivedBy := ""
	textReused := false
	if len(given) > 0 {
		segments = given
		derivedBy = firstNonEmpty(strings.TrimSpace(req.DerivedBy), "caller")
	} else {
		if len(file) == 0 {
			return nil, fmt.Errorf("cortexdb: invalid request: data, or caption, text or segments, is required")
		}
		derivedBy = limits.derivedBy(kind)
		switch {
		case derivedBy != "":
			stored, err := t.db.storedMediaSegments(ctx, req.DocumentID, sha, derivedBy)
			if err != nil {
				return nil, err
			}
			if stored != nil {
				segments, textReused = stored, true
			} else {
				modelCalled = true
				if segments, err = limits.derive(ctx, kind, in, frames); err != nil {
					return nil, err
				}
			}
		case len(vectors) > 0:
			// Found by its vectors alone: the one chunk they point at says
			// what the caller named the file.
			end := int64(0)
			for _, v := range vectors {
				end = max(end, v.EndMS)
			}
			segments = []MediaSegment{{EndMS: max(end, req.DurationMS), Text: firstNonEmpty(strings.TrimSpace(req.Title), strings.TrimSpace(req.SourceURL), req.DocumentID)}}
		default:
			return nil, ErrNoMediaDeriver
		}
	}
	if len(segments) > limits.segmentLimit() {
		return nil, fmt.Errorf("%w: %d segments derived, the limit is %d", ErrMediaTooLarge, len(segments), limits.segmentLimit())
	}
	reused := !modelCalled && (textReused || vectorsReused)

	opts := GraphRAGIngestOptions{Collection: req.Collection, ChunkSize: req.ChunkSize}
	applyGraphRAGIngestDefaults(&opts)
	payload := &mediaPayload{
		mediaType: strings.ToLower(strings.TrimSpace(req.MediaType)),
		sha:       sha,
		size:      size,
		derivedBy: derivedBy,
		timed:     kind != mediaImage,
		chunks:    groupMediaSegments(segments, kind != mediaImage, opts.ChunkSize),
		vectors:   vectors,
		space:     space,
	}
	if len(payload.chunks) == 0 {
		return nil, fmt.Errorf("%w: the media yielded no text", ErrEmptyText)
	}
	payload.durationMS = req.DurationMS
	if payload.timed {
		for _, c := range payload.chunks {
			payload.durationMS = max(payload.durationMS, c.endMS)
		}
		for _, v := range vectors {
			payload.durationMS = max(payload.durationMS, v.EndMS)
		}
	}
	texts := payload.texts()
	resp, err := t.IngestDocument(ctx, ToolIngestDocumentRequest{
		DocumentID:   req.DocumentID,
		Title:        req.Title,
		Content:      strings.Join(texts, "\n\n"),
		SourceURL:    req.SourceURL,
		Author:       req.Author,
		Collection:   req.Collection,
		ChunkSize:    req.ChunkSize,
		Metadata:     req.Metadata,
		Dedupe:       req.Dedupe,
		IfAbsent:     req.IfAbsent,
		IfSHA256:     req.IfSHA256,
		ExpiresAt:    req.ExpiresAt,
		ReviewStatus: req.ReviewStatus,
		ReviewedBy:   req.ReviewedBy,
		ReviewedAt:   req.ReviewedAt,
		media:        payload,
	})
	if err != nil {
		return nil, err
	}
	return &ToolIngestMediaResponse{
		ToolIngestDocumentResponse: *resp,
		MediaType:                  payload.mediaType,
		DerivedBy:                  derivedBy,
		DurationMS:                 payload.durationMS,
		Segments:                   len(segments),
		Reused:                     reused,
	}, nil
}

// mediaBytes is the file of a request, refused past limit before it is
// decoded.
func mediaBytes(req ToolIngestMediaRequest, limit int) ([]byte, error) {
	if len(req.Bytes) > 0 && req.Data != "" {
		return nil, fmt.Errorf("cortexdb: invalid request: bytes and data are not given together")
	}
	if req.Data != "" {
		if n := base64.StdEncoding.DecodedLen(len(req.Data)); n > limit+2 {
			return nil, fmt.Errorf("%w: about %d bytes, the limit is %d", ErrMediaTooLarge, n, limit)
		}
		decoded, err := base64.StdEncoding.DecodeString(req.Data)
		if err != nil {
			return nil, fmt.Errorf("cortexdb: invalid request: data is not base64: %w", err)
		}
		req.Bytes = decoded
	}
	if len(req.Bytes) > limit {
		return nil, fmt.Errorf("%w: %d bytes, the limit is %d", ErrMediaTooLarge, len(req.Bytes), limit)
	}
	return req.Bytes, nil
}

func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// callerSegments is the text the caller derived itself, as segments.
func callerSegments(kind mediaKind, req ToolIngestMediaRequest) []MediaSegment {
	var out []MediaSegment
	if caption := strings.TrimSpace(req.Caption); caption != "" {
		out = append(out, MediaSegment{Text: caption})
	}
	if text := strings.TrimSpace(req.Text); text != "" {
		out = append(out, MediaSegment{Text: text})
	}
	for _, s := range req.Segments {
		if strings.TrimSpace(s.Text) != "" {
			out = append(out, s)
		}
	}
	if kind == mediaImage {
		for i := range out {
			out[i].StartMS, out[i].EndMS = 0, 0
		}
	}
	return out
}

// derivedBy names the derivers a file of this kind is derived with, ""
// when the brain has none that can derive it.
func (m mediaRuntime) derivedBy(kind mediaKind) string {
	switch kind {
	case mediaImage:
		if m.describer != nil {
			return "describer:" + m.describer.Name()
		}
	case mediaAudio:
		if m.transcriber != nil {
			return "transcriber:" + m.transcriber.Name()
		}
	case mediaVideo:
		var parts []string
		if m.transcriber != nil {
			parts = append(parts, "transcriber:"+m.transcriber.Name())
		}
		if m.frames != nil && m.describer != nil {
			parts = append(parts, "frames:"+m.frames.Name(), "describer:"+m.describer.Name())
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// derive runs the derivers on the file.
func (m mediaRuntime) derive(ctx context.Context, kind mediaKind, in MediaInput, sample func() ([]MediaFrame, error)) ([]MediaSegment, error) {
	switch kind {
	case mediaImage:
		d, err := m.describer.DescribeImage(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("describe image: %w", err)
		}
		return describedSegments(d, 0, 0), nil
	case mediaAudio:
		segs, err := m.transcriber.Transcribe(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("transcribe: %w", err)
		}
		return segs, nil
	}
	var segs []MediaSegment
	if m.transcriber != nil {
		spoken, err := m.transcriber.Transcribe(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("transcribe: %w", err)
		}
		segs = append(segs, spoken...)
	}
	if m.frames != nil && m.describer != nil {
		sampled, err := sample()
		if err != nil {
			return nil, err
		}
		frames := slices.Clone(sampled)
		if len(frames) > m.segmentLimit() {
			return nil, fmt.Errorf("%w: %d frames, the limit is %d", ErrMediaTooLarge, len(frames), m.segmentLimit())
		}
		sort.SliceStable(frames, func(i, j int) bool { return frames[i].AtMS < frames[j].AtMS })
		end := int64(0)
		for _, s := range segs {
			end = max(end, s.EndMS)
		}
		var shown []MediaSegment
		for i, f := range frames {
			d, err := m.describer.DescribeImage(ctx, f.Image)
			if err != nil {
				return nil, fmt.Errorf("describe the frame at %d ms: %w", f.AtMS, err)
			}
			until := max(end, f.AtMS)
			if i+1 < len(frames) {
				until = frames[i+1].AtMS
			}
			for _, s := range describedSegments(d, f.AtMS, until) {
				// A scene that holds over several frames is described once,
				// for as long as it is shown.
				if n := len(shown); n > 0 && shown[n-1].Text == s.Text {
					shown[n-1].EndMS = s.EndMS
					continue
				}
				shown = append(shown, s)
			}
		}
		segs = append(segs, shown...)
	}
	// One timeline: what is said and what is shown, in order of time.
	sort.SliceStable(segs, func(i, j int) bool { return segs[i].StartMS < segs[j].StartMS })
	return segs, nil
}

func describedSegments(d *MediaDescription, start, end int64) []MediaSegment {
	if d == nil {
		return nil
	}
	text := strings.TrimSpace(strings.TrimSpace(d.Caption) + "\n" + strings.TrimSpace(d.Text))
	if text == "" {
		return nil
	}
	return []MediaSegment{{StartMS: start, EndMS: end, Text: text}}
}

type mediaChunk struct {
	text           string
	startMS, endMS int64
}

// maxChunkSpanMS is the longest stretch of a recording or video one chunk
// covers: a hit says where, and a chunk of several minutes says little.
const maxChunkSpanMS = 30_000

// groupMediaSegments joins consecutive segments into chunks of up to
// chunkSize words (and the character limit every chunk keeps) and, for a
// recording or video, up to maxChunkSpanMS, each chunk spanning its first
// segment's start to its last one's end. A segment
// longer than a chunk is split, every piece keeping its span. A speaker
// heads the text the speaker said.
func groupMediaSegments(segments []MediaSegment, timed bool, chunkSize int) []mediaChunk {
	maxChars := max(chunkSize*4, 400)
	var chunks []mediaChunk
	var cur *mediaChunk
	words := 0
	flush := func() {
		if cur != nil && strings.TrimSpace(cur.text) != "" {
			chunks = append(chunks, *cur)
		}
		cur, words = nil, 0
	}
	for _, s := range segments {
		text := strings.Join(strings.Fields(s.Text), " ")
		if text == "" {
			continue
		}
		if s.Speaker != "" {
			text = strings.TrimSpace(s.Speaker) + ": " + text
		}
		start, end := s.StartMS, max(s.EndMS, s.StartMS)
		if !timed {
			start, end = 0, 0
		}
		n := len(strings.Fields(text))
		if n > chunkSize || utf8.RuneCountInString(text) > maxChars {
			flush()
			for _, piece := range splitGraphRAGText(text, chunkSize, 0) {
				chunks = append(chunks, mediaChunk{text: piece, startMS: start, endMS: end})
			}
			continue
		}
		if cur != nil && (words+n > chunkSize || utf8.RuneCountInString(cur.text)+1+utf8.RuneCountInString(text) > maxChars ||
			(timed && end-cur.startMS > maxChunkSpanMS)) {
			flush()
		}
		if cur == nil {
			cur = &mediaChunk{text: text, startMS: start, endMS: end}
		} else {
			cur.text += "\n" + text
			cur.endMS = max(cur.endMS, end)
		}
		words += n
	}
	flush()
	return chunks
}

// mediaPayload is what IngestMedia hands IngestDocument.
type mediaPayload struct {
	mediaType  string
	sha        string
	size       int64
	durationMS int64
	derivedBy  string
	timed      bool
	chunks     []mediaChunk
	// vectors are the file's media vectors, in space; none without a media
	// embedder.
	vectors []MediaVector
	space   string
}

func (m *mediaPayload) texts() []string {
	out := make([]string, len(m.chunks))
	for i, c := range m.chunks {
		out[i] = c.text
	}
	return out
}

// stamp writes the media marks into a document's metadata, after
// stampIngestMarks, whose content hash it replaces with the file's.
func (m *mediaPayload) stamp(metadata map[string]string) {
	metadata[contentHashKey] = m.sha
	metadata[mediaTypeKey] = m.mediaType
	metadata[mediaDerivedByKey] = m.derivedBy
	if m.size > 0 {
		metadata[mediaSizeKey] = strconv.FormatInt(m.size, 10)
	}
	if len(m.vectors) > 0 {
		metadata[mediaEmbeddedByKey] = m.space
	}
	if !m.timed {
		return
	}
	if m.durationMS > 0 {
		metadata[mediaDurationKey] = strconv.FormatInt(m.durationMS, 10)
	}
	spans := make([][2]int64, len(m.chunks))
	for i, c := range m.chunks {
		spans[i] = [2]int64{c.startMS, c.endMS}
	}
	// Pairs of integers always encode.
	encoded, _ := json.Marshal(spans)
	metadata[mediaSpansKey] = string(encoded)
}

// markChunk puts chunk i's span on its row and its node.
func (m *mediaPayload) markChunk(i int, metadata map[string]string, props map[string]interface{}) {
	if !m.timed || i >= len(m.chunks) {
		return
	}
	c := m.chunks[i]
	metadata[chunkStartKey] = strconv.FormatInt(c.startMS, 10)
	metadata[chunkEndKey] = strconv.FormatInt(c.endMS, 10)
	props["start_ms"] = c.startMS
	props["end_ms"] = c.endMS
}

// storedMediaSegments returns the stored text of a media document as
// segments, one per chunk, when the document holds this file derived by
// these derivers: then nothing needs deriving again. Nil otherwise.
func (db *DB) storedMediaSegments(ctx context.Context, documentID, sha, derivedBy string) ([]MediaSegment, error) {
	doc, err := db.store.GetDocument(ctx, documentID)
	if errors.Is(err, core.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get existing document: %w", err)
	}
	stored := anyMapToStringMap(doc.Metadata)
	if stored[contentHashKey] != sha || stored[mediaDerivedByKey] != derivedBy {
		return nil, nil
	}
	chunks, err := db.store.GetByDocID(ctx, documentID)
	if err != nil {
		return nil, fmt.Errorf("list chunks of %s: %w", documentID, err)
	}
	sort.Slice(chunks, func(i, j int) bool {
		a, _ := strconv.Atoi(chunks[i].Metadata["chunk_index"])
		b, _ := strconv.Atoi(chunks[j].Metadata["chunk_index"])
		return a < b
	})
	out := make([]MediaSegment, 0, len(chunks))
	for _, c := range chunks {
		start, _ := strconv.ParseInt(c.Metadata[chunkStartKey], 10, 64)
		end, _ := strconv.ParseInt(c.Metadata[chunkEndKey], 10, 64)
		out = append(out, MediaSegment{StartMS: start, EndMS: end, Text: c.Content})
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// mediaHitOf reads a hit's media marks from its document's metadata and
// takes the spans, a list per chunk, out of the metadata it reports.
func mediaHitOf(metadata map[string]string, chunkIDs []string, documentID string) *MediaHit {
	mediaType := metadata[mediaTypeKey]
	raw, hasSpans := metadata[mediaSpansKey]
	delete(metadata, mediaSpansKey)
	if mediaType == "" {
		return nil
	}
	hit := &MediaHit{MediaType: mediaType}
	hit.DurationMS, _ = strconv.ParseInt(metadata[mediaDurationKey], 10, 64)
	if !hasSpans {
		return hit
	}
	var spans [][2]int64
	if json.Unmarshal([]byte(raw), &spans) != nil {
		return hit
	}
	prefix := "chunk:" + documentID + ":"
	for _, id := range chunkIDs {
		index, err := strconv.Atoi(strings.TrimPrefix(id, prefix))
		if !strings.HasPrefix(id, prefix) || err != nil || index < 0 || index >= len(spans) {
			continue
		}
		hit.Spans = append(hit.Spans, MediaSpan{ChunkID: id, StartMS: spans[index][0], EndMS: spans[index][1]})
	}
	return hit
}

// appendMediaEntities runs the extraction knowledge_save runs on a media
// document's chunks and adds the entity nodes, mention edges and relations
// to the plan. Entity vectors come from the embedder, or are lexical
// vectors of the collection's width without one.
func (t *GraphRAGToolbox) appendMediaEntities(ctx context.Context, req ToolIngestDocumentRequest, collection string, chunks, chunkIDs []string, plan *knowledgeMutationPlan) error {
	db := t.db
	input := knowledgeMutationInput{KnowledgeID: req.DocumentID, Title: req.Title, Content: req.Content, Collection: collection}
	entityTexts, entityMentions, relationships, err := db.extractKnowledgeEntities(ctx, input, chunks, chunkIDs)
	if err != nil {
		return err
	}
	if err := db.validateExtractedGraphData(ctx, entityTexts, relationships); err != nil {
		return fmt.Errorf("validate extracted graph data: %w", err)
	}
	vectors := func(ctx context.Context, texts []string) ([][]float32, error) {
		if db.HasEmbedder() {
			return db.embedder.EmbedBatch(ctx, texts)
		}
		dim, err := t.lexicalVectorDim(ctx, collection)
		if err != nil {
			return nil, err
		}
		out := make([][]float32, len(texts))
		for i, text := range texts {
			out[i] = lexicalVectorForText(text, dim)
		}
		return out, nil
	}
	entityNodes, _, mentionEdges, relationEdges, _, err := db.buildExtractedEntityArtifacts(ctx, entityTexts, entityMentions, relationships, vectors)
	if err != nil {
		return err
	}
	if err := db.mergeEntityNodeProperties(ctx, entityNodes, collection); err != nil {
		return err
	}
	recordEntitySourceDocument(entityNodes, req.DocumentID)
	plan.graphOps.NodeUpserts = append(plan.graphOps.NodeUpserts, sortedNodePointers(entityNodes)...)
	plan.graphOps.EdgeUpserts = append(plan.graphOps.EdgeUpserts, mentionEdges...)
	plan.graphOps.EdgeUpserts = append(plan.graphOps.EdgeUpserts, relationEdges...)
	return nil
}
