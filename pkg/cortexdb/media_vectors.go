package cortexdb

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// A media embedder puts files and text into one vector space (CLIP or
// SigLIP for images, CLAP for sound), so an image is found by what it shows
// even where no caption says it, and an image can be the question. Its
// vectors are not the text index's: they live in media_vectors, one space
// per model and width, written in the transaction that writes the chunks
// and deleted with the document. A hit there is a chunk of the document,
// the one that covers the moment the vector was taken at, and joins the
// text search's answer by reciprocal rank fusion.

// MediaVector is one vector of a file. StartMS and EndMS place it in a
// recording or a video, and are zero for an image.
type MediaVector struct {
	StartMS int64
	EndMS   int64
	Vector  []float32
}

// MediaEmbedder embeds files and text into one space. EmbedMedia returns
// the vectors of a file, one for an image, one or more spans for a
// recording, and ErrUnsupportedMedia for a kind it does not embed; a video
// is embedded frame by frame when the brain has a FrameSampler. Name names
// the model: vectors of another name, or of another width, are another
// space and are never compared.
type MediaEmbedder interface {
	Name() string
	EmbedMedia(ctx context.Context, media MediaInput) ([]MediaVector, error)
	EmbedText(ctx context.Context, text string) ([]float32, error)
}

// DefaultMediaScoreFloor is the cosine a media vector must reach to be a
// hit. It suits CLIP-style text-to-image scores, which sit far below the
// text index's; WithMediaScoreFloor sets another for another model.
const DefaultMediaScoreFloor = 0.2

// mediaScoreRatio is how close to the best media hit another must come to
// be kept. A model's scores sit on a scale of its own, and another for an
// image query than for a text one (CLIP: about 0.2 to 0.35 from text, 0.5
// to 1 from an image): a ratio to the best hit cuts the tail on either,
// where the floor only keeps a query no image matches from finding any.
const mediaScoreRatio = 0.85

// DegradedMediaSearchFailed: the media vectors were not searched (the query
// could not be embedded, or the search failed), so the answer is the text
// search alone.
const DegradedMediaSearchFailed = "media_search_failed"

// explainMedia names the media vector search in an explain record.
const explainMedia = "media"

// mediaEmbeddedByKey names the space a media document's vectors are in.
const mediaEmbeddedByKey = "_media_embedded_by"

// WithMediaEmbedder sets the model that embeds images, sound and text into
// one space.
func WithMediaEmbedder(e MediaEmbedder) Option { return func(db *DB) { db.media.embedder = e } }

// WithMediaScoreFloor sets the cosine a media vector must reach to be a
// hit; zero keeps DefaultMediaScoreFloor.
func WithMediaScoreFloor(floor float64) Option {
	return func(db *DB) { db.media.scoreFloor = floor }
}

func (m mediaRuntime) floor() float64 {
	if m.scoreFloor > 0 {
		return m.scoreFloor
	}
	return DefaultMediaScoreFloor
}

// mediaSpace names the space of vectors of this width by this model.
func mediaSpace(e MediaEmbedder, dim int) string { return fmt.Sprintf("%s:%d", e.Name(), dim) }

// mediaTable makes media_vectors once per process.
type mediaTable struct {
	mu    sync.Mutex
	ready bool
}

