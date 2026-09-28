package graphflow

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// Link-first resolution. See the header of resolve.go for the policy; this
// file is the scoring and the writes.

const (
	// nameSimIdentical, nameSimNormalized and nameSimFuzzyCap are the name
	// evidence tiers. The cap is what keeps spelling alone from merging two
	// differently spelled names: 0.92 is below the merge threshold, so a
	// fuzzy pair merges only when its neighbourhoods agree as well.
	nameSimIdentical  = 1.0
	nameSimNormalized = 0.97
	nameSimFuzzyCap   = 0.92
	// fuzzyCandidateFloor is the Jaro-Winkler below which a pair is not
	// considered at all. Lower than the link threshold, because shared
	// neighbours can lift a pair into the link band.
	fuzzyCandidateFloor = 0.80
	// llmNameFloor is what a model's grouping is worth on its own: enough to
	// link, never enough to merge.
	llmNameFloor = 0.90
	// disjointPenalty is subtracted when both entities have neighbours and
	// share none — the shape of a homonym.
	disjointPenalty = 0.10
	// neighbourLift is how far full neighbour overlap closes the gap to 1.
	//
	// Small enough that a differently spelled pair can never reach the merge
	// threshold on neighbours alone (0.92 capped name, Jaccard 1 → 0.948):
	// "Windows 10"/"Windows 11" and "GPT-4"/"GPT-4o" share nearly every
	// neighbour *because* they are siblings, and 0.5 merged both on the
	// labeled fixture. Neighbours still lift a normalized-key pair (0.97)
	// over it, and lift a fuzzy pair into the link band.
	neighbourLift = 0.35
	// untypedAgreement is type agreement when one side has no type.
	untypedAgreement = 0.9
	// fuzzyBlockCap bounds the quadratic fuzzy comparison per block (entities
	// sharing a node type and a first letter). A block larger than this is
	// compared by normalized key and LLM only, and the report says so.
	fuzzyBlockCap = 3000
)

type pairKey struct{ a, b string }

func orderedPair(a, b string) pairKey {
	if a > b {
		a, b = b, a
	}
	return pairKey{a, b}
}

type linkVerdict struct {
	grade string // grade a person gave an existing possiblySame edge
}

