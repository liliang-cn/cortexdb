package cortexdb

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// coveragePoolSize is how deep the search runs to find each named entity's
// best chunk for the question.
const coveragePoolSize = 60

// maxCoverageCandidates is how many chunks one entity's sub-search returns
// to choose its evidence from.
const maxCoverageCandidates = 8

// maxSharedNeighbours caps how many nodes the named entities share are
// followed to their evidence.
const maxSharedNeighbours = 2

// coverQueryEntities brings into a result the evidence that relates the
// entities a question names (#251). Retrieval ranks chunks against the whole
// question, so "What company did both Sal Khan and Neha Narkhede work for?"
// got a chunk where one of them names Oracle and none where the other does:
// the company is what the question asks for, and it cannot name it.
//
// For a question naming two or more graph entities (hub names, in nearly
// every chunk, left out), within the caller's chunk, character and
// per-document limits:
//
//   - shared neighbours: for a node two or more of them have a stated
//     relation with ("worked_at Oracle"), the chunk where each of them is
//     said to be related to it;
//   - coverage: a named entity no chunk of the result mentions gets its best
//     chunk for the question.
//
// Candidates come from the same search run deeper, then from the keyword
// search with the entities as entity_names, so a chunk brought in passed the
// same collection, access, review and expiry filters as the rest, with or
// without an embedder; neither runs when nothing is missing. Room is made by
// dropping the lowest-ranked chunk that is not some named entity's only
// evidence or one brought in here. It reports whether the result changed.
func (db *DB) coverQueryEntities(ctx context.Context, query string, plan RetrievalPlan, lexReq ToolSearchGraphRAGLexicalRequest, result *GraphRAGQueryResult, opts GraphRAGQueryOptions) (bool, error) {
	// An empty result is retrieval's verdict that the store holds nothing
	// on the question, and a name in it does not overturn that.
	if result == nil || len(result.Chunks) == 0 || opts.MaxContextChunks == 1 {
		return false, nil
	}
	ids, err := db.queryEntityNodeIDs(ctx, query, plan.EntityNames)
	if err != nil || len(ids) < 2 {
		return false, err
	}
	// A name in nearly every chunk ("Behind the Tech" in a podcast's
	// transcripts) is the corpus, not a subject of the question: every
	// result already holds it, and its "best chunk" would only displace
	// one. Dropped as PPR drops it from its seeds.
	counts, err := db.mentionCounts(ctx, ids)
	if err != nil {
		return false, err
	}
	if ids, err = db.dropHubEntities(ctx, ids, counts, "chunk"); err != nil || len(ids) < 2 {
		return false, err
	}
	names := make(map[string]string, len(ids)) // folded name -> node id
	display := make(map[string]string, len(ids))
	for _, id := range ids {
		node, err := db.graph.GetNode(ctx, id)
		if err != nil || node == nil || strings.TrimSpace(node.Content) == "" {
			continue
		}
		names[foldEntityName(node.Content)] = id
		display[id] = node.Content
	}
	if len(display) < 2 {
		return false, nil
	}
	named := make([]string, 0, len(display))
	for _, id := range ids {
		if _, ok := display[id]; ok {
			named = append(named, id)
		}
	}
	// What the named entities share: a node two or more of them have a
	// stated relation with ("Sal Khan worked_at Oracle", "Neha Narkhede
	// worked_at Oracle"). It is often the answer the question asks for, and
	// a question cannot name it.
	shared, err := db.sharedNeighbours(ctx, named, maxSharedNeighbours)
	if err != nil {
		return false, err
	}
	neighbourNames := map[string]string{} // folded name -> neighbour id
	for _, nb := range shared {
		neighbourNames[foldEntityName(nb.name)] = nb.id
	}

	mentions := map[string]map[string]bool{}   // chunk id -> named entity ids it mentions
	nbMentions := map[string]map[string]bool{} // chunk id -> shared neighbour ids it mentions
	learn := func(chunks []GraphRAGChunkResult) error {
		var unknown []string
		for _, c := range chunks {
			if _, ok := mentions[c.ID]; !ok {
				unknown = append(unknown, c.ID)
			}
		}
		byChunk, err := db.chunkEntityNamesBatch(ctx, unknown, 0)
		if err != nil {
			return err
		}
		for _, id := range unknown {
			set, nbs := map[string]bool{}, map[string]bool{}
			for _, name := range byChunk[id] {
				if node, ok := names[foldEntityName(name)]; ok {
					set[node] = true
				}
				if node, ok := neighbourNames[foldEntityName(name)]; ok {
					nbs[node] = true
				}
			}
			mentions[id], nbMentions[id] = set, nbs
		}
		return nil
	}
	if err := learn(result.Chunks); err != nil {
		return false, err
	}

	chunks := append([]GraphRAGChunkResult(nil), result.Chunks...)
	changed := false
	pinned := map[string]bool{} // chunks chosen here, never dropped for another

	// The pool each entity's evidence is chosen from: the same search, run
	// deeper. The result ranks the question's chunks; this finds, for each
	// entity, the chunk ranked highest for the question among those that
	// mention it, which is what the question asks of that entity.
	deep := opts
	deep.TopK, deep.MaxContextChunks = coveragePoolSize, coveragePoolSize
	deep.MaxContextChars, deep.PerDocumentLimit, deep.ChunkWindow = 1<<30, 1<<30, 0
	deepLex := lexReq
	deepLex.TopK, deepLex.MaxContextChunks = coveragePoolSize, coveragePoolSize
	deepLex.MaxContextChars, deepLex.PerDocumentLimit, deepLex.ChunkWindow = 1<<30, 1<<30, 0
	// Run only when some evidence is missing from the result: most
	// questions naming two entities already have it.
	var poolChunks []GraphRAGChunkResult
	poolDone := false
	getPool := func() ([]GraphRAGChunkResult, error) {
		if poolDone {
			return poolChunks, nil
		}
		poolDone = true
		var pool *GraphRAGQueryResult
		var err error
		if db.HasEmbedder() {
			pool, err = db.searchKnowledgeHybrid(ctx, query, deep, deepLex)
		} else {
			pool, err = db.GraphRAGTools().SearchGraphRAGLexical(ctx, deepLex)
		}
		if err != nil || pool == nil {
			return nil, err
		}
		poolChunks = pool.Chunks
		return poolChunks, learn(poolChunks)
	}

	search := func(entityNames []string) ([]GraphRAGChunkResult, error) {
		sub := lexReq
		sub.EntityNames = entityNames
		sub.TopK = maxCoverageCandidates
		sub.MaxContextChunks = maxCoverageCandidates
		sub.PerDocumentLimit = 1 << 30
		sub.MaxContextChars = 1 << 30
		sub.ChunkWindow = 0
		res, err := db.GraphRAGTools().SearchGraphRAGLexical(ctx, sub)
		if err != nil || res == nil {
			return nil, err
		}
		if err := learn(res.Chunks); err != nil {
			return nil, err
		}
		return res.Chunks, nil
	}

	// For each shared neighbour, the chunk where each named entity related
	// to it is said to be: the pool's best that mentions both, or the best
	// a search for the two names finds.
	for _, nb := range shared {
		for _, id := range nb.of {
			both := func(c GraphRAGChunkResult) bool { return mentions[c.ID][id] && nbMentions[c.ID][nb.id] }
			if containsChunk(chunks, both) {
				continue
			}
			cands, err := getPool()
			if err != nil {
				return changed, err
			}
			if !containsChunk(cands, both) {
				if cands, err = search([]string{display[id], nb.name}); err != nil {
					return changed, err
				}
			}
			for _, c := range cands {
				if both(c) {
					if next, ok := admitCoverageChunk(chunks, c, mentions, pinned, opts); ok {
						chunks, changed = next, true
						pinned[c.ID] = true
					}
					break
				}
			}
		}
	}

	// Each named entity with no evidence in the result gets its best chunk
	// for the question.
	for _, id := range named {
		if containsChunk(chunks, func(c GraphRAGChunkResult) bool { return mentions[c.ID][id] }) {
			continue
		}
		cands, err := getPool()
		if err != nil {
			return changed, err
		}
		if !containsChunk(cands, func(c GraphRAGChunkResult) bool { return mentions[c.ID][id] }) {
			if cands, err = search([]string{display[id]}); err != nil {
				return changed, err
			}
		}
		for _, c := range cands {
			if mentions[c.ID][id] {
				if next, ok := admitCoverageChunk(chunks, c, mentions, pinned, opts); ok {
					chunks, changed = next, true
					pinned[c.ID] = true
				}
				break
			}
		}
	}

	if changed {
		result.Chunks = chunks
	}
	return changed, nil
}

