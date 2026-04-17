package graph

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const (
	rdfTypeIRI              = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
	rdfPropertyIRI          = "http://www.w3.org/1999/02/22-rdf-syntax-ns#Property"
	rdfsSubClassOfIRI       = "http://www.w3.org/2000/01/rdf-schema#subClassOf"
	rdfsSubPropertyOfIRI    = "http://www.w3.org/2000/01/rdf-schema#subPropertyOf"
	rdfsClassIRI            = "http://www.w3.org/2000/01/rdf-schema#Class"
	rdfsDomainIRI           = "http://www.w3.org/2000/01/rdf-schema#domain"
	rdfsRangeIRI            = "http://www.w3.org/2000/01/rdf-schema#range"
	rdfsRuleSubClass        = "rdfs_subclass_transitive"
	rdfsRuleSubProperty     = "rdfs_subproperty_transitive"
	rdfsRuleTypeSubClass    = "rdfs_type_via_subclass"
	rdfsRuleDomain          = "rdfs_domain"
	rdfsRuleRange           = "rdfs_range"
	rdfsRuleSubPropertyUse  = "rdfs_subproperty_application"
	rdfsRuleSubclassClass   = "rdfs_subclass_declares_class"
	rdfsRuleSubpropProperty = "rdfs_subproperty_declares_property"
	rdfsRuleDomainSchema    = "rdfs_domain_declares_schema"
	rdfsRuleRangeSchema     = "rdfs_range_declares_schema"
	rdfsRuleClassReflexive  = "rdfs_class_reflexive"
	rdfsRulePropReflexive   = "rdfs_property_reflexive"
)

// RDFSInferenceRefreshResult summarizes a refresh of inferred triples.
type RDFSInferenceRefreshResult struct {
	ExplicitCount         int  `json:"explicit_count"`
	InferredCount         int  `json:"inferred_count"`
	Incremental           bool `json:"incremental,omitempty"`
	AffectedExplicitCount int  `json:"affected_explicit_count,omitempty"`
	RemovedInferredCount  int  `json:"removed_inferred_count,omitempty"`
}

// RDFSInferenceSummary provides persisted inference counts and rule breakdowns.
type RDFSInferenceSummary struct {
	ExplicitCount int            `json:"explicit_count"`
	InferredCount int            `json:"inferred_count"`
	Rules         map[string]int `json:"rules,omitempty"`
}

// RDFSInferenceExplanation returns provenance information for one triple.
type RDFSInferenceExplanation struct {
	Triple           RDFTriple `json:"triple"`
	Explicit         bool      `json:"explicit"`
	Rule             string    `json:"rule,omitempty"`
	SupportTripleIDs []string  `json:"support_triple_ids,omitempty"`
}

// RDFSInferenceTraceEntry is one flattened node in an explanation trace.
type RDFSInferenceTraceEntry struct {
	TripleID       string                   `json:"triple_id"`
	ParentTripleID string                   `json:"parent_triple_id,omitempty"`
	Depth          int                      `json:"depth"`
	Explanation    RDFSInferenceExplanation `json:"explanation"`
	Truncated      bool                     `json:"truncated,omitempty"`
}

// RDFSInferenceMatchExplanation combines explanation and optional trace for one matched triple.
type RDFSInferenceMatchExplanation struct {
	Explanation RDFSInferenceExplanation  `json:"explanation"`
	Trace       []RDFSInferenceTraceEntry `json:"trace,omitempty"`
}

type rdfsInferenceRecord struct {
	Triple     RDFTriple
	Explicit   bool
	Rule       string
	SupportIDs []string
}

// RefreshRDFSInferences recomputes and persists inferred triples using an RDFS-lite ruleset.
func (g *GraphStore) RefreshRDFSInferences(ctx context.Context) (*RDFSInferenceRefreshResult, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	if err := g.clearInferredTriples(ctx); err != nil {
		return nil, err
	}

	explicitOnly := false
	explicitTriples, err := g.FindTriples(ctx, TriplePattern{Inferred: &explicitOnly})
	if err != nil {
		return nil, err
	}
	records := computeRDFSInferenceRecords(explicitTriples)
	inferredCount, err := g.persistInferredRecords(ctx, records)
	if err != nil {
		return nil, err
	}

	return &RDFSInferenceRefreshResult{
		ExplicitCount:         len(explicitTriples),
		InferredCount:         inferredCount,
		AffectedExplicitCount: len(explicitTriples),
	}, nil
}

