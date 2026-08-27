package cortexdb

// Asking the ontology whether a relationship can hold more than one value.
//
// This is what makes automatic supersession possible without guessing. When a
// new fact arrives saying Leo lives in Chengdu and the graph already says Leo
// lives in Beijing, one of them has to stop being true — but only because a
// person lives in one city. A new "knows" edge contradicts nothing; people
// know many people.
//
// Graphiti asks a language model whether two facts conflict. This asks the
// schema, which already carries per-side cardinality because Foundry models
// multiplicity that way. Deterministic, auditable, and free at write time —
// and where the schema does not say, nothing is assumed.

import "context"

// LinkSingleValued reports whether a link type reaches at most one object from
// its subject, and whether the ontology had an opinion at all.
//
// known is false when there is no active schema, or the schema does not
// describe this link type. Callers must treat that as "do not assume": closing
// a fact that was not actually contradicted destroys history, and unlike a
// wrong search result nothing afterwards reveals it.
//
// The subject's object type is not resolved, so the rule is structural: a link
// with one ONE side and one MANY side is single-valued from the ONE side, which
// is the side a fact is written from in every case this serves — Person
// lives_in City, Person works_at Company. A link that is MANY on both sides
// (Person knows Person) is never single-valued, which is the case that matters
// most to get right, because that is the one where closing an old fact would
// be silent data loss.
func (db *DB) LinkSingleValued(ctx context.Context, linkType string) (single bool, known bool) {
	if db == nil || linkType == "" {
		return false, false
	}
	schema, err := db.loadActiveOntologySchema(ctx)
	if err != nil || schema == nil {
		return false, false
	}
	key := ontologyAPIKey(linkType)
	for _, link := range schema.LinkTypes {
		if ontologyAPIKey(link.APIName) != key {
			continue
		}
		aOne := link.A.Cardinality == OntologyCardinalityOne
		bOne := link.B.Cardinality == OntologyCardinalityOne
		// Both MANY is a genuine many-to-many: never single-valued.
		// Anything with a ONE side reaches one object from that side.
		return aOne || bOne, true
	}
	return false, false
}
