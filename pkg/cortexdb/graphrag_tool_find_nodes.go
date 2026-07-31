package cortexdb

import (
	"context"
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

	types := make(map[string]struct{}, len(req.NodeTypes))
	for _, nodeType := range req.NodeTypes {
		if trimmed := strings.TrimSpace(nodeType); trimmed != "" {
			types[strings.ToLower(trimmed)] = struct{}{}
		}
	}

	// One scan for every name asked about. The graph has no index on content —
	// which is why this tool is needed — so the cost is paid once here rather
	// than once per name, and callers are expected to batch.
	rows, err := t.db.SQL().QueryContext(ctx,
		`SELECT id, COALESCE(content,''), COALESCE(node_type,'') FROM graph_nodes WHERE TRIM(COALESCE(content,'')) != ''`)
	if err != nil {
		return nil, err
	}
	type candidate struct{ id, content, nodeType string }
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
		all = append(all, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	for _, name := range wanted {
		folded := foldName(name)
		type hit struct {
			id   string
			rank int // 0 exact, 1 folded, 2 contains
			size int
		}
		hits := make([]hit, 0, limit)
		for _, c := range all {
			rank := -1
			switch {
			case c.content == name:
				rank = 0
			// Both folds must survive folding. A name of nothing but punctuation
			// folds to "", and so does every other such name — they would all
			// match each other, and the caller would be handed a confident
			// answer assembled out of two things having nothing in common.
			case folded != "" && foldName(c.content) == folded:
				rank = 1
			case folded != "" && strings.Contains(foldName(c.content), folded):
				rank = 2
			}
			if rank >= 0 {
				hits = append(hits, hit{id: c.id, rank: rank, size: len([]rune(c.content))})
			}
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
		if len(hits) > limit {
			hits = hits[:limit]
		}
		match := ToolNodeNameMatch{Name: name, Nodes: make([]*graph.GraphNode, 0, len(hits))}
		if len(hits) > 0 {
			match.Match = [...]string{"exact", "fold", "contains"}[hits[0].rank]
			ids := make([]string, 0, len(hits))
			for _, h := range hits {
				ids = append(ids, h.id)
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
			for _, id := range ids {
				if node, ok := byID[id]; ok {
					match.Nodes = append(match.Nodes, node)
				}
			}
		}
		response.Matches = append(response.Matches, match)
	}
	return response, nil
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
