package graph

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// How triple terms are stored.
//
// A triple whose object is a triple term is an ordinary kg_triples row:
// object_kind is "triple" and object_value is the triple term's canonical
// N-Triples spelling. The row is self-describing — every reader of
// kg_triples, the exporters and the inference engine included, gets the whole
// term back from the row alone — and the existing (object_kind, object_value)
// index answers the question RDF 1.2 makes common: which reifiers does
// <<( s p o )>> have? That is one indexed probe for
// predicate = rdf:reifies, object = the canonical spelling.
//
// What the row cannot answer through an index is a question about the parts:
// which triple terms have :alice as their subject? kg_triple_terms holds each
// triple term once, keyed by a digest of its spelling, with its subject,
// predicate and object in indexed columns of their own, so a SPARQL pattern
// such as ?r rdf:reifies <<( :alice ?p ?o )>> narrows to the matching terms in
// SQL instead of reading every reifier back and testing it in Go.
//
// The table is derived from kg_triples and only ever consulted joined to it:
// a row left behind after its last referencing triple is deleted (deleteTriple
// removes it, bulk paths such as inference retraction may not) can never make
// a pattern match a triple that is not stored. Creating it is the whole
// migration — no existing row can hold a triple term, because nothing before
// RDF 1.2 support could write one — and CREATE TABLE IF NOT EXISTS is
// idempotent on both backends.

const createTripleTermSchemaSQL = `
	CREATE TABLE IF NOT EXISTS kg_triple_terms (
		id TEXT PRIMARY KEY,
		term TEXT NOT NULL,
		subject_kind TEXT NOT NULL,
		subject_value TEXT NOT NULL,
		predicate_value TEXT NOT NULL,
		object_kind TEXT NOT NULL,
		object_value TEXT NOT NULL,
		object_datatype TEXT,
		object_language TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_kg_triple_terms_subject ON kg_triple_terms(subject_kind, subject_value);
	CREATE INDEX IF NOT EXISTS idx_kg_triple_terms_predicate ON kg_triple_terms(predicate_value);
	CREATE INDEX IF NOT EXISTS idx_kg_triple_terms_object ON kg_triple_terms(object_kind, object_value);
`

// createTripleTermSchema creates kg_triple_terms. Callers hold schemaMu.
func (g *GraphStore) createTripleTermSchema(ctx context.Context) error {
	if _, err := g.db.ExecContext(ctx, createTripleTermSchemaSQL); err != nil {
		return fmt.Errorf("create kg_triple_terms: %w", err)
	}
	return nil
}

// tripleTermFilter narrows a pattern's object to triple terms whose parts
// match. A nil part matches anything. It is how SPARQL hands a partly bound
// <<( ?s :p ?o )>> to the store.
type tripleTermFilter struct {
	Subject   *RDFTerm
	Predicate *RDFTerm
	Object    *RDFTerm
}

// sqlCondition is the WHERE fragment that restricts kg_triples rows to the
// filter, and its arguments.
func (f tripleTermFilter) sqlCondition() (string, []any, error) {
	var conds []string
	var args []any
	if f.Subject != nil {
		if f.Subject.Kind != RDFTermIRI && f.Subject.Kind != RDFTermBlankNode {
			return "1 = 0", nil, nil
		}
		conds = append(conds, "tt.subject_kind = ?", "tt.subject_value = ?")
		args = append(args, f.Subject.Kind, strings.TrimPrefix(f.Subject.Value, "_:"))
	}
	if f.Predicate != nil {
		if f.Predicate.Kind != RDFTermIRI {
			return "1 = 0", nil, nil
		}
		conds = append(conds, "tt.predicate_value = ?")
		args = append(args, f.Predicate.Value)
	}
	if f.Object != nil {
		object, err := tripleTermComponentColumns(*f.Object)
		if err != nil {
			return "", nil, err
		}
		conds = append(conds, "tt.object_kind = ?", "tt.object_value = ?")
		args = append(args, object.Kind, object.Value)
		if object.Kind == RDFTermLiteral {
			conds = append(conds, "COALESCE(tt.object_datatype, '') = ?", "COALESCE(tt.object_language, '') = ?")
			args = append(args, object.Datatype, object.Language)
		}
	}
	if len(conds) == 0 {
		return "object_kind = ?", []any{RDFTermTriple}, nil
	}
	return "object_kind = ? AND object_value IN (SELECT tt.term FROM kg_triple_terms tt WHERE " + strings.Join(conds, " AND ") + ")",
		append([]any{RDFTermTriple}, args...), nil
}

