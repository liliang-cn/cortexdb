package graph

import (
	"bufio"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/liliang-cn/cortexdb/v2/internal/encoding"
)

const (
	// RDFTermIRI represents an IRI/resource term.
	RDFTermIRI = "iri"
	// RDFTermBlankNode represents a blank node term.
	RDFTermBlankNode = "blank_node"
	// RDFTermLiteral represents a literal term.
	RDFTermLiteral = "literal"
)

const (
	// RDFFormatNTriples exports triples in N-Triples syntax.
	RDFFormatNTriples RDFFormat = "ntriples"
	// RDFFormatNQuads exports statements in N-Quads syntax when graph names are present.
	RDFFormatNQuads RDFFormat = "nquads"
	// RDFFormatTurtle exports statements in a Turtle-like syntax with prefixes when possible.
	RDFFormatTurtle RDFFormat = "turtle"
	// RDFFormatTriG exports quads in TriG syntax with graph blocks.
	RDFFormatTriG RDFFormat = "trig"
	// RDFFormatJSONLD reads and writes JSON-LD 1.1. Remote contexts are never fetched.
	RDFFormatJSONLD RDFFormat = "jsonld"
)

// RDFFormat represents a supported RDF serialization format.
type RDFFormat string

// Namespace represents a prefix to IRI mapping.
type Namespace struct {
	Prefix string `json:"prefix"`
	URI    string `json:"uri"`
}

// RDFTerm represents one RDF term: an IRI, a blank node, a literal, or an RDF
// 1.2 triple term (see rdf_term12.go for how the last two carry RDF 1.2).
//
// For a literal, Language holds the language tag and, when the literal has
// one, its base direction after a double hyphen: "en--ltr".
type RDFTerm struct {
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	Datatype string `json:"datatype,omitempty"`
	Language string `json:"language,omitempty"`
}

// RDFTriple represents one RDF triple or quad when Graph is set.
type RDFTriple struct {
	ID         string   `json:"id,omitempty"`
	Subject    RDFTerm  `json:"subject"`
	Predicate  RDFTerm  `json:"predicate"`
	Object     RDFTerm  `json:"object"`
	Graph      *RDFTerm `json:"graph,omitempty"`
	Inferred   bool     `json:"inferred,omitempty"`
	Rule       string   `json:"rule,omitempty"`
	SupportIDs []string `json:"support_ids,omitempty"`

	// Provenance is carried onto the edge this triple becomes, so a triple
	// written by a machine can say which machine, under whose authority, and
	// on what grade. Nothing here is interpreted; it is the writer's statement
	// about its own output, and keys that collide with the triple's own
	// description are dropped rather than allowed to overwrite it.
	//
	// The RDF store had no such field, which meant every triple an import
	// wrote was anonymous: the row it came from, the plan that was signed to
	// read it, and the operator who signed it were all recoverable from the
	// ledger and from nothing on the fact itself.
	Provenance map[string]string `json:"provenance,omitempty"`
}

// TriplePattern filters triple lookup operations. Nil fields behave as wildcards.
type TriplePattern struct {
	Subject   *RDFTerm `json:"subject,omitempty"`
	Predicate *RDFTerm `json:"predicate,omitempty"`
	Object    *RDFTerm `json:"object,omitempty"`
	Graph     *RDFTerm `json:"graph,omitempty"`
	Inferred  *bool    `json:"inferred,omitempty"`
	Limit     int      `json:"limit,omitempty"`

	// objectTriple restricts the object to triple terms whose parts match,
	// for a SPARQL pattern like <<( ?s :p ?o )>> that is not a constant. It
	// applies only when Object is nil; unexported because the public way to
	// ask is a constant triple term in Object.
	objectTriple *tripleTermFilter
}

var builtinNamespaces = map[string]string{
	"rdf":    "http://www.w3.org/1999/02/22-rdf-syntax-ns#",
	"rdfs":   "http://www.w3.org/2000/01/rdf-schema#",
	"xsd":    "http://www.w3.org/2001/XMLSchema#",
	"owl":    "http://www.w3.org/2002/07/owl#",
	"schema": "https://schema.org/",
	"foaf":   "http://xmlns.com/foaf/0.1/",
	"skos":   "http://www.w3.org/2004/02/skos/core#",
	// The property-graph projection's vocabulary, built in so a query can be
	// written by hand or by a model without declaring anything first.
	"cxn": PropertyNodeNamespace,
	"cxt": PropertyTypeNamespace,
	"cxr": PropertyRelNamespace,
	"cxp": PropertyPropNamespace,
}

// NewIRI creates an IRI term.
func NewIRI(value string) RDFTerm {
	return RDFTerm{
		Kind:  RDFTermIRI,
		Value: strings.TrimSpace(strings.Trim(value, "<>")),
	}
}

// NewBlankNode creates a blank node term.
func NewBlankNode(value string) RDFTerm {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "_:")
	return RDFTerm{
		Kind:  RDFTermBlankNode,
		Value: value,
	}
}

// NewLiteral creates a plain literal term.
func NewLiteral(value string) RDFTerm {
	return RDFTerm{
		Kind:  RDFTermLiteral,
		Value: value,
	}
}

// NewLangLiteral creates a language-tagged literal term.
func NewLangLiteral(value, language string) RDFTerm {
	return RDFTerm{
		Kind:     RDFTermLiteral,
		Value:    value,
		Language: strings.ToLower(strings.TrimSpace(language)),
	}
}

// NewTypedLiteral creates a typed literal term.
func NewTypedLiteral(value, datatype string) RDFTerm {
	return RDFTerm{
		Kind:     RDFTermLiteral,
		Value:    value,
		Datatype: strings.TrimSpace(strings.Trim(datatype, "<>")),
	}
}

