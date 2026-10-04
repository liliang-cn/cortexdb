package graph

// RDF/XML, the syntax of RDF 1.1 XML Syntax (2014) and its RDF 1.2 additions.
//
// Most published ontologies — the OWL and Dublin Core vocabularies, FOAF, the
// W3C test suites' own data — are RDF/XML, and a store that could not read it
// could not load them. This reads the whole grammar of §7: node elements with
// rdf:about, rdf:ID, rdf:nodeID and property attributes; typed node elements;
// the property element forms (resource, literal, parseType Literal, Resource
// and Collection, empty); rdf:li numbering; rdf:datatype, xml:lang and
// xml:base; reification by rdf:ID on a property element; and the checks the
// W3C negative tests hold a parser to (reserved names in the wrong place,
// rdf:ID and rdf:nodeID that are not NCNames, a repeated rdf:ID, rdf:bagID and
// rdf:aboutEach, which RDF 1.1 removed).
//
// RDF 1.2 adds, under rdf:version="1.2": rdf:parseType="Triple", whose single
// node element with one property states a triple term rather than asserting
// it; rdf:annotation and rdf:annotationNodeID, naming a reifier of the
// statement a property element makes; and its:dir, giving a language-tagged
// literal its base direction.
//
// rdf:parseType="Literal" content becomes an rdf:XMLLiteral in exclusive
// canonical form, which is why the parser keeps the raw prefixes: Go's
// encoding/xml resolves names to namespace URIs and drops them, so names are
// resolved here instead, from xml.Decoder.RawToken.

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	rdfXMLNS        = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	xmlNamespaceURI = "http://www.w3.org/XML/1998/namespace"
	itsNamespaceURI = "http://www.w3.org/2005/11/its"
)

// xnode is one element of the document, names resolved, prefixes kept.
type xnode struct {
	space, local, prefix string
	attrs                []xattr
	decls                []xattr // xmlns declarations made on this element
	children             []any   // *xnode or string
	ns                   map[string]string
	base, lang, dir      string
	version12            bool
}

type xattr struct {
	space, local, prefix, value string
}

func (n *xnode) is(space, local string) bool { return n.space == space && n.local == local }

func (n *xnode) attr(space, local string) (string, bool) {
	for _, a := range n.attrs {
		if a.space == space && a.local == local {
			return a.value, true
		}
	}
	return "", false
}

func (n *xnode) elements() []*xnode {
	var out []*xnode
	for _, c := range n.children {
		if e, ok := c.(*xnode); ok {
			out = append(out, e)
		}
	}
	return out
}

func (n *xnode) text() string {
	var b strings.Builder
	for _, c := range n.children {
		if s, ok := c.(string); ok {
			b.WriteString(s)
		}
	}
	return b.String()
}

var xmlEntityDecl = regexp.MustCompile(`<!ENTITY\s+([A-Za-z_][\w.\-]*)\s+(?:"([^"]*)"|'([^']*)')\s*>`)