// admitCoverageChunk appends c to chunks after dropping, from the end, the
// chunks that must go for it to fit the caller's limits; a chunk that is the
// only one mentioning some named entity, or that connects several, is never
// dropped, nor is one pinned. It refuses when no such room can be made.
func admitCoverageChunk(chunks []GraphRAGChunkResult, c GraphRAGChunkResult, mentions map[string]map[string]bool, pinned map[string]bool, opts GraphRAGQueryOptions) ([]GraphRAGChunkResult, bool) {
	out := append([]GraphRAGChunkResult(nil), chunks...)
	docFull := func() bool {
		return opts.PerDocumentLimit > 0 && c.DocumentID != "" && docCount(out, c.DocumentID)+1 > opts.PerDocumentLimit
	}
	fits := func() bool {
		if docFull() || (opts.MaxContextChunks > 0 && len(out)+1 > opts.MaxContextChunks) {
			return false
		}
		if opts.MaxContextChars > 0 {
			total := contextLineLen(c)
			for _, o := range out {
				total += contextLineLen(o)
			}
			if total > opts.MaxContextChars {
				return false
			}
		}
		return true
	}
	for !fits() {
		sameDoc := docFull()
		drop := -1
		for i := len(out) - 1; i >= 0; i-- {
			if (!sameDoc || out[i].DocumentID == c.DocumentID) && !pinned[out[i].ID] && !essentialCoverage(out, i, mentions) {
				drop = i
				break
			}
		}
		if drop < 0 {
			return chunks, false
		}
		out = append(out[:drop], out[drop+1:]...)
	}
	return append(out, c), true
}

