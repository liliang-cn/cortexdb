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

import (
	"context"

	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

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
	return schemaLinkSingleValued(schema, linkType)
}

// schemaLinkSingleValued is LinkSingleValued against a schema already in hand,
// so a write that asks about many edges loads the schema once.
func schemaLinkSingleValued(schema *OntologySchema, linkType string) (single bool, known bool) {
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

// schemaSubjectSingleValued is the direction-aware form of the question: does
// a subject of this node type reach at most one object through this link?
//
// A side's Cardinality is how many objects a traversal starting at that side's
// object type reaches, so the answer is the cardinality of the side whose
// object type is the subject's. The structural LinkSingleValued cannot say
// which end a fact is written from; a write that closes data must, because
// Airport departures reach many flights while each flight reaches one airport,
// and closing on the wrong end would destroy every flight but the last.
//
// known is false when the link is not in the schema or the subject's type
// matches neither side, and nothing is closed then. A link between a type and
// itself is single-valued only if both sides say ONE: which side the fact was
// written from cannot be told.
func schemaSubjectSingleValued(schema *OntologySchema, linkType, subjectType string) (single bool, known bool) {
	typeKey := ontologyAPIKey(subjectType)
	if typeKey == "" {
		return false, false
	}
	key := ontologyAPIKey(linkType)
	for _, link := range schema.LinkTypes {
		if ontologyAPIKey(link.APIName) != key {
			continue
		}
		aMatch := ontologyAPIKey(link.A.ObjectTypeAPIName) == typeKey
		bMatch := ontologyAPIKey(link.B.ObjectTypeAPIName) == typeKey
		aOne := link.A.Cardinality == OntologyCardinalityOne
		bOne := link.B.Cardinality == OntologyCardinalityOne
		switch {
		case aMatch && bMatch:
			return aOne && bOne, true
		case aMatch:
			return aOne, true
		case bMatch:
			return bOne, true
		}
		return false, false
	}
	return false, false
}

type supersedingWriteKey struct{}

// withSupersedingWrite marks a write that closes the prior value of a
// single-valued link, so validation does not refuse what the write resolves.
func withSupersedingWrite(ctx context.Context) context.Context {
	return context.WithValue(ctx, supersedingWriteKey{}, true)
}

func supersessionEnabled(ctx context.Context) bool {
	on, _ := ctx.Value(supersedingWriteKey{}).(bool)
	return on
}

// ProducerRelationUpsert is recorded on a fact the ordinary relation upsert
// closed because the active schema says its link holds one value.
const ProducerRelationUpsert = "graphrag.upsert_relations"

// singleValuedSupersession is the rule a relation write applies, built from
// the active schema: nil when there is no schema, so nothing is ever closed on
// a guess. The schema's id and version ride along so the closure records the
// version that decided it.
func (db *DB) singleValuedSupersession(ctx context.Context) *graph.Supersession {
	schema, err := db.loadActiveOntologySchema(ctx)
	if err != nil || schema == nil {
		return nil
	}
	producer := graph.InvalidationFrom(ctx).Producer
	if producer == "" {
		producer = ProducerRelationUpsert
	}
	oneSided := map[string]bool{}
	for _, link := range schema.LinkTypes {
		oneSided[ontologyAPIKey(link.APIName)] = link.A.Cardinality == OntologyCardinalityOne || link.B.Cardinality == OntologyCardinalityOne
	}
	return &graph.Supersession{
		SchemaID:      schema.SchemaID,
		SchemaVersion: schema.Version,
		Producer:      producer,
		Candidate:     func(edgeType string) bool { return oneSided[ontologyAPIKey(edgeType)] },
		SingleValued: func(edgeType, subjectType string) bool {
			single, known := schemaSubjectSingleValued(schema, edgeType, subjectType)
			if !known {
				// Edge types are stored normalised; the schema may spell the
				// same link another way, as verify_claims also allows for.
				single, known = schemaSubjectSingleValued(schema, normalizeOntologyLabel(edgeType), subjectType)
			}
			return known && single
		},
	}
}

// ActiveOntologyRef names the active schema: the id and version a decision made
// now would be recorded under. ok is false when no schema is active.
func (db *DB) ActiveOntologyRef(ctx context.Context) (schemaID string, version int, ok bool) {
	if db == nil {
		return "", 0, false
	}
	schema, err := db.loadActiveOntologySchema(ctx)
	if err != nil || schema == nil {
		return "", 0, false
	}
	return schema.SchemaID, schema.Version, true
}
