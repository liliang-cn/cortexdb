package graph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/piprate/json-gold/ld"
)

// JSON-LD is the format most structured data on the web actually arrives in
// (schema.org markup, Wikidata and DBpedia dumps, API payloads), so it is the
// one callers will have in hand rather than one they convert to.
//
// Import is delegated to json-gold, the conformance-tested Go processor, and
// not to a hand-written subset: the part of JSON-LD people meet in practice
// includes scoped contexts, language and index maps, @nest, @included and
// @reverse, and a subset that silently mis-reads any of those produces wrong
// triples without an error. What json-gold is not allowed to do is reach the
// network; see offlineContextLoader.
//
// Export is written here rather than through json-gold's FromRDF+Compact,
// because the output has to be byte-for-byte deterministic to be diffed and
// tested, and it has to use exactly the store's registered prefixes.

const xsdString = "http://www.w3.org/2001/XMLSchema#string"

// schemaOrgContextURLs are the addresses documents use to name schema.org's
// context. The published context (about 200 KB, and revised with every
// schema.org release) is not embedded and never fetched. Read as a term
// mapping it says very little: "@vocab": "http://schema.org/", the aliases
// id -> @id and type -> @type, HTML -> rdf:HTML, some seventy vocabulary
// prefixes, and three thousand definitions of the form name -> schema:name,
// which the @vocab already implies. It declares no type coercions. That
// much is reproduced by schemaOrgContext, so for term expansion the
// approximation is faithful, with one deliberate difference:
//
// The published @vocab (and "schema" prefix) is http://schema.org/. Here it
// is https://schema.org/, the same IRI space as the store's built-in
// "schema:" namespace, so imported facts join facts written through that
// prefix instead of forming a parallel http:// vocabulary that no query
// written against schema: would match.
var schemaOrgContextURLs = map[string]bool{
	"http://schema.org":                              true,
	"http://schema.org/":                             true,
	"https://schema.org":                             true,
	"https://schema.org/":                            true,
	"http://schema.org/docs/jsonldcontext.json":      true,
	"https://schema.org/docs/jsonldcontext.json":     true,
	"http://schema.org/docs/jsonldcontext.jsonld":    true,
	"https://schema.org/docs/jsonldcontext.jsonld":   true,
	"http://schema.org/docs/jsonldcontext.json.txt":  true,
	"https://schema.org/docs/jsonldcontext.json.txt": true,
}