func (db *DB) ensureMediaVectorTable(ctx context.Context) error {
	db.mediaTable.mu.Lock()
	defer db.mediaTable.mu.Unlock()
	if db.mediaTable.ready {
		return nil
	}
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS media_vectors (
			id TEXT PRIMARY KEY,
			doc_id TEXT NOT NULL,
			collection TEXT NOT NULL,
			space TEXT NOT NULL,
			chunk_id TEXT NOT NULL,
			start_ms BIGINT NOT NULL DEFAULT 0,
			end_ms BIGINT NOT NULL DEFAULT 0,
			vector TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_media_vectors_space ON media_vectors(collection, space)`,
		`CREATE INDEX IF NOT EXISTS idx_media_vectors_doc ON media_vectors(doc_id)`,
	} {
		if _, err := db.store.GetDB().ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create media_vectors: %w", err)
		}
	}
	db.mediaTable.ready = true
	return nil
}

// mediaVectorRow is a vector as it is stored.
type mediaVectorRow struct {
	id, docID, collection, space, chunkID string
	startMS, endMS                        int64
	vector                                []float32
}

func encodeMediaVector(v []float32) string {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[4*i:], math.Float32bits(f))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func decodeMediaVector(s string) ([]float32, error) {
	buf, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(buf)%4 != 0 {
		return nil, fmt.Errorf("media vector is not a float32 vector")
	}
	v := make([]float32, len(buf)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[4*i:]))
	}
	return v, nil
}

// unitVector is v scaled to length one, nil for a zero or non-finite
// vector, which has no direction to compare.
func unitVector(v []float32) []float32 {
	var sum float64
	for _, f := range v {
		sum += float64(f) * float64(f)
	}
	norm := math.Sqrt(sum)
	if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return nil
	}
	out := make([]float32, len(v))
	for i, f := range v {
		out[i] = float32(float64(f) / norm)
	}
	return out
}

// writeMediaVectorsTx replaces a document's media vectors. A write over an
// existing document always clears them: a document saved again as text, or
// as media with no media embedder, has none.
func (db *DB) writeMediaVectorsTx(ctx context.Context, tx *sql.Tx, docID string, existing bool, rows []mediaVectorRow) error {
	if existing {
		if _, err := db.txExec(ctx, tx, `DELETE FROM media_vectors WHERE doc_id = ?`, docID); err != nil {
			return fmt.Errorf("delete prior media vectors: %w", err)
		}
	}
	for _, r := range rows {
		if _, err := db.txExec(ctx, tx,
			`INSERT INTO media_vectors (id, doc_id, collection, space, chunk_id, start_ms, end_ms, vector) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			r.id, r.docID, r.collection, r.space, r.chunkID, r.startMS, r.endMS, encodeMediaVector(r.vector)); err != nil {
			return fmt.Errorf("write media vector: %w", err)
		}
	}
	return nil
}

// reusableMediaVectors returns the vectors a media document holds for this
// file in the media embedder's space, and that space; nil when it holds
// another file, vectors of another model, or none.
func (db *DB) reusableMediaVectors(ctx context.Context, documentID, sha string) ([]MediaVector, string, error) {
	doc, err := db.store.GetDocument(ctx, documentID)
	if errors.Is(err, core.ErrNotFound) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("get existing document: %w", err)
	}
	stored := anyMapToStringMap(doc.Metadata)
	space := stored[mediaEmbeddedByKey]
	if stored[contentHashKey] != sha || !strings.HasPrefix(space, db.media.embedder.Name()+":") {
		return nil, "", nil
	}
	if err := db.ensureMediaVectorTable(ctx); err != nil {
		return nil, "", err
	}
	rows, err := db.query(ctx, `SELECT start_ms, end_ms, vector FROM media_vectors WHERE doc_id = ? AND space = ? ORDER BY id`, documentID, space)
	if err != nil {
		return nil, "", fmt.Errorf("read media vectors of %s: %w", documentID, err)
	}
	var out []MediaVector
	err = eachRow(rows, func() error {
		var v MediaVector
		var encoded string
		if err := rows.Scan(&v.StartMS, &v.EndMS, &encoded); err != nil {
			return fmt.Errorf("scan media vector: %w", err)
		}
		vec, err := decodeMediaVector(encoded)
		if err != nil {
			return err
		}
		v.Vector = vec
		out = append(out, v)
		return nil
	})
	if err != nil || len(out) == 0 {
		return nil, "", err
	}
	return out, space, nil
}