func resolveLinkFirst(ctx context.Context, db *cortexdb.DB, opts ResolveOptions) (*ResolveReport, error) {
	mergeAt := opts.MergeThreshold
	if mergeAt <= 0 {
		mergeAt = DefaultMergeThreshold
	}
	linkAt := opts.LinkThreshold
	if linkAt <= 0 {
		linkAt = DefaultLinkThreshold
	}
	if linkAt > mergeAt {
		return nil, fmt.Errorf("graphflow: resolve: link threshold %.2f is above merge threshold %.2f", linkAt, mergeAt)
	}

	entities := loadEntityInfos(ctx, db, opts.NodeTypes)
	report := &ResolveReport{EntitiesBefore: len(entities), DryRun: opts.DryRun, Groups: []ResolveGroup{}}
	if len(entities) < 2 {
		return report, nil
	}
	byID := make(map[string]entityInfo, len(entities))
	byName := make(map[string]entityInfo, len(entities))
	for _, e := range entities {
		byID[e.id] = e
		byName[strings.ToLower(e.name)] = e
	}
	neighbours, err := loadEntityNeighbours(ctx, db)
	if err != nil {
		return nil, err
	}
	verdicts, err := loadPossiblySameVerdicts(ctx, db)
	if err != nil {
		return nil, err
	}

	// --- candidates ---------------------------------------------------------
	candidates := map[pairKey]*ResolveEvidence{}
	add := func(x, y entityInfo) *ResolveEvidence {
		if x.id == y.id || !typesCompatible(x.nodeType, y.nodeType) {
			return nil
		}
		k := orderedPair(x.id, y.id)
		if ev, ok := candidates[k]; ok {
			return ev
		}
		if x.id != k.a {
			x, y = y, x
		}
		ev := &ResolveEvidence{A: x.name, B: y.name, AID: x.id, BID: y.id}
		candidates[k] = ev
		return ev
	}

	// 1. Same normalized key. Blocked by key alone, not type, so an untyped
	// and a typed spelling of one name meet — as a link, never a merge.
	keyGroups := map[string][]entityInfo{}
	claimed := map[string]struct{}{}
	for _, e := range entities {
		if k := canonicalKey(e.name); k != "" {
			keyGroups[k] = append(keyGroups[k], e)
		}
	}
	for _, g := range keyGroups {
		for i := range g {
			for j := i + 1; j < len(g); j++ {
				if add(g[i], g[j]) != nil {
					claimed[g[i].id] = struct{}{}
					claimed[g[j].id] = struct{}{}
				}
			}
		}
	}

	// 2. Near spellings, blocked by first rune of the key and type.
	blocks := map[string][]entityInfo{}
	for _, e := range entities {
		k := canonicalKey(e.name)
		if k == "" {
			continue
		}
		r, _ := utf8.DecodeRuneInString(k)
		blocks[string(r)] = append(blocks[string(r)], e)
	}
	for _, g := range blocks {
		if len(g) > fuzzyBlockCap {
			continue
		}
		for i := range g {
			ki := canonicalKey(g[i].name)
			for j := i + 1; j < len(g); j++ {
				kj := canonicalKey(g[j].name)
				if ki == kj || !typesCompatible(g[i].nodeType, g[j].nodeType) {
					continue
				}
				if jaroWinkler(ki, kj) >= fuzzyCandidateFloor {
					add(g[i], g[j])
				}
			}
		}
	}

	// 3. The model's groups, if a model was given.
	if opts.LLM != nil {
		for _, g := range llmAliasGroups(ctx, opts.LLM, entities, byName, claimed) {
			for i := range g {
				for j := i + 1; j < len(g); j++ {
					if ev := add(g[i], g[j]); ev != nil {
						ev.LLMProposed = true
					}
				}
			}
		}
	}

	// 4. Pairs a person already answered.
	for k, v := range verdicts {
		if v.grade != cortexdb.GradeVerified {
			continue
		}
		x, okx := byID[k.a]
		y, oky := byID[k.b]
		if okx && oky {
			if ev := add(x, y); ev != nil {
				ev.Confirmed = true
			}
		}
	}

	// --- scoring ------------------------------------------------------------
	var merges, links []*ResolveEvidence
	keys := make([]pairKey, 0, len(candidates))
	for k := range candidates {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].a != keys[j].a {
			return keys[i].a < keys[j].a
		}
		return keys[i].b < keys[j].b
	})
	for _, k := range keys {
		ev := candidates[k]
		if v, ok := verdicts[k]; ok && v.grade == cortexdb.GradeRefused {
			continue // a person said these are two things
		}
		scorePair(ev, byID[ev.AID], byID[ev.BID], neighbours)
		switch {
		case ev.Score >= mergeAt:
			merges = append(merges, ev)
		case ev.Score >= linkAt:
			links = append(links, ev)
		}
	}

	// --- merges -------------------------------------------------------------
	// Union-find over merge pairs, so A=B and B=C land in one group.
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if p, ok := parent[x]; ok && p != x {
			r := find(p)
			parent[x] = r
			return r
		}
		return x
	}
	for _, ev := range merges {
		ra, rb := find(ev.AID), find(ev.BID)
		if ra != rb {
			parent[ra] = rb
		}
	}
	members := map[string][]entityInfo{}
	evidence := map[string][]ResolveEvidence{}
	for _, ev := range merges {
		evidence[find(ev.AID)] = append(evidence[find(ev.AID)], *ev)
	}
	seen := map[string]bool{}
	for _, ev := range merges {
		for _, id := range []string{ev.AID, ev.BID} {
			if !seen[id] {
				seen[id] = true
				members[find(id)] = append(members[find(id)], byID[id])
			}
		}
	}
	roots := make([]string, 0, len(members))
	for r := range members {
		roots = append(roots, r)
	}
	sort.Strings(roots)

	survivor := map[string]string{} // entity id -> id it now lives under
	for _, r := range roots {
		g := members[r]
		canonical := pickCanonical(g)
		var aliasNames, aliasIDs []string
		for _, e := range g {
			if e.id == canonical.id {
				continue
			}
			aliasNames = append(aliasNames, e.name)
			aliasIDs = append(aliasIDs, e.id)
			survivor[e.id] = canonical.id
		}
		if len(aliasIDs) == 0 {
			continue
		}
		report.Groups = append(report.Groups, ResolveGroup{Canonical: canonical.name, Aliases: aliasNames, Evidence: evidence[r]})
		report.EntitiesMerged += len(aliasIDs)
		if opts.DryRun {
			continue
		}
		if err := db.Graph().MergeEntities(resolveMergeContext(ctx, canonical.id), canonical.id, aliasIDs); err != nil {
			return nil, fmt.Errorf("graphflow: resolve merge %q: %w", canonical.name, err)
		}
		recordAliases(ctx, db, canonical.id, aliasNames)
	}
	if !opts.DryRun && report.EntitiesMerged > 0 {
		if err := dedupeEntityEdges(ctx, db); err != nil {
			return nil, err
		}
	}

	// --- links --------------------------------------------------------------
	written := map[pairKey]bool{}
	for _, ev := range links {
		a, b := ev.AID, ev.BID
		if s, ok := survivor[a]; ok {
			a = s
		}
		if s, ok := survivor[b]; ok {
			b = s
		}
		if a == b {
			continue // the merge already joined them
		}
		k := orderedPair(a, b)
		if written[k] {
			continue
		}
		if v, ok := verdicts[k]; ok && v.grade != cortexdb.GradeHeld {
			continue // a person answered this pair; do not reopen it
		}
		written[k] = true
		report.Links = append(report.Links, *ev)
		report.EntitiesLinked++
		if opts.DryRun {
			continue
		}
		if err := writePossiblySame(ctx, db, k, *ev, mergeAt); err != nil {
			return nil, err
		}
	}
	return report, nil
}

