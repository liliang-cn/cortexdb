package graphflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Learning knowledge graphs — study material (physics, chemistry, math, a
// foreign language, …) as a graph of concepts linked by PREREQUISITE edges.
//
// The point is the one question retrieval cannot answer: "what must I learn,
// and in what order, before I can understand X?" That is a graph problem —
// collect the prerequisite closure of a target and topologically sort it —
// not a similarity search. The same edges answer "what can I learn next?"
// (concepts whose prerequisites are all mastered) and "why am I stuck?"
// (which prerequisites of X I am still missing).
//
// Edge direction: `A --requires--> B` reads "A requires B", i.e. B is a
// prerequisite of A and must be studied first.

// Relation types used by learning graphs.
const (
	RelRequires  = "requires"   // prerequisite: from requires to
	RelPartOf    = "part_of"    // topic hierarchy: concept part_of chapter/topic
	RelExampleOf = "example_of" // worked example / instance of a concept
	RelApplies   = "applies"    // a concept applied by another (law applied by technique)
)

// masteredKey is the node property holding the RFC3339 time a concept was mastered.
const masteredKey = "mastered_at"

// allowedConceptTypes is the vocabulary a learning concept may use. Extraction
// is normalized into it so a graph stays queryable across subjects; anything
// else degrades to the generic "concept".
var allowedConceptTypes = map[string]bool{
	"concept":    true, // any subject: an idea/topic
	"definition": true,
	"law":        true, // physics: Newton's second law; chemistry: conservation of mass
	"formula":    true,
	"theorem":    true, // math
	"proof":      true,
	"method":     true, // a technique/procedure (integration by parts, titration)
	"constant":   true,
	"unit":       true,
	"element":    true, // chemistry
	"compound":   true,
	"reaction":   true,
	"experiment": true,
	"vocabulary": true, // language
	"grammar":    true,
	"phrase":     true,
	"topic":      true, // chapter/unit grouping
}

// LearningConcept is one node in a learning graph.
type LearningConcept struct {
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"`    // see allowedConceptTypes
	Subject    string `json:"subject,omitempty"` // physics|chemistry|math|language|…
	Summary    string `json:"summary,omitempty"`
	Difficulty int    `json:"difficulty,omitempty"` // 1..5, optional
	Mastered   bool   `json:"mastered,omitempty"`   // filled in by queries
}

// LearningRelation is one edge in a learning graph.
type LearningRelation struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type,omitempty"` // requires|part_of|example_of|applies
}

// LearningGraph is an importable study-material graph.
type LearningGraph struct {
	Subject   string             `json:"subject,omitempty"`
	Concepts  []LearningConcept  `json:"concepts"`
	Relations []LearningRelation `json:"relations"`
}

// LearningImportReport summarizes an import.
type LearningImportReport struct {
	Subject   int `json:"-"`
	Concepts  int `json:"concepts"`
	Relations int `json:"relations"`
}

// ImportLearningGraph writes concepts and their prerequisite/structure edges
// into the knowledge graph through the standard GraphRAG upsert path, so they
// are queryable by every existing tool (expand_graph, SPARQL, the graph view)
// in addition to the learning queries below. Relation endpoints that were not
// declared as concepts are backfilled, so an edge is never left dangling.
// Idempotent: re-importing updates in place.
func ImportLearningGraph(ctx context.Context, db *cortexdb.DB, lg LearningGraph) (*LearningImportReport, error) {
	if db == nil {
		return nil, fmt.Errorf("graphflow: learning: nil db")
	}

	seen := make(map[string]struct{})
	entities := make([]cortexdb.ToolEntityInput, 0, len(lg.Concepts))
	add := func(c LearningConcept) {
		name := collapseSpaces(c.Name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		meta := map[string]string{}
		subject := strings.ToLower(strings.TrimSpace(firstNonEmptyLearn(c.Subject, lg.Subject)))
		if subject != "" {
			meta["subject"] = subject
		}
		if c.Difficulty > 0 {
			meta["difficulty"] = strconv.Itoa(c.Difficulty)
		}
		e := cortexdb.ToolEntityInput{
			Name:        name,
			Type:        normalizeConceptType(c.Type),
			Description: c.Summary,
		}
		if len(meta) > 0 {
			e.Metadata = meta
		}
		entities = append(entities, e)
	}
	for _, c := range lg.Concepts {
		add(c)
	}

	relSeen := make(map[string]struct{})
	relations := make([]cortexdb.ToolRelationInput, 0, len(lg.Relations))
	for _, r := range lg.Relations {
		from, to := collapseSpaces(r.From), collapseSpaces(r.To)
		if from == "" || to == "" || strings.EqualFold(from, to) {
			continue
		}
		add(LearningConcept{Name: from, Subject: lg.Subject})
		add(LearningConcept{Name: to, Subject: lg.Subject})
		typ := normalizeLearningRelation(r.Type)
		key := strings.ToLower(from) + "\x00" + strings.ToLower(to) + "\x00" + typ
		if _, dup := relSeen[key]; dup {
			continue
		}
		relSeen[key] = struct{}{}
		relations = append(relations, cortexdb.ToolRelationInput{From: from, To: to, Type: typ})
	}

	tools := db.GraphRAGTools()
	if len(entities) > 0 {
		if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{Entities: entities}); err != nil {
			return nil, fmt.Errorf("graphflow: learning upsert concepts: %w", err)
		}
	}
	if len(relations) > 0 {
		if _, err := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{Relations: relations}); err != nil {
			return nil, fmt.Errorf("graphflow: learning upsert relations: %w", err)
		}
	}
	return &LearningImportReport{Concepts: len(entities), Relations: len(relations)}, nil
}

