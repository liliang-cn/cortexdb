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

	// Deduplicated by the triple, not by edge id. Edge ids carry their
	// provenance — "edge:rel:<chunk>:<from>:<to>:<type>" — so one fact asserted
	// by two chunks is two rows with different ids, and the pack printed it
	// twice while the cap counted it twice.
	//
	// That repetition is worth something once it is counted instead of shown:
	// the number of distinct places a fact was asserted is the only evidence of
	// its strength this store has.
	type factKey struct{ from, predicate, to string }
	collected := make(map[factKey]*graphFactEvidence)
	order := make([]factKey, 0, maxGraphFacts*2)
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
			key := factKey{from: e.FromNodeID, predicate: e.EdgeType, to: e.ToNodeID}
			if existing, ok := collected[key]; ok {
				existing.assertions++
				continue
			}
			collected[key] = &graphFactEvidence{assertions: 1}
			order = append(order, key)
		}
	}

	facts := make([]KnowledgeMemoryGraphFact, 0, len(order))
	for _, key := range order {
		facts = append(facts, KnowledgeMemoryGraphFact{
			Subject:   label(key.from),
			Predicate: key.predicate,
			Object:    label(key.to),
			SubjectID: key.from,
			ObjectID:  key.to,
		})
	}

	// Ranked before the cap applies, so the budget buys facts that say
	// something. co_occurs means only that two names shared a sentence; it is
	// generated for every adjacent pair, so it outnumbers everything and used to
	// fill the pack while "oss-agent depends_on cortexdb" sat below the cut.
	sort.SliceStable(facts, func(i, j int) bool {
		iWeak, jWeak := isWeakPredicate(facts[i].Predicate), isWeakPredicate(facts[j].Predicate)
		if iWeak != jWeak {
			return jWeak
		}
		iN := collected[factKey{facts[i].SubjectID, facts[i].Predicate, facts[i].ObjectID}].assertions
		jN := collected[factKey{facts[j].SubjectID, facts[j].Predicate, facts[j].ObjectID}].assertions
		if iN != jN {
			return iN > jN
		}
		if facts[i].Subject != facts[j].Subject {
			return facts[i].Subject < facts[j].Subject
		}
		if facts[i].Predicate != facts[j].Predicate {
			return facts[i].Predicate < facts[j].Predicate
		}
		return facts[i].Object < facts[j].Object
	})
	if len(facts) > maxGraphFacts {
		facts = facts[:maxGraphFacts]
	}
	return facts
}

// graphFactEvidence counts how many stored edges assert one triple.
type graphFactEvidence struct{ assertions int }

// weakPredicates say that two entities were near each other and nothing more.
// They are kept — with a sparse graph they are sometimes all there is — but they
// rank below any relation that names what actually holds between the two.
var weakPredicates = map[string]struct{}{
	"co_occurs":  {},
	"related":    {},
	"related_to": {},
	"mentions":   {},
}

func isWeakPredicate(predicate string) bool {
	_, ok := weakPredicates[strings.ToLower(strings.TrimSpace(predicate))]
	return ok
}

func isEntityNodeID(id string) bool { return strings.HasPrefix(id, EntityNodeIDPrefix) }

// entityLabelFromID derives a readable label from an entity node id when the
// node has no stored content (e.g. "entity:apollo_plan" -> "apollo plan").
func entityLabelFromID(id string) string {
	s := strings.TrimPrefix(id, EntityNodeIDPrefix)
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