// String renders the term using RDF-compatible syntax. A triple term renders
// as its canonical spelling.
//
// The rendering of IRIs, blank nodes and literals is frozen: tripleDigest
// hashes it, so changing it would give every stored triple a new ID. The
// exporters write documents through rdfTermWriter instead, which spells
// literals the way N-Triples requires.
func (t RDFTerm) String() string {
	switch t.Kind {
	case RDFTermIRI:
		return "<" + escapeIRI(t.Value) + ">"
	case RDFTermBlankNode:
		return "_:" + t.Value
	case RDFTermLiteral:
		out := strconv.Quote(t.Value)
		if t.Language != "" {
			return out + "@" + t.Language
		}
		if t.Datatype != "" {
			return out + "^^<" + escapeIRI(t.Datatype) + ">"
		}
		return out
	default:
		return t.Value
	}
}

// String renders the triple/quad using RDF syntax.
func (t RDFTriple) String() string {
	if t.Graph != nil {
		return fmt.Sprintf("%s %s %s %s .", t.Subject.String(), t.Predicate.String(), t.Object.String(), t.Graph.String())
	}
	return fmt.Sprintf("%s %s %s .", t.Subject.String(), t.Predicate.String(), t.Object.String())
}

// UpsertNamespace stores or replaces one namespace mapping.
func (g *GraphStore) UpsertNamespace(ctx context.Context, ns Namespace) error {
	if err := g.InitGraphSchema(ctx); err != nil {
		return err
	}
	ns.Prefix = strings.TrimSpace(ns.Prefix)
	ns.URI = strings.TrimSpace(strings.Trim(ns.URI, "<>"))
	if ns.Prefix == "" {
		return fmt.Errorf("namespace prefix is required")
	}
	if ns.URI == "" {
		return fmt.Errorf("namespace uri is required")
	}
	_, err := g.exec(ctx, `
		INSERT INTO kg_namespaces (prefix, uri)
		VALUES (?, ?)
		ON CONFLICT(prefix) DO UPDATE SET uri = excluded.uri
	`, ns.Prefix, ns.URI)
	return err
}

// DeleteNamespace removes one user-defined namespace mapping.
func (g *GraphStore) DeleteNamespace(ctx context.Context, prefix string) error {
	if err := g.InitGraphSchema(ctx); err != nil {
		return err
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return fmt.Errorf("namespace prefix is required")
	}
	_, err := g.exec(ctx, `DELETE FROM kg_namespaces WHERE prefix = ?`, prefix)
	return err
}

// ListNamespaces returns built-in and user-defined namespaces.
func (g *GraphStore) ListNamespaces(ctx context.Context) ([]Namespace, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	merged := make(map[string]string, len(builtinNamespaces))
	for prefix, uri := range builtinNamespaces {
		merged[prefix] = uri
	}

	rows, err := g.query(ctx, `SELECT prefix, uri FROM kg_namespaces`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var ns Namespace
		if err := rows.Scan(&ns.Prefix, &ns.URI); err != nil {
			return nil, err
		}
		merged[ns.Prefix] = ns.URI
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	namespaces := make([]Namespace, 0, len(merged))
	for prefix, uri := range merged {
		namespaces = append(namespaces, Namespace{Prefix: prefix, URI: uri})
	}
	sort.Slice(namespaces, func(i, j int) bool {
		return namespaces[i].Prefix < namespaces[j].Prefix
	})
	return namespaces, nil
}

// ExpandIRI expands a compact IRI using registered namespaces when possible.
func (g *GraphStore) ExpandIRI(ctx context.Context, value string) (string, error) {
	value = strings.TrimSpace(strings.Trim(value, "<>"))
	if value == "" {
		return "", nil
	}

	namespaces, err := g.ListNamespaces(ctx)
	if err != nil {
		return "", err
	}
	return expandIRIWithNamespaces(value, namespaces), nil
}

// CompactIRI compacts an IRI using the longest matching namespace prefix when available.
func (g *GraphStore) CompactIRI(ctx context.Context, value string) (string, error) {
	value = strings.TrimSpace(strings.Trim(value, "<>"))
	if value == "" {
		return "", nil
	}
	namespaces, err := g.ListNamespaces(ctx)
	if err != nil {
		return "", err
	}
	return compactIRIWithNamespaces(value, namespaces), nil
}

// UpsertTriple writes one RDF triple/quad and mirrors it into the property graph tables.
func (g *GraphStore) UpsertTriple(ctx context.Context, triple *RDFTriple) error {
	if triple == nil {
		return fmt.Errorf("triple is required")
	}
	if err := g.InitGraphSchema(ctx); err != nil {
		return err
	}

	namespaces, err := g.ListNamespaces(ctx)
	if err != nil {
		return err
	}

	normalized, err := g.normalizeTripleWithNamespaces(*triple, namespaces)
	if err != nil {
		return err
	}
	if normalized.ID == "" {
		normalized.ID = tripleDigest(normalized)
	}
	triple.ID = normalized.ID

	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin triple transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	edgeType := compactIRIWithNamespaces(normalized.Predicate.Value, namespaces)
	if err := g.upsertPreparedTripleTx(ctx, tx, normalized, namespaces, edgeType); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit triple transaction: %w", err)
	}
	return nil
}

