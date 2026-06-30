package cortexdb

import (
	"context"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

const (
	maxGraphFactSeeds = 16
	maxGraphFacts     = 25
)

// collectGraphFacts resolves entity nodes named in the query (and in the seed
// entity list) and returns the relations among entity nodes incident to them.
//
// This folds expand_graph's edge-accurate traversal into recall: relational
// questions ("who uses X", "what does X depend on") are answered from graph
// edges rather than lexical chunk text, which is unreliable without an embedder.
// Only entity↔entity edges (both endpoints carry the "entity:" id prefix) are
// kept, so structural edges like chunk -mentions-> entity never leak in.
func (b *KnowledgeMemory) collectGraphFacts(ctx context.Context, query string, seedNames []string) []KnowledgeMemoryGraphFact {
	seen := make(map[string]struct{})
	candidates := make([]string, 0, maxGraphFactSeeds)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		if len(candidates) < maxGraphFactSeeds {
			candidates = append(candidates, name)
		}
	}
	for _, n := range seedNames {
		add(n)
	}
	// Query keywords let relational targets resolve even when lexical chunk
	// search surfaced nothing (the exact failure this fixes).
	for _, kw := range lexicalQueryKeywords(query) {
		add(kw)
	}
	if len(candidates) == 0 {
		return nil
	}

	g := b.db.Graph()
	nodeCache := make(map[string]*graph.GraphNode)
	getNode := func(id string) *graph.GraphNode {
		if n, ok := nodeCache[id]; ok {
			return n
		}
		n, err := g.GetNode(ctx, id)
		if err != nil {
			n = nil
		}
		nodeCache[id] = n
		return n
	}
	label := func(id string) string {
		if n := getNode(id); n != nil && strings.TrimSpace(n.Content) != "" {
			return strings.TrimSpace(n.Content)
		}
		return entityLabelFromID(id)
	}

	facts := make([]KnowledgeMemoryGraphFact, 0, maxGraphFacts)
	seenEdge := make(map[string]struct{})
	for _, name := range candidates {
		nodeID := resolveEntityNodeID("", name)
		if nodeID == "" || getNode(nodeID) == nil {
			continue
		}
		edges, err := g.GetEdges(ctx, nodeID, "both")
		if err != nil {
			continue
		}
		for _, e := range edges {
			if e == nil || strings.TrimSpace(e.EdgeType) == "" {
				continue
			}
			// Keep only entity↔entity relations.
			if !isEntityNodeID(e.FromNodeID) || !isEntityNodeID(e.ToNodeID) {
				continue
			}
			if _, ok := seenEdge[e.ID]; ok {
				continue
			}
			seenEdge[e.ID] = struct{}{}
			facts = append(facts, KnowledgeMemoryGraphFact{
				Subject:   label(e.FromNodeID),
				Predicate: e.EdgeType,
				Object:    label(e.ToNodeID),
				SubjectID: e.FromNodeID,
				ObjectID:  e.ToNodeID,
			})
			if len(facts) >= maxGraphFacts {
				break
			}
		}
		if len(facts) >= maxGraphFacts {
			break
		}
	}

	sort.SliceStable(facts, func(i, j int) bool {
		if facts[i].Subject != facts[j].Subject {
			return facts[i].Subject < facts[j].Subject
		}
		if facts[i].Predicate != facts[j].Predicate {
			return facts[i].Predicate < facts[j].Predicate
		}
		return facts[i].Object < facts[j].Object
	})
	return facts
}

func isEntityNodeID(id string) bool { return strings.HasPrefix(id, "entity:") }

// entityLabelFromID derives a readable label from an entity node id when the
// node has no stored content (e.g. "entity:apollo_plan" -> "apollo plan").
func entityLabelFromID(id string) string {
	s := strings.TrimPrefix(id, "entity:")
	s = strings.ReplaceAll(s, "_", " ")
	return strings.TrimSpace(s)
}

// renderGraphFacts formats facts as "Subject —predicate→ Object" lines for the
// context pack.
func renderGraphFacts(facts []KnowledgeMemoryGraphFact) string {
	if len(facts) == 0 {
		return ""
	}
	var b strings.Builder
	for i, f := range facts {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(f.Subject)
		b.WriteString(" —")
		b.WriteString(f.Predicate)
		b.WriteString("→ ")
		b.WriteString(f.Object)
	}
	return b.String()
}