// essentialCoverage reports whether chunks[i] connects several named
// entities or is the only chunk mentioning one of them.
func essentialCoverage(chunks []GraphRAGChunkResult, i int, mentions map[string]map[string]bool) bool {
	own := mentions[chunks[i].ID]
	if len(own) >= 2 {
		return true
	}
	for id := range own {
		alone := true
		for j, o := range chunks {
			if j != i && mentions[o.ID][id] {
				alone = false
				break
			}
		}
		if alone {
			return true
		}
	}
	return false
}

func docCount(chunks []GraphRAGChunkResult, doc string) int {
	n := 0
	for _, c := range chunks {
		if c.DocumentID == doc {
			n++
		}
	}
	return n
}

// contextLineLen is what a chunk takes in the assembled context, as
// packGraphRAGContext counts it.
func contextLineLen(c GraphRAGChunkResult) int {
	return len(c.Content) + len(c.ID) + len(c.DocumentID) + 8
}

func containsChunk(chunks []GraphRAGChunkResult, pred func(GraphRAGChunkResult) bool) bool {
	for _, c := range chunks {
		if pred(c) {
			return true
		}
	}
	return false
}

// foldEntityName is the form two spellings of one name share.
func foldEntityName(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(name)), " ")
}

type sharedNeighbour struct {
	id, name string
	of       []string // the named entities related to it
}

// sharedNeighbours returns the nodes two or more of the given entities have a
// stated relation with, in either direction: most of them first, then the
// most specific (fewest mentions), at most limit. Mentions, chunk structure
// and co-occurrence are not relations and do not count.
func (db *DB) sharedNeighbours(ctx context.Context, ids []string, limit int) ([]sharedNeighbour, error) {
	if len(ids) < 2 {
		return nil, nil
	}
	placeholders, args := sqlPlaceholders(ids)
	skip := append([]string{"mentions", "has_chunk", "next"}, OntologyDraftCoOccurrenceEdgeTypes...)
	skipPH, skipArgs := sqlPlaceholders(skip)
	q := `SELECT x.nb, x.named, COALESCE(n.content, '') FROM (
			SELECT to_node_id AS nb, from_node_id AS named FROM graph_edges
			WHERE from_node_id IN (` + placeholders + `) AND COALESCE(edge_type, '') NOT IN (` + skipPH + `)
			UNION
			SELECT from_node_id AS nb, to_node_id AS named FROM graph_edges
			WHERE to_node_id IN (` + placeholders + `) AND COALESCE(edge_type, '') NOT IN (` + skipPH + `)
		) x JOIN graph_nodes n ON n.id = x.nb
		WHERE COALESCE(n.node_type, '') NOT IN ('chunk', 'document') AND x.nb NOT IN (` + placeholders + `)`
	all := append(append(append(append(append([]any{}, args...), skipArgs...), args...), skipArgs...), args...)
	rows, err := db.query(ctx, q, all...)
	if err != nil {
		return nil, fmt.Errorf("shared neighbours: %w", err)
	}
	byID := map[string]*sharedNeighbour{}
	for rows.Next() {
		var nb, of, name string
		if err := rows.Scan(&nb, &of, &name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		s := byID[nb]
		if s == nil {
			s = &sharedNeighbour{id: nb, name: name}
			byID[nb] = s
		}
		if !slices.Contains(s.of, of) {
			s.of = append(s.of, of)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var out []sharedNeighbour
	var outIDs []string
	for _, s := range byID {
		if len(s.of) >= 2 && strings.TrimSpace(s.name) != "" {
			sort.Strings(s.of)
			out = append(out, *s)
			outIDs = append(outIDs, s.id)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	counts, err := db.mentionCounts(ctx, outIDs)
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].of) != len(out[j].of) {
			return len(out[i].of) > len(out[j].of)
		}
		if counts[out[i].id] != counts[out[j].id] {
			return counts[out[i].id] < counts[out[j].id]
		}
		return out[i].id < out[j].id
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