// UpsertTriplesBatch writes multiple RDF triples/quads.
func (g *GraphStore) UpsertTriplesBatch(ctx context.Context, triples []*RDFTriple) (*BatchResult, error) {
	if len(triples) == 0 {
		return &BatchResult{}, nil
	}

	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}

	namespaces, err := g.ListNamespaces(ctx)
	if err != nil {
		return nil, err
	}

	result := &BatchResult{Errors: make([]error, 0)}

	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin triples batch transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	edgeTypeCache := make(map[string]string)
	for _, triple := range triples {
		if triple == nil {
			result.Errors = append(result.Errors, fmt.Errorf("triple is required"))
			result.FailedCount++
			continue
		}

		normalized, err := g.normalizeTripleWithNamespaces(*triple, namespaces)
		if err != nil {
			result.Errors = append(result.Errors, err)
			result.FailedCount++
			continue
		}
		if normalized.ID == "" {
			normalized.ID = tripleDigest(normalized)
		}
		triple.ID = normalized.ID

		edgeType, ok := edgeTypeCache[normalized.Predicate.Value]
		if !ok {
			edgeType = compactIRIWithNamespaces(normalized.Predicate.Value, namespaces)
			edgeTypeCache[normalized.Predicate.Value] = edgeType
		}
		if err := g.upsertPreparedTripleTx(ctx, tx, normalized, namespaces, edgeType); err != nil {
			result.Errors = append(result.Errors, err)
			result.FailedCount++
			continue
		}
		result.SuccessCount++
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit triples batch transaction: %w", err)
	}
	return result, nil
}

// GetTriple fetches one RDF triple by ID.
func (g *GraphStore) GetTriple(ctx context.Context, id string) (*RDFTriple, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}
	if strings.HasPrefix(id, projectedTripleIDPrefix) {
		return g.getProjectedTriple(ctx, id)
	}
	row := g.queryRow(ctx, `
		SELECT
			id, graph_kind, graph_value,
			subject_kind, subject_value,
			predicate_value,
			object_kind, object_value, object_datatype, object_language,
			inferred, inference_rule, support_ids
		FROM kg_triples
		WHERE id = ?
	`, id)
	triple, err := scanTriple(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("triple not found: %s", id)
	}
	if err != nil {
		return nil, err
	}
	return &triple, nil
}

// FindTriples queries triples by pattern.
//
// The answer is the stored triples followed by the triples the property graph
// implies (see graph_projection.go), unless the projection has been turned
// off. Limit applies to the two together.
func (g *GraphStore) FindTriples(ctx context.Context, pattern TriplePattern) ([]RDFTriple, error) {
	if pattern.Subject != nil && pattern.Subject.Kind == RDFTermTriple {
		// Never stored (RDF 1.2), so the answer is none. Asked by every
		// consumer that walks from a value to its properties — SHACL from a
		// focus node, DESCRIBE from a resource — which should find a triple
		// term has no properties rather than fail on it.
		return nil, nil
	}
	stored, err := g.findStoredTriples(ctx, pattern)
	if err != nil {
		return nil, err
	}
	// The projection has no triple terms; a pattern that asks for one has
	// nothing to find there.
	if pattern.objectTriple != nil || (pattern.Object != nil && pattern.Object.Kind == RDFTermTriple) {
		return stored, nil
	}
	limit := 0
	if pattern.Limit > 0 {
		if len(stored) >= pattern.Limit {
			return stored, nil
		}
		limit = pattern.Limit - len(stored)
	}
	projected, err := g.findProjectedTriples(ctx, pattern, limit)
	if err != nil {
		return nil, err
	}
	return append(stored, projected...), nil
}

// findStoredTriples queries kg_triples alone.
//
// A pattern naming the projection's graph still reads kg_triples: RDFS
// inferences drawn from projected triples inherit that graph and are stored,
// and scoping a query to the graph must not hide them.
func (g *GraphStore) findStoredTriples(ctx context.Context, pattern TriplePattern) ([]RDFTriple, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return nil, err
	}

	var conditions []string
	args := make([]any, 0, 12)

	if pattern.Subject != nil {
		subject, err := g.normalizeTerm(ctx, *pattern.Subject, rdfPositionSubject)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, "subject_kind = ?", "subject_value = ?")
		args = append(args, subject.Kind, subject.Value)
	}
	if pattern.Predicate != nil {
		predicate, err := g.normalizeTerm(ctx, *pattern.Predicate, rdfPositionPredicate)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, "predicate_value = ?")
		args = append(args, predicate.Value)
	}
	if pattern.Object != nil {
		object, err := g.normalizeTerm(ctx, *pattern.Object, rdfPositionObject)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, "object_kind = ?", "object_value = ?")
		args = append(args, object.Kind, object.Value)
		if object.Kind == RDFTermLiteral {
			if object.Datatype != "" {
				conditions = append(conditions, "object_datatype = ?")
				args = append(args, object.Datatype)
			}
			if object.Language != "" {
				conditions = append(conditions, "object_language = ?")
				args = append(args, object.Language)
			}
		}
	} else if pattern.objectTriple != nil {
		condition, conditionArgs, err := pattern.objectTriple.sqlCondition()
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, condition)
		args = append(args, conditionArgs...)
	}
	if pattern.Graph != nil {
		graphTerm, err := g.normalizeTerm(ctx, *pattern.Graph, rdfPositionGraph)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, "graph_kind = ?", "graph_value = ?")
		args = append(args, graphTerm.Kind, graphTerm.Value)
	}
	if pattern.Inferred != nil {
		conditions = append(conditions, "inferred = ?")
		args = append(args, boolToInt(*pattern.Inferred))
	}

	query := `
		SELECT
			id, graph_kind, graph_value,
			subject_kind, subject_value,
			predicate_value,
			object_kind, object_value, object_datatype, object_language,
			inferred, inference_rule, support_ids
		FROM kg_triples
	`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY id"
	if pattern.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", pattern.Limit)
	}

	rows, err := g.query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	triples := make([]RDFTriple, 0)
	for rows.Next() {
		triple, err := scanTriple(rows)
		if err != nil {
			return nil, err
		}
		triples = append(triples, triple)
	}
	return triples, rows.Err()
}

