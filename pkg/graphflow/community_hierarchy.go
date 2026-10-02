package graphflow

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Hierarchical community summaries: the GraphRAG shape with its levels kept.
//
// BuildCommunitySummaries writes one report per community of one flat
// partition. This builds the whole community dendrogram (graph.LeidenHierarchy)
// and writes a report for every community at every level, bottom-up: a level-0
// report is written from the community's entities and the relations among
// them, and a report one level up is written from the reports of the
// communities it merged. So the coarse levels read as themes built out of the
// fine ones, and GlobalSearch can answer at whichever granularity the question
// needs — few broad reports (cheap, one map batch) or many narrow ones.
//
// A model is optional. With one, each report is the model's; without one — or
// when the model fails on a community — the report is assembled
// deterministically from the most connected member names, the relation types
// inside the community and, above level 0, the titles of its children. That is
// less readable and it is never wrong, and it means global search runs on a
// brain that has no model configured at all.

const (
	hierarchyDocPrefix      = "community-h:"
	maxDeterministicMembers = 8
	maxDeterministicRels    = 5
	maxChildReportsInPrompt = 20
)

// hierarchyExcludedEdges are structural edges: they say where text sits, not
// what it is about. The same set loadEntityRelations ignores.
var hierarchyExcludedEdges = []string{"has_chunk", "next", "mentions", "in_community"}

// HierarchyOptions configures BuildCommunityHierarchy.
type HierarchyOptions struct {
	// LLM writes the reports. Optional: nil gives deterministic reports.
	LLM JSONGenerator
	// MinSize skips communities with fewer entities (default 3). A skipped
	// community is still counted in its level and still feeds its parent.
	MinSize int
	// MaxLevels caps the levels kept (0 = all the algorithm produces).
	MaxLevels int
	// Resolution is the modularity resolution (0 = 1.0, standard modularity).
	Resolution float64
	// Algorithm is graph.CommunityAlgorithmLeiden when empty, the default
	// because every community it returns is connected;
	// graph.CommunityAlgorithmLouvain reproduces builds made before it.
	Algorithm graph.CommunityAlgorithm
}

// HierarchicalCommunitySummary is one report at one level.
type HierarchicalCommunitySummary struct {
	CommunitySummary
	Level int `json:"level"`
	// Key is the persisted knowledge id, "community-h:L<level>:C<id>".
	Key string `json:"key"`
	// Parent is the parent community's id one level up, -1 at the top.
	Parent   int   `json:"parent"`
	Children []int `json:"children,omitempty"`
	// Generated is "model" when the LLM wrote the report, "deterministic"
	// when it was assembled without one, "inherited" when a community
	// identical to its only child reused that child's report.
	Generated string `json:"generated"`
}

// HierarchyLevelInfo is one level's shape.
type HierarchyLevelInfo struct {
	Level       int     `json:"level"`
	Communities int     `json:"communities"`
	Summarized  int     `json:"summarized"`
	Modularity  float64 `json:"modularity"`
	// LargestCommunity is the member count of the biggest community.
	LargestCommunity int `json:"largest_community"`
}

// CommunityHierarchyReport is what BuildCommunityHierarchy produced.
type CommunityHierarchyReport struct {
	EntityCount int                            `json:"entity_count"`
	EdgeCount   int                            `json:"edge_count"`
	Levels      []HierarchyLevelInfo           `json:"levels"`
	Communities []HierarchicalCommunitySummary `json:"communities"`
	ModelCalls  int                            `json:"model_calls"`
}