// schemaOrgContextPrefixes are the vocabulary prefixes schema.org's published
// context declares, as of the release current in September 2026. A document
// under that context may write "dct:title" and expect the prefix to be
// known; without these it would silently become an IRI with the scheme
// "dct".
var schemaOrgContextPrefixes = map[string]string{
	"bibo":              "http://purl.org/ontology/bibo/",
	"brick":             "https://brickschema.org/schema/Brick#",
	"cmns-cls":          "https://www.omg.org/spec/Commons/Classifiers/",
	"cmns-col":          "https://www.omg.org/spec/Commons/Collections/",
	"cmns-dt":           "https://www.omg.org/spec/Commons/DatesAndTimes/",
	"cmns-ge":           "https://www.omg.org/spec/Commons/GeopoliticalEntities/",
	"cmns-id":           "https://www.omg.org/spec/Commons/Identifiers/",
	"cmns-loc":          "https://www.omg.org/spec/Commons/Locations/",
	"cmns-q":            "https://www.omg.org/spec/Commons/Quantities/",
	"cmns-txt":          "https://www.omg.org/spec/Commons/Text/",
	"csvw":              "http://www.w3.org/ns/csvw#",
	"dc":                "http://purl.org/dc/elements/1.1/",
	"dcam":              "http://purl.org/dc/dcam/",
	"dcat":              "http://www.w3.org/ns/dcat#",
	"dct":               "http://purl.org/dc/terms/",
	"dctype":            "http://purl.org/dc/dcmitype/",
	"doap":              "http://usefulinc.com/ns/doap#",
	"eli":               "http://data.europa.eu/eli/ontology#",
	"fibo-be-corp-corp": "https://spec.edmcouncil.org/fibo/ontology/BE/Corporations/Corporations/",
	"fibo-be-ge-ge":     "https://spec.edmcouncil.org/fibo/ontology/BE/GovernmentEntities/GovernmentEntities/",
	"fibo-be-le-cb":     "https://spec.edmcouncil.org/fibo/ontology/BE/LegalEntities/CorporateBodies/",
	"fibo-be-le-lp":     "https://spec.edmcouncil.org/fibo/ontology/BE/LegalEntities/LegalPersons/",
	"fibo-be-nfp-nfp":   "https://spec.edmcouncil.org/fibo/ontology/BE/NotForProfitOrganizations/NotForProfitOrganizations/",
	"fibo-be-oac-cctl":  "https://spec.edmcouncil.org/fibo/ontology/BE/OwnershipAndControl/CorporateControl/",
	"fibo-fbc-dae-dbt":  "https://spec.edmcouncil.org/fibo/ontology/FBC/DebtAndEquities/Debt/",
	"fibo-fbc-pas-fpas": "https://spec.edmcouncil.org/fibo/ontology/FBC/ProductsAndServices/FinancialProductsAndServices/",
	"fibo-fnd-acc-cur":  "https://spec.edmcouncil.org/fibo/ontology/FND/Accounting/CurrencyAmount/",
	"fibo-fnd-agr-ctr":  "https://spec.edmcouncil.org/fibo/ontology/FND/Agreements/Contracts/",
	"fibo-fnd-arr-doc":  "https://spec.edmcouncil.org/fibo/ontology/FND/Arrangements/Documents/",
	"fibo-fnd-arr-lif":  "https://spec.edmcouncil.org/fibo/ontology/FND/Arrangements/Lifecycles/",
	"fibo-fnd-dt-oc":    "https://spec.edmcouncil.org/fibo/ontology/FND/DatesAndTimes/Occurrences/",
	"fibo-fnd-org-org":  "https://spec.edmcouncil.org/fibo/ontology/FND/Organizations/Organizations/",
	"fibo-fnd-pas-pas":  "https://spec.edmcouncil.org/fibo/ontology/FND/ProductsAndServices/ProductsAndServices/",
	"fibo-fnd-plc-adr":  "https://spec.edmcouncil.org/fibo/ontology/FND/Places/Addresses/",
	"fibo-fnd-plc-fac":  "https://spec.edmcouncil.org/fibo/ontology/FND/Places/Facilities/",
	"fibo-fnd-plc-loc":  "https://spec.edmcouncil.org/fibo/ontology/FND/Places/Locations/",
	"fibo-fnd-pty-pty":  "https://spec.edmcouncil.org/fibo/ontology/FND/Parties/Parties/",
	"fibo-fnd-rel-rel":  "https://spec.edmcouncil.org/fibo/ontology/FND/Relations/Relations/",
	"fibo-pay-ps-ps":    "https://spec.edmcouncil.org/fibo/ontology/PAY/PaymentServices/PaymentServices/",
	"foaf":              "http://xmlns.com/foaf/0.1/",
	"gleif-L1":          "https://www.gleif.org/ontology/L1/",
	"gs1":               "https://ref.gs1.org/voc/",
	"hydra":             "http://www.w3.org/ns/hydra/core#",
	"lcc-3166-1":        "https://www.omg.org/spec/LCC/Countries/ISO3166-1-CountryCodes/",
	"lcc-4217":          "https://www.omg.org/spec/LCC/Countries/ISO4217-CurrencyCodes/",
	"lcc-cr":            "https://www.omg.org/spec/LCC/Countries/CountryRepresentation/",
	"lcc-lr":            "https://www.omg.org/spec/LCC/Languages/LanguageRepresentation/",
	"lrmoo":             "http://iflastandards.info/ns/lrm/lrmoo/",
	"mo":                "http://purl.org/ontology/mo/",
	"odrl":              "http://www.w3.org/ns/odrl/2/",
	"og":                "http://ogp.me/ns#",
	"org":               "http://www.w3.org/ns/org#",
	"owl":               "http://www.w3.org/2002/07/owl#",
	"prof":              "http://www.w3.org/ns/dx/prof/",
	"prov":              "http://www.w3.org/ns/prov#",
	"qb":                "http://purl.org/linked-data/cube#",
	"rdf":               "http://www.w3.org/1999/02/22-rdf-syntax-ns#",
	"rdfs":              "http://www.w3.org/2000/01/rdf-schema#",
	"sarif":             "http://sarif.info/",
	"sh":                "http://www.w3.org/ns/shacl#",
	"skos":              "http://www.w3.org/2004/02/skos/core#",
	"snomed":            "http://purl.bioontology.org/ontology/SNOMEDCT/",
	"sosa":              "http://www.w3.org/ns/sosa/",
	"ssn":               "http://www.w3.org/ns/ssn/",
	"time":              "http://www.w3.org/2006/time#",
	"unece":             "http://unece.org/vocab#",
	"vann":              "http://purl.org/vocab/vann/",
	"vcard":             "http://www.w3.org/2006/vcard/ns#",
	"void":              "http://rdfs.org/ns/void#",
	"wgs":               "https://www.w3.org/2003/01/geo/wgs84_pos#",
	"xml":               "http://www.w3.org/XML/1998/namespace",
	"xsd":               "http://www.w3.org/2001/XMLSchema#",
}