// DeleteTriple removes one RDF triple/quad by its normalized content.
//
// A triple the property-graph projection supplies cannot be deleted here and
// fails with ErrPropertyGraphReadOnly rather than succeeding at nothing.
func (g *GraphStore) DeleteTriple(ctx context.Context, triple RDFTriple) error {
	_, err := g.deleteTriple(ctx, triple)
	return err
}

// DeleteTripleCounted is DeleteTriple for callers that report a count: it
// returns how many stored triples were removed, which is zero for a triple
// that was never there. A caller that counted calls instead of removals told
// its own caller that facts were gone which had never existed.
func (g *GraphStore) DeleteTripleCounted(ctx context.Context, triple RDFTriple) (int, error) {
	return g.deleteTriple(ctx, triple)
}

// deleteTriple is DeleteTriple reporting how many stored triples it removed,
// which is zero for a triple that was never there. Callers that count
// deletions count this, not their own calls.
func (g *GraphStore) deleteTriple(ctx context.Context, triple RDFTriple) (int, error) {
	if err := g.InitGraphSchema(ctx); err != nil {
		return 0, err
	}
	if err := g.refuseProjectedDeletes(ctx, []RDFTriple{triple}); err != nil {
		return 0, err
	}
	normalized, err := g.normalizeTriple(ctx, triple)
	if err != nil {
		return 0, err
	}
	if normalized.ID == "" {
		normalized.ID = tripleDigest(normalized)
	}

	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin delete triple transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := g.txExec(ctx, tx, `DELETE FROM kg_triples WHERE id = ?`, normalized.ID)
	if err != nil {
		return 0, fmt.Errorf("delete kg triple: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete kg triple: %w", err)
	}
	if _, err := g.txExec(ctx, tx, `DELETE FROM graph_edges WHERE id = ?`, normalized.ID); err != nil {
		return 0, fmt.Errorf("delete rdf edge: %w", err)
	}
	if err := g.cleanupOrphanRDFNodeTx(ctx, tx, normalized.Subject); err != nil {
		return 0, err
	}
	if err := g.cleanupOrphanRDFNodeTx(ctx, tx, normalized.Object); err != nil {
		return 0, err
	}
	if err := g.cleanupTripleTermRowsTx(ctx, tx, normalized.Object); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit delete triple transaction: %w", err)
	}
	return int(removed), nil
}

// DeleteTriples removes all triples matched by the given pattern and reports
// how many were removed. A pattern that matches a projected triple fails
// before anything is removed.
func (g *GraphStore) DeleteTriples(ctx context.Context, pattern TriplePattern) (int, error) {
	triples, err := g.FindTriples(ctx, pattern)
	if err != nil {
		return 0, err
	}
	if err := g.refuseProjectedDeletes(ctx, triples); err != nil {
		return 0, err
	}
	return g.deleteTriples(ctx, triples)
}

func (g *GraphStore) deleteTriples(ctx context.Context, triples []RDFTriple) (int, error) {
	deleted := 0
	for _, triple := range triples {
		removed, err := g.deleteTriple(ctx, triple)
		if err != nil {
			return deleted, err
		}
		deleted += removed
	}
	return deleted, nil
}

// ExportRDF writes the stored triples in the requested RDF format.
//
// The property-graph projection is not exported. An export is read back by
// ImportRDF, which stores what it reads, so exporting the projection would
// turn every node and edge into a stored triple on the way back in — a second
// copy of the property graph that nothing keeps in step with the first. A
// caller that wants the projection as RDF asks for it with CONSTRUCT.
func (g *GraphStore) ExportRDF(ctx context.Context, writer io.Writer, format RDFFormat) error {
	triples, err := g.findStoredTriples(ctx, TriplePattern{})
	if err != nil {
		return err
	}

	switch format {
	case RDFFormatNTriples:
		return writeLineStatements(writer, triples, false)
	case RDFFormatNQuads:
		return writeLineStatements(writer, triples, true)
	case RDFFormatTurtle:
		return g.exportTurtle(ctx, writer, triples)
	case RDFFormatTriG:
		return g.exportTriG(ctx, writer, triples)
	case RDFFormatJSONLD:
		return g.exportJSONLD(ctx, writer, triples)
	default:
		return fmt.Errorf("unsupported rdf format: %s", format)
	}
}

// ImportRDF parses and stores triples from supported RDF serializations.
func (g *GraphStore) ImportRDF(ctx context.Context, reader io.Reader, format RDFFormat) (int, error) {
	switch format {
	case RDFFormatNTriples:
		return g.importLineStatements(ctx, reader, false)
	case RDFFormatNQuads:
		return g.importLineStatements(ctx, reader, true)
	case RDFFormatTurtle:
		return g.importTurtle(ctx, reader)
	case RDFFormatTriG:
		return g.importTriG(ctx, reader)
	case RDFFormatJSONLD:
		return g.importJSONLD(ctx, reader)
	default:
		return 0, fmt.Errorf("unsupported rdf import format: %s", format)
	}
}