// BuildCommunityHierarchy detects the community hierarchy of the entity graph,
// writes a report per community bottom-up, and persists every report as a
// knowledge document in the "communities" collection, replacing the reports of
// any previous build.
func BuildCommunityHierarchy(ctx context.Context, db *cortexdb.DB, opts HierarchyOptions) (*CommunityHierarchyReport, error) {
	if db == nil {
		return nil, fmt.Errorf("graphflow: community hierarchy: nil db")
	}
	minSize := opts.MinSize
	if minSize <= 0 {
		minSize = defaultMinCommunitySize
	}
	h, err := db.Graph().HierarchicalCommunities(ctx, graph.HierarchyOptions{
		NodeIDPrefix:     "entity:",
		ExcludeEdgeTypes: hierarchyExcludedEdges,
		MaxLevels:        opts.MaxLevels,
		Resolution:       opts.Resolution,
		Algorithm:        opts.Algorithm,
	})
	if err != nil {
		return nil, fmt.Errorf("graphflow: hierarchical communities: %w", err)
	}
	names := loadEntityDisplayNames(ctx, db)
	relations := loadEntityRelations(ctx, db, names)
	degree := make(map[string]int)
	for _, r := range relations {
		degree[strings.ToLower(r.from)]++
		degree[strings.ToLower(r.to)]++
	}

	report := &CommunityHierarchyReport{EntityCount: h.NodeCount, EdgeCount: h.EdgeCount}
	// summaries[level][id] — nil where the community was below MinSize.
	summaries := make([][]*HierarchicalCommunitySummary, len(h.Levels))
	for li, level := range h.Levels {
		info := HierarchyLevelInfo{Level: level.Level, Communities: len(level.Communities), Modularity: level.Modularity}
		summaries[li] = make([]*HierarchicalCommunitySummary, len(level.Communities))
		for ci, c := range level.Communities {
			if len(c.Nodes) > info.LargestCommunity {
				info.LargestCommunity = len(c.Nodes)
			}
			members := memberNames(c.Nodes, names)
			if len(members) < minSize {
				continue
			}
			s := &HierarchicalCommunitySummary{
				Level:    level.Level,
				Key:      hierarchyKey(level.Level, c.ID),
				Parent:   c.Parent,
				Children: c.Children,
			}
			s.ID = c.ID
			s.Size = len(members)
			sortByDegree(members, degree)
			s.Entities = members
			if len(s.Entities) > defaultMaxEntitiesInPrompt {
				s.Entities = s.Entities[:defaultMaxEntitiesInPrompt]
			}
			memberSet := make(map[string]struct{}, len(members))
			for _, m := range members {
				memberSet[strings.ToLower(m)] = struct{}{}
			}

			var children []*HierarchicalCommunitySummary
			if li > 0 {
				for _, child := range c.Children {
					if cs := summaries[li-1][child]; cs != nil {
						children = append(children, cs)
					}
				}
			}
			switch {
			case li > 0 && len(c.Children) == 1 && len(children) == 1:
				// Same members as its only child: the report is the same.
				s.Title, s.Summary, s.Findings = children[0].Title, children[0].Summary, children[0].Findings
				s.Generated = "inherited"
			default:
				rels := relationsWithin(relations, memberSet)
				if opts.LLM != nil {
					var sum *CommunitySummary
					var err error
					if len(children) > 0 {
						sum, err = summarizeParentCommunity(ctx, opts.LLM, s.Entities, children)
					} else {
						promptMembers := s.Entities
						sum, err = summarizeCommunity(ctx, opts.LLM, promptMembers, rels)
					}
					report.ModelCalls++
					if err == nil {
						s.Title, s.Summary, s.Findings = sum.Title, sum.Summary, sum.Findings
						s.Generated = "model"
					}
				}
				if s.Generated == "" {
					s.Title, s.Summary, s.Findings = deterministicCommunityReport(members, rels, children)
					s.Generated = "deterministic"
				}
			}
			summaries[li][ci] = s
			info.Summarized++
		}
		report.Levels = append(report.Levels, info)
	}

	// Replace the previous build wholesale: a level the new hierarchy does
	// not have must not linger and answer questions about a graph that no
	// longer exists.
	if err := deleteHierarchyDocs(ctx, db); err != nil {
		return nil, err
	}
	for li := range summaries {
		for _, s := range summaries[li] {
			if s == nil {
				continue
			}
			report.Communities = append(report.Communities, *s)
			parent := ""
			if s.Parent >= 0 {
				parent = hierarchyKey(s.Level+1, s.Parent)
			}
			if _, err := db.SaveKnowledge(ctx, cortexdb.KnowledgeSaveRequest{
				KnowledgeID: s.Key,
				Title:       s.Title,
				Content:     communityDocContent(&s.CommunitySummary),
				Collection:  communityCollection,
				Metadata: map[string]string{
					"kind":      "community",
					"level":     strconv.Itoa(s.Level),
					"size":      strconv.Itoa(s.Size),
					"parent":    parent,
					"generated": s.Generated,
				},
			}); err != nil {
				return nil, fmt.Errorf("graphflow: persist community %s: %w", s.Key, err)
			}
		}
	}
	return report, nil
}

func hierarchyKey(level, id int) string {
	return fmt.Sprintf("%sL%d:C%d", hierarchyDocPrefix, level, id)
}

// parseHierarchyKey returns the level of a "community-h:L<level>:C<id>" key.
func parseHierarchyKey(key string) (level, id int, ok bool) {
	rest, found := strings.CutPrefix(key, hierarchyDocPrefix+"L")
	if !found {
		return 0, 0, false
	}
	lvl, cid, found := strings.Cut(rest, ":C")
	if !found {
		return 0, 0, false
	}
	l, err1 := strconv.Atoi(lvl)
	c, err2 := strconv.Atoi(cid)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return l, c, true
}

func memberNames(nodeIDs []string, names map[string]string) []string {
	out := make([]string, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		if name, ok := names[id]; ok {
			out = append(out, name)
		}
	}
	return out
}

