package cortexdb

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Widening a retrieved chunk to the chunks around it.
//
// Chunking is a trade with no winning side: a chunk small enough to retrieve
// precisely is too small to read, and a chunk large enough to read dilutes the
// one sentence that matched with everything around it. The standard answer —
// the parent document retriever — is to embed the small unit and return the
// larger one it came from. This is the same move done at chunk granularity:
// the hit is still what matched, and its neighbours in the same document come
// along so the passage starts at a sentence boundary, ends at one, and says
// who "she" is.
//
// Every chunk this brain writes already carries the two facts needed for that
// — its document_id and its chunk_index — and until now nothing read them
// back. The widening is opt-in via GraphRAGQueryOptions.ChunkWindow because
// what retrieval returns is what every downstream answer is built from, and a
// silent change to it is a silent change to all of them.

// windowNeighbourTag marks a context line that is present for continuity
// rather than because it matched.
//
// It goes inside the same bracketed prefix the assembled context already uses,
// so a hit's line is byte-for-byte what it was before widening existed and the
// only new bytes in the prompt are the neighbours' own lines. A model, or a
// person reading the prompt, can then tell retrieved text from surrounding
// text — which is the same demand this repository makes of every other claim
// it stores.
const windowNeighbourTag = "+context"

// GraphRAGWindowSegment is one chunk inside a widened span.
type GraphRAGWindowSegment struct {
	ID         string
	DocumentID string
	// ChunkIndex is the chunk's position in its document. It is -1 when the
	// chunk carries no chunk_index metadata, which is how a row written by
	// something other than the three ingest paths appears: such a chunk cannot
	// be widened, and says so rather than guessing at a position.
	ChunkIndex int
	Content    string
	// Hit reports whether this chunk was retrieved on its own merits. False
	// means it was pulled in only to make its neighbour readable.
	Hit bool
	// Score is the retrieval score of a hit. It is zero for a neighbour, which
	// was never scored — not scored zero, never scored at all, which is why
	// neighbours are never reranked and never counted against the caller's
	// top-K.
	Score float64
}

// GraphRAGChunkWindow is a run of consecutive chunks from one document, at
// least one of which is a hit.
//
// A run, not a chunk plus its two neighbours: when two hits in one document sit
// close enough that their windows touch or overlap, they become one span, so
// the chunks between them appear once instead of once per hit. Repeating a
// passage in a prompt wastes the budget and tells the model the passage matters
// twice.
//
// Segments are in document order and carry their chunk text verbatim. Adjacent
// chunks can still share a sentence or two, because the chunker overlaps them
// on purpose when asked to; that repetition is the chunker's and is left alone
// rather than trimmed, since the alternative is a segment whose Content is not
// what the store holds.
type GraphRAGChunkWindow struct {
	DocumentID string
	Segments   []GraphRAGWindowSegment
	// Truncated reports that this span wanted more neighbours than the
	// caller's MaxContextChars would hold. The hits are always kept — a
	// widening that dropped the text that matched in order to fit the text
	// that did not would be worse than no widening — so a truncated span is
	// the hit plus however much continuity fitted around it.
	Truncated bool
}

// documentChunkIndex is one document's chunks, addressable by position.
type documentChunkIndex struct {
	byIndex map[int]documentChunk
	indexOf map[string]int
}

type documentChunk struct {
	id      string
	content string
}

// loadDocumentChunkIndex reads back every chunk of one document and positions
// it by its chunk_index metadata.
//
// The whole document is read because the store indexes chunks by id and by
// doc_id, not by position, so there is no cheaper way to ask for "the chunk
// before this one" — and because the answer is reused by every hit in the same
// document, which is the common case once PerDocumentLimit allows more than
// one.
func (db *DB) loadDocumentChunkIndex(ctx context.Context, documentID string) (*documentChunkIndex, error) {
	rows, err := db.store.GetByDocID(ctx, documentID)
	if err != nil {
		return nil, err
	}
	index := &documentChunkIndex{
		byIndex: make(map[int]documentChunk, len(rows)),
		indexOf: make(map[string]int, len(rows)),
	}
	for _, row := range rows {
		if row == nil {
			continue
		}
		// chunk_index is a string here and an int in the graph node's
		// properties, because embedding metadata is map[string]string and node
		// properties are map[string]interface{}. Same number, two stores, two
		// types the stores each demand; the vector row is the one being read.
		raw, ok := row.Metadata["chunk_index"]
		if !ok {
			continue
		}
		position, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		if _, taken := index.byIndex[position]; taken {
			continue
		}
		index.byIndex[position] = documentChunk{id: row.ID, content: row.Content}
		index.indexOf[row.ID] = position
	}
	return index, nil
}