// LearningPathResult is an ordered study plan.
type LearningPathResult struct {
	Target string            `json:"target"`
	Steps  []LearningConcept `json:"steps"`   // prerequisites first, target last
	Known  []string          `json:"known"`   // already-mastered concepts that were skipped
	Cycles []string          `json:"cycles"`  // concepts involved in a prerequisite cycle, if any
	Missing bool             `json:"missing"` // target not present in the graph
}

// LearningPath returns an ordered study plan for reaching `target`: every
// concept in the target's prerequisite closure, topologically sorted so that a
// concept always appears after the concepts it requires, with anything already
// mastered removed. When `known` is nil the mastered set is loaded from the
// graph.
//
// If the prerequisite edges contain a cycle (LLM-extracted material sometimes
// does), the cycle is broken deterministically — the remaining concepts are
// still emitted, lowest difficulty first — and the involved concepts are
// reported in Cycles rather than hanging or silently dropping them.
func LearningPath(ctx context.Context, db *cortexdb.DB, target string, known []string) (*LearningPathResult, error) {
	if db == nil {
		return nil, fmt.Errorf("graphflow: learning: nil db")
	}
	target = collapseSpaces(target)
	if target == "" {
		return nil, fmt.Errorf("graphflow: learning: empty target concept")
	}
	g, err := loadLearningGraph(ctx, db)
	if err != nil {
		return nil, err
	}
	targetKey, ok := g.resolve(target)
	if !ok {
		return &LearningPathResult{Target: target, Missing: true}, nil
	}

	knownSet := make(map[string]struct{})
	if known == nil {
		for k, c := range g.concepts {
			if c.Mastered {
				knownSet[k] = struct{}{}
			}
		}
	} else {
		for _, k := range known {
			if key, ok := g.resolve(k); ok {
				knownSet[key] = struct{}{}
			}
		}
	}

	// Prerequisite closure of the target (following `requires` out-edges).
	closure := make(map[string]struct{})
	var walk func(string)
	walk = func(k string) {
		if _, done := closure[k]; done {
			return
		}
		closure[k] = struct{}{}
		for _, pre := range g.requires[k] {
			walk(pre)
		}
	}
	walk(targetKey)

	// Drop mastered concepts (and anything only needed for them stays — a
	// mastered concept's own prerequisites are implicitly known).
	pending := make(map[string]struct{}, len(closure))
	for k := range closure {
		if _, isKnown := knownSet[k]; !isKnown {
			pending[k] = struct{}{}
		}
	}

	result := &LearningPathResult{Target: g.concepts[targetKey].Name}
	for k := range knownSet {
		if _, inClosure := closure[k]; inClosure {
			result.Known = append(result.Known, g.concepts[k].Name)
		}
	}
	sort.Strings(result.Known)

	// Kahn's algorithm over the pending sub-DAG: a concept is ready when all of
	// its pending prerequisites have been emitted.
	remainingDeps := make(map[string]int, len(pending))
	dependents := make(map[string][]string, len(pending))
	for k := range pending {
		n := 0
		for _, pre := range g.requires[k] {
			if _, isPending := pending[pre]; isPending {
				n++
				dependents[pre] = append(dependents[pre], k)
			}
		}
		remainingDeps[k] = n
	}

	ready := make([]string, 0, len(pending))
	for k := range pending {
		if remainingDeps[k] == 0 {
			ready = append(ready, k)
		}
	}
	g.sortKeys(ready)

	emitted := make(map[string]struct{}, len(pending))
	for len(emitted) < len(pending) {
		if len(ready) == 0 {
			// Cycle: nothing is dependency-free. Break it deterministically by
			// releasing the easiest remaining concept, and report the cycle.
			stuck := make([]string, 0)
			for k := range pending {
				if _, done := emitted[k]; !done {
					stuck = append(stuck, k)
				}
			}
			g.sortKeys(stuck)
			for _, k := range stuck {
				result.Cycles = append(result.Cycles, g.concepts[k].Name)
			}
			ready = append(ready, stuck[0])
			remainingDeps[stuck[0]] = 0
		}
		k := ready[0]
		ready = ready[1:]
		if _, done := emitted[k]; done {
			continue
		}
		emitted[k] = struct{}{}
		result.Steps = append(result.Steps, g.concepts[k])
		next := make([]string, 0)
		for _, dep := range dependents[k] {
			remainingDeps[dep]--
			if remainingDeps[dep] <= 0 {
				if _, done := emitted[dep]; !done {
					next = append(next, dep)
				}
			}
		}
		g.sortKeys(next)
		ready = append(ready, next...)
		g.sortKeys(ready)
	}
	return result, nil
}

