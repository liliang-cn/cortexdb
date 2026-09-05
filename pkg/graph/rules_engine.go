package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Default caps for forward chaining.
//
// A rule set is a program, and a program written in a tool call can loop. The
// caps are what stops one: not a timeout, which would leave whatever it had
// managed to write behind, but a hard bound after which ApplyRules writes
// nothing and says why. Sixteen rounds is far more than transitive closure over
// a real graph needs — closure over n nodes converges in ceil(log2 n) rounds of
// this naive chaining — and 50k edges is more than any single call should be
// adding to a brain without somebody having decided to.
const (
	DefaultRuleMaxIterations = 16
	DefaultRuleMaxDerived    = 50000
)

// RuleProvenance is the provenance value written onto every rule-derived edge.
// The original two-hop inference wrote it, and it is what tells a reader that a
// rule and not an extractor put the edge there.
const RuleProvenance = "rule"

// RuleOptions configures one forward-chaining run.
type RuleOptions struct {
	// DocumentID scopes which edges may take part and is stamped onto what is
	// derived. Empty means the whole graph, and derived edges inherit a
	// document only when every premise agrees on one.
	DocumentID string
	// MaxIterations caps the chaining rounds. Zero means DefaultRuleMaxIterations.
	MaxIterations int
	// MaxDerived caps how many edges one run may derive. Zero means
	// DefaultRuleMaxDerived.
	MaxDerived int
	// DryRun computes the derivation and writes nothing. The result carries the
	// edges it would have written.
	DryRun bool
	// Validate, when set, is given the derived edges before they are written
	// and can refuse them. It exists because the ontology that decides whether
	// a relation is legal lives a layer up, and derived edges have to pass the
	// same gate hand-written ones do.
	Validate func(ctx context.Context, edges []*GraphEdge) error
}

func (o RuleOptions) maxIterations() int {
	if o.MaxIterations > 0 {
		return o.MaxIterations
	}
	return DefaultRuleMaxIterations
}

func (o RuleOptions) maxDerived() int {
	if o.MaxDerived > 0 {
		return o.MaxDerived
	}
	return DefaultRuleMaxDerived
}

// RuleApplyResult reports what one forward-chaining run did.
type RuleApplyResult struct {
	// Iterations is how many chaining rounds actually ran.
	Iterations int `json:"iterations"`
	// CandidateEdges is how many stored edges took part as facts.
	CandidateEdges int `json:"candidate_edges"`
	// CreatedEdgeIDs are the edges this run wrote, sorted. On a dry run they
	// are the edges it would have written.
	CreatedEdgeIDs []string `json:"created_edge_ids,omitempty"`
	// UnchangedEdgeIDs are edges this rule set re-derived that were already
	// stored by the same rule. Reported rather than rewritten, which is what
	// makes a second run of the same rules a no-op.
	UnchangedEdgeIDs []string `json:"unchanged_edge_ids,omitempty"`
	// Edges are the derived edges in CreatedEdgeIDs order, with the provenance
	// they carry.
	Edges []*GraphEdge `json:"edges,omitempty"`
	// UnresolvedTerms are literal terms in the rules that match no stored node,
	// so the rules mentioning them could never fire. Reported because a rule
	// that silently matches nothing looks exactly like a rule that is simply
	// not true of this graph.
	UnresolvedTerms []string `json:"unresolved_terms,omitempty"`
	// CapHit and CapReason say the run stopped at a cap rather than at a
	// fixpoint. When they are set nothing was written and ApplyRules returned
	// ErrRuleCapExceeded.
	CapHit    bool   `json:"cap_hit,omitempty"`
	CapReason string `json:"cap_reason,omitempty"`
	// DryRun echoes the option, so a caller reading only the result can tell
	// whether the edges it lists are in the store.
	DryRun bool `json:"dry_run,omitempty"`
}

