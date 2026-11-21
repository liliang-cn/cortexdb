package cortexdb

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
	"github.com/liliang-cn/cortexdb/v2/pkg/sqldialect"
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

	// Through the name index (graph_node_names.go) each name reads only the
	// nodes whose folded content holds its trigrams. A name folding to fewer
	// than three characters has none, and PostgreSQL has no such index: then
	// one scan of every node serves all the names, so callers are expected
	// to batch.
	indexed := t.db.Dialect().Kind() == sqldialect.SQLite
	for _, name := range wanted {
		if utf8.RuneCountInString(foldName(name)) < 3 {
			indexed = false
		}
	}
	var all []nodeCandidate
	if indexed {
		if err := t.db.nodeFoldIndexReady(ctx); err != nil {
			return nil, err
		}
		defer observability.Time(ctx, "find-nodes-index")()
	} else {
		defer observability.Time(ctx, "find-nodes-scan")()
		if all, err = readNodeCandidates(ctx, t.db.query, types, findNodesScan(req.SkipChunks)); err != nil {
			return nil, err
		}
	}

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
		aliasOf, isAlias := aliases[entityAliasKey(name)]
		var hits nodeHits
		if indexed {
			if hits, err = t.db.indexedNodeHits(ctx, name, folded, req.SkipChunks, types, aliasOf, isAlias); err != nil {
				return nil, err
			}
		} else {
			hits = scannedNodeHits(all, name, folded, aliasOf, isAlias)
		}
		// A hidden node is not a match: the chunk and document nodes of an
		// expired document, or of one the review gate hides, and a memory
		// the gate hides, are left out as get_nodes leaves them out. They
		// are taken out a window at a time, before the limit, so they do
		// not use it up, and the strength of the answer is that of the
		// first node served.
		match := ToolNodeNameMatch{Name: name, Nodes: []*graph.GraphNode{}}
		for len(match.Nodes) < limit {
			window, err := hits.next(ctx, limit)
			if err != nil {
				return nil, err
			}
			if len(window) == 0 {
				break
			}
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

// nodeCandidate is a node find_nodes compares a name against; folded is its
// content folded once, and not once per name asked about: folding is most of
// the cost of a scan.
type nodeCandidate struct{ id, content, nodeType, folded string }

// nodeHit is a node that matches a name, with the strength of the match
// (0 exact, 1 folded, 2 alias, 3 contains) and the length of its content.
type nodeHit struct {
	id   string
	rank int
	size int
}

// compareHits is the order matches are served in. Best match first: stronger
// kind wins, then the shorter name — extra words narrow a concept, so "Limit"
// is what a bare "limit" was after and "Infinite Limit" is a different thing
// that merely contains it.
func compareHits(a, b nodeHit) int {
	return cmp.Or(cmp.Compare(a.rank, b.rank), cmp.Compare(a.size, b.size), strings.Compare(a.id, b.id))
}

// nodeHits serves the matches of a name in order, n at a time; an empty
// window means there are no more.
type nodeHits interface {
	next(ctx context.Context, n int) ([]nodeHit, error)
}

// hitList is every match of a name, ranked.
type hitList []nodeHit

func (l *hitList) next(_ context.Context, n int) ([]nodeHit, error) {
	window := (*l)[:min(n, len(*l))]
	*l = (*l)[len(window):]
	return window, nil
}

// rankNode is how a node's content matches a name, or -1.
func rankNode(content, contentFolded, name, folded string) int {
	switch {
	case content == name:
		return 0
	// Both folds must survive folding. A name of nothing but punctuation
	// folds to "", and so does every other such name — they would all
	// match each other, and the caller would be handed a confident
	// answer assembled out of two things having nothing in common.
	case folded != "" && contentFolded == folded:
		return 1
	case folded != "" && strings.Contains(contentFolded, folded):
		return 3
	}
	return -1
}

// scannedNodeHits ranks every node read by the scan against a name.
func scannedNodeHits(all []nodeCandidate, name, folded, aliasOf string, isAlias bool) nodeHits {
	hits := hitList{}
	for _, c := range all {
		rank := rankNode(c.content, c.folded, name, folded)
		// The entity that carries the name as an alias is served once,
		// under the stronger of its two matches.
		if isAlias && c.id == aliasOf {
			if isAlias = false; rank < 0 || rank > 2 {
				rank = 2
			}
		}
		if rank >= 0 {
			hits = append(hits, nodeHit{id: c.id, rank: rank, size: utf8.RuneCountInString(c.content)})
		}
	}
	if isAlias {
		// Not among the nodes compared: its type or its content kept
		// it out of the scan. The filters below still apply to it.
		hits = append(hits, nodeHit{id: aliasOf, rank: 2})
	}
	slices.SortFunc(hits, compareHits)
	return &hits
}

// rowQuery runs a read: the store's (DB.query), or a transaction's.
type rowQuery func(ctx context.Context, query string, args ...any) (*sql.Rows, error)

// readNodeCandidates runs a read of (id, content, node_type) rows and keeps
// those of the wanted types, folded.
func readNodeCandidates(ctx context.Context, read rowQuery, types map[string]struct{}, query string, args ...any) ([]nodeCandidate, error) {
	rows, err := read(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []nodeCandidate
	for rows.Next() {
		var c nodeCandidate
		if err := rows.Scan(&c.id, &c.content, &c.nodeType); err != nil {
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
	return all, rows.Err()
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