func typesCompatible(a, b string) bool {
	return a == b || a == "" || b == ""
}

// scorePair fills the evidence fields and the score.
func scorePair(ev *ResolveEvidence, x, y entityInfo, neighbours map[string]map[string]struct{}) {
	ka, kb := canonicalKey(x.name), canonicalKey(y.name)
	switch {
	case strings.TrimSpace(x.name) == strings.TrimSpace(y.name):
		ev.NameSimilarity = nameSimIdentical
	case ka == kb:
		ev.NameSimilarity = nameSimNormalized
	default:
		ev.NameSimilarity = math.Min(jaroWinkler(ka, kb), nameSimFuzzyCap)
	}
	base := ev.NameSimilarity
	if ev.LLMProposed && base < llmNameFloor {
		base = llmNameFloor
	}

	na, nb := neighbours[x.id], neighbours[y.id]
	var union, shared int
	for n := range na {
		if n == y.id {
			continue
		}
		union++
		if _, ok := nb[n]; ok {
			shared++
		}
	}
	for n := range nb {
		if n == x.id {
			continue
		}
		if _, ok := na[n]; !ok {
			union++
		}
	}
	ev.SharedNeighbours = shared
	hasA := len(na) > 0 && !(len(na) == 1 && has(na, y.id))
	hasB := len(nb) > 0 && !(len(nb) == 1 && has(nb, x.id))
	if hasA && hasB && union > 0 {
		ev.NeighbourJaccard = round3(float64(shared) / float64(union))
		if shared == 0 {
			ev.NeighboursDisjoint = true
			base -= disjointPenalty
		} else {
			base = 1 - (1-base)*(1-neighbourLift*ev.NeighbourJaccard)
		}
	}

	ev.TypeAgreement = 1
	if x.nodeType != y.nodeType {
		ev.TypeAgreement = untypedAgreement
	}
	score := base * ev.TypeAgreement
	if ev.Confirmed {
		score = 1
	}
	ev.NameSimilarity = round3(ev.NameSimilarity)
	ev.Score = round3(math.Max(0, math.Min(1, score)))
}

func has(set map[string]struct{}, k string) bool { _, ok := set[k]; return ok }

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

