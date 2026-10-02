package graph

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Writers for N-Triples, N-Quads, Turtle and TriG at RDF 1.2.
//
// Every writer spells literals, IRIs and triple terms through the canonical
// N-Triples rules in rdf_term12.go, which are also valid Turtle. The writers
// they replace used Go's strconv.Quote, whose \x and \a escapes no RDF parser
// accepts, so a literal holding a control character exported as a document
// that could not be imported back.

// rdfTermWriter spells terms for one export. It remembers the blank node
// labels it has had to rename, so a node keeps one name throughout a
// document.
type rdfTermWriter struct {
	namespaces []Namespace
	turtle     bool
	bnodes     map[string]string
}

func newRDFTermWriter(namespaces []Namespace, turtle bool) *rdfTermWriter {
	return &rdfTermWriter{namespaces: namespaces, turtle: turtle, bnodes: make(map[string]string)}
}

var validBlankNodeLabel = regexp.MustCompile(`^[A-Za-z0-9_\x{00C0}-\x{00D6}\x{00D8}-\x{00F6}\x{00F8}-\x{02FF}\x{0370}-\x{037D}\x{037F}-\x{1FFF}\x{200C}-\x{200D}\x{2070}-\x{218F}\x{2C00}-\x{2FEF}\x{3001}-\x{D7FF}\x{F900}-\x{FDCF}\x{FDF0}-\x{FFFD}\x{10000}-\x{EFFFF}]([A-Za-z0-9_\-.\x{00B7}\x{0300}-\x{036F}\x{203F}-\x{2040}\x{00C0}-\x{00D6}\x{00D8}-\x{00F6}\x{00F8}-\x{02FF}\x{0370}-\x{037D}\x{037F}-\x{1FFF}\x{200C}-\x{200D}\x{2070}-\x{218F}\x{2C00}-\x{2FEF}\x{3001}-\x{D7FF}\x{F900}-\x{FDCF}\x{FDF0}-\x{FFFD}\x{10000}-\x{EFFFF}]*[A-Za-z0-9_\-\x{00B7}\x{0300}-\x{036F}\x{203F}-\x{2040}\x{00C0}-\x{00D6}\x{00D8}-\x{00F6}\x{00F8}-\x{02FF}\x{0370}-\x{037D}\x{037F}-\x{1FFF}\x{200C}-\x{200D}\x{2070}-\x{218F}\x{2C00}-\x{2FEF}\x{3001}-\x{D7FF}\x{F900}-\x{FDCF}\x{FDF0}-\x{FFFD}\x{10000}-\x{EFFFF}])?$`)

// blankNode writes a blank node. A label the store holds that the syntax
// cannot (the store accepts any string) is renamed to a digest of itself:
// stable across exports, distinct from other labels, and parseable.
func (w *rdfTermWriter) blankNode(label string) string {
	if validBlankNodeLabel.MatchString(label) {
		return "_:" + label
	}
	if renamed, ok := w.bnodes[label]; ok {
		return "_:" + renamed
	}
	sum := sha1.Sum([]byte(label))
	renamed := "b" + hex.EncodeToString(sum[:10])
	w.bnodes[label] = renamed
	return "_:" + renamed
}

func (w *rdfTermWriter) iri(value string) string {
	if w.turtle {
		if compacted, ok := turtlePrefixedName(value, w.namespaces); ok {
			return compacted
		}
	}
	return canonicalIRI(value)
}

func (w *rdfTermWriter) term(t RDFTerm) (string, error) {
	switch t.Kind {
	case RDFTermIRI:
		return w.iri(t.Value), nil
	case RDFTermBlankNode:
		return w.blankNode(strings.TrimPrefix(t.Value, "_:")), nil
	case RDFTermLiteral:
		var b strings.Builder
		b.WriteByte('"')
		writeCanonicalString(&b, t.Value)
		b.WriteByte('"')
		switch {
		case t.Language != "":
			b.WriteByte('@')
			b.WriteString(strings.ToLower(t.Language))
		case t.Datatype != "" && t.Datatype != rdf12XSDStringIRI:
			b.WriteString("^^")
			b.WriteString(w.iri(t.Datatype))
		}
		return b.String(), nil
	case RDFTermTriple:
		triple, err := decodeTripleTermValue(t.Value)
		if err != nil {
			return "", err
		}
		s, err := w.term(triple.Subject)
		if err != nil {
			return "", err
		}
		p, err := w.term(triple.Predicate)
		if err != nil {
			return "", err
		}
		o, err := w.term(triple.Object)
		if err != nil {
			return "", err
		}
		return "<<( " + s + " " + p + " " + o + " )>>", nil
	default:
		return "", fmt.Errorf("cannot write rdf term of kind %q", t.Kind)
	}
}