// readXMLTree reads the document into xnodes.
func readXMLTree(src, base string) (*xnode, error) {
	dec := xml.NewDecoder(strings.NewReader(src))
	dec.Strict = true
	dec.Entity = map[string]string{}
	root := &xnode{ns: map[string]string{"xml": xmlNamespaceURI}, base: base, children: nil}
	stack := []*xnode{root}
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		parent := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.Directive:
			// The internal subset's entities, which RDF/XML documents use
			// to abbreviate namespace IRIs (&rdf;, &xsd;).
			for _, m := range xmlEntityDecl.FindAllStringSubmatch(string(t), -1) {
				value := m[2]
				if m[3] != "" {
					value = m[3]
				}
				dec.Entity[m[1]] = value
			}
		case xml.StartElement:
			n := &xnode{ns: map[string]string{}, base: parent.base, lang: parent.lang, dir: parent.dir, version12: parent.version12}
			for k, v := range parent.ns {
				n.ns[k] = v
			}
			var raw []xml.Attr
			for _, a := range t.Attr {
				switch {
				case a.Name.Space == "xmlns":
					n.ns[a.Name.Local] = a.Value
					n.decls = append(n.decls, xattr{prefix: "xmlns", local: a.Name.Local, value: a.Value})
				case a.Name.Space == "" && a.Name.Local == "xmlns":
					n.ns[""] = a.Value
					n.decls = append(n.decls, xattr{local: "xmlns", value: a.Value})
				default:
					raw = append(raw, a)
				}
			}
			resolve := func(name xml.Name, attr bool) (string, error) {
				if name.Space == "" {
					if attr {
						return "", nil
					}
					return n.ns[""], nil
				}
				uri, ok := n.ns[name.Space]
				if !ok {
					return "", fmt.Errorf("undeclared namespace prefix %q", name.Space)
				}
				return uri, nil
			}
			space, err := resolve(t.Name, false)
			if err != nil {
				return nil, err
			}
			n.space, n.local, n.prefix = space, t.Name.Local, t.Name.Space
			for _, a := range raw {
				as, err := resolve(a.Name, true)
				if err != nil {
					return nil, err
				}
				n.attrs = append(n.attrs, xattr{space: as, local: a.Name.Local, prefix: a.Name.Space, value: a.Value})
			}
			for _, a := range n.attrs {
				switch {
				case a.space == xmlNamespaceURI && a.local == "base":
					n.base = resolveIRIReference(parent.base, a.value)
				case a.space == xmlNamespaceURI && a.local == "lang":
					n.lang = strings.ToLower(a.value)
				case a.space == itsNamespaceURI && a.local == "dir":
					n.dir = a.value
				case a.space == rdfXMLNS && a.local == "version":
					n.version12 = true
				}
			}
			parent.children = append(parent.children, n)
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			parent.children = append(parent.children, string(t))
		}
	}
	return root, nil
}

// rdfXMLParser turns the tree into triples.
type rdfXMLParser struct {
	out      *[]RDFTriple
	blank    int
	nodeIDs  map[string]RDFTerm
	usedIDs  map[string]bool
	liCounts map[*xnode]int
}

func parseRDFXML(src, base string) (triples []RDFTriple, err error) {
	root, err := readXMLTree(src, base)
	if err != nil {
		return nil, fmt.Errorf("RDF/XML: %w", err)
	}
	p := &rdfXMLParser{out: &triples, nodeIDs: map[string]RDFTerm{}, usedIDs: map[string]bool{}, liCounts: map[*xnode]int{}}
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(rdfXMLError)
			if !ok {
				panic(r)
			}
			triples, err = nil, fmt.Errorf("RDF/XML: %s", string(e))
		}
	}()
	for _, s := range root.children {
		if text, ok := s.(string); ok && strings.TrimSpace(text) != "" {
			p.fail("text outside the document element")
		}
	}
	tops := root.elements()
	if len(tops) != 1 {
		p.fail("a document has exactly one root element")
	}
	top := tops[0]
	if top.is(rdfXMLNS, "RDF") {
		p.checkOnlySyntaxAttrs(top)
		p.nodeElementList(top)
	} else {
		p.nodeElement(top)
	}
	return triples, nil
}

type rdfXMLError string

func (p *rdfXMLParser) fail(format string, args ...any) {
	panic(rdfXMLError(fmt.Sprintf(format, args...)))
}

func (p *rdfXMLParser) emit(s, pr, o RDFTerm) {
	*p.out = append(*p.out, RDFTriple{Subject: s, Predicate: pr, Object: o})
}

func (p *rdfXMLParser) fresh() RDFTerm {
	p.blank++
	return NewBlankNode("x" + strconv.Itoa(p.blank))
}

func (p *rdfXMLParser) nodeID(id string) RDFTerm {
	if !isNCName(id) {
		p.fail("rdf:nodeID %q is not an XML name", id)
	}
	if t, ok := p.nodeIDs[id]; ok {
		return t
	}
	t := NewBlankNode("n" + id)
	p.nodeIDs[id] = t
	return t
}

// idIRI is the IRI rdf:ID names, which may be used once per base.
func (p *rdfXMLParser) idIRI(n *xnode, id string) RDFTerm {
	if !isNCName(id) {
		p.fail("rdf:ID %q is not an XML name", id)
	}
	iri := resolveIRIReference(n.base, "#"+id)
	if p.usedIDs[iri] {
		p.fail("rdf:ID %q is used twice", id)
	}
	p.usedIDs[iri] = true
	return NewIRI(iri)
}