// schemaOrgContext builds a fresh context each time because json-gold may
// keep references into the document it is given.
func schemaOrgContext() map[string]any {
	context := make(map[string]any, len(schemaOrgContextPrefixes)+5)
	for prefix, iri := range schemaOrgContextPrefixes {
		context[prefix] = iri
	}
	context["@vocab"] = "https://schema.org/"
	context["schema"] = "https://schema.org/"
	context["id"] = "@id"
	context["type"] = "@type"
	context["HTML"] = map[string]any{"@id": "rdf:HTML"}
	return context
}

// ErrRemoteJSONLDContext is returned when a JSON-LD document names a context
// by URL that is not one of the built-in ones. Such a context is never
// fetched.
var ErrRemoteJSONLDContext = errors.New("remote JSON-LD @context is not fetched")

// offlineContextLoader is the only document loader the importer ever hands to
// json-gold. json-gold's own default loader dereferences any URL a document
// names, over HTTP or from the local filesystem, which in a server importing
// a caller's file is both server-side request forgery and a data exfiltration
// channel (the URL itself carries whatever the document author chose). This
// loader performs no I/O at all: it answers the schema.org names from memory
// and refuses everything else with an error that names the URL, so the caller
// knows exactly which context to inline.
type offlineContextLoader struct{}

func (offlineContextLoader) LoadDocument(u string) (*ld.RemoteDocument, error) {
	if schemaOrgContextURLs[u] {
		return &ld.RemoteDocument{DocumentURL: u, Document: map[string]any{"@context": schemaOrgContext()}}, nil
	}
	return nil, fmt.Errorf("%w: %q; inline that context object in the document instead of referencing it by URL", ErrRemoteJSONLDContext, u)
}

func jsonldOptions(base string) *ld.JsonLdOptions {
	opts := ld.NewJsonLdOptions(base)
	// NewJsonLdOptions installs an HTTP loader; it must never survive to a
	// processor call, so it is replaced before the options leave this function.
	opts.DocumentLoader = offlineContextLoader{}
	opts.ProcessingMode = ld.JsonLd_1_1
	opts.ProduceGeneralizedRdf = false
	return opts
}

func (g *GraphStore) importJSONLD(ctx context.Context, reader io.Reader) (int, error) {
	payload, err := io.ReadAll(reader)
	if err != nil {
		return 0, fmt.Errorf("read jsonld payload: %w", err)
	}
	triples, err := parseJSONLD(payload, g.importBaseIRI())
	if err != nil {
		return 0, err
	}
	if len(triples) == 0 {
		return 0, nil
	}
	result, err := g.UpsertTriplesBatch(ctx, triples)
	if err != nil {
		return 0, err
	}
	if result.FailedCount > 0 {
		return result.SuccessCount, result.Errors[0]
	}
	return result.SuccessCount, nil
}