// ruleFact is one edge as the engine sees it: an atom that is true.
type ruleFact struct {
	edgeID       string
	predicate    string
	subject      string
	object       string
	weight       float64
	confidence   float64
	derived      bool
	stored       bool
	ruleID       string
	supports     []string
	supportTypes []string
	documentID   string
}

func (f *ruleFact) key() string {
	return f.predicate + "\x00" + f.subject + "\x00" + f.object
}

type ruleFactIndex struct {
	byPred     map[string][]*ruleFact
	byPredSubj map[string]map[string][]*ruleFact
	byPredObj  map[string]map[string][]*ruleFact
	seen       map[string]*ruleFact
}

func newRuleFactIndex() *ruleFactIndex {
	return &ruleFactIndex{
		byPred:     make(map[string][]*ruleFact),
		byPredSubj: make(map[string]map[string][]*ruleFact),
		byPredObj:  make(map[string]map[string][]*ruleFact),
		seen:       make(map[string]*ruleFact),
	}
}

func (idx *ruleFactIndex) add(f *ruleFact) {
	idx.byPred[f.predicate] = append(idx.byPred[f.predicate], f)
	if idx.byPredSubj[f.predicate] == nil {
		idx.byPredSubj[f.predicate] = make(map[string][]*ruleFact)
	}
	idx.byPredSubj[f.predicate][f.subject] = append(idx.byPredSubj[f.predicate][f.subject], f)
	if idx.byPredObj[f.predicate] == nil {
		idx.byPredObj[f.predicate] = make(map[string][]*ruleFact)
	}
	idx.byPredObj[f.predicate][f.object] = append(idx.byPredObj[f.predicate][f.object], f)
	idx.seen[f.key()] = f
}

// ApplyRules forward-chains rules over the stored graph to a fixpoint and
// materializes what it derives.
//
// Every derived edge carries inferred=true, provenance=rule, the rule's id and
// text, the exact premise edge ids, and a confidence that is the minimum of the
// premise confidences times the rule's own — the same provenance the two-hop
// inference wrote, which is what lets ExplainEdge explain either.
//
// A run that hits a cap writes nothing and returns ErrRuleCapExceeded along
// with a result whose CapReason says which cap. Both are returned: the error is
// what a caller cannot ignore, the result is what tells them why.
func (g *GraphStore) ApplyRules(ctx context.Context, rules []Rule, opts RuleOptions) (*RuleApplyResult, error) {
	if len(rules) == 0 {
		return nil, fmt.Errorf("at least one rule is required")
	}
	for _, rule := range rules {
		if err := rule.Validate(); err != nil {
			return nil, err
		}
	}
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}

	result := &RuleApplyResult{DryRun: opts.DryRun}

	predicates, literals := ruleVocabulary(rules)
	resolved, unresolved, err := g.resolveRuleTerms(ctx, literals)
	if err != nil {
		return nil, err
	}
	result.UnresolvedTerms = unresolved

	facts, err := g.loadRuleFacts(ctx, predicates, opts.DocumentID)
	if err != nil {
		return nil, err
	}
	result.CandidateEdges = len(facts)

	idx := newRuleFactIndex()
	for _, fact := range facts {
		// An edge whose relation already holds between the same two nodes is
		// the one fact; the first one loaded (edges arrive in id order) wins.
		if _, dup := idx.seen[fact.key()]; dup {
			continue
		}
		idx.add(fact)
	}

	derived := make([]*ruleFact, 0)
	unchanged := make(map[string]struct{})
	maxIter := opts.maxIterations()
	maxDerived := opts.maxDerived()

	for {
		round, capReason := chainOnce(rules, idx, resolved, unchanged, len(derived), maxDerived)
		if capReason == "" && len(round) > 0 && result.Iterations >= maxIter {
			// Every allowed round produced facts and there are still more
			// arriving, so this is not a fixpoint — it is a truncation, which
			// is the one thing the caller must not be left to discover later.
			capReason = fmt.Sprintf("still deriving after %d iterations", maxIter)
		}
		if capReason != "" {
			result.CapHit = true
			result.CapReason = capReason
			return result, fmt.Errorf("%w: %s", ErrRuleCapExceeded, capReason)
		}
		if len(round) == 0 {
			break
		}
		result.Iterations++
		for _, fact := range round {
			idx.add(fact)
			derived = append(derived, fact)
		}
	}

	result.UnchangedEdgeIDs = sortedSetKeys(unchanged)

	byID := rulesByID(rules)
	sort.Slice(derived, func(i, j int) bool { return derived[i].edgeID < derived[j].edgeID })
	edges := make([]*GraphEdge, 0, len(derived))
	for _, fact := range derived {
		edges = append(edges, fact.edge(byID[fact.ruleID], opts.DocumentID))
		result.CreatedEdgeIDs = append(result.CreatedEdgeIDs, fact.edgeID)
	}
	result.Edges = edges
	if len(edges) == 0 || opts.DryRun {
		return result, nil
	}

	if opts.Validate != nil {
		if err := opts.Validate(ctx, edges); err != nil {
			return nil, err
		}
	}
	batch, err := g.UpsertEdgesBatch(ctx, edges)
	if err != nil {
		return nil, fmt.Errorf("upsert derived edges: %w", err)
	}
	// Rejected rows live in the result, not in err: without this the response
	// would list edges that were never written.
	if err := batch.Err(); err != nil {
		return nil, fmt.Errorf("upsert derived edges: %w", err)
	}
	return result, nil
}