func (g *GraphStore) exportTurtle(ctx context.Context, writer io.Writer, triples []RDFTriple) error {
	namespaces, err := g.ListNamespaces(ctx)
	if err != nil {
		return err
	}
	for _, triple := range triples {
		if triple.Graph != nil {
			return fmt.Errorf("turtle export currently supports only default graph statements")
		}
	}
	buffered := bufio.NewWriter(writer)
	if err := writeTurtleHeader(buffered, namespaces, triples); err != nil {
		return err
	}
	w := newRDFTermWriter(namespaces, true)
	for _, triple := range triples {
		line, err := w.statement(triple, false)
		if err != nil {
			return err
		}
		if _, err := buffered.WriteString(line + "\n"); err != nil {
			return err
		}
	}
	return buffered.Flush()
}

func scanTriple(scanner interface {
	Scan(dest ...any) error
}) (RDFTriple, error) {
	var (
		triple                                 RDFTriple
		graphKind, graphValue                  sql.NullString
		objectDatatype, objLang, inferenceRule sql.NullString
		supportIDsJSON                         sql.NullString
		inferred                               int
	)
	if err := scanner.Scan(
		&triple.ID,
		&graphKind,
		&graphValue,
		&triple.Subject.Kind,
		&triple.Subject.Value,
		&triple.Predicate.Value,
		&triple.Object.Kind,
		&triple.Object.Value,
		&objectDatatype,
		&objLang,
		&inferred,
		&inferenceRule,
		&supportIDsJSON,
	); err != nil {
		return RDFTriple{}, err
	}
	triple.Predicate.Kind = RDFTermIRI
	if objectDatatype.Valid {
		triple.Object.Datatype = objectDatatype.String
	}
	if objLang.Valid {
		triple.Object.Language = objLang.String
	}
	if graphKind.Valid {
		triple.Graph = &RDFTerm{
			Kind:  graphKind.String,
			Value: graphValue.String,
		}
	}
	triple.Inferred = inferred != 0
	if inferenceRule.Valid {
		triple.Rule = inferenceRule.String
	}
	if supportIDsJSON.Valid && supportIDsJSON.String != "" {
		if err := json.Unmarshal([]byte(supportIDsJSON.String), &triple.SupportIDs); err != nil {
			return RDFTriple{}, err
		}
	}
	return triple, nil
}

type rdfPosition string

const (
	rdfPositionSubject   rdfPosition = "subject"
	rdfPositionPredicate rdfPosition = "predicate"
	rdfPositionObject    rdfPosition = "object"
	rdfPositionGraph     rdfPosition = "graph"
)

func (g *GraphStore) normalizeTriple(ctx context.Context, triple RDFTriple) (RDFTriple, error) {
	namespaces, err := g.ListNamespaces(ctx)
	if err != nil {
		return RDFTriple{}, err
	}
	return g.normalizeTripleWithNamespaces(triple, namespaces)
}

func (g *GraphStore) normalizeTripleWithNamespaces(triple RDFTriple, namespaces []Namespace) (RDFTriple, error) {
	subject, err := normalizeTermWithNamespaces(triple.Subject, rdfPositionSubject, namespaces)
	if err != nil {
		return RDFTriple{}, err
	}
	predicate, err := normalizeTermWithNamespaces(triple.Predicate, rdfPositionPredicate, namespaces)
	if err != nil {
		return RDFTriple{}, err
	}
	object, err := normalizeTermWithNamespaces(triple.Object, rdfPositionObject, namespaces)
	if err != nil {
		return RDFTriple{}, err
	}
	var graphTerm *RDFTerm
	if triple.Graph != nil {
		normalizedGraph, err := normalizeTermWithNamespaces(*triple.Graph, rdfPositionGraph, namespaces)
		if err != nil {
			return RDFTriple{}, err
		}
		graphTerm = &normalizedGraph
	}
	return RDFTriple{
		ID:         triple.ID,
		Subject:    subject,
		Predicate:  predicate,
		Object:     object,
		Graph:      graphTerm,
		Inferred:   triple.Inferred,
		Rule:       strings.TrimSpace(triple.Rule),
		SupportIDs: append([]string(nil), triple.SupportIDs...),
		Provenance: triple.Provenance,
	}, nil
}

func (g *GraphStore) normalizeTerm(ctx context.Context, term RDFTerm, position rdfPosition) (RDFTerm, error) {
	namespaces, err := g.ListNamespaces(ctx)
	if err != nil {
		return RDFTerm{}, err
	}
	return normalizeTermWithNamespaces(term, position, namespaces)
}