// RefreshRDFSInferencesIncremental recomputes inferred triples only for the neighborhood
// affected by the supplied changed explicit triples.
func (g *GraphStore) RefreshRDFSInferencesIncremental(ctx context.Context, changedTriples []RDFTriple) (*RDFSInferenceRefreshResult, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	if len(changedTriples) == 0 {
		return g.RefreshRDFSInferences(ctx)
	}

	explicitOnly := false
	explicitTriples, err := g.FindTriples(ctx, TriplePattern{Inferred: &explicitOnly})
	if err != nil {
		return nil, err
	}
	inferredOnly := true
	inferredTriples, err := g.FindTriples(ctx, TriplePattern{Inferred: &inferredOnly})
	if err != nil {
		return nil, err
	}

	normalizedSeeds, err := g.normalizeInferenceSeedTriples(ctx, changedTriples)
	if err != nil {
		return nil, err
	}
	affectedExplicit := expandRDFSExplicitNeighborhood(explicitTriples, normalizedSeeds)
	impactedInferredIDs := collectImpactedInferredTripleIDs(inferredTriples, normalizedSeeds, affectedExplicit)

	removed, err := g.deleteInferredTriplesByID(ctx, impactedInferredIDs)
	if err != nil {
		return nil, err
	}

	records := computeRDFSInferenceRecords(affectedExplicit)
	inferredCount, err := g.persistInferredRecords(ctx, records)
	if err != nil {
		return nil, err
	}

	return &RDFSInferenceRefreshResult{
		ExplicitCount:         len(explicitTriples),
		InferredCount:         inferredCount,
		Incremental:           true,
		AffectedExplicitCount: len(affectedExplicit),
		RemovedInferredCount:  removed,
	}, nil
}

// ExplainTriple returns whether a triple is explicit or inferred and its immediate provenance.
func (g *GraphStore) ExplainTriple(ctx context.Context, tripleID string) (*RDFSInferenceExplanation, error) {
	triple, err := g.GetTriple(ctx, tripleID)
	if err != nil {
		return nil, err
	}
	return &RDFSInferenceExplanation{
		Triple:           *triple,
		Explicit:         !triple.Inferred,
		Rule:             triple.Rule,
		SupportTripleIDs: append([]string(nil), triple.SupportIDs...),
	}, nil
}

// InferenceSummary returns explicit/inferred counts and an inference-rule breakdown.
func (g *GraphStore) InferenceSummary(ctx context.Context) (*RDFSInferenceSummary, error) {
	explicitOnly := false
	explicitTriples, err := g.FindTriples(ctx, TriplePattern{Inferred: &explicitOnly})
	if err != nil {
		return nil, err
	}
	inferredOnly := true
	inferredTriples, err := g.FindTriples(ctx, TriplePattern{Inferred: &inferredOnly})
	if err != nil {
		return nil, err
	}
	rules := make(map[string]int)
	for _, triple := range inferredTriples {
		if strings.TrimSpace(triple.Rule) == "" {
			continue
		}
		rules[triple.Rule]++
	}
	return &RDFSInferenceSummary{
		ExplicitCount: len(explicitTriples),
		InferredCount: len(inferredTriples),
		Rules:         rules,
	}, nil
}

// ExplainTriplesByPattern expands explanations for all triples matched by the given pattern.
func (g *GraphStore) ExplainTriplesByPattern(ctx context.Context, pattern TriplePattern, depth int) ([]RDFSInferenceMatchExplanation, error) {
	triples, err := g.FindTriples(ctx, pattern)
	if err != nil {
		return nil, err
	}
	out := make([]RDFSInferenceMatchExplanation, 0, len(triples))
	for _, triple := range triples {
		explanation, err := g.ExplainTriple(ctx, triple.ID)
		if err != nil {
			return nil, err
		}
		item := RDFSInferenceMatchExplanation{Explanation: *explanation}
		if depth > 0 {
			trace, err := g.ExplainTripleTrace(ctx, triple.ID, depth)
			if err != nil {
				return nil, err
			}
			item.Trace = trace
		}
		out = append(out, item)
	}
	return out, nil
}