// parseJSONLD turns a JSON-LD document into triples and quads, resolving
// relative IRIs against base unless the document sets its own @base.
//
// Blank nodes are relabeled with a prefix derived from the payload's hash.
// json-gold numbers them _:b0, _:b1, … in every document, and the store keys
// blank nodes by label, so without the prefix the anonymous address of one
// imported person would become the address of every other. Hashing the
// payload rather than drawing a random prefix keeps re-importing the same
// file idempotent.
//
// Numbers are decoded as float64, as the JSON-LD algorithms specify, so an
// integer beyond 2^53 loses precision exactly as it does in every JavaScript
// processor; a document that needs exact large integers should give them as
// typed string literals.
func parseJSONLD(payload []byte, base string) (triples []*RDFTriple, err error) {
	// json-gold is a large processor fed attacker-shaped input; a panic in it
	// must surface as a failed import, not take the server down with it.
	defer func() {
		if r := recover(); r != nil {
			triples = nil
			err = fmt.Errorf("process jsonld document: invalid input (%v)", r)
		}
	}()

	decoder := json.NewDecoder(bytes.NewReader(payload))
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("parse jsonld document: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("parse jsonld document: unexpected data after the top-level value")
	}
	switch document.(type) {
	case map[string]any, []any:
	default:
		return nil, fmt.Errorf("parse jsonld document: top-level value must be an object or array")
	}

	out, err := ld.NewJsonLdProcessor().ToRDF(document, jsonldOptions(base))
	if err != nil {
		return nil, fmt.Errorf("process jsonld document: %w", err)
	}
	dataset, ok := out.(*ld.RDFDataset)
	if !ok {
		return nil, fmt.Errorf("process jsonld document: unexpected result %T", out)
	}

	sum := sha256.Sum256(payload)
	labelPrefix := "j" + hex.EncodeToString(sum[:6]) + "_"

	graphNames := make([]string, 0, len(dataset.Graphs))
	for name := range dataset.Graphs {
		graphNames = append(graphNames, name)
	}
	sort.Strings(graphNames)

	for _, name := range graphNames {
		for _, quad := range dataset.Graphs[name] {
			triple, err := jsonldQuadToTriple(quad, name, labelPrefix)
			if err != nil {
				return nil, err
			}
			triples = append(triples, triple)
		}
	}
	return triples, nil
}

func jsonldQuadToTriple(quad *ld.Quad, graphName, labelPrefix string) (*RDFTriple, error) {
	subject, err := jsonldNodeToTerm(quad.Subject, labelPrefix)
	if err != nil {
		return nil, err
	}
	predicate, err := jsonldNodeToTerm(quad.Predicate, labelPrefix)
	if err != nil {
		return nil, err
	}
	object, err := jsonldNodeToTerm(quad.Object, labelPrefix)
	if err != nil {
		return nil, err
	}
	triple := &RDFTriple{Subject: subject, Predicate: predicate, Object: object}
	if graphName != "" && graphName != "@default" {
		var graphTerm RDFTerm
		if strings.HasPrefix(graphName, "_:") {
			graphTerm = NewBlankNode(labelPrefix + strings.TrimPrefix(graphName, "_:"))
		} else {
			graphTerm = NewIRI(graphName)
		}
		triple.Graph = &graphTerm
	}
	return triple, nil
}

func jsonldNodeToTerm(node ld.Node, labelPrefix string) (RDFTerm, error) {
	switch value := node.(type) {
	case ld.IRI:
		return NewIRI(value.Value), nil
	case ld.BlankNode:
		return NewBlankNode(labelPrefix + strings.TrimPrefix(value.Attribute, "_:")), nil
	case ld.Literal:
		switch {
		case value.Language != "":
			return NewLangLiteral(value.Value, value.Language), nil
		case value.Datatype == "" || value.Datatype == xsdString:
			// RDF 1.1 makes "x" and "x"^^xsd:string the same term; the other
			// importers store it without a datatype, and so does this one.
			return NewLiteral(value.Value), nil
		default:
			return NewTypedLiteral(value.Value, value.Datatype), nil
		}
	default:
		return RDFTerm{}, fmt.Errorf("unsupported jsonld node type %T", node)
	}
}