// chunkWindowSpan is a span under construction: an interval of chunk positions
// in one document, the hits inside it, and the neighbours admitted so far.
type chunkWindowSpan struct {
	documentID string
	// rank is the best position in the packed chunk list held by any hit in
	// this span. Spans are emitted in rank order, so the most relevant passage
	// still comes first and a span keeps the place of its best hit when two
	// hits merge into it.
	rank     int
	lo, hi   int
	hits     map[int]*GraphRAGChunkResult
	admitted map[int]struct{}
	// closed stops a span growing once one of its neighbours did not fit, so
	// the neighbours it did get stay contiguous with the hit instead of
	// leaving a hole in the middle of the passage.
	closed    bool
	truncated bool
	// unresolved holds the chunk when its position in the document could not
	// be determined. Such a span is the hit and nothing else — it is still
	// emitted, because dropping it would lose a hit from the context.
	unresolved *GraphRAGChunkResult
}

// widenGraphRAGContext replaces the assembled context with one built from
// widened spans, and records those spans on the result.
//
// It is a no-op unless the caller asked for a window, and deliberately so: the
// caller who does not ask gets the chunks, the ordering and the context string
// it got before this file existed.
func (db *DB) widenGraphRAGContext(ctx context.Context, result *GraphRAGQueryResult, opts GraphRAGQueryOptions) error {
	if result == nil || opts.ChunkWindow <= 0 || len(result.Chunks) == 0 {
		return nil
	}

	indexes, err := db.loadWindowDocumentIndexes(ctx, result.Chunks)
	if err != nil {
		return err
	}

	spans := buildChunkWindowSpans(result.Chunks, indexes, opts.ChunkWindow)
	admitChunkWindowNeighbours(spans, indexes, opts)
	windows := renderChunkWindows(spans, indexes)

	result.Windows = windows
	result.Context = buildChunkWindowContext(windows)
	return nil
}

// loadWindowDocumentIndexes loads the position index of every document the
// packed chunks came from, once per document.
func (db *DB) loadWindowDocumentIndexes(ctx context.Context, chunks []GraphRAGChunkResult) (map[string]*documentChunkIndex, error) {
	indexes := make(map[string]*documentChunkIndex, len(chunks))
	for i := range chunks {
		documentID := chunks[i].DocumentID
		if documentID == "" {
			continue
		}
		if _, done := indexes[documentID]; done {
			continue
		}
		index, err := db.loadDocumentChunkIndex(ctx, documentID)
		if err != nil {
			return nil, fmt.Errorf("load chunks of document %q for widening: %w", documentID, err)
		}
		indexes[documentID] = index
	}
	return indexes, nil
}

// buildChunkWindowSpans turns each packed chunk into the interval it would like
// to occupy, then merges the intervals that touch within a document.
func buildChunkWindowSpans(chunks []GraphRAGChunkResult, indexes map[string]*documentChunkIndex, window int) []*chunkWindowSpan {
	perDocument := make(map[string][]*chunkWindowSpan)
	documentOrder := make([]string, 0, len(indexes))
	spans := make([]*chunkWindowSpan, 0, len(chunks))

	for rank := range chunks {
		chunk := &chunks[rank]
		index := indexes[chunk.DocumentID]
		position, located := -1, false
		if index != nil {
			position, located = index.indexOf[chunk.ID]
		}
		if !located {
			spans = append(spans, &chunkWindowSpan{
				documentID: chunk.DocumentID,
				rank:       rank,
				unresolved: chunk,
			})
			continue
		}

		// The interval stops at the first position the document does not have,
		// so a window never claims continuity across a gap it cannot see.
		lo, hi := position, position
		for step := 1; step <= window; step++ {
			if _, present := index.byIndex[position-step]; !present {
				break
			}
			lo = position - step
		}
		for step := 1; step <= window; step++ {
			if _, present := index.byIndex[position+step]; !present {
				break
			}
			hi = position + step
		}

		if _, seen := perDocument[chunk.DocumentID]; !seen {
			documentOrder = append(documentOrder, chunk.DocumentID)
		}
		perDocument[chunk.DocumentID] = append(perDocument[chunk.DocumentID], &chunkWindowSpan{
			documentID: chunk.DocumentID,
			rank:       rank,
			lo:         lo,
			hi:         hi,
			hits:       map[int]*GraphRAGChunkResult{position: chunk},
			admitted:   make(map[int]struct{}),
		})
	}

	for _, documentID := range documentOrder {
		spans = append(spans, mergeChunkWindowSpans(perDocument[documentID])...)
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].rank < spans[j].rank })
	return spans
}