func (p *rdfXMLParser) iri(n *xnode, ref string) RDFTerm {
	return NewIRI(resolveIRIReference(n.base, ref))
}

// The names RDF/XML reserves, and where each may not appear.
var (
	rdfCoreSyntaxTerms = map[string]bool{"RDF": true, "ID": true, "about": true, "parseType": true, "resource": true, "nodeID": true, "datatype": true, "annotation": true, "annotationNodeID": true}
	rdfOldTerms        = map[string]bool{"aboutEach": true, "aboutEachPrefix": true, "bagID": true}
)

func (p *rdfXMLParser) nodeElementList(n *xnode) {
	p.noText(n)
	for _, e := range n.elements() {
		p.nodeElement(e)
	}
}

func (p *rdfXMLParser) noText(n *xnode) {
	if strings.TrimSpace(n.text()) != "" {
		p.fail("unexpected text in <%s>", n.local)
	}
}

func (p *rdfXMLParser) checkOnlySyntaxAttrs(n *xnode) {
	for _, a := range n.attrs {
		if a.space == xmlNamespaceURI || a.space == itsNamespaceURI || (a.space == rdfXMLNS && a.local == "version") {
			continue
		}
		p.fail("attribute %s:%s is not allowed on rdf:RDF", a.prefix, a.local)
	}
}

// nodeElement states a node and its properties and returns it.
func (p *rdfXMLParser) nodeElement(n *xnode) RDFTerm {
	if n.space == rdfXMLNS && (rdfCoreSyntaxTerms[n.local] || rdfOldTerms[n.local] || n.local == "li") {
		p.fail("rdf:%s cannot be a node element", n.local)
	}
	if n.space == "" {
		p.fail("node element <%s> has no namespace", n.local)
	}
	var subject *RDFTerm
	set := func(t RDFTerm) {
		if subject != nil {
			p.fail("a node element takes one of rdf:about, rdf:ID and rdf:nodeID")
		}
		subject = &t
	}
	type propAttr struct{ name, value string }
	var props []propAttr
	for _, a := range n.attrs {
		switch {
		case isIgnoredXMLAttr(a):
		case a.space == rdfXMLNS && a.local == "about":
			set(p.iri(n, a.value))
		case a.space == rdfXMLNS && a.local == "ID":
			set(p.idIRI(n, a.value))
		case a.space == rdfXMLNS && a.local == "nodeID":
			set(p.nodeID(a.value))
		case a.space == rdfXMLNS && (rdfCoreSyntaxTerms[a.local] || rdfOldTerms[a.local] || a.local == "li" || a.local == "Description"):
			p.fail("rdf:%s is not allowed on a node element", a.local)
		case a.space == "":
			p.fail("attribute %q has no namespace", a.local)
		default:
			props = append(props, propAttr{a.space + a.local, a.value})
		}
	}
	if subject == nil {
		t := p.fresh()
		subject = &t
	}
	if !n.is(rdfXMLNS, "Description") {
		p.emit(*subject, NewIRI(rdfXMLNS+"type"), NewIRI(n.space+n.local))
	}
	for _, pa := range props {
		if pa.name == rdfXMLNS+"type" {
			p.emit(*subject, NewIRI(pa.name), p.iri(n, pa.value))
			continue
		}
		p.emit(*subject, NewIRI(pa.name), p.literal(n, pa.value))
	}
	p.propertyEltList(n, *subject)
	return *subject
}

// literal is a plain literal with the element's language and direction.
func (p *rdfXMLParser) literal(n *xnode, value string) RDFTerm {
	if n.lang == "" {
		return NewLiteral(value)
	}
	lang := n.lang
	if n.version12 && (n.dir == "ltr" || n.dir == "rtl") {
		lang += "--" + n.dir
	}
	return RDFTerm{Kind: RDFTermLiteral, Value: value, Language: lang}
}