// chainOnce runs every rule once over the current facts and returns the facts
// that are new. It never mutates idx: a round sees a consistent world, which is
// what makes the derivation independent of rule order within a round.
func chainOnce(rules []Rule, idx *ruleFactIndex, literals map[string][]string, unchanged map[string]struct{}, alreadyDerived, maxDerived int) ([]*ruleFact, string) {
	pending := make(map[string]*ruleFact)
	order := make([]*ruleFact, 0)
	capReason := ""

	for _, rule := range rules {
		matchPremises(rule, 0, idx, literals, map[string]string{}, nil, func(bindings map[string]string, supports []*ruleFact) bool {
			subjects, ok := conclusionTerms(rule.Then.Subject, bindings, literals)
			if !ok {
				return true
			}
			objects, ok := conclusionTerms(rule.Then.Object, bindings, literals)
			if !ok {
				return true
			}
			predicate := normalizeRuleLabel(rule.Then.Predicate)
			for _, subject := range subjects {
				for _, object := range objects {
					fact := deriveFact(rule, predicate, subject, object, supports)
					key := fact.key()
					if existing, ok := idx.seen[key]; ok {
						// Already true. Recording the ones this same rule put
						// there is how a re-run reports "nothing new" instead
						// of looking like it found nothing.
						if existing.stored && existing.derived && existing.ruleID == rule.ID {
							unchanged[existing.edgeID] = struct{}{}
						}
						continue
					}
					if _, ok := pending[key]; ok {
						continue
					}
					if alreadyDerived+len(pending) >= maxDerived {
						capReason = fmt.Sprintf("would derive more than %d edges", maxDerived)
						return false
					}
					pending[key] = fact
					order = append(order, fact)
				}
			}
			return true
		})
		if capReason != "" {
			return nil, capReason
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i].edgeID < order[j].edgeID })
	return order, ""
}

