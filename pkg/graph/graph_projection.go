package graph

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The property graph, read as RDF.
//
// This database holds two graphs that did not see each other. Everything real
// — extracted entities, relations, memories, documents, chunks — is written to
// graph_nodes and graph_edges. SPARQL, RDFS inference and SHACL read only
// kg_triples, which on a brain that never imported RDF is empty: the shared
// brain had 3812 nodes and 5809 edges and not one triple, so every structural
// question those features exist to answer came back with nothing.
//
// The fix is a view, not a copy. FindTriples is the one function every RDF
// read goes through, and it now returns, beside the stored triples, the
// triples the property graph implies. Nothing is written and nothing has to be
// kept in sync, because there is no second copy to drift: a node renamed a
// moment ago is renamed in the next SPARQL answer.
//
// The mapping, which facade documentation and tool descriptions are written
// against, so it changes only on purpose:
//
//	node id X            <urn:cortexdb:node:esc(X)>
//	node_type T          <node> rdf:type <urn:cortexdb:type:esc(T)>
//	edge (a, b, type R)  <a> <urn:cortexdb:rel:esc(R)> <b>
//	property k = scalar  <node> <urn:cortexdb:prop:esc(k)> literal
//	name, else title     <node> rdfs:label "..."
//
// All of it lives in the named graph <urn:cortexdb:graph:property>, so a query
// can scope itself to the projection with GRAPH, and SPARQL treats that graph
// as part of the default graph so a query without GRAPH sees it too.
//
// What is deliberately left out: node content, which for a memory or a chunk
// is kilobytes of prose and is what text search is for, not SPARQL; edge
// properties and edge weight, which would need reification and turn one edge
// into five triples nobody asked for; nested objects and nulls inside
// properties, which have no literal to become; and empty strings, which this
// store cannot match as a pattern term, so a query that joined on one would
// fail rather than find nothing.
//
// The mirror rows are excluded. Every stored RDF triple is also written into
// graph_nodes and graph_edges (see upsertPreparedTripleTx), and projecting
// those back would answer every stored triple twice, once as itself and once
// as a stranger with a cxn: subject.
const (
	// PropertyGraphIRI names the graph every projected triple lives in.
	PropertyGraphIRI = "urn:cortexdb:graph:property"
	// PropertyNodeNamespace prefixes the IRI of every projected node.
	PropertyNodeNamespace = "urn:cortexdb:node:"
	// PropertyTypeNamespace prefixes the IRI of every node_type used as a class.
	PropertyTypeNamespace = "urn:cortexdb:type:"
	// PropertyRelNamespace prefixes the IRI of every edge_type used as a predicate.
	PropertyRelNamespace = "urn:cortexdb:rel:"
	// PropertyPropNamespace prefixes the IRI of every property key used as a predicate.
	PropertyPropNamespace = "urn:cortexdb:prop:"

	// projectedTripleIDPrefix marks a triple ID as one the projection made up.
	// A stored triple's ID is a digest beginning "rdf:triple:" or whatever its
	// writer chose, so the two cannot be confused, and an ID that names a
	// projected triple can be resolved back to the row it came from.
	projectedTripleIDPrefix = "pg:"
)

// ErrPropertyGraphReadOnly is returned when a write targets a triple the
// property-graph projection supplies rather than one kg_triples stores.
//
// Loud on purpose. Before this, a DELETE over such a triple would have removed
// nothing and counted it as removed, and the triple would still be in the next
// answer — the store telling its caller a thing had happened that had not.
var ErrPropertyGraphReadOnly = errors.New("triple belongs to the read-only property-graph projection; change the node or edge with the graph API instead")

var (
	projectionRDFTypeIRI    = builtinNamespaces["rdf"] + "type"
	projectionRDFSLabelIRI  = builtinNamespaces["rdfs"] + "label"
	projectionXSDInteger    = builtinNamespaces["xsd"] + "integer"
	projectionXSDDecimal    = builtinNamespaces["xsd"] + "decimal"
	projectionXSDBoolean    = builtinNamespaces["xsd"] + "boolean"
	projectionRDFMirrorKind = []string{"rdf_resource", "rdf_blank_node", "rdf_literal", "rdf_term"}
)