// mergeChunkWindowSpans collapses intervals in one document that overlap or sit
// next to each other into a single span.
//
// Touching counts as overlapping — hi+1 == lo means the two intervals cover an
// unbroken run of chunks, and emitting them separately would print a seam in
// the middle of continuous prose.
func mergeChunkWindowSpans(spans []*chunkWindowSpan) []*chunkWindowSpan {
	if len(spans) < 2 {
		return spans
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].lo < spans[j].lo })

	merged := make([]*chunkWindowSpan, 0, len(spans))
	for _, span := range spans {
		last := len(merged) - 1
		if last >= 0 && span.lo <= merged[last].hi+1 {
			into := merged[last]
			if span.hi > into.hi {
				into.hi = span.hi
			}
			if span.rank < into.rank {
				into.rank = span.rank
			}
			for position, hit := range span.hits {
				into.hits[position] = hit
			}
			continue
		}
		merged = append(merged, span)
	}
	return merged
}

// admitChunkWindowNeighbours spends the caller's character budget on continuity.
//
// The hits are charged to the budget first and never refused: they are what the
// caller asked for, packGraphRAGContext already fitted them, and a widening
// that evicted a hit to make room for a neighbour would be a bug wearing a
// feature's clothes. What is left over buys neighbours, and it buys them
// breadth-first — every span's innermost ring before any span's second ring —
// so one long passage cannot eat the continuity of all the others.
func admitChunkWindowNeighbours(spans []*chunkWindowSpan, indexes map[string]*documentChunkIndex, opts GraphRAGQueryOptions) {
	budget := opts.MaxContextChars
	used := 0
	for _, span := range spans {
		if span.unresolved != nil {
			used += len(windowSegmentLine(GraphRAGWindowSegment{
				ID:         span.unresolved.ID,
				DocumentID: span.unresolved.DocumentID,
				Content:    span.unresolved.Content,
				Hit:        true,
			})) + 1
			continue
		}
		for _, hit := range span.hits {
			used += len(windowSegmentLine(GraphRAGWindowSegment{
				ID:         hit.ID,
				DocumentID: hit.DocumentID,
				Content:    hit.Content,
				Hit:        true,
			})) + 1
		}
	}

	fits := func(cost int) bool {
		// A non-positive budget is no budget rather than a zero one: the query
		// defaults always set one, so this only happens when a caller builds
		// the options by hand, and refusing everything would be a stranger
		// reading of an unset field than allowing everything.
		return budget <= 0 || used+cost <= budget
	}
	cost := func(span *chunkWindowSpan, index *documentChunkIndex, position int) int {
		chunk := index.byIndex[position]
		return len(windowSegmentLine(GraphRAGWindowSegment{
			ID:         chunk.id,
			DocumentID: span.documentID,
			Content:    chunk.content,
		})) + 1
	}

	// The gaps between the hits of a merged span come first and go in together.
	// Half a gap is a hole, and a span with a hole claims a continuity it does
	// not have; a span that cannot afford its own middle is better left as the
	// separate passages it was before the merge, which is what the contiguous
	// runs at render time turn it back into.
	for _, span := range spans {
		if span.unresolved != nil {
			continue
		}
		low, high := span.hitBounds()
		interior := make([]int, 0, high-low)
		total := 0
		index := indexes[span.documentID]
		for position := low + 1; position < high; position++ {
			if _, isHit := span.hits[position]; isHit {
				continue
			}
			interior = append(interior, position)
			total += cost(span, index, position)
		}
		if len(interior) == 0 {
			continue
		}
		if !fits(total) {
			span.closed = true
			span.truncated = true
			continue
		}
		for _, position := range interior {
			span.admitted[position] = struct{}{}
		}
		used += total
	}

	// Then outward, one ring at a time across all spans.
	for step := 1; ; step++ {
		grew := false
		for _, span := range spans {
			if span.closed || span.unresolved != nil {
				continue
			}
			low, high := span.hitBounds()
			index := indexes[span.documentID]
			for _, position := range []int{low - step, high + step} {
				if position < span.lo || position > span.hi {
					continue
				}
				if _, isHit := span.hits[position]; isHit {
					continue
				}
				if _, already := span.admitted[position]; already {
					continue
				}
				grew = true
				next := cost(span, index, position)
				if !fits(next) {
					span.closed = true
					span.truncated = true
					break
				}
				span.admitted[position] = struct{}{}
				used += next
			}
		}
		if !grew {
			return
		}
	}
}