// matchPremises walks the premises left to right, binding variables as it goes,
// and calls emit for every complete consistent assignment. emit returns false
// to abandon the search.
func matchPremises(rule Rule, i int, idx *ruleFactIndex, literals map[string][]string, bindings map[string]string, supports []*ruleFact, emit func(map[string]string, []*ruleFact) bool) bool {
	if i == len(rule.When) {
		return emit(bindings, supports)
	}
	atom := rule.When[i]
	predicate := normalizeRuleLabel(atom.Predicate)

	subjectIDs, subjectBound := bindTerm(atom.Subject, bindings, literals)
	objectIDs, objectBound := bindTerm(atom.Object, bindings, literals)
	if (subjectBound && len(subjectIDs) == 0) || (objectBound && len(objectIDs) == 0) {
		return true
	}

	for _, candidate := range premiseCandidates(idx, predicate, subjectIDs, subjectBound, objectIDs, objectBound) {
		if subjectBound && !containsString(subjectIDs, candidate.subject) {
			continue
		}
		if objectBound && !containsString(objectIDs, candidate.object) {
			continue
		}
		nextBindings := bindings
		if IsRuleVariable(atom.Subject) && !subjectBound {
			nextBindings = withBinding(nextBindings, atom.Subject, candidate.subject)
		}
		if IsRuleVariable(atom.Object) {
			if bound, ok := nextBindings[atom.Object]; ok {
				if bound != candidate.object {
					continue
				}
			} else {
				nextBindings = withBinding(nextBindings, atom.Object, candidate.object)
			}
		}
		// Copied, not appended in place: the slice is shared with every sibling
		// branch of this search, and a premise left behind by a branch that
		// failed would be recorded as support for one that succeeded.
		nextSupports := make([]*ruleFact, len(supports), len(supports)+1)
		copy(nextSupports, supports)
		nextSupports = append(nextSupports, candidate)
		if !matchPremises(rule, i+1, idx, literals, nextBindings, nextSupports, emit) {
			return false
		}
	}
	return true
}

// premiseCandidates picks the narrowest index that answers this premise.
func premiseCandidates(idx *ruleFactIndex, predicate string, subjectIDs []string, subjectBound bool, objectIDs []string, objectBound bool) []*ruleFact {
	switch {
	case subjectBound:
		if len(subjectIDs) == 1 {
			return idx.byPredSubj[predicate][subjectIDs[0]]
		}
		out := make([]*ruleFact, 0)
		for _, id := range subjectIDs {
			out = append(out, idx.byPredSubj[predicate][id]...)
		}
		return out
	case objectBound:
		if len(objectIDs) == 1 {
			return idx.byPredObj[predicate][objectIDs[0]]
		}
		out := make([]*ruleFact, 0)
		for _, id := range objectIDs {
			out = append(out, idx.byPredObj[predicate][id]...)
		}
		return out
	default:
		return idx.byPred[predicate]
	}
}

// bindTerm says what a term is currently pinned to. A variable that no premise
// has bound yet is free; a literal is whatever nodes it resolved to.
func bindTerm(term string, bindings map[string]string, literals map[string][]string) ([]string, bool) {
	if IsRuleVariable(term) {
		if bound, ok := bindings[term]; ok {
			return []string{bound}, true
		}
		return nil, false
	}
	return literals[term], true
}

func conclusionTerms(term string, bindings map[string]string, literals map[string][]string) ([]string, bool) {
	if IsRuleVariable(term) {
		bound, ok := bindings[term]
		if !ok {
			return nil, false
		}
		return []string{bound}, true
	}
	ids := literals[term]
	if len(ids) == 0 {
		return nil, false
	}
	return ids, true
}

// withBinding copies rather than mutating: the map is shared with every sibling
// branch of the search, and a binding left behind by a branch that failed is
// the classic way a backtracking join starts inventing answers.
func withBinding(bindings map[string]string, name, value string) map[string]string {
	next := make(map[string]string, len(bindings)+1)
	for k, v := range bindings {
		next[k] = v
	}
	next[name] = value
	return next
}