// SetPropertyGraphProjection turns the property-graph projection on or off
// for this store. It is on unless turned off: the projection is the reason
// SPARQL, RDFS and SHACL have anything to read on a brain that never imported
// RDF, and a switch that defaulted the other way would leave them empty for
// everyone who did not know to flip it.
func (g *GraphStore) SetPropertyGraphProjection(enabled bool) {
	g.projectionOff.Store(!enabled)
}

// PropertyGraphProjectionEnabled reports whether FindTriples includes the
// property-graph projection.
func (g *GraphStore) PropertyGraphProjectionEnabled() bool {
	return !g.projectionOff.Load()
}

// PropertyGraphNodeIRI is the IRI the projection gives a property-graph node.
func PropertyGraphNodeIRI(nodeID string) string {
	return PropertyNodeNamespace + escapeProjectionName(nodeID)
}

// PropertyGraphNodeID is the inverse of PropertyGraphNodeIRI. It reports false
// for an IRI that is not a node IRI in its canonical spelling.
func PropertyGraphNodeID(iri string) (string, bool) {
	return decodeProjectionIRI(iri, PropertyNodeNamespace)
}

// escapeProjectionName percent-encodes everything that is not an unreserved
// IRI character, so a node id, type, relation or key survives the trip into
// an IRI and back exactly.
//
// "Unreserved" is RFC 3987's iunreserved: ASCII letters, digits and -._~,
// plus the non-ASCII characters an IRI may carry as themselves. That keeps a
// Chinese entity name readable in a SPARQL answer instead of turning it into
// a wall of %E4%B8%AD. Two exceptions within that range are encoded anyway:
// whitespace and invisible characters. Both are legal in an IRI and both make
// two IRIs that differ look identical to whoever reads them, which in a query
// someone has to retype is the same as being wrong.
//
// ':' and '/' are reserved and encoded, so an id like entity:abc123 becomes
// entity%3Aabc123. The tokenizer accepts % in a prefixed name, so the short
// form cxn:entity%3Aabc123 works as well as the full IRI.
func escapeProjectionName(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); {
		r, size := utf8.DecodeRuneInString(value[i:])
		if r != utf8.RuneError || size > 1 {
			if isProjectionUnreserved(r) {
				b.WriteString(value[i : i+size])
				i += size
				continue
			}
		}
		for j := i; j < i+size; j++ {
			fmt.Fprintf(&b, "%%%02X", value[j])
		}
		i += size
	}
	return b.String()
}

// unescapeProjectionName reverses escapeProjectionName, and accepts only its
// canonical output.
//
// Strict rather than forgiving, because SPARQL compares terms by their exact
// spelling: were <cxn:entity:abc> accepted as another name for
// <cxn:entity%3Aabc>, FindTriples would find the node and the query engine
// would then discard the answer for not matching the pattern it asked with.
// One node, one IRI, and a misspelled one matches nothing.
func unescapeProjectionName(value string) (string, bool) {
	if value == "" {
		return "", false
	}
	out := make([]byte, 0, len(value))
	for i := 0; i < len(value); i++ {
		if value[i] != '%' {
			out = append(out, value[i])
			continue
		}
		if i+2 >= len(value) {
			return "", false
		}
		decoded, err := strconv.ParseUint(value[i+1:i+3], 16, 8)
		if err != nil {
			return "", false
		}
		out = append(out, byte(decoded))
		i += 2
	}
	name := string(out)
	if escapeProjectionName(name) != value {
		return "", false
	}
	return name, true
}

func isProjectionUnreserved(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '-', r == '.', r == '_', r == '~':
		return true
	case r < 0xA0:
		return false
	case unicode.IsSpace(r) || !unicode.IsPrint(r):
		return false
	}
	// RFC 3987 ucschar.
	switch {
	case r <= 0xD7FF, r >= 0xF900 && r <= 0xFDCF, r >= 0xFDF0 && r <= 0xFFEF:
		return true
	case r >= 0xE0000 && r < 0xE1000:
		return false
	case r >= 0x10000 && r <= 0xEFFFD:
		return r&0xFFFF <= 0xFFFD
	}
	return false
}

func decodeProjectionIRI(iri, namespace string) (string, bool) {
	if !strings.HasPrefix(iri, namespace) {
		return "", false
	}
	return unescapeProjectionName(strings.TrimPrefix(iri, namespace))
}

func isPropertyGraphTerm(term *RDFTerm) bool {
	return term != nil && term.Kind == RDFTermIRI && strings.TrimSpace(term.Value) == PropertyGraphIRI
}