func normalizeTermWithNamespaces(term RDFTerm, position rdfPosition, namespaces []Namespace) (RDFTerm, error) {
	term.Kind = strings.TrimSpace(term.Kind)
	// A literal's lexical form is kept exactly, white space and all, and may
	// be empty: "" and " x " are RDF literals distinct from "x", and an
	// importer that trimmed them stored a different fact than the document
	// stated — and refused outright a document holding an empty string.
	if term.Kind != RDFTermLiteral {
		term.Value = strings.TrimSpace(term.Value)
	}
	term.Datatype = strings.TrimSpace(strings.Trim(term.Datatype, "<>"))
	term.Language = strings.ToLower(strings.TrimSpace(term.Language))

	// RDF 1.2 admits a triple term in one position only. Checked before the
	// generic position rules so the error says why, not merely what.
	if term.Kind == RDFTermTriple && position != rdfPositionObject {
		return RDFTerm{}, fmt.Errorf("rdf %s cannot be a triple term: RDF 1.2 allows triple terms only as objects", position)
	}

	switch position {
	case rdfPositionSubject:
		if term.Kind != RDFTermIRI && term.Kind != RDFTermBlankNode {
			return RDFTerm{}, fmt.Errorf("rdf subject must be iri or blank node")
		}
	case rdfPositionPredicate:
		if term.Kind == "" {
			term.Kind = RDFTermIRI
		}
		if term.Kind != RDFTermIRI {
			return RDFTerm{}, fmt.Errorf("rdf predicate must be iri")
		}
	case rdfPositionGraph:
		if term.Kind != RDFTermIRI && term.Kind != RDFTermBlankNode {
			return RDFTerm{}, fmt.Errorf("rdf graph must be iri or blank node")
		}
	case rdfPositionObject:
		if term.Kind != RDFTermIRI && term.Kind != RDFTermBlankNode && term.Kind != RDFTermLiteral && term.Kind != RDFTermTriple {
			return RDFTerm{}, fmt.Errorf("rdf object must be iri, blank node, literal, or triple term")
		}
	}

	if term.Kind == "" {
		return RDFTerm{}, fmt.Errorf("rdf term kind is required")
	}
	if term.Value == "" && term.Kind != RDFTermLiteral {
		return RDFTerm{}, fmt.Errorf("rdf term value is required")
	}
	if term.Kind == RDFTermIRI {
		term.Value = expandIRIWithNamespaces(term.Value, namespaces)
	}
	if term.Kind == RDFTermBlankNode {
		term.Value = strings.TrimPrefix(term.Value, "_:")
	}
	if term.Kind == RDFTermLiteral && term.Datatype != "" {
		term.Datatype = expandIRIWithNamespaces(term.Datatype, namespaces)
	}
	if term.Kind == RDFTermLiteral && strings.Contains(term.Language, "--") {
		if dir := term.BaseDirection(); dir != "ltr" && dir != "rtl" {
			return RDFTerm{}, fmt.Errorf("literal base direction must be ltr or rtl, got %q", dir)
		}
	}
	if term.Kind == RDFTermTriple {
		return normalizeTripleTermWithNamespaces(term, namespaces, 0)
	}
	return term, nil
}

// normalizeTripleTermWithNamespaces checks a triple term and re-spells it
// canonically, expanding any prefixed IRI inside it the way a top-level term
// is expanded. Literals inside are kept exactly: the canonical spelling is the
// term's identity, and two literals that differ only in white space are two
// different terms.
func normalizeTripleTermWithNamespaces(term RDFTerm, namespaces []Namespace, depth int) (RDFTerm, error) {
	if depth > maxTripleTermDepth {
		return RDFTerm{}, fmt.Errorf("triple term nested deeper than %d", maxTripleTermDepth)
	}
	triple, err := decodeTripleTermValue(term.Value)
	if err != nil {
		return RDFTerm{}, err
	}
	expand := func(part RDFTerm) (RDFTerm, error) {
		switch part.Kind {
		case RDFTermIRI:
			part.Value = expandIRIWithNamespaces(part.Value, namespaces)
		case RDFTermLiteral:
			if part.Datatype != "" {
				part.Datatype = expandIRIWithNamespaces(part.Datatype, namespaces)
			}
		case RDFTermTriple:
			return normalizeTripleTermWithNamespaces(part, namespaces, depth+1)
		}
		return part, nil
	}
	if triple.Subject, err = expand(triple.Subject); err != nil {
		return RDFTerm{}, err
	}
	if triple.Predicate, err = expand(triple.Predicate); err != nil {
		return RDFTerm{}, err
	}
	if triple.Object, err = expand(triple.Object); err != nil {
		return RDFTerm{}, err
	}
	return NewTripleTerm(triple.Subject, triple.Predicate, triple.Object)
}

func (g *GraphStore) upsertRDFTermNodeTx(ctx context.Context, tx *sql.Tx, term RDFTerm) error {
	label := g.rdfTermLabel(ctx, term)
	return g.upsertRDFTermNodeWithLabelTx(ctx, tx, term, label)
}

func (g *GraphStore) upsertRDFTermNodeWithLabelTx(ctx context.Context, tx *sql.Tx, term RDFTerm, label string) error {
	node := &GraphNode{
		ID:       rdfNodeID(term),
		Vector:   rdfVector(g.rdfVectorDim(), rdfVectorParts(term)...),
		Content:  label,
		NodeType: rdfNodeType(term),
		Properties: map[string]any{
			"rdf":       true,
			"term_kind": term.Kind,
			"value":     term.Value,
		},
	}
	if term.Datatype != "" {
		node.Properties["datatype"] = term.Datatype
	}
	if term.Language != "" {
		node.Properties["language"] = term.Language
	}

	vectorBytes, err := encoding.EncodeVector(node.Vector)
	if err != nil {
		return fmt.Errorf("encode rdf node vector: %w", err)
	}
	propertiesJSON, err := json.Marshal(node.Properties)
	if err != nil {
		return fmt.Errorf("encode rdf node properties: %w", err)
	}
	_, err = g.txExec(ctx, tx, `
		INSERT INTO graph_nodes (id, vector, content, node_type, properties, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			vector = excluded.vector,
			content = excluded.content,
			node_type = excluded.node_type,
			properties = excluded.properties,
			updated_at = CURRENT_TIMESTAMP
	`, node.ID, vectorBytes, node.Content, node.NodeType, string(propertiesJSON))
	if err != nil {
		return fmt.Errorf("upsert rdf node: %w", err)
	}
	return nil
}