// tripleTermComponentColumns is how one part of a triple term is written to
// kg_triple_terms: as it is spelled inside the canonical form, so a literal
// typed xsd:string and the same literal untyped are one value, as RDF says.
func tripleTermComponentColumns(term RDFTerm) (RDFTerm, error) {
	switch term.Kind {
	case RDFTermTriple:
		return normalizeTripleTerm(term)
	case RDFTermLiteral:
		if term.Datatype == rdf12XSDStringIRI {
			term.Datatype = ""
		}
		term.Language = strings.ToLower(term.Language)
		return term, nil
	case RDFTermBlankNode:
		term.Value = strings.TrimPrefix(term.Value, "_:")
		return term, nil
	default:
		return term, nil
	}
}

// upsertTripleTermRowsTx records a triple term, and every triple term nested
// in it, in kg_triple_terms.
func (g *GraphStore) upsertTripleTermRowsTx(ctx context.Context, tx *sql.Tx, term RDFTerm) error {
	for depth := 0; term.Kind == RDFTermTriple && depth <= maxTripleTermDepth; depth++ {
		triple, err := decodeTripleTermValue(term.Value)
		if err != nil {
			return err
		}
		object, err := tripleTermComponentColumns(triple.Object)
		if err != nil {
			return err
		}
		if _, err := g.txExec(ctx, tx, `
			INSERT INTO kg_triple_terms (
				id, term, subject_kind, subject_value, predicate_value,
				object_kind, object_value, object_datatype, object_language
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO NOTHING
		`,
			tripleTermDigest(term.Value), term.Value,
			triple.Subject.Kind, strings.TrimPrefix(triple.Subject.Value, "_:"),
			triple.Predicate.Value,
			object.Kind, object.Value, nullIfEmpty(object.Datatype), nullIfEmpty(object.Language),
		); err != nil {
			return fmt.Errorf("upsert triple term: %w", err)
		}
		term = object
	}
	return nil
}

// cleanupTripleTermRowsTx removes the kg_triple_terms rows of a triple term
// once no stored triple has it as object and no other triple term nests it.
func (g *GraphStore) cleanupTripleTermRowsTx(ctx context.Context, tx *sql.Tx, term RDFTerm) error {
	for depth := 0; term.Kind == RDFTermTriple && depth <= maxTripleTermDepth; depth++ {
		if _, err := g.txExec(ctx, tx, `
			DELETE FROM kg_triple_terms
			WHERE id = ?
			  AND NOT EXISTS (SELECT 1 FROM kg_triples WHERE object_kind = ? AND object_value = ?)
			  AND NOT EXISTS (SELECT 1 FROM kg_triple_terms nested WHERE nested.object_kind = ? AND nested.object_value = ?)
		`, tripleTermDigest(term.Value), RDFTermTriple, term.Value, RDFTermTriple, term.Value); err != nil {
			return fmt.Errorf("clean up triple term: %w", err)
		}
		triple, err := decodeTripleTermValue(term.Value)
		if err != nil {
			return err
		}
		term = triple.Object
	}
	return nil
}

// FindReifiers returns every reifier of the triple term <<( subject predicate
// object )>>: each r for which r rdf:reifies <<( subject predicate object )>>
// is stored, in any graph. It is one indexed lookup, however many reifying
// triples the store holds for other triple terms.
func (g *GraphStore) FindReifiers(ctx context.Context, subject, predicate, object RDFTerm) ([]RDFTerm, error) {
	namespaces, err := g.ListNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	normalize := func(term RDFTerm, position rdfPosition) (RDFTerm, error) {
		return normalizeTermWithNamespaces(term, position, namespaces)
	}
	s, err := normalize(subject, rdfPositionSubject)
	if err != nil {
		return nil, err
	}
	p, err := normalize(predicate, rdfPositionPredicate)
	if err != nil {
		return nil, err
	}
	o, err := normalize(object, rdfPositionObject)
	if err != nil {
		return nil, err
	}
	tripleTerm, err := NewTripleTerm(s, p, o)
	if err != nil {
		return nil, err
	}
	reifies := NewIRI(RDFReifiesIRI)
	triples, err := g.findStoredTriples(ctx, TriplePattern{Predicate: &reifies, Object: &tripleTerm})
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(triples))
	out := make([]RDFTerm, 0, len(triples))
	for _, triple := range triples {
		key := triple.Subject.Kind + "|" + triple.Subject.Value
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, triple.Subject)
	}
	return out, nil
}

// FindReifiedTriples returns the triple terms a reifier reifies.
func (g *GraphStore) FindReifiedTriples(ctx context.Context, reifier RDFTerm) ([]RDFTerm, error) {
	reifies := NewIRI(RDFReifiesIRI)
	triples, err := g.findStoredTriples(ctx, TriplePattern{Subject: &reifier, Predicate: &reifies})
	if err != nil {
		return nil, err
	}
	out := make([]RDFTerm, 0, len(triples))
	seen := make(map[string]bool, len(triples))
	for _, triple := range triples {
		if triple.Object.Kind != RDFTermTriple || seen[triple.Object.Value] {
			continue
		}
		seen[triple.Object.Value] = true
		out = append(out, triple.Object)
	}
	return out, nil
}