// projectionPlan is a TriplePattern translated into what it can match in the
// property graph. Every bound position narrows the SQL, and a position bound
// to something the projection never produces ends the plan before any SQL is
// issued at all: SPARQL calls FindTriples once per binding per pattern, so a
// projection that scanned the graph on every call would make a three-pattern
// join over a few thousand nodes take minutes.
type projectionPlan struct {
	subjectID string

	nodeTypes bool // rdf:type triples
	labels    bool // rdfs:label triples
	props     bool // cxp: triples
	edges     bool // cxr: triples

	propKey  string // set when the predicate names one key
	edgeType string // set when the predicate names one relation
	edgeID   string // set only when resolving one projected triple by ID

	objectNodeID string
	objectType   string
	objectLit    *RDFTerm
}

func (p projectionPlan) wantsNodes() bool {
	return p.nodeTypes || p.labels || p.props
}

// planProjection reports false when the pattern cannot match any projected
// triple, which is most patterns SPARQL issues against a brain that also
// holds real RDF.
func (g *GraphStore) planProjection(ctx context.Context, pattern TriplePattern) (projectionPlan, bool, error) {
	plan := projectionPlan{nodeTypes: true, labels: true, props: true, edges: true}
	if !g.PropertyGraphProjectionEnabled() {
		return plan, false, nil
	}
	if pattern.Inferred != nil && *pattern.Inferred {
		return plan, false, nil
	}

	namespaces, err := g.projectionNamespaces(ctx, pattern)
	if err != nil {
		return plan, false, err
	}

	if pattern.Graph != nil {
		graphTerm, err := normalizeTermWithNamespaces(*pattern.Graph, rdfPositionGraph, namespaces)
		if err != nil {
			return plan, false, err
		}
		if !isPropertyGraphTerm(&graphTerm) {
			return plan, false, nil
		}
	}

	if pattern.Subject != nil {
		subject, err := normalizeTermWithNamespaces(*pattern.Subject, rdfPositionSubject, namespaces)
		if err != nil {
			return plan, false, err
		}
		if subject.Kind != RDFTermIRI {
			return plan, false, nil
		}
		id, ok := decodeProjectionIRI(subject.Value, PropertyNodeNamespace)
		if !ok {
			return plan, false, nil
		}
		plan.subjectID = id
	}

	if pattern.Predicate != nil {
		predicate, err := normalizeTermWithNamespaces(*pattern.Predicate, rdfPositionPredicate, namespaces)
		if err != nil {
			return plan, false, err
		}
		plan.nodeTypes, plan.labels, plan.props, plan.edges = false, false, false, false
		switch value := predicate.Value; {
		case value == projectionRDFTypeIRI:
			plan.nodeTypes = true
		case value == projectionRDFSLabelIRI:
			plan.labels = true
		case strings.HasPrefix(value, PropertyRelNamespace):
			rel, ok := decodeProjectionIRI(value, PropertyRelNamespace)
			if !ok {
				return plan, false, nil
			}
			plan.edges, plan.edgeType = true, rel
		case strings.HasPrefix(value, PropertyPropNamespace):
			key, ok := decodeProjectionIRI(value, PropertyPropNamespace)
			if !ok {
				return plan, false, nil
			}
			plan.props, plan.propKey = true, key
		default:
			return plan, false, nil
		}
	}

	if pattern.Object != nil {
		object, err := normalizeTermWithNamespaces(*pattern.Object, rdfPositionObject, namespaces)
		if err != nil {
			return plan, false, err
		}
		switch object.Kind {
		case RDFTermIRI:
			if id, ok := decodeProjectionIRI(object.Value, PropertyNodeNamespace); ok {
				plan.objectNodeID = id
				plan.nodeTypes, plan.labels, plan.props = false, false, false
			} else if nodeType, ok := decodeProjectionIRI(object.Value, PropertyTypeNamespace); ok {
				plan.objectType = nodeType
				plan.labels, plan.props, plan.edges = false, false, false
			} else {
				return plan, false, nil
			}
		case RDFTermLiteral:
			// Compared against the value as the caller wrote it, not as
			// normalization trimmed it: a property whose value has a trailing
			// space is still that value, and a binding carried over from an
			// earlier answer must find the triple it came from.
			literal := object
			literal.Value = pattern.Object.Value
			plan.objectLit = &literal
			plan.nodeTypes, plan.edges = false, false
		default:
			return plan, false, nil
		}
	}

	if !plan.wantsNodes() && !plan.edges {
		return plan, false, nil
	}
	return plan, true, nil
}