// sortByDegree orders members most-connected first, then by name, so the
// head of the list is what the community is organised around.
func sortByDegree(members []string, degree map[string]int) {
	sort.SliceStable(members, func(i, j int) bool {
		di, dj := degree[strings.ToLower(members[i])], degree[strings.ToLower(members[j])]
		if di != dj {
			return di > dj
		}
		return members[i] < members[j]
	})
}

// deterministicCommunityReport assembles a report with no model: the most
// connected members name it, the relations inside it say how they connect,
// and the child reports (above level 0) say what it is made of.
func deterministicCommunityReport(members, rels []string, children []*HierarchicalCommunitySummary) (string, string, []string) {
	head := members
	if len(head) > 3 {
		head = head[:3]
	}
	title := strings.Join(head, ", ")
	var b strings.Builder
	shown := members
	if len(shown) > maxDeterministicMembers {
		shown = shown[:maxDeterministicMembers]
	}
	fmt.Fprintf(&b, "%d entities, most connected: %s.", len(members), strings.Join(shown, ", "))
	if len(children) > 0 {
		titles := make([]string, 0, len(children))
		for _, c := range children {
			titles = append(titles, c.Title)
		}
		if len(titles) > maxDeterministicMembers {
			titles = titles[:maxDeterministicMembers]
		}
		fmt.Fprintf(&b, " Made of %d sub-communities: %s.", len(children), strings.Join(titles, "; "))
	}
	findings := make([]string, 0, maxDeterministicRels)
	for _, r := range rels {
		if len(findings) >= maxDeterministicRels {
			break
		}
		findings = append(findings, r)
	}
	return title, b.String(), findings
}

const parentCommunitySystemPrompt = "You write a concise report describing a community of related entities from a knowledge graph, " +
	"built from the reports of the smaller communities it contains. Summarise what unites them rather than repeating each one. " +
	"Return JSON only: {\"title\":\"short title\",\"summary\":\"2-4 sentence description\",\"findings\":[\"key point\", …]}."

func summarizeParentCommunity(ctx context.Context, llm JSONGenerator, members []string, children []*HierarchicalCommunitySummary) (*CommunitySummary, error) {
	var b strings.Builder
	b.WriteString("Most connected entities:\n")
	b.WriteString(strings.Join(members, ", "))
	b.WriteString("\n\nSub-community reports:\n")
	for i, c := range children {
		if i >= maxChildReportsInPrompt {
			fmt.Fprintf(&b, "\n(%d more sub-communities omitted)\n", len(children)-i)
			break
		}
		b.WriteString("\n## ")
		b.WriteString(c.Title)
		b.WriteString("\n")
		b.WriteString(truncateRunes(c.Summary, 600))
		b.WriteString("\n")
	}
	b.WriteString("\nWrite the community report as specified. JSON only.")
	raw, err := llm.GenerateJSON(ctx, parentCommunitySystemPrompt, b.String())
	if err != nil {
		return nil, err
	}
	return parseCommunityReport(raw)
}

// deleteHierarchyDocs removes every persisted hierarchical report.
func deleteHierarchyDocs(ctx context.Context, db *cortexdb.DB) error {
	rows, err := db.SQL().QueryContext(ctx, db.Dialect().Rebind(`SELECT id FROM documents WHERE id LIKE ?`), hierarchyDocPrefix+"%")
	if err != nil {
		return fmt.Errorf("graphflow: list community reports: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	for _, id := range ids {
		if _, err := db.DeleteKnowledge(ctx, cortexdb.KnowledgeDeleteRequest{KnowledgeID: id}); err != nil {
			return fmt.Errorf("graphflow: delete community report %s: %w", id, err)
		}
	}
	return nil
}

// loadHierarchyLevels returns, per level, the persisted reports at that level.
func loadHierarchyLevels(ctx context.Context, db *cortexdb.DB) map[int][]CommunitySummary {
	out := make(map[int][]CommunitySummary)
	rows, err := db.SQL().QueryContext(ctx, db.Dialect().Rebind(
		`SELECT id, COALESCE(title,''), COALESCE(content,'') FROM documents WHERE id LIKE ? ORDER BY id`), hierarchyDocPrefix+"%")
	if err != nil {
		return out
	}
	defer func() { _ = rows.Close() }()
	type keyed struct {
		id int
		s  CommunitySummary
	}
	tmp := make(map[int][]keyed)
	for rows.Next() {
		var key, title, content string
		if err := rows.Scan(&key, &title, &content); err != nil {
			break
		}
		level, id, ok := parseHierarchyKey(key)
		if !ok {
			continue
		}
		tmp[level] = append(tmp[level], keyed{id: id, s: CommunitySummary{ID: id, Title: title, Summary: content}})
	}
	for level, items := range tmp {
		sort.Slice(items, func(i, j int) bool { return items[i].id < items[j].id })
		for _, it := range items {
			out[level] = append(out[level], it.s)
		}
	}
	return out
}