func isIgnoredXMLAttr(a xattr) bool {
	return a.space == xmlNamespaceURI || a.space == itsNamespaceURI ||
		(a.space == rdfXMLNS && a.local == "version") ||
		// Attributes in no namespace whose names begin "xml" are reserved
		// by XML and ignored, as the RDF/XML grammar says.
		(a.space == "" && strings.HasPrefix(strings.ToLower(a.local), "xml"))
}

func (p *rdfXMLParser) propertyEltList(n *xnode, subject RDFTerm) {
	p.noText(n)
	for _, e := range n.elements() {
		p.propertyElt(e, n, subject)
	}
}

func (p *rdfXMLParser) propertyElt(e, node *xnode, subject RDFTerm) {
	if e.space == rdfXMLNS && (rdfCoreSyntaxTerms[e.local] || rdfOldTerms[e.local] || e.local == "Description") {
		p.fail("rdf:%s cannot be a property element", e.local)
	}
	if e.space == "" {
		p.fail("property element <%s> has no namespace", e.local)
	}
	predicate := NewIRI(e.space + e.local)
	if e.is(rdfXMLNS, "li") {
		p.liCounts[node]++
		predicate = NewIRI(rdfXMLNS + "_" + strconv.Itoa(p.liCounts[node]))
	}

	attrs := map[string]string{}
	var props []xattr
	for _, a := range e.attrs {
		switch {
		case isIgnoredXMLAttr(a):
		case a.space == rdfXMLNS && (a.local == "ID" || a.local == "parseType" || a.local == "resource" || a.local == "nodeID" || a.local == "datatype" || a.local == "annotation" || a.local == "annotationNodeID"):
			attrs[a.local] = a.value
		case a.space == rdfXMLNS && (rdfOldTerms[a.local] || a.local == "li" || a.local == "about" || a.local == "RDF" || a.local == "Description"):
			p.fail("rdf:%s is not allowed on a property element", a.local)
		case a.space == "":
			p.fail("attribute %q has no namespace", a.local)
		default:
			props = append(props, a)
		}
	}
	_, hasAnn := attrs["annotation"]
	_, hasAnnNode := attrs["annotationNodeID"]
	if hasAnn && hasAnnNode {
		p.fail("rdf:annotation and rdf:annotationNodeID together")
	}
	state := func(object RDFTerm) {
		p.emit(subject, predicate, object)
		if id, ok := attrs["ID"]; ok {
			p.reify(p.idIRI(e, id), subject, predicate, object)
		}
		if v, ok := attrs["annotation"]; ok {
			p.emit(p.iri(e, v), NewIRI(rdfXMLNS+"reifies"), p.triple(subject, predicate, object))
		}
		if v, ok := attrs["annotationNodeID"]; ok {
			p.emit(p.nodeID(v), NewIRI(rdfXMLNS+"reifies"), p.triple(subject, predicate, object))
		}
	}

	if pt, ok := attrs["parseType"]; ok {
		if _, r := attrs["resource"]; r {
			p.fail("rdf:parseType with rdf:resource")
		}
		if _, r := attrs["nodeID"]; r {
			p.fail("rdf:parseType with rdf:nodeID")
		}
		if _, r := attrs["datatype"]; r {
			p.fail("rdf:parseType with rdf:datatype")
		}
		if len(props) > 0 {
			p.fail("rdf:parseType with property attributes")
		}
		switch pt {
		case "Resource":
			object := p.fresh()
			state(object)
			p.propertyEltList(e, object)
		case "Collection":
			p.noText(e)
			items := e.elements()
			var objects []RDFTerm
			for _, item := range items {
				objects = append(objects, p.nodeElement(item))
			}
			state(p.list(objects))
		case "Triple":
			if !e.version12 {
				return // RDF 1.1 knows no triple terms: the element states nothing
			}
			state(p.tripleTerm(e))
		default: // "Literal", and any other value, which RDF/XML reads as Literal
			state(NewTypedLiteral(canonicalXMLContent(e), rdfXMLNS+"XMLLiteral"))
		}
		return
	}

	elems := e.elements()
	text := e.text()
	switch {
	case len(elems) > 0:
		// resourcePropertyElt: one node element, whitespace around it.
		if strings.TrimSpace(text) != "" {
			p.fail("property element <%s> mixes text and elements", e.local)
		}
		if len(elems) != 1 {
			p.fail("property element <%s> holds more than one node element", e.local)
		}
		for _, k := range []string{"resource", "nodeID", "datatype"} {
			if _, ok := attrs[k]; ok {
				p.fail("rdf:%s on a property element with content", k)
			}
		}
		if len(props) > 0 {
			p.fail("property attributes on a property element with content")
		}
		state(p.nodeElement(elems[0]))
	case text != "" || attrs["datatype"] != "" || hasDatatype(attrs):
		// literalPropertyElt
		for _, k := range []string{"resource", "nodeID"} {
			if _, ok := attrs[k]; ok {
				p.fail("rdf:%s on a literal property element", k)
			}
		}
		if len(props) > 0 {
			p.fail("property attributes on a literal property element")
		}
		if dt, ok := attrs["datatype"]; ok {
			state(NewTypedLiteral(text, p.iri(e, dt).Value))
		} else {
			state(p.literal(e, text))
		}
	default:
		// emptyPropertyElt
		resource, hasResource := attrs["resource"]
		nodeIDValue, hasNodeID := attrs["nodeID"]
		if hasResource && hasNodeID {
			p.fail("rdf:resource and rdf:nodeID together")
		}
		if !hasResource && !hasNodeID && len(props) == 0 {
			state(p.literal(e, ""))
			return
		}
		var object RDFTerm
		switch {
		case hasResource:
			object = p.iri(e, resource)
		case hasNodeID:
			object = p.nodeID(nodeIDValue)
		default:
			object = p.fresh()
		}
		for _, a := range props {
			if a.space == rdfXMLNS && a.local == "type" {
				p.emit(object, NewIRI(rdfXMLNS+"type"), p.iri(e, a.value))
				continue
			}
			p.emit(object, NewIRI(a.space+a.local), p.literal(e, a.value))
		}
		state(object)
	}
}