// projectionNamespaces loads the namespace table only when some term in the
// pattern is written in prefixed form. SPARQL hands FindTriples expanded IRIs,
// so the common case costs no query at all.
func (g *GraphStore) projectionNamespaces(ctx context.Context, pattern TriplePattern) ([]Namespace, error) {
	for _, term := range []*RDFTerm{pattern.Subject, pattern.Predicate, pattern.Object, pattern.Graph} {
		if term == nil {
			continue
		}
		iri := term.Value
		if term.Kind == RDFTermLiteral {
			iri = term.Datatype
		} else if term.Kind != RDFTermIRI && term.Kind != "" {
			continue
		}
		iri = strings.TrimSpace(strings.Trim(iri, "<>"))
		if iri != "" && !looksLikeAbsoluteIRI(iri) {
			return g.ListNamespaces(ctx)
		}
	}
	return nil, nil
}

// findProjectedTriples returns the projected triples matching a pattern, at
// most limit of them when limit is positive. Node triples come first ordered
// by node id, then edge triples ordered by edge id, so two reads of one graph
// agree on order as well as content.
func (g *GraphStore) findProjectedTriples(ctx context.Context, pattern TriplePattern, limit int) ([]RDFTriple, error) {
	plan, ok, err := g.planProjection(ctx, pattern)
	if err != nil || !ok {
		return nil, err
	}
	return g.runProjection(ctx, plan, limit)
}

func (g *GraphStore) runProjection(ctx context.Context, plan projectionPlan, limit int) ([]RDFTriple, error) {
	out := make([]RDFTriple, 0)
	full := func() bool { return limit > 0 && len(out) >= limit }

	if plan.wantsNodes() {
		if err := g.projectNodes(ctx, plan, func(triple RDFTriple) bool {
			out = append(out, triple)
			return !full()
		}); err != nil {
			return nil, err
		}
	}
	if plan.edges && !full() {
		remaining := 0
		if limit > 0 {
			remaining = limit - len(out)
		}
		edges, err := g.projectEdges(ctx, plan, remaining)
		if err != nil {
			return nil, err
		}
		out = append(out, edges...)
	}
	return out, nil
}

// projectionNodeFilter excludes the nodes RDF writes mirror its own terms
// into. Both conditions, because each alone could catch a real node: a caller
// may choose an id beginning "rdf:", and nothing stops a writer naming a
// type rdf_resource, but only the mirror does both.
func projectionNodeFilter() string {
	return `NOT (n.id LIKE 'rdf:%' AND COALESCE(n.node_type, '') IN ('` +
		strings.Join(projectionRDFMirrorKind, "', '") + `'))`
}

