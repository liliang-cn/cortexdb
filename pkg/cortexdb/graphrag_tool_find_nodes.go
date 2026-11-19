package cortexdb

import (
	"context"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// defaultFindNodesLimit caps how many nodes one name may resolve to.
//
// A name is a question with usually one answer; the extras exist so a caller can
// see that "Limit" also matches "Infinite Limit" and decide for itself.
const defaultFindNodesLimit = 5

// FindNodes resolves names to graph nodes.
//
// Until this existed the property graph could only be entered by ID, which meant
// a caller holding a name had to *derive* the ID the writer would have produced —
// re-implementing someone else's hashing, guessing the entity type, and getting
// an empty subgraph when either was wrong. Empty is the same answer the graph
// gives for a subject it genuinely knows nothing about, so the failure was silent
// and looked like missing data.
//
// It also cannot work across wordings, and that is where it bites hardest. A
// graph built from mixed-language material holds "Left-Hand Limit" next to
// "含绝对值的极限"; a reader asking for 左极限 hashes to nothing, and is told the
// material does not cover it. Three matching passes, weakest last, so a caller
// learns not just what was found but how sure it should be.
func (t *GraphRAGToolbox) FindNodes(ctx context.Context, req ToolFindNodesRequest) (*ToolFindNodesResponse, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultFindNodesLimit
	}
	wanted := make([]string, 0, len(req.Names))
	for _, name := range req.Names {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			wanted = append(wanted, trimmed)
		}
	}
	response := &ToolFindNodesResponse{Matches: make([]ToolNodeNameMatch, 0, len(wanted))}
	if len(wanted) == 0 {
		return response, nil
	}

	// An interface stands for the object types implementing it, so the filter
	// has to be widened before it is applied — nothing is stored under the
	// interface's own name.
	nodeTypes, err := t.db.expandOntologyTypeFilter(ctx, req.NodeTypes)
	if err != nil {
		return nil, err
	}
	types := make(map[string]struct{}, len(nodeTypes))
	for _, nodeType := range nodeTypes {
		if trimmed := strings.TrimSpace(nodeType); trimmed != "" {
			types[strings.ToLower(trimmed)] = struct{}{}
		}
	}

	// One scan for every name asked about. The graph has no index on content —
	// which is why this tool is needed — so the cost is paid once here rather
	// than once per name, and callers are expected to batch.
	rows, err := t.db.query(ctx, findNodesScan(req.SkipChunks))
	if err != nil {
		return nil, err
	}
	// folded is the content folded once, here, and not once per name asked
	// about: folding every node is most of the cost of a call.
	type candidate struct{ id, content, nodeType, folded string }
	all := make([]candidate, 0, 1024)
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.content, &c.nodeType); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if len(types) > 0 {
			if _, ok := types[strings.ToLower(c.nodeType)]; !ok {
				continue
			}
		}
		c.folded = foldName(c.content)
		all = append(all, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	// A name may be an alias entity resolution recorded on the entity it
	// kept (entity_alias_follow.go). A recorded equivalence says more than
	// a name that merely contains the one asked about, so it is looked up
	// for every name and ranked before containment: an answer that waited
	// for no other node to contain the name would be hidden by the chunks
	// that still spell it.
	aliases, err := t.db.entityAliasTable(ctx)
	if err != nil {
		return nil, err
	}

	for _, name := range wanted {
		folded := foldName(name)
		type hit struct {
			id   string
			rank int // 0 exact, 1 folded, 2 alias, 3 contains
			size int
		}
		hits := make([]hit, 0, min(limit, len(all)))
		aliasOf, isAlias := aliases[entityAliasKey(name)]
		for _, c := range all {
			rank := -1
			switch {
			case c.content == name:
				rank = 0
			// Both folds must survive folding. A name of nothing but punctuation
			// folds to "", and so does every other such name — they would all
			// match each other, and the caller would be handed a confident
			// answer assembled out of two things having nothing in common.
			case folded != "" && c.folded == folded:
				rank = 1
			case folded != "" && strings.Contains(c.folded, folded):
				rank = 3
			}
			// The entity that carries the name as an alias is served once,
			// under the stronger of its two matches.
			if isAlias && c.id == aliasOf {
				if isAlias = false; rank < 0 || rank > 2 {
					rank = 2
				}
			}
			if rank >= 0 {
				hits = append(hits, hit{id: c.id, rank: rank, size: len([]rune(c.content))})
			}
		}
		if isAlias {
			// Not among the nodes compared: its type or its content kept
			// it out of the scan. The filters below still apply to it.
			hits = append(hits, hit{id: aliasOf, rank: 2})
		}
		// Best match first: stronger kind wins, then the shorter name — extra
		// words narrow a concept, so "Limit" is what a bare "limit" was after
		// and "Infinite Limit" is a different thing that merely contains it.
		sort.SliceStable(hits, func(i, j int) bool {
			if hits[i].rank != hits[j].rank {
				return hits[i].rank < hits[j].rank
			}
			if hits[i].size != hits[j].size {
				return hits[i].size < hits[j].size
			}
			return hits[i].id < hits[j].id
		})
		// A hidden node is not a match: the chunk and document nodes of an
		// expired document, or of one the review gate hides, and a memory
		// the gate hides, are left out as get_nodes leaves them out. They
		// are taken out a window at a time, before the limit, so they do
		// not use it up, and the strength of the answer is that of the
		// first node served.
		match := ToolNodeNameMatch{Name: name, Nodes: make([]*graph.GraphNode, 0, min(limit, len(hits)))}
		for start := 0; start < len(hits) && len(match.Nodes) < limit; start += limit {
			window := hits[start:min(start+limit, len(hits))]
			ids := make([]string, 0, len(window))
			rank := make(map[string]int, len(window))
			for _, h := range window {
				ids = append(ids, h.id)
				rank[h.id] = h.rank
			}
			nodes, err := t.db.graph.GetNodesBatch(ctx, ids)
			if err != nil {
				return nil, err
			}
			// GetNodesBatch does not promise the order it was asked in, and the
			// order is the ranking — losing it would hand back the weakest match
			// as the answer.
			byID := make(map[string]*graph.GraphNode, len(nodes))
			for _, node := range nodes {
				byID[node.ID] = node
			}
			ordered := make([]*graph.GraphNode, 0, len(ids))
			for _, id := range ids {
				if node, ok := byID[id]; ok {
					ordered = append(ordered, node)
				}
			}
			kept, _ := t.db.withoutExpiredDocuments(ctx, ordered, nil, nil)
			// The type filter above read the stored type. An entity the
			// caller's scope reduces is served without it (scopedNode),
			// so under a type filter it is no match, as in the walks
			// (ofTypes).
			if len(types) > 0 {
				kept = slices.DeleteFunc(kept, func(node *graph.GraphNode) bool {
					_, ok := types[strings.ToLower(node.NodeType)]
					return !ok
				})
			}
			for _, node := range kept[:min(len(kept), limit-len(match.Nodes))] {
				if len(match.Nodes) == 0 {
					match.Match = [...]string{"exact", "fold", "alias", "contains"}[rank[node.ID]]
				}
				match.Nodes = append(match.Nodes, node)
			}
		}
		response.Matches = append(response.Matches, match)
	}
	return response, nil
}

// findNodesScan is the read of every node FindNodes compares names against.
// With skipChunks, nodes whose id begins "chunk:", the ids ingest gives the
// chunks of a document (chunk:<id>:NNN), are left out; the id is tested
// before the content, so SQLite does not decode a chunk's text. The id, not
// the type, which is the last writer's: an entity typed "chunk" is still
// matched, and under a scope the filter reads nothing another collection
// wrote. An execution run or step given an id that begins "chunk:" is left
// out too.
func findNodesScan(skipChunks bool) string {
	scan := `SELECT id, COALESCE(content,''), COALESCE(node_type,'') FROM graph_nodes WHERE `
	if skipChunks {
		scan += `substr(id, 1, 6) != 'chunk:' AND `
	}
	return scan + `TRIM(COALESCE(content,'')) != ''`
}

// foldName normalises a name for comparison: case, and anything that is neither
// a letter nor a digit.
//
// Punctuation and spacing are where the same concept is written differently by
// two extractions of the same book — "Two-Phase Commit" against "two phase
// commit" — and neither spelling is more correct than the other. CJK is left
// alone by the letter test, which is what makes this work on a mixed-language
// graph.
func foldName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
