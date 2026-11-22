package cortexdb

import (
	"context"
	"sort"
	"strings"
	"time"

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
func (b *KnowledgeMemory) collectGraphFacts(ctx context.Context, query string, namedHints, seedNames []string) []KnowledgeMemoryGraphFact {
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
	// The entities the question names come first, and what two or more of
	// them share is what relates them (#251): their facts lead the pack.
	connecting := map[string]bool{}
	var named []string
	if ids, err := b.db.queryEntityNodeIDs(ctx, query, namedHints); err == nil && len(ids) >= 2 {
		if counts, err := b.db.mentionCounts(ctx, ids); err == nil {
			if kept, err := b.db.dropHubEntities(ctx, ids, counts, "chunk"); err == nil {
				ids = kept
			}
		}
		if len(ids) >= 2 {
			for _, id := range ids {
				connecting[id] = true
				if n, err := b.db.Graph().GetNode(ctx, id); err == nil && n != nil {
					named = append(named, n.Content)
				}
			}
			if shared, err := b.db.sharedNeighbours(ctx, ids, maxSharedNeighbours); err == nil {
				for _, nb := range shared {
					connecting[nb.id] = true
				}
			}
		}
	}
	links := func(f KnowledgeMemoryGraphFact) bool {
		return len(named) >= 2 && connecting[f.SubjectID] && connecting[f.ObjectID]
	}
	for _, n := range named {
		add(n)
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
	asOf := memoryAsOf(ctx)
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
			// as_of answers as the store stood: an edge recorded after it
			// was not yet known.
			if !asOf.IsZero() && recordedAfter(e, asOf) {
				continue
			}
			key := factKey{from: e.FromNodeID, predicate: e.EdgeType, to: e.ToNodeID}
			from, to := eventTimeOf(e.Properties)
			if existing, ok := collected[key]; ok {
				existing.assertions++
				existing.widen(from, to)
				continue
			}
			ev := &graphFactEvidence{assertions: 1}
			ev.widen(from, to)
			collected[key] = ev
			order = append(order, key)
		}
	}

	facts := make([]KnowledgeMemoryGraphFact, 0, len(order))
	for _, key := range order {
		ev := collected[key]
		facts = append(facts, KnowledgeMemoryGraphFact{
			Subject:   label(key.from),
			Predicate: key.predicate,
			Object:    label(key.to),
			SubjectID: key.from,
			ObjectID:  key.to,
			ValidFrom: formatEventTime(ev.from),
			ValidTo:   formatEventTime(ev.to),
		})
	}

	// Ranked before the cap applies, so the budget buys facts that say
	// something. co_occurs means only that two names shared a sentence; it is
	// generated for every adjacent pair, so it outnumbers everything and used to
	// fill the pack while "oss-agent depends_on cortexdb" sat below the cut.
	sort.SliceStable(facts, func(i, j int) bool {
		if li, lj := links(facts[i]), links(facts[j]); li != lj && !isWeakPredicate(facts[i].Predicate) && !isWeakPredicate(facts[j].Predicate) {
			return li
		}
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
	return inTimeOrder(facts)
}

// inTimeOrder keeps a subject's facts of one predicate together, in the
// order they became true, where the first of them ranked: "how did X's
// employer change" reads as a sequence. Facts without a time follow the
// timed ones of their group; the groups keep their rank order.
func inTimeOrder(facts []KnowledgeMemoryGraphFact) []KnowledgeMemoryGraphFact {
	type group struct{ subject, predicate string }
	members := map[group][]KnowledgeMemoryGraphFact{}
	var groups []group
	for _, f := range facts {
		g := group{f.SubjectID, f.Predicate}
		if _, ok := members[g]; !ok {
			groups = append(groups, g)
		}
		members[g] = append(members[g], f)
	}
	out := make([]KnowledgeMemoryGraphFact, 0, len(facts))
	for _, g := range groups {
		m := members[g]
		sort.SliceStable(m, func(i, j int) bool {
			ti, tj := m[i].ValidFrom, m[j].ValidFrom
			if ti == "" || tj == "" {
				return ti != "" && tj == ""
			}
			return ti < tj
		})
		out = append(out, m...)
	}
	return out
}

// recordedAfter reports whether the store learnt the edge after at.
func recordedAfter(e *graph.GraphEdge, at time.Time) bool {
	recorded := e.RecordedAt
	if recorded.IsZero() {
		recorded = e.CreatedAt
	}
	return !recorded.IsZero() && recorded.After(at)
}

func formatEventTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// graphFactEvidence counts how many stored edges assert one triple, and the
// widest time they give it: one source saying 2005 and another 2008 for the
// same fact still says it held from 2005.
type graphFactEvidence struct {
	assertions int
	from, to   time.Time
}

func (e *graphFactEvidence) widen(from, to time.Time) {
	if !from.IsZero() && (e.from.IsZero() || from.Before(e.from)) {
		e.from = from
	}
	if !to.IsZero() && (e.to.IsZero() || to.After(e.to)) {
		e.to = to
	}
}

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
// factPeriod reads a fact's interval as the pack shows it: "from 2005",
// "2005 until 2008", "until 2008", each bound as precise as it was stated.
func factPeriod(validFrom, validTo string) string {
	show := func(v string) string {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return v
		}
		switch {
		case t.Month() == time.January && t.Day() == 1 && t.Hour() == 0 && t.Minute() == 0:
			return t.Format("2006")
		case t.Day() == 1 && t.Hour() == 0 && t.Minute() == 0:
			return t.Format("2006-01")
		case t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0:
			return t.Format("2006-01-02")
		}
		return t.Format(time.RFC3339)
	}
	switch {
	case validFrom != "" && validTo != "":
		return show(validFrom) + " until " + show(validTo)
	case validFrom != "":
		return "from " + show(validFrom)
	case validTo != "":
		return "until " + show(validTo)
	}
	return ""
}

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
		if when := factPeriod(f.ValidFrom, f.ValidTo); when != "" {
			b.WriteString(" (")
			b.WriteString(when)
			b.WriteString(")")
		}
	}
	return b.String()
}
