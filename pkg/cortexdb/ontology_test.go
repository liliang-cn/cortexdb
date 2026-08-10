package cortexdb

import (
	"context"
	"strings"
)

type typedFixtureExtractor struct{}

func (typedFixtureExtractor) Extract(_ context.Context, text string) (*GraphExtraction, error) {
	lower := strings.ToLower(text)
	extraction := &GraphExtraction{}
	if strings.Contains(lower, "alice") {
		extraction.Entities = append(extraction.Entities, GraphEntity{Name: "Alice", Type: "person"})
	}
	if strings.Contains(lower, "acme") {
		extraction.Entities = append(extraction.Entities, GraphEntity{Name: "Acme", Type: "organization"})
	}
	if strings.Contains(lower, "alice works at acme") {
		extraction.Relationships = append(extraction.Relationships, GraphRelationship{
			From: "Alice",
			To:   "Acme",
			Type: "works_at",
		})
	}
	return extraction, nil
}

// TODO(phase2): four v1 ontology tests were removed here when object/relation
// types were replaced by the v2 type system.
//
// Superseded, no action needed: schema-level rejection is now covered by
// ontology_validation_test.go; the explicit deactivate path moved to
// ontology_storage_test.go; and the tool-dispatch round trip that
// TestOntologySchemaAPIAndToolValidation used to provide incidentally is now
// covered directly by ontology_dispatch_test.go.
//
// Still owed, once write-path enforcement lands (plan tasks 7-8):
//
//   - TestOntologySchemaAPIAndToolValidation: required-property and relation
//     direction rejection through tools.UpsertEntities / tools.UpsertRelations.
//   - TestInsertGraphDocumentRespectsActiveOntologySchema: InsertGraphDocument
//     rejecting an extractor whose entity types the active schema does not
//     define. typedFixtureExtractor above is kept for exactly this.