// ExplainTripleTrace recursively expands provenance for a triple into a flattened trace list.
func (g *GraphStore) ExplainTripleTrace(ctx context.Context, tripleID string, depth int) ([]RDFSInferenceTraceEntry, error) {
	if depth < 0 {
		depth = 0
	}
	seen := make(map[string]bool)
	entries := make([]RDFSInferenceTraceEntry, 0)
	if err := g.explainTripleTrace(ctx, tripleID, "", 0, depth, seen, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (g *GraphStore) explainTripleTrace(ctx context.Context, tripleID, parentTripleID string, currentDepth, remainingDepth int, seen map[string]bool, entries *[]RDFSInferenceTraceEntry) error {
	explanation, err := g.ExplainTriple(ctx, tripleID)
	if err != nil {
		return err
	}
	entry := RDFSInferenceTraceEntry{
		TripleID:       tripleID,
		ParentTripleID: parentTripleID,
		Depth:          currentDepth,
		Explanation:    *explanation,
	}
	*entries = append(*entries, entry)
	if explanation.Explicit {
		return nil
	}
	if remainingDepth == 0 {
		if len(explanation.SupportTripleIDs) > 0 {
			(*entries)[len(*entries)-1].Truncated = true
		}
		return nil
	}
	if seen[tripleID] {
		(*entries)[len(*entries)-1].Truncated = true
		return nil
	}
	seen[tripleID] = true
	defer delete(seen, tripleID)

	for _, supportID := range explanation.SupportTripleIDs {
		if err := g.explainTripleTrace(ctx, supportID, tripleID, currentDepth+1, remainingDepth-1, seen, entries); err != nil {
			return err
		}
	}
	return nil
}

func computeRDFSInferenceRecords(explicitTriples []RDFTriple) map[string]rdfsInferenceRecord {
	records := make(map[string]rdfsInferenceRecord, len(explicitTriples))
	for _, triple := range explicitTriples {
		record := rdfsInferenceRecord{
			Triple:   tripleWithoutInference(triple),
			Explicit: true,
		}
		records[tripleKey(record.Triple)] = record
	}

	changed := true
	for changed {
		changed = false
		snapshot := make([]rdfsInferenceRecord, 0, len(records))
		for _, record := range records {
			snapshot = append(snapshot, record)
		}

		for _, recordA := range snapshot {
			a := recordA.Triple
			if a.Predicate.Value == rdfsSubClassOfIRI {
				for _, recordB := range snapshot {
					b := recordB.Triple
					if b.Predicate.Value != rdfsSubClassOfIRI || !termsEqual(a.Object, b.Subject) {
						continue
					}
					graphTerm := mergeInferenceGraph(a.Graph, b.Graph)
					changed = addInferredRecord(records, RDFTriple{
						Subject:   a.Subject,
						Predicate: NewIRI(rdfsSubClassOfIRI),
						Object:    b.Object,
						Graph:     graphTerm,
					}, rdfsRuleSubClass, supportPair(recordA, recordB)) || changed
				}
			}
			if a.Predicate.Value == rdfsSubPropertyOfIRI {
				for _, recordB := range snapshot {
					b := recordB.Triple
					if b.Predicate.Value != rdfsSubPropertyOfIRI || !termsEqual(a.Object, b.Subject) {
						continue
					}
					graphTerm := mergeInferenceGraph(a.Graph, b.Graph)
					changed = addInferredRecord(records, RDFTriple{
						Subject:   a.Subject,
						Predicate: NewIRI(rdfsSubPropertyOfIRI),
						Object:    b.Object,
						Graph:     graphTerm,
					}, rdfsRuleSubProperty, supportPair(recordA, recordB)) || changed
				}
			}
		}

		for _, record := range snapshot {
			triple := record.Triple

			switch triple.Predicate.Value {
			case rdfsSubClassOfIRI:
				classTerm := NewIRI(rdfsClassIRI)
				changed = addInferredRecord(records, RDFTriple{
					Subject:   triple.Subject,
					Predicate: NewIRI(rdfTypeIRI),
					Object:    classTerm,
					Graph:     cloneGraphTerm(triple.Graph),
				}, rdfsRuleSubclassClass, supportSingle(record)) || changed
				changed = addInferredRecord(records, RDFTriple{
					Subject:   triple.Object,
					Predicate: NewIRI(rdfTypeIRI),
					Object:    classTerm,
					Graph:     cloneGraphTerm(triple.Graph),
				}, rdfsRuleSubclassClass, supportSingle(record)) || changed
			case rdfsSubPropertyOfIRI:
				propertyTerm := NewIRI(rdfPropertyIRI)
				changed = addInferredRecord(records, RDFTriple{
					Subject:   triple.Subject,
					Predicate: NewIRI(rdfTypeIRI),
					Object:    propertyTerm,
					Graph:     cloneGraphTerm(triple.Graph),
				}, rdfsRuleSubpropProperty, supportSingle(record)) || changed
				changed = addInferredRecord(records, RDFTriple{
					Subject:   triple.Object,
					Predicate: NewIRI(rdfTypeIRI),
					Object:    propertyTerm,
					Graph:     cloneGraphTerm(triple.Graph),
				}, rdfsRuleSubpropProperty, supportSingle(record)) || changed
			case rdfsDomainIRI:
				propertyTerm := NewIRI(rdfPropertyIRI)
				classTerm := NewIRI(rdfsClassIRI)
				changed = addInferredRecord(records, RDFTriple{
					Subject:   triple.Subject,
					Predicate: NewIRI(rdfTypeIRI),
					Object:    propertyTerm,
					Graph:     cloneGraphTerm(triple.Graph),
				}, rdfsRuleDomainSchema, supportSingle(record)) || changed
				changed = addInferredRecord(records, RDFTriple{
					Subject:   triple.Object,
					Predicate: NewIRI(rdfTypeIRI),
					Object:    classTerm,
					Graph:     cloneGraphTerm(triple.Graph),
				}, rdfsRuleDomainSchema, supportSingle(record)) || changed
			case rdfsRangeIRI:
				propertyTerm := NewIRI(rdfPropertyIRI)
				classTerm := NewIRI(rdfsClassIRI)
				changed = addInferredRecord(records, RDFTriple{
					Subject:   triple.Subject,
					Predicate: NewIRI(rdfTypeIRI),
					Object:    propertyTerm,
					Graph:     cloneGraphTerm(triple.Graph),
				}, rdfsRuleRangeSchema, supportSingle(record)) || changed
				changed = addInferredRecord(records, RDFTriple{
					Subject:   triple.Object,
					Predicate: NewIRI(rdfTypeIRI),
					Object:    classTerm,
					Graph:     cloneGraphTerm(triple.Graph),
				}, rdfsRuleRangeSchema, supportSingle(record)) || changed
			}

			if triple.Predicate.Value == rdfTypeIRI {
				if triple.Object.Kind == RDFTermIRI && triple.Object.Value == rdfsClassIRI {
					changed = addInferredRecord(records, RDFTriple{
						Subject:   triple.Subject,
						Predicate: NewIRI(rdfsSubClassOfIRI),
						Object:    triple.Subject,
						Graph:     cloneGraphTerm(triple.Graph),
					}, rdfsRuleClassReflexive, supportSingle(record)) || changed
				}
				if triple.Object.Kind == RDFTermIRI && triple.Object.Value == rdfPropertyIRI {
					changed = addInferredRecord(records, RDFTriple{
						Subject:   triple.Subject,
						Predicate: NewIRI(rdfsSubPropertyOfIRI),
						Object:    triple.Subject,
						Graph:     cloneGraphTerm(triple.Graph),
					}, rdfsRulePropReflexive, supportSingle(record)) || changed
				}
				for _, schema := range snapshot {
					if schema.Triple.Predicate.Value != rdfsSubClassOfIRI || !termsEqual(triple.Object, schema.Triple.Subject) {
						continue
					}
					graphTerm := preferInferenceGraph(triple.Graph, schema.Triple.Graph)
					changed = addInferredRecord(records, RDFTriple{
						Subject:   triple.Subject,
						Predicate: NewIRI(rdfTypeIRI),
						Object:    schema.Triple.Object,
						Graph:     graphTerm,
					}, rdfsRuleTypeSubClass, supportPair(record, schema)) || changed
				}
			}

			for _, schema := range snapshot {
				switch schema.Triple.Predicate.Value {
				case rdfsSubPropertyOfIRI:
					if termsEqual(triple.Predicate, schema.Triple.Subject) {
						graphTerm := preferInferenceGraph(triple.Graph, schema.Triple.Graph)
						changed = addInferredRecord(records, RDFTriple{
							Subject:   triple.Subject,
							Predicate: schema.Triple.Object,
							Object:    triple.Object,
							Graph:     graphTerm,
						}, rdfsRuleSubPropertyUse, supportPair(record, schema)) || changed
					}
				case rdfsDomainIRI:
					if termsEqual(triple.Predicate, schema.Triple.Subject) {
						graphTerm := preferInferenceGraph(triple.Graph, schema.Triple.Graph)
						changed = addInferredRecord(records, RDFTriple{
							Subject:   triple.Subject,
							Predicate: NewIRI(rdfTypeIRI),
							Object:    schema.Triple.Object,
							Graph:     graphTerm,
						}, rdfsRuleDomain, supportPair(record, schema)) || changed
					}
				case rdfsRangeIRI:
					if termsEqual(triple.Predicate, schema.Triple.Subject) && triple.Object.Kind != RDFTermLiteral {
						graphTerm := preferInferenceGraph(triple.Graph, schema.Triple.Graph)
						changed = addInferredRecord(records, RDFTriple{
							Subject:   triple.Object,
							Predicate: NewIRI(rdfTypeIRI),
							Object:    schema.Triple.Object,
							Graph:     graphTerm,
						}, rdfsRuleRange, supportPair(record, schema)) || changed
					}
				}
			}
		}
	}

	return records
}

func (g *GraphStore) persistInferredRecords(ctx context.Context, records map[string]rdfsInferenceRecord) (int, error) {
	inferredTriples := make([]*RDFTriple, 0, len(records))
	for _, record := range records {
		if record.Explicit {
			continue
		}
		inferredTriple := record.Triple
		inferredTriple.Inferred = true
		inferredTriple.Rule = record.Rule
		inferredTriple.SupportIDs = append([]string(nil), record.SupportIDs...)
		tripleCopy := inferredTriple
		inferredTriples = append(inferredTriples, &tripleCopy)
	}
	if len(inferredTriples) == 0 {
		return 0, nil
	}

	result, err := g.UpsertTriplesBatch(ctx, inferredTriples)
	if err != nil {
		return 0, err
	}
	if result.FailedCount > 0 {
		if len(result.Errors) > 0 {
			return result.SuccessCount, result.Errors[0]
		}
		return result.SuccessCount, fmt.Errorf("failed to persist %d inferred triples", result.FailedCount)
	}
	return result.SuccessCount, nil
}

func (g *GraphStore) normalizeInferenceSeedTriples(ctx context.Context, triples []RDFTriple) ([]RDFTriple, error) {
	out := make([]RDFTriple, 0, len(triples))
	for _, triple := range triples {
		normalized, err := g.normalizeTriple(ctx, tripleWithoutInference(triple))
		if err != nil {
			return nil, err
		}
		if normalized.ID == "" {
			normalized.ID = tripleDigest(normalized)
		}
		out = append(out, normalized)
	}
	return out, nil
}

func expandRDFSExplicitNeighborhood(explicitTriples, seeds []RDFTriple) []RDFTriple {
	if len(seeds) == 0 || len(explicitTriples) == 0 {
		return nil
	}
	impactedTerms := make(map[string]struct{})
	for _, triple := range seeds {
		addInferenceNeighborhoodSeedTerms(impactedTerms, triple)
	}

	selected := make(map[string]RDFTriple)
	changed := true
	for changed {
		changed = false
		for _, triple := range explicitTriples {
			key := tripleKey(triple)
			if _, ok := selected[key]; ok {
				continue
			}
			if !tripleTouchesInferenceNeighborhood(triple, impactedTerms) {
				continue
			}
			selected[key] = triple
			if addInferenceNeighborhoodTerms(impactedTerms, triple) {
				changed = true
			}
		}
	}

	out := make([]RDFTriple, 0, len(selected))
	for _, triple := range selected {
		out = append(out, triple)
	}
	sort.Slice(out, func(i, j int) bool {
		return tripleKey(out[i]) < tripleKey(out[j])
	})
	return out
}

func collectImpactedInferredTripleIDs(inferredTriples, seeds, affectedExplicit []RDFTriple) []string {
	if len(inferredTriples) == 0 || (len(seeds) == 0 && len(affectedExplicit) == 0) {
		return nil
	}
	reverse := make(map[string][]string)
	for _, triple := range inferredTriples {
		for _, supportID := range triple.SupportIDs {
			reverse[supportID] = append(reverse[supportID], triple.ID)
		}
	}

	seen := make(map[string]struct{})
	queue := make([]string, 0, len(seeds))
	for _, triple := range seeds {
		if triple.ID == "" {
			continue
		}
		queue = append(queue, triple.ID)
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, dependentID := range reverse[current] {
			if _, ok := seen[dependentID]; ok {
				continue
			}
			seen[dependentID] = struct{}{}
			queue = append(queue, dependentID)
		}
	}

	impactedTerms := make(map[string]struct{})
	for _, triple := range seeds {
		addInferenceNeighborhoodSeedTerms(impactedTerms, triple)
	}
	for _, triple := range affectedExplicit {
		addInferenceNeighborhoodTerms(impactedTerms, triple)
	}
	for _, triple := range inferredTriples {
		if !tripleTouchesInferenceNeighborhood(triple, impactedTerms) {
			continue
		}
		seen[triple.ID] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func addInferenceNeighborhoodSeedTerms(target map[string]struct{}, triple RDFTriple) {
	addInferenceTerm(target, triple.Subject)
	addInferenceTerm(target, triple.Object)
	if triple.Graph != nil {
		addInferenceTerm(target, *triple.Graph)
	}
	if !isStructuralInferencePredicate(triple.Predicate.Value) {
		addInferenceTerm(target, triple.Predicate)
	}
}

func addInferenceNeighborhoodTerms(target map[string]struct{}, triple RDFTriple) bool {
	before := len(target)
	addInferenceTerm(target, triple.Subject)
	addInferenceTerm(target, triple.Object)
	if triple.Graph != nil {
		addInferenceTerm(target, *triple.Graph)
	}
	if !isStructuralInferencePredicate(triple.Predicate.Value) {
		addInferenceTerm(target, triple.Predicate)
	}
	return len(target) != before
}

func addInferenceTerm(target map[string]struct{}, term RDFTerm) {
	target[inferenceTermKey(term)] = struct{}{}
}

func tripleTouchesInferenceNeighborhood(triple RDFTriple, impactedTerms map[string]struct{}) bool {
	if _, ok := impactedTerms[inferenceTermKey(triple.Subject)]; ok {
		return true
	}
	if _, ok := impactedTerms[inferenceTermKey(triple.Object)]; ok {
		return true
	}
	if triple.Graph != nil {
		if _, ok := impactedTerms[inferenceTermKey(*triple.Graph)]; ok {
			return true
		}
	}
	if !isStructuralInferencePredicate(triple.Predicate.Value) {
		if _, ok := impactedTerms[inferenceTermKey(triple.Predicate)]; ok {
			return true
		}
	}
	return false
}

func inferenceTermKey(term RDFTerm) string {
	return term.Kind + "|" + term.Value + "|" + term.Datatype + "|" + term.Language
}

func isStructuralInferencePredicate(value string) bool {
	switch value {
	case rdfTypeIRI, rdfsSubClassOfIRI, rdfsSubPropertyOfIRI, rdfsDomainIRI, rdfsRangeIRI:
		return true
	default:
		return false
	}
}

func (g *GraphStore) deleteInferredTriplesByID(ctx context.Context, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	ids = uniqueSortedStrings(ids)
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin delete inferred transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	deleted := 0
	for _, chunk := range chunkStrings(ids, 900) {
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")

		result, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM graph_edges WHERE id IN (%s)`, placeholders), args...)
		if err != nil {
			return deleted, fmt.Errorf("delete inferred graph edge: %w", err)
		}
		if _, err := result.RowsAffected(); err != nil {
			return deleted, fmt.Errorf("count inferred graph edge delete: %w", err)
		}

		result, err = tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM kg_triples WHERE inferred = 1 AND id IN (%s)`, placeholders), args...)
		if err != nil {
			return deleted, fmt.Errorf("delete inferred triple: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return deleted, fmt.Errorf("count inferred triple delete: %w", err)
		}
		deleted += int(rows)
	}
	if err := tx.Commit(); err != nil {
		return deleted, fmt.Errorf("commit delete inferred transaction: %w", err)
	}
	return deleted, nil
}

func chunkStrings(values []string, size int) [][]string {
	if len(values) == 0 {
		return nil
	}
	if size <= 0 {
		size = len(values)
	}
	chunks := make([][]string, 0, (len(values)+size-1)/size)
	for start := 0; start < len(values); start += size {
		end := start + size
		if end > len(values) {
			end = len(values)
		}
		chunks = append(chunks, values[start:end])
	}
	return chunks
}

func addInferredRecord(records map[string]rdfsInferenceRecord, triple RDFTriple, rule string, supportIDs []string) bool {
	triple = tripleWithoutInference(triple)
	key := tripleKey(triple)
	if existing, ok := records[key]; ok {
		if existing.Explicit {
			return false
		}
		return false
	}
	records[key] = rdfsInferenceRecord{
		Triple:     triple,
		Explicit:   false,
		Rule:       rule,
		SupportIDs: uniqueSortedStrings(supportIDs),
	}
	return true
}

func tripleWithoutInference(triple RDFTriple) RDFTriple {
	return RDFTriple{
		ID:        triple.ID,
		Subject:   triple.Subject,
		Predicate: triple.Predicate,
		Object:    triple.Object,
		Graph:     cloneGraphTerm(triple.Graph),
	}
}

func cloneGraphTerm(term *RDFTerm) *RDFTerm {
	if term == nil {
		return nil
	}
	out := *term
	return &out
}

func tripleKey(triple RDFTriple) string {
	clone := tripleWithoutInference(triple)
	if clone.ID != "" {
		return clone.ID
	}
	return tripleDigest(clone)
}

func supportPair(left, right rdfsInferenceRecord) []string {
	ids := make([]string, 0, 4)
	if left.Triple.ID != "" {
		ids = append(ids, left.Triple.ID)
	}
	if right.Triple.ID != "" {
		ids = append(ids, right.Triple.ID)
	}
	return ids
}

func supportSingle(record rdfsInferenceRecord) []string {
	if record.Triple.ID == "" {
		return nil
	}
	return []string{record.Triple.ID}
}

func mergeInferenceGraph(left, right *RDFTerm) *RDFTerm {
	switch {
	case left == nil && right == nil:
		return nil
	case left == nil:
		return cloneGraphTerm(right)
	case right == nil:
		return cloneGraphTerm(left)
	case termsEqual(*left, *right):
		return cloneGraphTerm(left)
	default:
		return nil
	}
}

func preferInferenceGraph(primary, fallback *RDFTerm) *RDFTerm {
	if primary != nil {
		return cloneGraphTerm(primary)
	}
	return cloneGraphTerm(fallback)
}

func (g *GraphStore) clearInferredTriples(ctx context.Context) error {
	if err := g.InitGraphSchema(ctx); err != nil {
		return err
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin clear inferred transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM graph_edges WHERE id IN (SELECT id FROM kg_triples WHERE inferred = 1)`); err != nil {
		return fmt.Errorf("delete inferred graph edges: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM kg_triples WHERE inferred = 1`); err != nil {
		return fmt.Errorf("delete inferred triples: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit clear inferred transaction: %w", err)
	}
	return nil
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