func hasDatatype(attrs map[string]string) bool {
	_, ok := attrs["datatype"]
	return ok
}

func (p *rdfXMLParser) reify(statement, s, pr, o RDFTerm) {
	p.emit(statement, NewIRI(rdfXMLNS+"type"), NewIRI(rdfXMLNS+"Statement"))
	p.emit(statement, NewIRI(rdfXMLNS+"subject"), s)
	p.emit(statement, NewIRI(rdfXMLNS+"predicate"), pr)
	p.emit(statement, NewIRI(rdfXMLNS+"object"), o)
}

func (p *rdfXMLParser) list(items []RDFTerm) RDFTerm {
	if len(items) == 0 {
		return NewIRI(rdfXMLNS + "nil")
	}
	head := p.fresh()
	cell := head
	for i, item := range items {
		p.emit(cell, NewIRI(rdfXMLNS+"first"), item)
		if i == len(items)-1 {
			p.emit(cell, NewIRI(rdfXMLNS+"rest"), NewIRI(rdfXMLNS+"nil"))
			break
		}
		next := p.fresh()
		p.emit(cell, NewIRI(rdfXMLNS+"rest"), next)
		cell = next
	}
	return head
}

// tripleTerm reads parseType="Triple": one node element with exactly one
// property, whose statement becomes a triple term instead of being asserted.
func (p *rdfXMLParser) tripleTerm(e *xnode) RDFTerm {
	p.noText(e)
	elems := e.elements()
	if len(elems) != 1 {
		p.fail("rdf:parseType=\"Triple\" holds exactly one node element")
	}
	var captured []RDFTriple
	saved := p.out
	p.out = &captured
	p.nodeElement(elems[0])
	p.out = saved
	if len(captured) != 1 {
		p.fail("rdf:parseType=\"Triple\" states exactly one triple, not %d", len(captured))
	}
	t := captured[0]
	return p.triple(t.Subject, t.Predicate, t.Object)
}

func (p *rdfXMLParser) triple(s, pr, o RDFTerm) RDFTerm {
	t, err := NewTripleTerm(s, pr, o)
	if err != nil {
		p.fail("%v", err)
	}
	return t
}