// rdfEdgeProperties is what an RDF triple carries once it is an edge.
//
// Written once for both upsert paths, which had the same twenty lines copied.
// The reason it matters that there is one of them is Provenance: a triple that
// could describe its origin through one path and not the other would be a
// provenance surface with a hole in it, and the hole would be invisible.
func rdfEdgeProperties(triple RDFTriple) map[string]any {
	properties := map[string]any{
		"rdf":         true,
		"triple_id":   triple.ID,
		"predicate":   triple.Predicate.Value,
		"object_kind": triple.Object.Kind,
		"inferred":    triple.Inferred,
	}
	if triple.Object.Datatype != "" {
		properties["datatype"] = triple.Object.Datatype
	}
	if triple.Object.Language != "" {
		properties["language"] = triple.Object.Language
	}
	if triple.Graph != nil {
		properties["graph_kind"] = triple.Graph.Kind
		properties["graph_value"] = triple.Graph.Value
	}
	if triple.Rule != "" {
		properties["inference_rule"] = triple.Rule
	}
	if len(triple.SupportIDs) > 0 {
		properties["support_ids"] = triple.SupportIDs
	}
	// Provenance is merged last but cannot win: the keys above describe what
	// the triple *is*, and a caller that could overwrite "inferred" or
	// "triple_id" could make a fact lie about itself through the same field
	// that is supposed to make it accountable.
	for k, v := range triple.Provenance {
		if _, taken := properties[k]; taken {
			continue
		}
		properties[k] = v
	}
	return properties
}

func (g *GraphStore) upsertRDFEdgeTx(ctx context.Context, tx *sql.Tx, triple RDFTriple) error {
	propertiesJSON, err := json.Marshal(rdfEdgeProperties(triple))
	if err != nil {
		return fmt.Errorf("encode rdf edge properties: %w", err)
	}
	edgeType, err := g.CompactIRI(ctx, triple.Predicate.Value)
	if err != nil {
		return err
	}
	return g.upsertRDFEdgeWithTypeJSONTx(ctx, tx, triple, edgeType, string(propertiesJSON))
}

func (g *GraphStore) upsertRDFEdgeWithTypeTx(ctx context.Context, tx *sql.Tx, triple RDFTriple, edgeType string) error {
	propertiesJSON, err := json.Marshal(rdfEdgeProperties(triple))
	if err != nil {
		return fmt.Errorf("encode rdf edge properties: %w", err)
	}
	return g.upsertRDFEdgeWithTypeJSONTx(ctx, tx, triple, edgeType, string(propertiesJSON))
}

func (g *GraphStore) upsertRDFEdgeWithTypeJSONTx(ctx context.Context, tx *sql.Tx, triple RDFTriple, edgeType, propertiesJSON string) error {
	_, err := g.txExec(ctx, tx, `
		INSERT INTO graph_edges (id, from_node_id, to_node_id, edge_type, weight, properties, vector)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			from_node_id = excluded.from_node_id,
			to_node_id = excluded.to_node_id,
			edge_type = excluded.edge_type,
			weight = excluded.weight,
			properties = excluded.properties,
			vector = excluded.vector
	`, triple.ID, rdfNodeID(triple.Subject), rdfNodeID(triple.Object), edgeType, 1.0, propertiesJSON, nil)
	if err != nil {
		return fmt.Errorf("upsert rdf edge: %w", err)
	}
	return nil
}

func (g *GraphStore) cleanupOrphanRDFNodeTx(ctx context.Context, tx *sql.Tx, term RDFTerm) error {
	nodeID := rdfNodeID(term)
	var refCount int
	query := `
		SELECT COUNT(*)
		FROM kg_triples
		WHERE (subject_kind = ? AND subject_value = ?)
		   OR (object_kind = ? AND object_value = ?)
	`
	if err := g.txQueryRow(ctx, tx, query, term.Kind, term.Value, term.Kind, term.Value).Scan(&refCount); err != nil {
		return fmt.Errorf("count rdf node refs: %w", err)
	}
	if refCount > 0 {
		return nil
	}
	if _, err := g.txExec(ctx, tx, `DELETE FROM graph_nodes WHERE id = ?`, nodeID); err != nil {
		return fmt.Errorf("delete orphan rdf node: %w", err)
	}
	return nil
}

func (g *GraphStore) rdfVectorDim() int {
	dim := g.store.Config().VectorDim
	if dim <= 0 {
		dim = 64
	}
	return dim
}

func (g *GraphStore) rdfTermLabel(ctx context.Context, term RDFTerm) string {
	namespaces, err := g.ListNamespaces(ctx)
	if err != nil {
		switch term.Kind {
		case RDFTermBlankNode:
			return "_:" + term.Value
		default:
			return term.Value
		}
	}
	return rdfTermLabelWithNamespaces(term, namespaces)
}

func rdfTermLabelWithNamespaces(term RDFTerm, namespaces []Namespace) string {
	switch term.Kind {
	case RDFTermIRI:
		if compacted := compactIRIWithNamespaces(term.Value, namespaces); compacted != "" {
			return compacted
		}
		return term.Value
	case RDFTermBlankNode:
		return "_:" + term.Value
	case RDFTermLiteral:
		return term.Value
	case RDFTermTriple:
		return compactTripleTermLabel(term, namespaces)
	default:
		return term.Value
	}
}