// embedMediaFile returns the vectors of a file and the space they are in:
// nil when the brain has no media embedder or it does not embed this kind.
// A video is embedded frame by frame when frames are at hand, each frame's
// vector covering the time until the next frame.
func (m mediaRuntime) embedMediaFile(ctx context.Context, kind mediaKind, in MediaInput, frames func() ([]MediaFrame, error), durationMS int64) ([]MediaVector, string, error) {
	e := m.embedder
	if e == nil || len(in.Data) == 0 {
		return nil, "", nil
	}
	var out []MediaVector
	if kind == mediaVideo && m.frames != nil {
		got, err := frames()
		if err != nil {
			return nil, "", err
		}
		sampled := slices.Clone(got)
		sort.SliceStable(sampled, func(i, j int) bool { return sampled[i].AtMS < sampled[j].AtMS })
		for i, f := range sampled {
			vecs, err := e.EmbedMedia(ctx, f.Image)
			if errors.Is(err, ErrUnsupportedMedia) {
				return nil, "", nil
			}
			if err != nil {
				return nil, "", fmt.Errorf("embed the frame at %d ms: %w", f.AtMS, err)
			}
			until := max(durationMS, f.AtMS)
			if i+1 < len(sampled) {
				until = sampled[i+1].AtMS
			}
			for _, v := range vecs {
				out = append(out, MediaVector{StartMS: f.AtMS, EndMS: until, Vector: v.Vector})
			}
		}
	} else {
		vecs, err := e.EmbedMedia(ctx, in)
		if errors.Is(err, ErrUnsupportedMedia) {
			return nil, "", nil
		}
		if err != nil {
			return nil, "", fmt.Errorf("embed media: %w", err)
		}
		out = vecs
	}
	if len(out) > m.segmentLimit() {
		return nil, "", fmt.Errorf("%w: %d media vectors, the limit is %d", ErrMediaTooLarge, len(out), m.segmentLimit())
	}
	dim := 0
	kept := out[:0]
	for _, v := range out {
		unit := unitVector(v.Vector)
		if unit == nil {
			return nil, "", fmt.Errorf("embed media: %s returned a vector with no direction", e.Name())
		}
		if dim == 0 {
			dim = len(unit)
		} else if len(unit) != dim {
			return nil, "", fmt.Errorf("embed media: %s returned vectors of widths %d and %d", e.Name(), dim, len(unit))
		}
		if kind == mediaImage {
			v.StartMS, v.EndMS = 0, 0
		}
		v.Vector = unit
		kept = append(kept, v)
	}
	if len(kept) == 0 {
		return nil, "", nil
	}
	return kept, mediaSpace(e, dim), nil
}

// vectorRows places each vector on the chunk that covers it: of the chunks
// whose span holds the vector's midpoint, the narrowest, so a frame lands
// on what is said while it is shown rather than on a caption that holds
// the whole video; else the nearest chunk.
func (m *mediaPayload) vectorRows(docID, collection string, chunkIDs []string) []mediaVectorRow {
	if len(m.vectors) == 0 || len(chunkIDs) == 0 {
		return nil
	}
	rows := make([]mediaVectorRow, 0, len(m.vectors))
	for i, v := range m.vectors {
		chunk := 0
		if m.timed {
			mid := (v.StartMS + max(v.EndMS, v.StartMS)) / 2
			bestGap, bestSpan := int64(math.MaxInt64), int64(math.MaxInt64)
			for j, c := range m.chunks {
				if j >= len(chunkIDs) {
					break
				}
				var gap int64
				switch {
				case mid < c.startMS:
					gap = c.startMS - mid
				case mid > c.endMS:
					gap = mid - c.endMS
				}
				span := c.endMS - c.startMS
				if gap < bestGap || (gap == bestGap && span < bestSpan) {
					bestGap, bestSpan, chunk = gap, span, j
				}
			}
		}
		rows = append(rows, mediaVectorRow{
			id:         fmt.Sprintf("media:%s:%d", docID, i),
			docID:      docID,
			collection: collection,
			space:      m.space,
			chunkID:    chunkIDs[chunk],
			startMS:    v.StartMS,
			endMS:      v.EndMS,
			vector:     v.Vector,
		})
	}
	return rows
}