func (g *GraphStore) projectNodes(ctx context.Context, plan projectionPlan, emit func(RDFTriple) bool) error {
	from := "graph_nodes n"
	conditions := []string{projectionNodeFilter()}
	args := make([]any, 0, 4)

	if plan.subjectID != "" {
		conditions = append(conditions, "n.id = ?")
		args = append(args, plan.subjectID)
	}
	// The kind-specific narrowing applies only when the plan wants exactly
	// one kind of node triple; an unbound predicate wants all three and can
	// only be narrowed by subject.
	switch {
	case plan.nodeTypes && !plan.labels && !plan.props:
		if plan.objectType != "" {
			conditions = append(conditions, "n.node_type = ?")
			args = append(args, plan.objectType)
		} else {
			conditions = append(conditions, "COALESCE(n.node_type, '') <> ''")
		}
	case plan.labels && !plan.nodeTypes && !plan.props:
		name := g.dialect.JSONTextGuarded("n.properties", "name")
		title := g.dialect.JSONTextGuarded("n.properties", "title")
		if plan.objectLit != nil {
			conditions = append(conditions, "(("+name+") = ? OR ("+title+") = ?)")
			args = append(args, plan.objectLit.Value, plan.objectLit.Value)
		} else {
			conditions = append(conditions, "(("+name+") IS NOT NULL OR ("+title+") IS NOT NULL)")
		}
	case plan.props && plan.propKey != "" && !plan.nodeTypes && !plan.labels:
		// The key is bound as a parameter against the enumerated keys rather
		// than spliced into a JSON path: a key with a dot or a quote in it is
		// a different path to json_extract, and exactly itself to je.key.
		from += g.dialect.JSONEachEntry("n.properties")
		conditions = append(conditions, "je.key = ?")
		args = append(args, plan.propKey)
		if lit := plan.objectLit; lit != nil && literalPrefilterSafe(*lit) {
			// A superset, never a subset: the exact comparison happens in Go.
			// Only a value that can have come from nothing but a JSON string
			// is compared here, because numbers and booleans read back
			// differently on the two databases. The second arm keeps arrays,
			// whose elements are compared one by one in Go.
			conditions = append(conditions, "(CAST(je.value AS TEXT) = ? OR LTRIM(CAST(je.value AS TEXT)) LIKE '[%')")
			args = append(args, lit.Value)
		}
	}

	query := "SELECT n.id, COALESCE(n.node_type, ''), n.properties FROM " + from +
		" WHERE " + strings.Join(conditions, " AND ") + " ORDER BY n.id"
	rows, err := g.query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("project property-graph nodes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	lastID := ""
	for rows.Next() {
		var id, nodeType string
		var properties sql.NullString
		if err := rows.Scan(&id, &nodeType, &properties); err != nil {
			return fmt.Errorf("project property-graph nodes: %w", err)
		}
		if id == lastID {
			continue // a JSON object with a repeated key enumerates twice
		}
		lastID = id
		for _, triple := range projectNodeTriples(id, nodeType, properties.String) {
			if !plan.matchesNodeTriple(triple) {
				continue
			}
			if !emit(triple) {
				return nil
			}
		}
	}
	return rows.Err()
}

// literalPrefilterSafe reports whether SQL can compare a literal against a
// stored JSON value without risk of excluding a genuine match.
func literalPrefilterSafe(lit RDFTerm) bool {
	if lit.Language != "" {
		return false
	}
	if lit.Datatype != "" && lit.Datatype != builtinNamespaces["xsd"]+"string" {
		return false
	}
	if lit.Value == "true" || lit.Value == "false" {
		return false
	}
	if _, err := strconv.ParseFloat(strings.TrimSpace(lit.Value), 64); err == nil {
		return false
	}
	return true
}

func (p projectionPlan) matchesNodeTriple(triple RDFTriple) bool {
	switch predicate := triple.Predicate.Value; {
	case predicate == projectionRDFTypeIRI:
		if !p.nodeTypes {
			return false
		}
		if p.objectType != "" {
			return triple.Object.Value == PropertyTypeNamespace+escapeProjectionName(p.objectType)
		}
	case predicate == projectionRDFSLabelIRI:
		if !p.labels {
			return false
		}
	default:
		if !p.props {
			return false
		}
		if p.propKey != "" && predicate != PropertyPropNamespace+escapeProjectionName(p.propKey) {
			return false
		}
	}
	if p.objectLit != nil {
		return projectionLiteralMatches(*p.objectLit, triple.Object)
	}
	return true
}

// projectionLiteralMatches follows the rule FindTriples applies to kg_triples:
// the value must match, and the datatype and language only when the pattern
// names one.
func projectionLiteralMatches(pattern, value RDFTerm) bool {
	if value.Kind != RDFTermLiteral || value.Value != pattern.Value {
		return false
	}
	if pattern.Datatype != "" && pattern.Datatype != value.Datatype {
		return false
	}
	if pattern.Language != "" && pattern.Language != value.Language {
		return false
	}
	return true
}

// projectNodeTriples is every triple one node implies, in a fixed order:
// its type, its label, then its properties by key.
func projectNodeTriples(id, nodeType, propertiesJSON string) []RDFTriple {
	escapedID := escapeProjectionName(id)
	subject := NewIRI(PropertyNodeNamespace + escapedID)
	out := make([]RDFTriple, 0, 4)

	if nodeType != "" {
		out = append(out, projectedTriple("type:"+escapedID, subject,
			NewIRI(projectionRDFTypeIRI), NewIRI(PropertyTypeNamespace+escapeProjectionName(nodeType))))
	}

	properties := decodeProjectionProperties(propertiesJSON)
	if label := projectionLabel(properties); label != "" {
		out = append(out, projectedTriple("label:"+escapedID, subject,
			NewIRI(projectionRDFSLabelIRI), NewLiteral(label)))
	}

	keys := make([]string, 0, len(properties))
	for key := range properties {
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		values := []any{properties[key]}
		if list, ok := properties[key].([]any); ok {
			values = list
		}
		escapedKey := escapeProjectionName(key)
		predicate := NewIRI(PropertyPropNamespace + escapedKey)
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			literal, ok := projectedLiteral(value)
			if !ok {
				continue
			}
			digest := sha1.Sum([]byte(literal.Datatype + "|" + literal.Value))
			suffix := "prop:" + escapedID + ":" + escapedKey + ":" + hex.EncodeToString(digest[:6])
			if _, dup := seen[suffix]; dup {
				continue // RDF is a set: ["a", "a"] is one triple
			}
			seen[suffix] = struct{}{}
			out = append(out, projectedTriple(suffix, subject, predicate, literal))
		}
	}
	return out
}