// loadEntityNeighbours maps each entity node to the nodes it shares an edge
// with, in either direction. possiblySame edges are left out: they are this
// pass's own output, and counting them would let a link vouch for itself.
func loadEntityNeighbours(ctx context.Context, db *cortexdb.DB) (map[string]map[string]struct{}, error) {
	rows, err := db.SQL().QueryContext(ctx, db.Dialect().Rebind(`
		SELECT from_node_id, to_node_id FROM graph_edges
		WHERE (from_node_id LIKE 'entity:%' OR to_node_id LIKE 'entity:%')
		  AND (edge_type IS NULL OR edge_type <> ?)`), PossiblySameEdgeType)
	if err != nil {
		return nil, fmt.Errorf("graphflow: resolve: neighbours: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]map[string]struct{}{}
	link := func(a, b string) {
		if out[a] == nil {
			out[a] = map[string]struct{}{}
		}
		out[a][b] = struct{}{}
	}
	for rows.Next() {
		var f, t string
		if err := rows.Scan(&f, &t); err != nil {
			return nil, fmt.Errorf("graphflow: resolve: neighbours: %w", err)
		}
		if f == t {
			continue
		}
		link(f, t)
		link(t, f)
	}
	return out, rows.Err()
}

// loadPossiblySameVerdicts reads the grade on every existing possiblySame edge.
func loadPossiblySameVerdicts(ctx context.Context, db *cortexdb.DB) (map[pairKey]linkVerdict, error) {
	rows, err := db.SQL().QueryContext(ctx, db.Dialect().Rebind(`
		SELECT from_node_id, to_node_id, COALESCE(`+db.Dialect().JSONTextGuarded("properties", cortexdb.KeyGrade)+`, '')
		FROM graph_edges WHERE edge_type = ?`), PossiblySameEdgeType)
	if err != nil {
		return nil, fmt.Errorf("graphflow: resolve: read links: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[pairKey]linkVerdict{}
	for rows.Next() {
		var f, t, grade string
		if err := rows.Scan(&f, &t, &grade); err != nil {
			return nil, fmt.Errorf("graphflow: resolve: read links: %w", err)
		}
		out[orderedPair(f, t)] = linkVerdict{grade: grade}
	}
	return out, rows.Err()
}

// possiblySameEdgeID is stable per unordered pair, so a second pass rewrites
// the same link instead of adding another.
func possiblySameEdgeID(k pairKey) string {
	return "edge:" + PossiblySameEdgeType + ":" + k.a + ":" + k.b
}

// writePossiblySame records a link for a person, graded held with the
// evidence in words — which is what contract_needs_attention shows.
func writePossiblySame(ctx context.Context, db *cortexdb.DB, k pairKey, ev ResolveEvidence, mergeAt float64) error {
	typeWord := "same type"
	if ev.TypeAgreement < 1 {
		typeWord = "one side untyped"
	}
	neighbourWord := "no neighbours to compare"
	switch {
	case ev.NeighboursDisjoint:
		neighbourWord = "no shared neighbours"
	case ev.SharedNeighbours > 0:
		neighbourWord = fmt.Sprintf("%d shared neighbours (Jaccard %.2f)", ev.SharedNeighbours, ev.NeighbourJaccard)
	}
	why := fmt.Sprintf("%q and %q may be one entity: name similarity %.2f, %s, %s",
		ev.A, ev.B, ev.NameSimilarity, neighbourWord, typeWord)
	if ev.LLMProposed {
		why += ", grouped by the model"
	}
	why += fmt.Sprintf("; score %.2f is below the %.2f merge threshold. Set _grade to verified to merge on the next resolve, or refused to keep them apart.", ev.Score, mergeAt)

	producer := cortexdb.ProducerCompiled
	if ev.LLMProposed {
		producer = cortexdb.ProducerLLMExtract
	}
	props := map[string]interface{}{
		"name_similarity":      ev.NameSimilarity,
		"shared_neighbours":    ev.SharedNeighbours,
		"neighbour_jaccard":    ev.NeighbourJaccard,
		"type_agreement":       ev.TypeAgreement,
		"llm_proposed":         ev.LLMProposed,
		"score":                ev.Score,
		cortexdb.KeyGrade:      cortexdb.GradeHeld,
		cortexdb.KeyState:      "possibly_same",
		cortexdb.KeyWhy:        why,
		cortexdb.KeyProducer:   producer,
		cortexdb.KeySource:     ProducerResolve,
		cortexdb.KeyAt:         time.Now().UTC().Format(time.RFC3339),
		cortexdb.KeyConfidence: fmt.Sprintf("%.3f", ev.Score),
	}
	if ev.NeighboursDisjoint {
		props["neighbours_disjoint"] = true
	}
	edge := &graph.GraphEdge{
		ID:         possiblySameEdgeID(k),
		FromNodeID: k.a,
		ToNodeID:   k.b,
		EdgeType:   PossiblySameEdgeType,
		Weight:     ev.Score,
		Properties: props,
	}
	if err := db.Graph().UpsertEdge(ctx, edge); err != nil {
		return fmt.Errorf("graphflow: resolve: link %s: %w", edge.ID, err)
	}
	return nil
}

// possiblySameContract is the contract metadata writePossiblySame produces,
// as strings, for ValidateContract.
func possiblySameContract(props map[string]interface{}) map[string]string {
	out := map[string]string{}
	for k, v := range props {
		if strings.HasPrefix(k, cortexdb.ContractPrefix) {
			out[k] = fmt.Sprint(v)
		}
	}
	return out
}

// jaroWinkler is the Jaro-Winkler similarity of two strings, by rune.
func jaroWinkler(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 && len(rb) == 0 {
		return 1
	}
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}
	window := max(len(ra), len(rb))/2 - 1
	if window < 0 {
		window = 0
	}
	ma := make([]bool, len(ra))
	mb := make([]bool, len(rb))
	matches := 0
	for i := range ra {
		lo, hi := max(0, i-window), min(len(rb), i+window+1)
		for j := lo; j < hi; j++ {
			if !mb[j] && ra[i] == rb[j] {
				ma[i], mb[j] = true, true
				matches++
				break
			}
		}
	}
	if matches == 0 {
		return 0
	}
	transpositions, j := 0, 0
	for i := range ra {
		if !ma[i] {
			continue
		}
		for !mb[j] {
			j++
		}
		if ra[i] != rb[j] {
			transpositions++
		}
		j++
	}
	m := float64(matches)
	jaro := (m/float64(len(ra)) + m/float64(len(rb)) + (m-float64(transpositions)/2)/m) / 3
	prefix := 0
	for i := 0; i < min(4, len(ra), len(rb)) && ra[i] == rb[i]; i++ {
		prefix++
	}
	return jaro + float64(prefix)*0.1*(1-jaro)
}