// exportJSONLD writes the store as a flattened JSON-LD document: a @context
// holding the registered prefixes the output actually uses, and a @graph of
// one node object per subject. Named graphs are node objects with their own
// @graph. Everything is sorted and serialised through maps (which
// encoding/json writes in key order), so equal stores produce equal bytes.
//
// Literals keep their lexical form: typed values are written as
// {"@value": "<lexical>", "@type": ...} and never as native JSON numbers or
// booleans, which would re-canonicalise "01"^^xsd:integer into 1 on the way
// back in. Lists are written as the rdf:first/rdf:rest statements they are
// stored as; importing them yields the same statements.
func (g *GraphStore) exportJSONLD(ctx context.Context, writer io.Writer, triples []RDFTriple) error {
	namespaces, err := g.ListNamespaces(ctx)
	if err != nil {
		return err
	}
	compactor := newJSONLDCompactor(namespaces, triples)

	type nodeKey struct{ graph, subject string }
	nodes := make(map[nodeKey]map[string][]jsonldValue)
	types := make(map[nodeKey][]string)
	graphs := make(map[string]bool)

	for _, triple := range triples {
		graphID := ""
		if triple.Graph != nil {
			graphID = compactor.nodeID(*triple.Graph)
			graphs[graphID] = true
		}
		key := nodeKey{graph: graphID, subject: compactor.nodeID(triple.Subject)}
		if nodes[key] == nil {
			nodes[key] = make(map[string][]jsonldValue)
		}
		if triple.Predicate.Value == rdfTypeIRI && triple.Object.Kind == RDFTermIRI {
			types[key] = append(types[key], compactor.iri(triple.Object.Value))
			continue
		}
		property := compactor.iri(triple.Predicate.Value)
		nodes[key][property] = append(nodes[key][property], compactor.value(triple.Object))
	}

	buildNode := func(key nodeKey) map[string]any {
		node := map[string]any{"@id": key.subject}
		if list := types[key]; len(list) > 0 {
			sort.Strings(list)
			list = dedupeStrings(list)
			if len(list) == 1 {
				node["@type"] = list[0]
			} else {
				node["@type"] = list
			}
		}
		for property, values := range nodes[key] {
			sort.Slice(values, func(i, j int) bool { return values[i].sortKey < values[j].sortKey })
			rendered := make([]any, 0, len(values))
			for i, value := range values {
				if i > 0 && value.sortKey == values[i-1].sortKey {
					continue
				}
				rendered = append(rendered, value.json)
			}
			if len(rendered) == 1 {
				node[property] = rendered[0]
			} else {
				node[property] = rendered
			}
		}
		return node
	}

	subjectsByGraph := make(map[string][]string)
	for key := range nodes {
		subjectsByGraph[key.graph] = append(subjectsByGraph[key.graph], key.subject)
	}
	// A graph name that is never a subject in the default graph still needs a
	// node object to carry its @graph.
	for graphID := range graphs {
		key := nodeKey{graph: "", subject: graphID}
		if _, ok := nodes[key]; !ok {
			nodes[key] = map[string][]jsonldValue{}
			subjectsByGraph[""] = append(subjectsByGraph[""], graphID)
		}
	}

	topLevel := make([]any, 0, len(subjectsByGraph[""]))
	defaultSubjects := subjectsByGraph[""]
	sort.Strings(defaultSubjects)
	for _, subject := range defaultSubjects {
		node := buildNode(nodeKey{graph: "", subject: subject})
		if graphs[subject] {
			members := subjectsByGraph[subject]
			sort.Strings(members)
			inner := make([]any, 0, len(members))
			for _, member := range members {
				inner = append(inner, buildNode(nodeKey{graph: subject, subject: member}))
			}
			node["@graph"] = inner
		}
		topLevel = append(topLevel, node)
	}

	document := map[string]any{"@graph": topLevel}
	if context := compactor.context(); len(context) > 0 {
		document["@context"] = context
	}

	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(document)
}

type jsonldValue struct {
	sortKey string
	json    any
}

// jsonldCompactor decides which registered prefixes the exported @context
// may declare, and renders IRIs against exactly that set.
//
// A prefix cannot be declared when some IRI in the output that it does not
// compact has a scheme equal to its name: with "urn" declared as a prefix, a
// full IRI written as "urn:isbn:1" would be read back as the prefix's
// namespace followed by "isbn:1". Such prefixes are left out, which may leave
// other IRIs uncompacted and so bring more schemes into play; the set is
// shrunk until it stops changing.
type jsonldCompactor struct {
	active []Namespace
	used   map[string]bool
}

func newJSONLDCompactor(namespaces []Namespace, triples []RDFTriple) *jsonldCompactor {
	candidates := make([]Namespace, 0, len(namespaces))
	for _, ns := range namespaces {
		if validJSONLDPrefix(ns.Prefix) && ns.URI != "" {
			candidates = append(candidates, ns)
		}
	}
	iris := collectTripleIRIs(triples)
	for {
		declared := make(map[string]bool, len(candidates))
		for _, ns := range candidates {
			declared[ns.Prefix] = true
		}
		clashing := make(map[string]bool)
		for _, iri := range iris {
			if _, ok := compactJSONLDIRI(iri, candidates); ok {
				continue
			}
			if colon := strings.IndexByte(iri, ':'); colon > 0 && declared[iri[:colon]] {
				clashing[iri[:colon]] = true
			}
		}
		if len(clashing) == 0 {
			break
		}
		kept := candidates[:0:0]
		for _, ns := range candidates {
			if !clashing[ns.Prefix] {
				kept = append(kept, ns)
			}
		}
		candidates = kept
	}
	return &jsonldCompactor{active: candidates, used: make(map[string]bool)}
}