func projectedTriple(idSuffix string, subject, predicate, object RDFTerm) RDFTriple {
	graphTerm := NewIRI(PropertyGraphIRI)
	return RDFTriple{
		ID:        projectedTripleIDPrefix + idSuffix,
		Subject:   subject,
		Predicate: predicate,
		Object:    object,
		Graph:     &graphTerm,
	}
}

// decodeProjectionProperties reads a properties column as a JSON object and
// yields nothing for anything else — NULL, the empty string an edge written
// without properties carries, a bare string or array, or text that is not
// JSON at all. One odd row must cost its own triples, not the whole answer.
func decodeProjectionProperties(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var properties map[string]any
	if err := decoder.Decode(&properties); err != nil {
		return nil
	}
	return properties
}

// projectionLabel is the node's name, or failing that its title. Only a
// non-blank string counts: a label is what a person reads, and a number
// under "name" is an identifier someone put in the wrong field.
func projectionLabel(properties map[string]any) string {
	for _, key := range []string{"name", "title"} {
		if value, ok := properties[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// projectedLiteral turns one JSON scalar into a literal. Non-integer numbers
// become xsd:decimal because that is what this engine's SPARQL parser makes
// of 1.5, so a number written in a query matches the number in the graph.
func projectedLiteral(value any) (RDFTerm, bool) {
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return RDFTerm{}, false
		}
		return NewLiteral(v), true
	case bool:
		return NewTypedLiteral(strconv.FormatBool(v), projectionXSDBoolean), true
	case json.Number:
		text := v.String()
		if isJSONInteger(text) {
			return NewTypedLiteral(text, projectionXSDInteger), true
		}
		f, err := v.Float64()
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return RDFTerm{}, false
		}
		return NewTypedLiteral(strconv.FormatFloat(f, 'f', -1, 64), projectionXSDDecimal), true
	}
	return RDFTerm{}, false
}