// mediaVectorHit is a chunk a media vector search found, with its best
// cosine.
type mediaVectorHit struct {
	docID, chunkID string
	score          float64
}

// searchMediaVectors compares the query vectors with every vector of the
// space in collection and returns the chunks whose best cosine reaches
// floor and mediaScoreRatio of the best one, best first, at most topK. The scan is exact: a media library is
// small next to a text corpus.
func (db *DB) searchMediaVectors(ctx context.Context, collection, space string, queries [][]float32, floor float64, topK int) ([]mediaVectorHit, error) {
	if err := db.ensureMediaVectorTable(ctx); err != nil {
		return nil, err
	}
	rows, err := db.query(ctx, `SELECT doc_id, chunk_id, vector FROM media_vectors WHERE collection = ? AND space = ?`, collection, space)
	if err != nil {
		return nil, fmt.Errorf("search media vectors: %w", err)
	}
	best := map[string]*mediaVectorHit{}
	err = eachRow(rows, func() error {
		var docID, chunkID, encoded string
		if err := rows.Scan(&docID, &chunkID, &encoded); err != nil {
			return fmt.Errorf("scan media vector: %w", err)
		}
		vec, err := decodeMediaVector(encoded)
		if err != nil {
			return err
		}
		for _, q := range queries {
			if len(q) != len(vec) {
				continue
			}
			var dot float64
			for i := range q {
				dot += float64(q[i]) * float64(vec[i])
			}
			if dot < floor {
				continue
			}
			if h, ok := best[chunkID]; !ok || dot > h.score {
				best[chunkID] = &mediaVectorHit{docID: docID, chunkID: chunkID, score: dot}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]mediaVectorHit, 0, len(best))
	for _, h := range best {
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].chunkID < out[j].chunkID
	})
	for i := range out {
		if out[i].score < out[0].score*mediaScoreRatio {
			out = out[:i]
			break
		}
	}
	if topK > 0 && len(out) > topK {
		out = out[:topK]
	}
	return out, nil
}

// mediaChunksFor searches the media vectors of collection with the query
// vectors and loads the chunks they found that the caller may see.
func (db *DB) mediaChunksFor(ctx context.Context, collection string, filters *RetrievalFilters, queries [][]float32, topK int) ([]GraphRAGChunkResult, error) {
	if len(queries) == 0 {
		return nil, nil
	}
	// Ingest names the default collection, a search leaves it empty.
	collection = firstNonEmpty(collection, defaultGraphRAGCollection)
	space := mediaSpace(db.media.embedder, len(queries[0]))
	hits, err := db.searchMediaVectors(ctx, collection, space, queries, db.media.floor(), topK)
	if err != nil || len(hits) == 0 {
		return nil, err
	}
	docIDs := make([]string, 0, len(hits))
	for _, h := range hits {
		docIDs = append(docIDs, h.docID)
	}
	hidden := db.hiddenAmong(ctx, filters, docIDs)
	ids := make([]string, 0, len(hits))
	scores := make(map[string]float64, len(hits))
	for _, h := range hits {
		if _, gone := hidden[h.docID]; gone {
			continue
		}
		ids = append(ids, h.chunkID)
		scores[h.chunkID] = h.score
	}
	loaded, err := db.GraphRAGTools().loadToolChunks(ctx, scores, ids, false, 0)
	if err != nil {
		return nil, err
	}
	out := make([]GraphRAGChunkResult, 0, len(loaded))
	for i, c := range loaded {
		out = append(out, GraphRAGChunkResult{
			ID:          c.ID,
			DocumentID:  c.DocumentID,
			Content:     c.Content,
			Score:       c.Score,
			BaseScore:   c.Score,
			RerankScore: c.Score,
			MediaScore:  c.Score,
			MediaRank:   i + 1,
		})
	}
	return out, nil
}