func deriveFact(rule Rule, predicate, subject, object string, supports []*ruleFact) *ruleFact {
	confidence := rule.effectiveConfidence()
	weight := 0.0
	supportIDs := make([]string, 0, len(supports))
	supportTypes := make([]string, 0, len(supports))
	minPremise := 1.0
	documentID := ""
	sharedDocument := true
	for i, support := range supports {
		supportIDs = append(supportIDs, support.edgeID)
		supportTypes = append(supportTypes, support.predicate)
		if support.confidence < minPremise {
			minPremise = support.confidence
		}
		weight += support.weight
		if i == 0 {
			documentID = support.documentID
			continue
		}
		if support.documentID != documentID {
			sharedDocument = false
		}
	}
	if len(supports) > 0 {
		weight /= float64(len(supports))
	}
	if rule.Weight != 0 {
		weight = rule.Weight
	}
	if !sharedDocument {
		documentID = ""
	}
	return &ruleFact{
		edgeID:       DerivedEdgeID(rule.ID, subject, object, predicate),
		predicate:    predicate,
		subject:      subject,
		object:       object,
		weight:       weight,
		confidence:   minPremise * confidence,
		derived:      true,
		ruleID:       rule.ID,
		supports:     supportIDs,
		supportTypes: supportTypes,
		documentID:   documentID,
	}
}

func (f *ruleFact) edge(rule Rule, scopeDocumentID string) *GraphEdge {
	documentID := strings.TrimSpace(scopeDocumentID)
	if documentID == "" {
		documentID = f.documentID
	}
	properties := map[string]any{
		"inferred":               true,
		"provenance":             RuleProvenance,
		"rule_id":                f.ruleID,
		"rule_text":              rule.Text(),
		"support_edge_ids":       f.supports,
		"support_relation_types": f.supportTypes,
		"confidence":             f.confidence,
	}
	if documentID != "" {
		properties["document_id"] = documentID
	}
	for key, value := range rule.Metadata {
		if _, reserved := reservedRuleEdgeProperties[key]; reserved {
			continue
		}
		properties[key] = value
	}
	return &GraphEdge{
		ID:         f.edgeID,
		FromNodeID: f.subject,
		ToNodeID:   f.object,
		EdgeType:   f.predicate,
		Weight:     f.weight,
		Properties: properties,
	}
}

// ruleVocabulary collects the predicates a rule set touches and the literal
// terms it names. Conclusions are included in the predicate set on purpose:
// without them a re-run cannot see what it derived last time and would derive
// it again.
func ruleVocabulary(rules []Rule) ([]string, []string) {
	predicates := make(map[string]struct{})
	literals := make(map[string]struct{})
	note := func(atom Atom) {
		predicates[normalizeRuleLabel(atom.Predicate)] = struct{}{}
		for _, term := range []string{atom.Subject, atom.Object} {
			if !IsRuleVariable(term) {
				literals[term] = struct{}{}
			}
		}
	}
	for _, rule := range rules {
		for _, atom := range rule.When {
			note(atom)
		}
		note(rule.Then)
	}
	return sortedSetKeys(predicates), sortedSetKeys(literals)
}

func rulesByID(rules []Rule) map[string]Rule {
	out := make(map[string]Rule, len(rules))
	for _, rule := range rules {
		out[rule.ID] = rule
	}
	return out
}

// loadRuleFacts reads every stored edge whose type a rule mentions.
//
// Unlike the two-hop loader it replaced, it does not exclude edges that were
// themselves inferred. That is deliberate and it is what makes chaining work:
// a fixpoint over rules that never see their own output is one round.
func (g *GraphStore) loadRuleFacts(ctx context.Context, predicates []string, documentID string) ([]*ruleFact, error) {
	if len(predicates) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(predicates))
	args := make([]any, 0, len(predicates)+1)
	for i, predicate := range predicates {
		placeholders[i] = "?"
		args = append(args, predicate)
	}
	query := fmt.Sprintf(`
		SELECT id, from_node_id, to_node_id, edge_type, weight, properties
		FROM graph_edges
		WHERE edge_type IN (%s)
	`, strings.Join(placeholders, ","))
	if strings.TrimSpace(documentID) != "" {
		query += ` AND ` + g.dialect.JSONTextGuarded("properties", "document_id") + ` = ?`
		args = append(args, documentID)
	}
	query += ` ORDER BY id ASC`

	rows, err := g.query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query rule candidate edges: %w", err)
	}
	defer func() { _ = rows.Close() }()

	facts := make([]*ruleFact, 0)
	for rows.Next() {
		var (
			id, from, to  string
			edgeType      sql.NullString
			weight        float64
			propertiesRaw sql.NullString
		)
		if err := rows.Scan(&id, &from, &to, &edgeType, &weight, &propertiesRaw); err != nil {
			return nil, fmt.Errorf("scan rule candidate edge: %w", err)
		}
		properties := map[string]any{}
		if propertiesRaw.Valid && propertiesRaw.String != "" {
			if err := json.Unmarshal([]byte(propertiesRaw.String), &properties); err != nil {
				return nil, fmt.Errorf("decode rule candidate edge properties: %w", err)
			}
		}
		fact := &ruleFact{
			edgeID:     id,
			predicate:  normalizeRuleLabel(edgeType.String),
			subject:    from,
			object:     to,
			weight:     weight,
			confidence: ruleFloatProperty(properties, "confidence", 1),
			stored:     true,
		}
		if inferred, ok := properties["inferred"].(bool); ok && inferred {
			fact.derived = true
		}
		if ruleID, ok := properties["rule_id"].(string); ok {
			fact.ruleID = ruleID
		}
		if document, ok := properties["document_id"].(string); ok {
			fact.documentID = document
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rule candidate edges: %w", err)
	}
	return facts, nil
}