func collectTripleIRIs(triples []RDFTriple) []string {
	var out []string
	add := func(term RDFTerm) {
		switch term.Kind {
		case RDFTermIRI:
			out = append(out, term.Value)
		case RDFTermLiteral:
			if term.Language == "" && term.Datatype != "" && term.Datatype != xsdString {
				out = append(out, term.Datatype)
			}
		}
	}
	for _, triple := range triples {
		add(triple.Subject)
		add(triple.Predicate)
		add(triple.Object)
		if triple.Graph != nil {
			add(*triple.Graph)
		}
	}
	return out
}

// validJSONLDPrefix rejects store prefixes that cannot be JSON-LD terms used
// as prefixes: "_" is reserved for blank nodes, "@" starts keywords, and ':'
// or '/' would make the term itself an IRI.
func validJSONLDPrefix(prefix string) bool {
	return prefix != "" && prefix != "_" && !strings.HasPrefix(prefix, "@") && !strings.ContainsAny(prefix, ":/")
}

// compactJSONLDIRI returns prefix:local for the longest matching namespace.
// It declines an empty local part and one starting with "//", which JSON-LD
// would read as an absolute IRI rather than a compact one.
func compactJSONLDIRI(iri string, namespaces []Namespace) (string, bool) {
	best := ""
	bestLen := 0
	for _, ns := range namespaces {
		if len(ns.URI) > bestLen && strings.HasPrefix(iri, ns.URI) {
			local := iri[len(ns.URI):]
			if local == "" || strings.HasPrefix(local, "//") {
				continue
			}
			best = ns.Prefix + ":" + local
			bestLen = len(ns.URI)
		}
	}
	return best, bestLen > 0
}

func (c *jsonldCompactor) iri(value string) string {
	if compacted, ok := compactJSONLDIRI(value, c.active); ok {
		c.used[compacted[:strings.IndexByte(compacted, ':')]] = true
		return compacted
	}
	return value
}

func (c *jsonldCompactor) nodeID(term RDFTerm) string {
	if term.Kind == RDFTermBlankNode {
		return "_:" + term.Value
	}
	return c.iri(term.Value)
}

func (c *jsonldCompactor) value(term RDFTerm) jsonldValue {
	switch term.Kind {
	case RDFTermIRI, RDFTermBlankNode:
		id := c.nodeID(term)
		return jsonldValue{sortKey: "0" + id, json: map[string]any{"@id": id}}
	default:
		switch {
		case term.Language != "":
			return jsonldValue{
				sortKey: "2" + term.Value + "@" + term.Language,
				json:    map[string]any{"@value": term.Value, "@language": term.Language},
			}
		case term.Datatype != "" && term.Datatype != xsdString:
			datatype := c.iri(term.Datatype)
			return jsonldValue{
				sortKey: "3" + term.Value + "^^" + datatype,
				json:    map[string]any{"@value": term.Value, "@type": datatype},
			}
		default:
			return jsonldValue{sortKey: "1" + term.Value, json: term.Value}
		}
	}
}

// context declares only the prefixes the document uses. A namespace whose
// IRI does not end in a JSON-LD gen-delim is not a prefix under JSON-LD 1.1
// unless it says so, hence the expanded {"@id", "@prefix": true} form.
func (c *jsonldCompactor) context() map[string]any {
	out := make(map[string]any, len(c.used))
	for _, ns := range c.active {
		if !c.used[ns.Prefix] {
			continue
		}
		if strings.ContainsAny(ns.URI[len(ns.URI)-1:], ":/?#[]@") {
			out[ns.Prefix] = ns.URI
		} else {
			out[ns.Prefix] = map[string]any{"@id": ns.URI, "@prefix": true}
		}
	}
	return out
}

func dedupeStrings(sorted []string) []string {
	out := sorted[:0:0]
	for i, value := range sorted {
		if i > 0 && value == sorted[i-1] {
			continue
		}
		out = append(out, value)
	}
	return out
}