// fuseMediaChunks adds the media hits to a search's answer. The chunks the
// text search found keep their order and their scores, a media hit among
// them only marked with its own: a caption and a CLIP-style model mostly
// read the same thing in an image, and counting both would put an image
// above the text that answers. A chunk found by its media vectors alone
// joins at its rank, after the text hit of the same rank, as reciprocal
// rank fusion of the two lists would place it, with a score between its
// neighbours' so that whatever orders by score next keeps the place.
// Returns at most topK chunks (0 = all).
func fuseMediaChunks(text, media []GraphRAGChunkResult, topK int) []GraphRAGChunkResult {
	type ranked struct {
		chunk     GraphRAGChunkResult
		rrf       float64
		mediaOnly bool
	}
	inText := make(map[string]int, len(text))
	merged := make([]ranked, 0, len(text)+len(media))
	for rank, c := range text {
		inText[c.ID] = len(merged)
		merged = append(merged, ranked{chunk: c, rrf: 1.0 / (hybridRRFK + float64(rank+1))})
	}
	for rank, c := range media {
		if i, ok := inText[c.ID]; ok {
			merged[i].chunk.MediaScore, merged[i].chunk.MediaRank = c.MediaScore, rank+1
			continue
		}
		c.MediaRank = rank + 1
		merged = append(merged, ranked{chunk: c, rrf: 1.0 / (hybridRRFK + float64(rank+1)), mediaOnly: true})
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].rrf > merged[j].rrf })
	if topK > 0 && len(merged) > topK {
		merged = merged[:topK]
	}
	out := make([]GraphRAGChunkResult, len(merged))
	for i, r := range merged {
		out[i] = r.chunk
	}
	if len(text) == 0 {
		for i, r := range merged {
			out[i].Score, out[i].BaseScore, out[i].RerankScore = r.rrf, r.rrf, r.rrf
		}
		return out
	}
	// Each run of media-only chunks takes scores evenly between the text
	// hit above it and the one below (half the one above at the end).
	for i := 0; i < len(merged); {
		if !merged[i].mediaOnly {
			i++
			continue
		}
		j := i
		for j < len(merged) && merged[j].mediaOnly {
			j++
		}
		hi := out[i-1].Score
		lo := hi / 2
		if j < len(merged) {
			lo = out[j].Score
		}
		for k := i; k < j; k++ {
			score := hi - (hi-lo)*float64(k-i+1)/float64(j-i+1)
			out[k].Score, out[k].BaseScore, out[k].RerankScore = score, score, score
		}
		i = j
	}
	return out
}

// addMediaHits runs the text query against the media vectors and fuses
// what it finds into result. Nothing changes when no media vector reaches
// the floor, so a brain without media answers as it did.
func (db *DB) addMediaHits(ctx context.Context, result *GraphRAGQueryResult, query string, filters *RetrievalFilters, opts GraphRAGQueryOptions) error {
	if db.media.embedder == nil || strings.TrimSpace(query) == "" {
		return nil
	}
	qv, err := db.media.embedder.EmbedText(ctx, query)
	var media []GraphRAGChunkResult
	if err == nil {
		if unit := unitVector(qv); unit != nil {
			media, err = db.mediaChunksFor(ctx, opts.Collection, filters, [][]float32{unit}, max(opts.TopK, 1))
		}
	}
	if err != nil {
		result.Degraded = addDegraded(result.Degraded, DegradedMediaSearchFailed)
		return nil
	}
	recallExplainerOf(ctx).candidates(explainKnowledge, explainMedia, chunkCandidates(media, func(c GraphRAGChunkResult) float64 { return c.MediaScore }))
	if len(media) == 0 {
		return nil
	}
	result.Chunks = fuseMediaChunks(result.Chunks, media, max(opts.TopK, len(result.Chunks)))
	result.Context = buildGraphRAGContext(result.Chunks)
	if opts.ChunkWindow > 0 {
		return db.widenGraphRAGContext(ctx, result, opts)
	}
	return nil
}