// turtlePrefixedName compacts an IRI to prefix:local when some namespace is a
// prefix of it and the remainder is a local name Turtle can read back as
// written. The longest namespace wins, as compactIRIWithNamespaces does.
func turtlePrefixedName(value string, namespaces []Namespace) (string, bool) {
	best, bestLen := "", -1
	for _, ns := range namespaces {
		if ns.URI == "" || !strings.HasPrefix(value, ns.URI) || len(ns.URI) <= bestLen {
			continue
		}
		local := value[len(ns.URI):]
		if !validTurtlePrefix(ns.Prefix) || !simpleTurtleLocal(local) {
			continue
		}
		best, bestLen = ns.Prefix+":"+local, len(ns.URI)
	}
	return best, bestLen >= 0
}

func validTurtlePrefix(prefix string) bool {
	if prefix == "" {
		return true
	}
	for i, r := range prefix {
		switch {
		case i == 0 && !isPNCharsBase(r):
			return false
		case !isPNChars(r) && r != '.':
			return false
		}
	}
	return !strings.HasSuffix(prefix, ".")
}

// simpleTurtleLocal accepts the local names that need no escaping: PN_CHARS,
// ':' and inner dots. Anything else is written as a full IRI, which is always
// correct and only ever longer.
func simpleTurtleLocal(local string) bool {
	if local == "" {
		return true
	}
	for i, r := range local {
		switch {
		case i == 0 && !(isPNCharsU(r) || r == ':' || (r >= '0' && r <= '9')):
			return false
		case !isPNChars(r) && r != ':' && r != '.':
			return false
		}
	}
	last, _ := utf8.DecodeLastRuneInString(local)
	return last != '.'
}

// usesRDF12 reports whether any triple needs RDF 1.2 syntax: a triple term or
// a literal with a base direction. Turtle and TriG exports then announce the
// version, so an RDF 1.1 reader fails loudly instead of misreading them.
func usesRDF12(triples []RDFTriple) bool {
	for _, triple := range triples {
		if triple.Object.Kind == RDFTermTriple || (triple.Object.Kind == RDFTermLiteral && strings.Contains(triple.Object.Language, "--")) {
			return true
		}
	}
	return false
}

// writeLineStatements writes N-Triples, or N-Quads when quads is set.
func writeLineStatements(writer io.Writer, triples []RDFTriple, quads bool) error {
	w := newRDFTermWriter(nil, false)
	buffered := bufio.NewWriter(writer)
	for _, triple := range triples {
		if triple.Graph != nil && !quads {
			return fmt.Errorf("ntriples cannot represent named graphs; use nquads or turtle")
		}
		line, err := w.statement(triple, quads)
		if err != nil {
			return err
		}
		if _, err := buffered.WriteString(line + "\n"); err != nil {
			return err
		}
	}
	return buffered.Flush()
}

func (w *rdfTermWriter) statement(triple RDFTriple, withGraph bool) (string, error) {
	s, err := w.term(triple.Subject)
	if err != nil {
		return "", err
	}
	p, err := w.term(triple.Predicate)
	if err != nil {
		return "", err
	}
	o, err := w.term(triple.Object)
	if err != nil {
		return "", err
	}
	line := s + " " + p + " " + o
	if withGraph && triple.Graph != nil {
		g, err := w.term(*triple.Graph)
		if err != nil {
			return "", err
		}
		line += " " + g
	}
	return line + " .", nil
}

// writeTurtleHeader writes the version announcement when one is needed and
// the prefixes.
func writeTurtleHeader(writer io.Writer, namespaces []Namespace, triples []RDFTriple) error {
	if usesRDF12(triples) {
		if _, err := fmt.Fprintln(writer, `VERSION "1.2"`); err != nil {
			return err
		}
	}
	for _, ns := range namespaces {
		if !validTurtlePrefix(ns.Prefix) {
			continue
		}
		if _, err := fmt.Fprintf(writer, "@prefix %s: %s .\n", ns.Prefix, canonicalIRI(ns.URI)); err != nil {
			return err
		}
	}
	if len(namespaces) > 0 {
		if _, err := fmt.Fprintln(writer); err != nil {
			return err
		}
	}
	return nil
}