func (g *GraphStore) upsertPreparedTripleTx(ctx context.Context, tx *sql.Tx, triple RDFTriple, namespaces []Namespace, edgeType string) error {
	subjectLabel := rdfTermLabelWithNamespaces(triple.Subject, namespaces)
	if err := g.upsertRDFTermNodeWithLabelTx(ctx, tx, triple.Subject, subjectLabel); err != nil {
		return err
	}
	if triple.Object.Kind == RDFTermIRI || triple.Object.Kind == RDFTermBlankNode || triple.Object.Kind == RDFTermLiteral || triple.Object.Kind == RDFTermTriple {
		objectLabel := rdfTermLabelWithNamespaces(triple.Object, namespaces)
		if err := g.upsertRDFTermNodeWithLabelTx(ctx, tx, triple.Object, objectLabel); err != nil {
			return err
		}
	}
	if triple.Object.Kind == RDFTermTriple {
		if err := g.upsertTripleTermRowsTx(ctx, tx, triple.Object); err != nil {
			return err
		}
	}

	var graphKind any
	var graphValue any
	if triple.Graph != nil {
		graphKind = triple.Graph.Kind
		graphValue = triple.Graph.Value
	}
	var supportIDsJSON any
	if len(triple.SupportIDs) > 0 {
		payload, err := json.Marshal(triple.SupportIDs)
		if err != nil {
			return fmt.Errorf("encode triple support ids: %w", err)
		}
		supportIDsJSON = string(payload)
	}

	_, err := g.txExec(ctx, tx, `
		INSERT INTO kg_triples (
			id, graph_kind, graph_value,
			subject_kind, subject_value,
			predicate_value,
			object_kind, object_value, object_datatype, object_language,
			inferred, inference_rule, support_ids
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			graph_kind = excluded.graph_kind,
			graph_value = excluded.graph_value,
			subject_kind = excluded.subject_kind,
			subject_value = excluded.subject_value,
			predicate_value = excluded.predicate_value,
			object_kind = excluded.object_kind,
			object_value = excluded.object_value,
			object_datatype = excluded.object_datatype,
			object_language = excluded.object_language,
			inferred = excluded.inferred,
			inference_rule = excluded.inference_rule,
			support_ids = excluded.support_ids
	`,
		triple.ID,
		graphKind,
		graphValue,
		triple.Subject.Kind,
		triple.Subject.Value,
		triple.Predicate.Value,
		triple.Object.Kind,
		triple.Object.Value,
		nullIfEmpty(triple.Object.Datatype),
		nullIfEmpty(triple.Object.Language),
		boolToInt(triple.Inferred),
		nullIfEmpty(triple.Rule),
		supportIDsJSON,
	)
	if err != nil {
		return fmt.Errorf("upsert kg triple: %w", err)
	}

	if err := g.upsertRDFEdgeWithTypeTx(ctx, tx, triple, edgeType); err != nil {
		return err
	}
	return nil
}

func rdfNodeType(term RDFTerm) string {
	switch term.Kind {
	case RDFTermIRI:
		return "rdf_resource"
	case RDFTermBlankNode:
		return "rdf_blank_node"
	case RDFTermLiteral:
		return "rdf_literal"
	default:
		// Triple terms included. "rdf_term" is on the projection's list of
		// mirror node types, so the node a triple term gets in the property
		// graph is never read back as a property-graph node of its own.
		return "rdf_term"
	}
}

func rdfVectorParts(term RDFTerm) []string {
	parts := []string{term.Kind, term.Value}
	if term.Datatype != "" {
		parts = append(parts, term.Datatype)
	}
	if term.Language != "" {
		parts = append(parts, term.Language)
	}
	return parts
}

func rdfVector(dim int, values ...string) []float32 {
	vector := make([]float32, dim)
	for _, value := range values {
		for _, token := range tokenizeRDFValue(value) {
			hasher := fnv.New32a()
			_, _ = hasher.Write([]byte(strings.ToLower(token)))
			index := int(hasher.Sum32() % uint32(dim))
			vector[index]++
		}
	}
	return vector
}

func tokenizeRDFValue(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r))
	})
	if len(fields) == 0 && value != "" {
		return []string{value}
	}
	return fields
}

func tripleDigest(triple RDFTriple) string {
	sum := sha1.Sum([]byte(triple.String()))
	return "rdf:triple:" + hex.EncodeToString(sum[:12])
}

func rdfNodeID(term RDFTerm) string {
	sum := sha1.Sum([]byte(term.Kind + "|" + term.Value + "|" + term.Datatype + "|" + term.Language))
	switch term.Kind {
	case RDFTermIRI:
		return "rdf:iri:" + hex.EncodeToString(sum[:12])
	case RDFTermBlankNode:
		return "rdf:bnode:" + hex.EncodeToString(sum[:12])
	case RDFTermLiteral:
		return "rdf:literal:" + hex.EncodeToString(sum[:12])
	default:
		return "rdf:term:" + hex.EncodeToString(sum[:12])
	}
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func looksLikeAbsoluteIRI(value string) bool {
	if value == "" {
		return false
	}
	if strings.Contains(value, "://") {
		return true
	}
	for _, prefix := range []string{"urn:", "mailto:", "did:", "tag:"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func expandIRIWithNamespaces(value string, namespaces []Namespace) string {
	value = strings.TrimSpace(strings.Trim(value, "<>"))
	if value == "" || looksLikeAbsoluteIRI(value) {
		return value
	}

	colon := strings.IndexByte(value, ':')
	if colon <= 0 {
		return value
	}
	prefix := value[:colon]
	local := value[colon+1:]
	if strings.Contains(prefix, "/") {
		return value
	}

	for _, ns := range namespaces {
		if ns.Prefix == prefix {
			return ns.URI + local
		}
	}
	return value
}

func compactIRIWithNamespaces(value string, namespaces []Namespace) string {
	value = strings.TrimSpace(strings.Trim(value, "<>"))
	if value == "" {
		return ""
	}
	best := value
	bestLen := 0
	for _, ns := range namespaces {
		if strings.HasPrefix(value, ns.URI) && len(ns.URI) > bestLen {
			best = ns.Prefix + ":" + strings.TrimPrefix(value, ns.URI)
			bestLen = len(ns.URI)
		}
	}
	return best
}

func escapeIRI(value string) string {
	return strings.ReplaceAll(value, ">", "%3E")
}