// NextConcepts returns the learnable frontier: concepts that are not yet
// mastered but whose every prerequisite is. These are exactly what the learner
// is ready to study now. When `known` is nil the mastered set comes from the
// graph. limit <= 0 returns all.
func NextConcepts(ctx context.Context, db *cortexdb.DB, known []string, limit int) ([]LearningConcept, error) {
	if db == nil {
		return nil, fmt.Errorf("graphflow: learning: nil db")
	}
	g, err := loadLearningGraph(ctx, db)
	if err != nil {
		return nil, err
	}
	knownSet := make(map[string]struct{})
	if known == nil {
		for k, c := range g.concepts {
			if c.Mastered {
				knownSet[k] = struct{}{}
			}
		}
	} else {
		for _, k := range known {
			if key, ok := g.resolve(k); ok {
				knownSet[key] = struct{}{}
			}
		}
	}

	candidates := make([]string, 0)
	for k := range g.concepts {
		if _, isKnown := knownSet[k]; isKnown {
			continue
		}
		ready := true
		for _, pre := range g.requires[k] {
			if _, ok := knownSet[pre]; !ok {
				ready = false
				break
			}
		}
		if ready {
			candidates = append(candidates, k)
		}
	}
	g.sortKeys(candidates)
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]LearningConcept, 0, len(candidates))
	for _, k := range candidates {
		out = append(out, g.concepts[k])
	}
	return out, nil
}

// MissingPrerequisites returns the concepts in `target`'s prerequisite closure
// that are not yet mastered — the direct answer to "why am I stuck on this?".
// When `known` is nil the mastered set comes from the graph.
func MissingPrerequisites(ctx context.Context, db *cortexdb.DB, target string, known []string) ([]LearningConcept, error) {
	path, err := LearningPath(ctx, db, target, known)
	if err != nil {
		return nil, err
	}
	out := make([]LearningConcept, 0, len(path.Steps))
	for _, s := range path.Steps {
		if !strings.EqualFold(s.Name, path.Target) {
			out = append(out, s)
		}
	}
	return out, nil
}