func isJSONInteger(text string) bool {
	digits := strings.TrimPrefix(text, "-")
	if digits == "" {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// projectEdges returns the triples the property graph's edges imply.
//
// An edge with no type is skipped rather than given a stand-in predicate such
// as cxr:related. Extraction models emit "related" as a real type, so a
// stand-in would make an untyped edge indistinguishable from one somebody
// meant, and a query for cxr:related would answer with both.
//
// The mirror edge of a stored triple carries "rdf": true in its properties
// and is excluded, for the reason the node mirror is.
func (g *GraphStore) projectEdges(ctx context.Context, plan projectionPlan, limit int) ([]RDFTriple, error) {
	if plan.objectType != "" || plan.objectLit != nil {
		return nil, nil
	}
	conditions := []string{
		"COALESCE(e.edge_type, '') <> ''",
		g.dialect.JSONFlag("e.properties", "rdf") + " = 0",
	}
	args := make([]any, 0, 4)
	if plan.subjectID != "" {
		conditions = append(conditions, "e.from_node_id = ?")
		args = append(args, plan.subjectID)
	}
	if plan.objectNodeID != "" {
		conditions = append(conditions, "e.to_node_id = ?")
		args = append(args, plan.objectNodeID)
	}
	if plan.edgeType != "" {
		conditions = append(conditions, "e.edge_type = ?")
		args = append(args, plan.edgeType)
	}
	if plan.edgeID != "" {
		conditions = append(conditions, "e.id = ?")
		args = append(args, plan.edgeID)
	}
	query := "SELECT e.id, e.from_node_id, e.to_node_id, e.edge_type FROM graph_edges e WHERE " +
		strings.Join(conditions, " AND ") + " ORDER BY e.id"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := g.query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("project property-graph edges: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]RDFTriple, 0)
	for rows.Next() {
		var id, from, to, edgeType string
		if err := rows.Scan(&id, &from, &to, &edgeType); err != nil {
			return nil, fmt.Errorf("project property-graph edges: %w", err)
		}
		out = append(out, projectedTriple("edge:"+escapeProjectionName(id),
			NewIRI(PropertyGraphNodeIRI(from)),
			NewIRI(PropertyRelNamespace+escapeProjectionName(edgeType)),
			NewIRI(PropertyGraphNodeIRI(to))))
	}
	return out, rows.Err()
}

// getProjectedTriple resolves a projected triple's ID back to the triple, so
// ExplainTriple and an inference's support IDs can name a projected triple
// and still be followed.
func (g *GraphStore) getProjectedTriple(ctx context.Context, id string) (*RDFTriple, error) {
	notFound := fmt.Errorf("triple not found: %s", id)
	if !g.PropertyGraphProjectionEnabled() {
		return nil, notFound
	}
	rest := strings.TrimPrefix(id, projectedTripleIDPrefix)
	kind, rest, _ := strings.Cut(rest, ":")
	escapedName, _, _ := strings.Cut(rest, ":")
	name, ok := unescapeProjectionName(escapedName)
	if !ok {
		return nil, notFound
	}

	plan := projectionPlan{}
	switch kind {
	case "edge":
		plan.edges, plan.edgeID = true, name
	case "type", "label", "prop":
		plan.nodeTypes, plan.labels, plan.props, plan.subjectID = true, true, true, name
	default:
		return nil, notFound
	}
	triples, err := g.runProjection(ctx, plan, 0)
	if err != nil {
		return nil, err
	}
	for i := range triples {
		if triples[i].ID == id {
			return &triples[i], nil
		}
	}
	return nil, notFound
}

// projectionHolds reports whether the projection currently supplies this
// (already normalized) triple. A triple with no graph counts, because SPARQL
// reads the projection as part of the default graph and a DELETE DATA
// written without GRAPH is aimed at what that query showed.
func (g *GraphStore) projectionHolds(ctx context.Context, triple RDFTriple) (bool, error) {
	if !g.PropertyGraphProjectionEnabled() {
		return false, nil
	}
	if triple.Graph != nil && !isPropertyGraphTerm(triple.Graph) {
		return false, nil
	}
	subject, predicate, object := triple.Subject, triple.Predicate, triple.Object
	matches, err := g.findProjectedTriples(ctx, TriplePattern{
		Subject:   &subject,
		Predicate: &predicate,
		Object:    &object,
	}, 1)
	if err != nil {
		return false, err
	}
	return len(matches) > 0, nil
}

// refuseProjectedDeletes fails, before anything is removed, if any of these
// triples is one only the projection supplies. Checking them all first is
// what keeps a mixed DELETE from removing the stored half and then failing
// on the rest.
func (g *GraphStore) refuseProjectedDeletes(ctx context.Context, triples []RDFTriple) error {
	for _, triple := range triples {
		if strings.HasPrefix(triple.ID, projectedTripleIDPrefix) {
			return fmt.Errorf("%w: %s", ErrPropertyGraphReadOnly, triple.String())
		}
		normalized, err := g.normalizeTriple(ctx, triple)
		if err != nil {
			return err
		}
		holds, err := g.projectionHolds(ctx, normalized)
		if err != nil {
			return err
		}
		if !holds {
			continue
		}
		if normalized.ID == "" {
			normalized.ID = tripleDigest(normalized)
		}
		var stored int
		if err := g.queryRow(ctx, `SELECT COUNT(*) FROM kg_triples WHERE id = ?`, normalized.ID).Scan(&stored); err != nil {
			return fmt.Errorf("check stored triple: %w", err)
		}
		if stored == 0 {
			return fmt.Errorf("%w: %s", ErrPropertyGraphReadOnly, triple.String())
		}
	}
	return nil
}

// refuseProjectedInserts rejects a write into the projection's graph. The
// name is reserved whether or not the projection is on: a triple stored
// there would sit beside the projected ones indistinguishable from them, and
// then refuse to be deleted as one of them.
func refuseProjectedInserts(triples []RDFTriple) error {
	for _, triple := range triples {
		if isPropertyGraphTerm(triple.Graph) {
			return fmt.Errorf("graph <%s> is the read-only property-graph projection; write nodes and edges with the graph API instead: %s",
				PropertyGraphIRI, triple.String())
		}
	}
	return nil
}