// isNCName is the XML Namespaces NCName production over the XML 1.0 (Fifth
// Edition) NameStartChar and NameChar ranges, colon excluded.
func isNCName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if !isXMLNameStartChar(r) && (i == 0 || !isXMLNameChar(r)) {
			return false
		}
	}
	return true
}

func isXMLNameStartChar(r rune) bool {
	switch {
	case r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z':
		return true
	case r >= 0xC0 && r <= 0xD6, r >= 0xD8 && r <= 0xF6, r >= 0xF8 && r <= 0x2FF,
		r >= 0x370 && r <= 0x37D, r >= 0x37F && r <= 0x1FFF, r >= 0x200C && r <= 0x200D,
		r >= 0x2070 && r <= 0x218F, r >= 0x2C00 && r <= 0x2FEF, r >= 0x3001 && r <= 0xD7FF,
		r >= 0xF900 && r <= 0xFDCF, r >= 0xFDF0 && r <= 0xFFFD, r >= 0x10000 && r <= 0xEFFFF:
		return true
	}
	return false
}

func isXMLNameChar(r rune) bool {
	return isXMLNameStartChar(r) || r == '-' || r == '.' || r >= '0' && r <= '9' ||
		r == 0xB7 || r >= 0x300 && r <= 0x36F || r >= 0x203F && r <= 0x2040
}

// --- XML literals -------------------------------------------------------------

// canonicalXMLContent is Exclusive XML Canonicalization (without comments)
// of a property element's content: each element declares the namespaces it
// and its attributes visibly use that an output ancestor has not already
// declared, attributes in namespace-then-name order, empty elements as a
// start and end tag, and the canonical escapes.
func canonicalXMLContent(e *xnode) string {
	var b bytes.Buffer
	for _, c := range e.children {
		writeCanonical(&b, c, map[string]string{})
	}
	return b.String()
}

func writeCanonical(b *bytes.Buffer, c any, rendered map[string]string) {
	switch t := c.(type) {
	case string:
		b.WriteString(escapeCanonicalText(t))
	case *xnode:
		used := map[string]bool{t.prefix: true}
		for _, a := range t.attrs {
			if a.prefix != "" && a.prefix != "xml" {
				used[a.prefix] = true
			}
		}
		scope := map[string]string{}
		for k, v := range rendered {
			scope[k] = v
		}
		var decls []string
		for prefix := range used {
			uri := t.ns[prefix]
			if prev, ok := rendered[prefix]; ok && prev == uri {
				continue
			}
			if prefix == "" && uri == "" {
				if _, ok := rendered[""]; !ok {
					continue
				}
			}
			scope[prefix] = uri
			decls = append(decls, prefix)
		}
		sort.Strings(decls)
		name := t.local
		if t.prefix != "" {
			name = t.prefix + ":" + t.local
		}
		b.WriteString("<" + name)
		for _, prefix := range decls {
			if prefix == "" {
				b.WriteString(` xmlns="` + escapeCanonicalAttr(scope[""]) + `"`)
			} else {
				b.WriteString(` xmlns:` + prefix + `="` + escapeCanonicalAttr(scope[prefix]) + `"`)
			}
		}
		attrs := append([]xattr(nil), t.attrs...)
		sort.SliceStable(attrs, func(i, j int) bool {
			if attrs[i].space != attrs[j].space {
				return attrs[i].space < attrs[j].space
			}
			return attrs[i].local < attrs[j].local
		})
		for _, a := range attrs {
			an := a.local
			if a.prefix != "" {
				an = a.prefix + ":" + a.local
			}
			b.WriteString(" " + an + `="` + escapeCanonicalAttr(a.value) + `"`)
		}
		b.WriteString(">")
		for _, k := range t.children {
			writeCanonical(b, k, scope)
		}
		b.WriteString("</" + name + ">")
	}
}

func escapeCanonicalText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\r", "&#xD;")
	return r.Replace(s)
}

func escapeCanonicalAttr(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", `"`, "&quot;", "\t", "&#x9;", "\n", "&#xA;", "\r", "&#xD;")
	return r.Replace(s)
}