// MarkMastered records that the learner has mastered the given concepts, by
// stamping `mastered_at` on their graph nodes. Unknown concept names are
// reported back so a caller can surface a typo rather than silently no-op.
func MarkMastered(ctx context.Context, db *cortexdb.DB, concepts []string, at time.Time) (marked []string, unknown []string, err error) {
	if db == nil {
		return nil, nil, fmt.Errorf("graphflow: learning: nil db")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	stamp := at.UTC().Format(time.RFC3339)
	for _, name := range concepts {
		name = collapseSpaces(name)
		if name == "" {
			continue
		}
		id := cortexdb.EntityNodeID(name)
		node, gerr := db.Graph().GetNode(ctx, id)
		if gerr != nil || node == nil {
			unknown = append(unknown, name)
			continue
		}
		if node.Properties == nil {
			node.Properties = map[string]interface{}{}
		}
		node.Properties[masteredKey] = stamp
		if uerr := db.Graph().UpsertNode(ctx, node); uerr != nil {
			return marked, unknown, fmt.Errorf("graphflow: learning mark mastered %q: %w", name, uerr)
		}
		marked = append(marked, name)
	}
	return marked, unknown, nil
}

// --- internal graph model ---

// learningGraph is the in-memory view used by the queries: concepts keyed by
// lowercased name, plus prerequisite adjacency (concept -> its prerequisites).
type learningGraph struct {
	concepts map[string]LearningConcept // key: lower(name)
	requires map[string][]string        // key -> prerequisite keys
}

// resolve maps a user-supplied concept name to its key, case-insensitively.
func (g *learningGraph) resolve(name string) (string, bool) {
	key := strings.ToLower(collapseSpaces(name))
	if _, ok := g.concepts[key]; ok {
		return key, true
	}
	return "", false
}

// sortKeys orders concept keys by difficulty then name, so output is stable and
// easier material comes first among equally-ready concepts.
func (g *learningGraph) sortKeys(keys []string) {
	sort.SliceStable(keys, func(i, j int) bool {
		a, b := g.concepts[keys[i]], g.concepts[keys[j]]
		if a.Difficulty != b.Difficulty {
			// Unset difficulty (0) sorts with the easiest.
			return a.Difficulty < b.Difficulty
		}
		return a.Name < b.Name
	})
}

// loadLearningGraph reads concepts (entity nodes) and prerequisite edges.
func loadLearningGraph(ctx context.Context, db *cortexdb.DB) (*learningGraph, error) {
	if err := db.Graph().InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("graphflow: learning: init graph schema: %w", err)
	}
	g := &learningGraph{
		concepts: make(map[string]LearningConcept),
		requires: make(map[string][]string),
	}

	rows, err := db.SQL().QueryContext(ctx,
		`SELECT id, COALESCE(content,''), COALESCE(node_type,''), COALESCE(properties,'')
		 FROM graph_nodes WHERE id LIKE 'entity:%'`)
	if err != nil {
		return nil, fmt.Errorf("graphflow: learning: load concepts: %w", err)
	}
	idToKey := make(map[string]string)
	for rows.Next() {
		var id, content, ntype, props string
		if err := rows.Scan(&id, &content, &ntype, &props); err != nil {
			_ = rows.Close()
			return nil, err
		}
		name := strings.TrimSpace(content)
		if name == "" {
			name = trimEntityPrefix(id)
		}
		key := strings.ToLower(name)
		idToKey[id] = key
		g.concepts[key] = LearningConcept{
			Name:       name,
			Type:       ntype,
			Subject:    jsonStringField(props, "subject"),
			Difficulty: jsonIntField(props, "difficulty"),
			Mastered:   jsonStringField(props, masteredKey) != "",
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	edges, err := db.SQL().QueryContext(ctx,
		`SELECT from_node_id, to_node_id FROM graph_edges WHERE edge_type = ?`, RelRequires)
	if err != nil {
		return nil, fmt.Errorf("graphflow: learning: load prerequisites: %w", err)
	}
	defer func() { _ = edges.Close() }()
	for edges.Next() {
		var from, to string
		if err := edges.Scan(&from, &to); err != nil {
			return nil, err
		}
		fk, ok1 := idToKey[from]
		tk, ok2 := idToKey[to]
		if !ok1 || !ok2 || fk == tk {
			continue
		}
		if !containsString(g.requires[fk], tk) {
			g.requires[fk] = append(g.requires[fk], tk)
		}
	}
	return g, edges.Err()
}

// jsonStringField pulls a top-level string field out of a properties JSON blob
// without a full unmarshal target (properties are free-form).
func jsonStringField(props, field string) string {
	if props == "" {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(props), &m); err != nil {
		return ""
	}
	if v, ok := m[field].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func jsonIntField(props, field string) int {
	s := jsonStringField(props, field)
	if s == "" {
		if props == "" {
			return 0
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(props), &m); err == nil {
			if f, ok := m[field].(float64); ok {
				return int(f)
			}
		}
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// normalizeConceptType constrains an extracted type to the learning vocabulary.
func normalizeConceptType(t string) string {
	t = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(t)), " ", "_")
	if t == "" || !allowedConceptTypes[t] {
		return "concept"
	}
	return t
}

// normalizeLearningRelation constrains an extracted relation to the learning
// vocabulary, defaulting to the prerequisite edge (the one that matters).
func normalizeLearningRelation(t string) string {
	switch strings.ReplaceAll(strings.ToLower(strings.TrimSpace(t)), " ", "_") {
	case RelPartOf:
		return RelPartOf
	case RelExampleOf:
		return RelExampleOf
	case RelApplies:
		return RelApplies
	case "", RelRequires, "prerequisite", "depends_on", "needs":
		return RelRequires
	default:
		return RelRequires
	}
}

func firstNonEmptyLearn(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