// hitBounds returns the lowest and highest chunk positions that actually
// matched in this span.
func (s *chunkWindowSpan) hitBounds() (int, int) {
	low, high := 0, 0
	first := true
	for position := range s.hits {
		if first || position < low {
			low = position
		}
		if first || position > high {
			high = position
		}
		first = false
	}
	return low, high
}

// renderChunkWindows turns each span into the windows it actually earned.
//
// A span is split into its maximal contiguous runs rather than emitted whole,
// so a span whose middle the budget refused comes back as the separate
// passages it really is instead of two hits printed side by side as though the
// text between them were not missing.
func renderChunkWindows(spans []*chunkWindowSpan, indexes map[string]*documentChunkIndex) []GraphRAGChunkWindow {
	windows := make([]GraphRAGChunkWindow, 0, len(spans))
	for _, span := range spans {
		if span.unresolved != nil {
			windows = append(windows, GraphRAGChunkWindow{
				DocumentID: span.unresolved.DocumentID,
				Segments: []GraphRAGWindowSegment{{
					ID:         span.unresolved.ID,
					DocumentID: span.unresolved.DocumentID,
					ChunkIndex: -1,
					Content:    span.unresolved.Content,
					Hit:        true,
					Score:      span.unresolved.Score,
				}},
			})
			continue
		}

		positions := make([]int, 0, len(span.hits)+len(span.admitted))
		for position := range span.hits {
			positions = append(positions, position)
		}
		for position := range span.admitted {
			positions = append(positions, position)
		}
		sort.Ints(positions)

		index := indexes[span.documentID]
		var run []GraphRAGWindowSegment
		flush := func() {
			if len(run) == 0 {
				return
			}
			windows = append(windows, GraphRAGChunkWindow{
				DocumentID: span.documentID,
				Segments:   run,
				Truncated:  span.truncated,
			})
			run = nil
		}
		previous := 0
		for i, position := range positions {
			if i > 0 && position != previous+1 {
				flush()
			}
			previous = position
			if hit, isHit := span.hits[position]; isHit {
				run = append(run, GraphRAGWindowSegment{
					ID:         hit.ID,
					DocumentID: hit.DocumentID,
					ChunkIndex: position,
					Content:    hit.Content,
					Hit:        true,
					Score:      hit.Score,
				})
				continue
			}
			chunk := index.byIndex[position]
			run = append(run, GraphRAGWindowSegment{
				ID:         chunk.id,
				DocumentID: span.documentID,
				ChunkIndex: position,
				Content:    chunk.content,
			})
		}
		flush()
	}
	return windows
}

// windowSegmentLine renders one segment the way buildGraphRAGContext renders a
// chunk, with the neighbour tag added for text that did not match.
func windowSegmentLine(segment GraphRAGWindowSegment) string {
	prefix := segment.ID
	if segment.DocumentID != "" {
		prefix = segment.DocumentID + "/" + segment.ID
	}
	if segment.Hit {
		return fmt.Sprintf("[%s] %s", prefix, segment.Content)
	}
	return fmt.Sprintf("[%s %s] %s", prefix, windowNeighbourTag, segment.Content)
}

// buildChunkWindowContext assembles the widened context.
//
// One line per chunk joined by newlines, exactly as the unwidened context is
// assembled, so the two differ only by the neighbour lines that were added and
// a caller diffing them sees the widening and nothing else.
func buildChunkWindowContext(windows []GraphRAGChunkWindow) string {
	if len(windows) == 0 {
		return ""
	}
	var lines []string
	for _, window := range windows {
		for _, segment := range window.Segments {
			lines = append(lines, windowSegmentLine(segment))
		}
	}
	return strings.Join(lines, "\n")
}