// ToolSearchMediaRequest searches the media vectors with text, or with a
// file: an image finds the images and frames that look like it.
type ToolSearchMediaRequest struct {
	Query      string `json:"query,omitempty"`
	MediaType  string `json:"media_type,omitempty"`
	Bytes      []byte `json:"-"`
	Data       string `json:"data,omitempty"`
	Collection string `json:"collection,omitempty"`
	TopK       int    `json:"top_k,omitempty"`
}

// ToolSearchMediaResponse is the media documents found, grouped as
// knowledge_search groups them, with the chunks behind them.
type ToolSearchMediaResponse struct {
	Results []KnowledgeSearchHit  `json:"results"`
	Chunks  []GraphRAGChunkResult `json:"chunks,omitempty"`
}

// ErrNoMediaEmbedder is returned by search_media on a brain without a media
// embedder.
var ErrNoMediaEmbedder = errors.New("cortexdb: no media embedder: configure one to search images and sound by their content")

// SearchMedia finds the media documents whose images, frames or sound are
// nearest a text or a file, in the media embedder's space.
func (t *GraphRAGToolbox) SearchMedia(ctx context.Context, req ToolSearchMediaRequest) (*ToolSearchMediaResponse, error) {
	db := t.db
	e := db.media.embedder
	if e == nil {
		return nil, ErrNoMediaEmbedder
	}
	file, err := mediaBytes(ToolIngestMediaRequest{Bytes: req.Bytes, Data: req.Data}, db.media.byteLimit())
	if err != nil {
		return nil, err
	}
	query := strings.TrimSpace(req.Query)
	if (query == "") == (len(file) == 0) {
		return nil, fmt.Errorf("cortexdb: invalid request: query or data is required, not both")
	}
	opts := GraphRAGQueryOptions{Collection: req.Collection, TopK: req.TopK}
	applyGraphRAGQueryDefaults(&opts)
	if !knowledgeReadPermitted(ctx, opts.Collection) {
		return &ToolSearchMediaResponse{Results: []KnowledgeSearchHit{}}, nil
	}
	var queries [][]float32
	if query != "" {
		qv, err := e.EmbedText(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("embed query: %w", err)
		}
		queries = append(queries, qv)
	} else {
		kind, ok := mediaKindOf(req.MediaType)
		if !ok {
			return nil, ErrUnsupportedMedia
		}
		vecs, _, err := db.media.embedMediaFile(ctx, kind, MediaInput{Data: file, MediaType: req.MediaType}, func() ([]MediaFrame, error) {
			return db.media.frames.SampleFrames(ctx, MediaInput{Data: file, MediaType: req.MediaType})
		}, 0)
		if err != nil {
			return nil, err
		}
		if len(vecs) == 0 {
			return nil, fmt.Errorf("%w: %s does not embed %s", ErrUnsupportedMedia, e.Name(), req.MediaType)
		}
		for _, v := range vecs {
			queries = append(queries, v.Vector)
		}
	}
	for i, q := range queries {
		if queries[i] = unitVector(q); queries[i] == nil {
			return nil, fmt.Errorf("embed query: %s returned a vector with no direction", e.Name())
		}
	}
	chunks, err := db.mediaChunksFor(ctx, opts.Collection, nil, queries, opts.TopK)
	if err != nil {
		return nil, err
	}
	hits, err := db.aggregateKnowledgeHits(ctx, chunks)
	if err != nil {
		return nil, err
	}
	if hits == nil {
		hits = []KnowledgeSearchHit{}
	}
	return &ToolSearchMediaResponse{Results: hits, Chunks: chunks}, nil
}