// resolveRuleTerms turns the literal terms of a rule set into node ids.
//
// The matching a caller has to be told about, in order:
//
//  1. a node whose id is exactly the term;
//  2. otherwise, if the term contains a colon, "Type:Name" — every node whose
//     node_type equals Type and whose name (the name property, or the node
//     content) equals Name, both compared case-insensitively;
//  3. otherwise the term is a bare name, matched the same way across all types.
//
// A term matching several nodes matches all of them; a term matching none is
// returned as unresolved, because a rule that can never fire should say so
// rather than look like a rule that found nothing true.
func (g *GraphStore) resolveRuleTerms(ctx context.Context, terms []string) (map[string][]string, []string, error) {
	resolved := make(map[string][]string, len(terms))
	unresolved := make([]string, 0)
	for _, term := range terms {
		ids, err := g.resolveRuleTerm(ctx, term)
		if err != nil {
			return nil, nil, err
		}
		if len(ids) == 0 {
			unresolved = append(unresolved, term)
			continue
		}
		resolved[term] = ids
	}
	if len(unresolved) == 0 {
		unresolved = nil
	}
	return resolved, unresolved, nil
}

func (g *GraphStore) resolveRuleTerm(ctx context.Context, term string) ([]string, error) {
	var exact string
	err := g.queryRow(ctx, `SELECT id FROM graph_nodes WHERE id = ? ORDER BY id ASC`, term).Scan(&exact)
	switch {
	case err == nil:
		return []string{exact}, nil
	case err != sql.ErrNoRows:
		return nil, fmt.Errorf("resolve rule term %q: %w", term, err)
	}

	nameExpr := g.dialect.JSONTextGuarded("properties", "name")
	nodeType, name := "", term
	if idx := strings.Index(term, ":"); idx > 0 && idx < len(term)-1 {
		nodeType, name = term[:idx], term[idx+1:]
	}

	query := fmt.Sprintf(`SELECT id FROM graph_nodes WHERE (LOWER(%s) = ? OR LOWER(content) = ?)`, nameExpr)
	args := []any{strings.ToLower(name), strings.ToLower(name)}
	if nodeType != "" {
		query += ` AND LOWER(node_type) = ?`
		args = append(args, strings.ToLower(nodeType))
	}
	query += ` ORDER BY id ASC`

	rows, err := g.query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("resolve rule term %q: %w", term, err)
	}
	defer func() { _ = rows.Close() }()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan rule term %q: %w", term, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rule term %q: %w", term, err)
	}
	return ids, nil
}

func ruleFloatProperty(properties map[string]any, key string, fallback float64) float64 {
	switch value := properties[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case json.Number:
		if parsed, err := value.Float64(); err == nil {
			return parsed
		}
	}
	return fallback
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sortedSetKeys[V any](set map[string]V) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
