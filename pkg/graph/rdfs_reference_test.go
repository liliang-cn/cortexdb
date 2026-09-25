package graph

// naiveRDFSInferenceRecords is the inference engine as it was before
// semi-naive evaluation, kept verbatim as a test oracle.
//
// It re-joins every pair of records in every round, which is what made it too
// slow for real graphs and is also what makes it easy to believe: there is no
// index to get wrong and no delta to forget. The semi-naive engine must reach
// the same set of triples on any input, and TestSemiNaiveEvaluationAgreesWith
// TheNaiveEngineOnRandomGraphs holds it to that.
//
// Two known defects are preserved on purpose, because an oracle that has been
// "fixed" is no longer the thing the rewrite is being compared against: rule
// credit depends on map iteration order when a triple is derivable more than
// one way, and inferred records carry no id, so support lists omit inferred
// premises. The comparison therefore looks at triples, not at credit or
// support; checkDerivations judges those on the new engine directly.

func naiveRDFSInferenceRecords(explicitTriples []RDFTriple) map[string]rdfsInferenceRecord {
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
					changed = naiveAddInferredRecord(records, RDFTriple{
						Subject:   a.Subject,
						Predicate: NewIRI(rdfsSubClassOfIRI),
						Object:    b.Object,
						Graph:     graphTerm,
					}, rdfsRuleSubClass, naiveSupportPair(recordA, recordB)) || changed
				}
			}
			if a.Predicate.Value == rdfsSubPropertyOfIRI {
				for _, recordB := range snapshot {
					b := recordB.Triple
					if b.Predicate.Value != rdfsSubPropertyOfIRI || !termsEqual(a.Object, b.Subject) {
						continue
					}
					graphTerm := mergeInferenceGraph(a.Graph, b.Graph)
					changed = naiveAddInferredRecord(records, RDFTriple{
						Subject:   a.Subject,
						Predicate: NewIRI(rdfsSubPropertyOfIRI),
						Object:    b.Object,
						Graph:     graphTerm,
					}, rdfsRuleSubProperty, naiveSupportPair(recordA, recordB)) || changed
				}
			}
		}

		for _, record := range snapshot {
			triple := record.Triple

			switch triple.Predicate.Value {
			case rdfsSubClassOfIRI:
				classTerm := NewIRI(rdfsClassIRI)
				changed = naiveAddInferredRecord(records, RDFTriple{
					Subject:   triple.Subject,
					Predicate: NewIRI(rdfTypeIRI),
					Object:    classTerm,
					Graph:     cloneGraphTerm(triple.Graph),
				}, rdfsRuleSubclassClass, naiveSupportSingle(record)) || changed
				changed = naiveAddInferredRecord(records, RDFTriple{
					Subject:   triple.Object,
					Predicate: NewIRI(rdfTypeIRI),
					Object:    classTerm,
					Graph:     cloneGraphTerm(triple.Graph),
				}, rdfsRuleSubclassClass, naiveSupportSingle(record)) || changed
			case rdfsSubPropertyOfIRI:
				propertyTerm := NewIRI(rdfPropertyIRI)
				changed = naiveAddInferredRecord(records, RDFTriple{
					Subject:   triple.Subject,
					Predicate: NewIRI(rdfTypeIRI),
					Object:    propertyTerm,
					Graph:     cloneGraphTerm(triple.Graph),
				}, rdfsRuleSubpropProperty, naiveSupportSingle(record)) || changed
				changed = naiveAddInferredRecord(records, RDFTriple{
					Subject:   triple.Object,
					Predicate: NewIRI(rdfTypeIRI),
					Object:    propertyTerm,
					Graph:     cloneGraphTerm(triple.Graph),
				}, rdfsRuleSubpropProperty, naiveSupportSingle(record)) || changed
			case rdfsDomainIRI:
				propertyTerm := NewIRI(rdfPropertyIRI)
				classTerm := NewIRI(rdfsClassIRI)
				changed = naiveAddInferredRecord(records, RDFTriple{
					Subject:   triple.Subject,
					Predicate: NewIRI(rdfTypeIRI),
					Object:    propertyTerm,
					Graph:     cloneGraphTerm(triple.Graph),
				}, rdfsRuleDomainSchema, naiveSupportSingle(record)) || changed
				changed = naiveAddInferredRecord(records, RDFTriple{
					Subject:   triple.Object,
					Predicate: NewIRI(rdfTypeIRI),
					Object:    classTerm,
					Graph:     cloneGraphTerm(triple.Graph),
				}, rdfsRuleDomainSchema, naiveSupportSingle(record)) || changed
			case rdfsRangeIRI:
				propertyTerm := NewIRI(rdfPropertyIRI)
				classTerm := NewIRI(rdfsClassIRI)
				changed = naiveAddInferredRecord(records, RDFTriple{
					Subject:   triple.Subject,
					Predicate: NewIRI(rdfTypeIRI),
					Object:    propertyTerm,
					Graph:     cloneGraphTerm(triple.Graph),
				}, rdfsRuleRangeSchema, naiveSupportSingle(record)) || changed
				changed = naiveAddInferredRecord(records, RDFTriple{
					Subject:   triple.Object,
					Predicate: NewIRI(rdfTypeIRI),
					Object:    classTerm,
					Graph:     cloneGraphTerm(triple.Graph),
				}, rdfsRuleRangeSchema, naiveSupportSingle(record)) || changed
			}

			if triple.Predicate.Value == rdfTypeIRI {
				if triple.Object.Kind == RDFTermIRI && triple.Object.Value == rdfsClassIRI {
					changed = naiveAddInferredRecord(records, RDFTriple{
						Subject:   triple.Subject,
						Predicate: NewIRI(rdfsSubClassOfIRI),
						Object:    triple.Subject,
						Graph:     cloneGraphTerm(triple.Graph),
					}, rdfsRuleClassReflexive, naiveSupportSingle(record)) || changed
				}
				if triple.Object.Kind == RDFTermIRI && triple.Object.Value == rdfPropertyIRI {
					changed = naiveAddInferredRecord(records, RDFTriple{
						Subject:   triple.Subject,
						Predicate: NewIRI(rdfsSubPropertyOfIRI),
						Object:    triple.Subject,
						Graph:     cloneGraphTerm(triple.Graph),
					}, rdfsRulePropReflexive, naiveSupportSingle(record)) || changed
				}
				for _, schema := range snapshot {
					if schema.Triple.Predicate.Value != rdfsSubClassOfIRI || !termsEqual(triple.Object, schema.Triple.Subject) {
						continue
					}
					graphTerm := preferInferenceGraph(triple.Graph, schema.Triple.Graph)
					changed = naiveAddInferredRecord(records, RDFTriple{
						Subject:   triple.Subject,
						Predicate: NewIRI(rdfTypeIRI),
						Object:    schema.Triple.Object,
						Graph:     graphTerm,
					}, rdfsRuleTypeSubClass, naiveSupportPair(record, schema)) || changed
				}
			}

			for _, schema := range snapshot {
				switch schema.Triple.Predicate.Value {
				case rdfsSubPropertyOfIRI:
					if termsEqual(triple.Predicate, schema.Triple.Subject) {
						graphTerm := preferInferenceGraph(triple.Graph, schema.Triple.Graph)
						changed = naiveAddInferredRecord(records, RDFTriple{
							Subject:   triple.Subject,
							Predicate: schema.Triple.Object,
							Object:    triple.Object,
							Graph:     graphTerm,
						}, rdfsRuleSubPropertyUse, naiveSupportPair(record, schema)) || changed
					}
				case rdfsDomainIRI:
					if termsEqual(triple.Predicate, schema.Triple.Subject) {
						graphTerm := preferInferenceGraph(triple.Graph, schema.Triple.Graph)
						changed = naiveAddInferredRecord(records, RDFTriple{
							Subject:   triple.Subject,
							Predicate: NewIRI(rdfTypeIRI),
							Object:    schema.Triple.Object,
							Graph:     graphTerm,
						}, rdfsRuleDomain, naiveSupportPair(record, schema)) || changed
					}
				case rdfsRangeIRI:
					if termsEqual(triple.Predicate, schema.Triple.Subject) && triple.Object.Kind != RDFTermLiteral {
						graphTerm := preferInferenceGraph(triple.Graph, schema.Triple.Graph)
						changed = naiveAddInferredRecord(records, RDFTriple{
							Subject:   triple.Object,
							Predicate: NewIRI(rdfTypeIRI),
							Object:    schema.Triple.Object,
							Graph:     graphTerm,
						}, rdfsRuleRange, naiveSupportPair(record, schema)) || changed
					}
				}
			}
		}
	}

	return records
}

func naiveAddInferredRecord(records map[string]rdfsInferenceRecord, triple RDFTriple, rule string, supportIDs []string) bool {
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
func naiveSupportPair(left, right rdfsInferenceRecord) []string {
	ids := make([]string, 0, 4)
	if left.Triple.ID != "" {
		ids = append(ids, left.Triple.ID)
	}
	if right.Triple.ID != "" {
		ids = append(ids, right.Triple.ID)
	}
	return ids
}

func naiveSupportSingle(record rdfsInferenceRecord) []string {
	if record.Triple.ID == "" {
		return nil
	}
	return []string{record.Triple.ID}
}
