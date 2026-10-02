package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

// RDF 1.2 terms: triple terms, and literals with a base direction.
//
// A triple term is an RDFTerm of kind RDFTermTriple whose Value is the
// canonical N-Triples 1.2 spelling of the triple, `<<( <s> <p> <o> )>>`.
// Holding the triple as a string rather than as nested structs is what keeps
// every existing piece of code that compares terms correct without touching
// it: termsEqual, the DISTINCT key, the inference engine's index keys and the
// kg_triples columns all compare Kind and Value, and two triple terms are the
// same term exactly when their canonical spellings are equal — that is what
// "canonical" buys. The parts are recovered with TripleTermParts.
//
// The base direction of a literal ("hello"@en--ltr) rides in Language, after
// the language tag and a double hyphen, exactly as every RDF 1.2 concrete
// syntax writes it. BCP 47 never puts two hyphens in a row, so the split is
// unambiguous, and keeping the two together means the object_language column,
// FindTriples and every comparison already treat "en--ltr" and "en" as the
// different terms RDF 1.2 says they are. LanguageTag and BaseDirection take
// it apart.

const (
	// RDFTermTriple represents an RDF 1.2 triple term. Its Value is the
	// canonical N-Triples form `<<( s p o )>>`; see TripleTermParts.
	RDFTermTriple = "triple"

	// RDFReifiesIRI is rdf:reifies, the predicate RDF 1.2 relates a reifier to
	// the triple term it reifies with.
	RDFReifiesIRI = "http://www.w3.org/1999/02/22-rdf-syntax-ns#reifies"

	rdf12NamespaceIRI     = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	rdf12LangStringIRI    = rdf12NamespaceIRI + "langString"
	rdf12DirLangStringIRI = rdf12NamespaceIRI + "dirLangString"
	rdf12XSDStringIRI     = "http://www.w3.org/2001/XMLSchema#string"

	// tripleTermIDPrefix names a row of kg_triple_terms.
	tripleTermIDPrefix = "rdf:tterm:"

	// maxTripleTermDepth bounds nesting. RDF puts no limit on it, but a
	// recursive parser and a recursive matcher on untrusted input need one,
	// and no real statement about a statement about a statement goes 64 deep.
	maxTripleTermDepth = 64
)

// NewTripleTerm builds the triple term <<( subject predicate object )>>.
//
// RDF 1.2 allows a triple term only where an object may stand, and its own
// subject only an IRI or blank node, so this fails for anything that is not a
// well-formed triple instead of producing a term no store could hold.
func NewTripleTerm(subject, predicate, object RDFTerm) (RDFTerm, error) {
	value, err := canonicalTripleTerm(subject, predicate, object)
	if err != nil {
		return RDFTerm{}, err
	}
	if object.Kind == RDFTermTriple {
		// One level deeper than the object: read it back once so a term
		// past the nesting limit is refused here, not when it is stored.
		if _, err := decodeTripleTermValue(value); err != nil {
			return RDFTerm{}, err
		}
	}
	return RDFTerm{Kind: RDFTermTriple, Value: value}, nil
}

// NewDirLangLiteral creates a literal with a language tag and an initial base
// direction ("ltr" or "rtl"), whose datatype is rdf:dirLangString.
func NewDirLangLiteral(value, language, direction string) RDFTerm {
	language = strings.ToLower(strings.TrimSpace(language))
	direction = strings.TrimSpace(direction)
	if direction != "" {
		language += "--" + direction
	}
	return RDFTerm{Kind: RDFTermLiteral, Value: value, Language: language}
}

// IsTripleTerm reports whether the term is an RDF 1.2 triple term.
func (t RDFTerm) IsTripleTerm() bool { return t.Kind == RDFTermTriple }

// TripleTermParts returns the subject, predicate and object of a triple term.
func (t RDFTerm) TripleTermParts() (subject, predicate, object RDFTerm, err error) {
	if t.Kind != RDFTermTriple {
		return RDFTerm{}, RDFTerm{}, RDFTerm{}, fmt.Errorf("rdf term of kind %q is not a triple term", t.Kind)
	}
	triple, err := decodeTripleTermValue(t.Value)
	if err != nil {
		return RDFTerm{}, RDFTerm{}, RDFTerm{}, err
	}
	return triple.Subject, triple.Predicate, triple.Object, nil
}

// LanguageTag is the language tag of a language-tagged literal, without the
// base direction.
func (t RDFTerm) LanguageTag() string {
	if i := strings.Index(t.Language, "--"); i >= 0 {
		return t.Language[:i]
	}
	return t.Language
}

// BaseDirection is the initial base direction of a literal, "ltr" or "rtl",
// or "" when it has none.
func (t RDFTerm) BaseDirection() string {
	if i := strings.Index(t.Language, "--"); i >= 0 {
		return t.Language[i+2:]
	}
	return ""
}

// rdfLiteralDatatype is the datatype IRI RDF 1.2 assigns a literal.
func rdfLiteralDatatype(t RDFTerm) string {
	switch {
	case t.Language != "" && strings.Contains(t.Language, "--"):
		return rdf12DirLangStringIRI
	case t.Language != "":
		return rdf12LangStringIRI
	case t.Datatype == "":
		return rdf12XSDStringIRI
	default:
		return t.Datatype
	}
}

// canonicalTripleTerm checks a triple term's parts and spells it canonically.
// A part that is itself a triple term is read back from its spelling, so a
// hand-written nested term is checked and re-spelled too.
func canonicalTripleTerm(subject, predicate, object RDFTerm) (string, error) {
	return spellTripleTerm(subject, predicate, object, false)
}

// spellTripleTerm is canonicalTripleTerm for a caller that may vouch for a
// nested triple term's spelling: the parser, which built it a moment ago.
// Re-reading it there would re-read every level below it again, at a cost
// that doubles with each level of nesting.
func spellTripleTerm(subject, predicate, object RDFTerm, trustNested bool) (string, error) {
	if subject.Kind != RDFTermIRI && subject.Kind != RDFTermBlankNode {
		return "", fmt.Errorf("triple term subject must be iri or blank node, got %q", subject.Kind)
	}
	if predicate.Kind == "" {
		predicate.Kind = RDFTermIRI
	}
	if predicate.Kind != RDFTermIRI {
		return "", fmt.Errorf("triple term predicate must be iri, got %q", predicate.Kind)
	}
	s, err := canonicalTerm(subject)
	if err != nil {
		return "", err
	}
	p, err := canonicalTerm(predicate)
	if err != nil {
		return "", err
	}
	var o string
	if trustNested && object.Kind == RDFTermTriple {
		o = object.Value
	} else if o, err = canonicalTerm(object); err != nil {
		return "", err
	}
	return "<<( " + s + " " + p + " " + o + " )>>", nil
}

// canonicalTerm is a term in canonical N-Triples 1.2 form (RDF 1.2 N-Triples
// §3): the spelling two writers of the same term always agree on, which is
// what makes it usable as a triple term's identity.
func canonicalTerm(t RDFTerm) (string, error) {
	switch t.Kind {
	case RDFTermIRI:
		value := strings.TrimSpace(strings.Trim(t.Value, "<>"))
		if value == "" {
			return "", fmt.Errorf("iri is empty")
		}
		return canonicalIRI(value), nil
	case RDFTermBlankNode:
		label := strings.TrimPrefix(strings.TrimSpace(t.Value), "_:")
		if label == "" {
			return "", fmt.Errorf("blank node label is empty")
		}
		if strings.IndexFunc(label, isRDFWhitespace) >= 0 {
			return "", fmt.Errorf("blank node label %q cannot be written in a triple term", label)
		}
		return "_:" + label, nil
	case RDFTermLiteral:
		return canonicalLiteral(t)
	case RDFTermTriple:
		_, canonical, err := decodeTripleTerm(t.Value)
		return canonical, err
	default:
		return "", fmt.Errorf("unsupported rdf term kind %q", t.Kind)
	}
}

func isRDFWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

// canonicalIRI writes an IRIREF. The characters IRIREF cannot hold raw are
// written as \u escapes, which is the only spelling canonical form allows for
// them; everything else is written as itself.
func canonicalIRI(value string) string {
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('<')
	for _, r := range value {
		if r <= 0x20 || strings.ContainsRune("<>\"{}|^`\\", r) {
			fmt.Fprintf(&b, "\\u%04X", r)
			continue
		}
		b.WriteRune(r)
	}
	b.WriteByte('>')
	return b.String()
}

// canonicalLiteral writes a literal the way canonical N-Triples requires: the
// seven ECHARs for BS HT LF FF CR " \, lower-case \u escapes with upper-case
// hex for the other controls, everything else raw, the language tag in lower
// case, and no datatype for xsd:string.
func canonicalLiteral(t RDFTerm) (string, error) {
	var b strings.Builder
	b.Grow(len(t.Value) + 2)
	b.WriteByte('"')
	writeCanonicalString(&b, t.Value)
	b.WriteByte('"')
	switch {
	case t.Language != "":
		if t.Datatype != "" && t.Datatype != rdf12LangStringIRI && t.Datatype != rdf12DirLangStringIRI {
			return "", fmt.Errorf("literal cannot have both a language tag and datatype %s", t.Datatype)
		}
		lang := strings.ToLower(t.Language)
		if err := validateLangDir(lang); err != nil {
			return "", err
		}
		b.WriteByte('@')
		b.WriteString(lang)
	case t.Datatype != "" && t.Datatype != rdf12XSDStringIRI:
		if t.Datatype == rdf12LangStringIRI || t.Datatype == rdf12DirLangStringIRI {
			return "", fmt.Errorf("a %s literal needs a language tag", t.Datatype)
		}
		b.WriteString("^^")
		b.WriteString(canonicalIRI(t.Datatype))
	}
	return b.String(), nil
}

func writeCanonicalString(b *strings.Builder, value string) {
	for i := 0; i < len(value); {
		r, size := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && size == 1 {
			// Not UTF-8. Canonical form has no spelling for a lone byte, so
			// it becomes U+FFFD rather than an invalid document.
			b.WriteString("�")
			i++
			continue
		}
		i += size
		switch r {
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			if r <= 0x1F || r == 0x7F || r == 0xFFFE || r == 0xFFFF {
				fmt.Fprintf(b, "\\u%04X", r)
				continue
			}
			b.WriteRune(r)
		}
	}
}

// validateLangDir checks a lower-cased LANG_DIR body: a well-formed BCP 47
// tag and, after "--", a direction of exactly "ltr" or "rtl".
func validateLangDir(langDir string) error {
	tag, dir := langDir, ""
	if i := strings.Index(langDir, "--"); i >= 0 {
		tag, dir = langDir[:i], langDir[i+2:]
		if dir != "ltr" && dir != "rtl" {
			return fmt.Errorf("base direction must be ltr or rtl, got %q", dir)
		}
	}
	if !wellFormedLanguageTag(tag) {
		return fmt.Errorf("language tag %q is not well-formed BCP 47", tag)
	}
	return nil
}

// tripleTermDigest names a triple term's row in kg_triple_terms. A digest and
// not the canonical text, because the text can be as long as the literals in
// it and a primary key should not be.
func tripleTermDigest(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return tripleTermIDPrefix + hex.EncodeToString(sum[:16])
}

// decodeTripleTermValue reads a triple term's stored spelling back into its
// parts. It accepts any N-Triples 1.2 spelling, canonical or not, so a caller
// that wrote <<(<a> <b> <c>)>> by hand is normalized rather than refused.
func decodeTripleTermValue(value string) (RDFTriple, error) {
	triple, _, err := decodeTripleTerm(value)
	return triple, err
}

// decodeTripleTerm is decodeTripleTermValue that also returns the canonical
// spelling, which reading the term produced anyway.
func decodeTripleTerm(value string) (triple RDFTriple, canonical string, err error) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "<<(") {
		return RDFTriple{}, "", fmt.Errorf("invalid triple term %q: not a triple term", value)
	}
	p := newRDFSyntaxParser(trimmed, rdfSyntaxNTriples, "")
	p.lenientTerms = true
	defer func() {
		if r := recover(); r != nil {
			syntaxErr, ok := r.(*rdfSyntaxError)
			if !ok {
				panic(r)
			}
			triple, canonical, err = RDFTriple{}, "", fmt.Errorf("invalid triple term %q: %s", value, syntaxErr.Msg)
		}
	}()
	term := p.parseNTObject(0)
	p.skipHorizontalSpace()
	if !p.eof() {
		return RDFTriple{}, "", fmt.Errorf("invalid triple term %q: trailing content", value)
	}
	return p.lastTripleTerm, term.Value, nil
}

// normalizeTripleTerm re-spells a triple term canonically after checking it,
// so two spellings of one triple term are stored, indexed and compared as one.
func normalizeTripleTerm(t RDFTerm) (RDFTerm, error) {
	triple, err := decodeTripleTermValue(t.Value)
	if err != nil {
		return RDFTerm{}, err
	}
	return NewTripleTerm(triple.Subject, triple.Predicate, triple.Object)
}

// compactTripleTermLabel renders a triple term with prefixed names, for the
// property-graph node that mirrors it. It is a label, never parsed back.
func compactTripleTermLabel(t RDFTerm, namespaces []Namespace) string {
	triple, err := decodeTripleTermValue(t.Value)
	if err != nil {
		return t.Value
	}
	part := func(term RDFTerm) string {
		switch term.Kind {
		case RDFTermIRI:
			if compacted := compactIRIWithNamespaces(term.Value, namespaces); compacted != term.Value {
				return compacted
			}
			return "<" + term.Value + ">"
		case RDFTermTriple:
			return compactTripleTermLabel(term, namespaces)
		default:
			out, err := canonicalTerm(term)
			if err != nil {
				return term.Value
			}
			return out
		}
	}
	return "<<( " + part(triple.Subject) + " " + part(triple.Predicate) + " " + part(triple.Object) + " )>>"
}

// wellFormedLanguageTag is the well-formedness check of BCP 47 §2.2.9
// against the ABNF of §2.1: language, then optional script, region, variants,
// extensions and a private-use part, in that order; or a private-use tag
// alone; or one of the grandfathered tags. Validity against the IANA registry
// is a different and stricter thing, and not what RDF asks for.
func wellFormedLanguageTag(tag string) bool {
	if tag == "" {
		return false
	}
	lower := strings.ToLower(tag)
	if bcp47Grandfathered[lower] {
		return true
	}
	parts := strings.Split(lower, "-")
	for _, part := range parts {
		if part == "" || len(part) > 8 || !isASCIIAlnum(part) {
			return false
		}
	}
	if parts[0] == "x" {
		return len(parts) > 1
	}
	i := 0
	// language = 2*3ALPHA ["-" extlang] / 4ALPHA / 5*8ALPHA
	if !isASCIIAlpha(parts[0]) || len(parts[0]) < 2 {
		return false
	}
	shortLanguage := len(parts[0]) <= 3
	i++
	if shortLanguage {
		// extlang = 3ALPHA *2("-" 3ALPHA)
		for n := 0; n < 3 && i < len(parts) && len(parts[i]) == 3 && isASCIIAlpha(parts[i]); n++ {
			i++
		}
	}
	// script = 4ALPHA
	if i < len(parts) && len(parts[i]) == 4 && isASCIIAlpha(parts[i]) {
		i++
	}
	// region = 2ALPHA / 3DIGIT
	if i < len(parts) && ((len(parts[i]) == 2 && isASCIIAlpha(parts[i])) || (len(parts[i]) == 3 && isASCIIDigits(parts[i]))) {
		i++
	}
	// variant = 5*8alphanum / (DIGIT 3alphanum)
	for i < len(parts) && (len(parts[i]) >= 5 || (len(parts[i]) == 4 && parts[i][0] >= '0' && parts[i][0] <= '9')) {
		i++
	}
	// extension = singleton 1*("-" (2*8alphanum)); singletons are not repeated
	seen := map[string]bool{}
	for i < len(parts) && len(parts[i]) == 1 && parts[i] != "x" {
		if seen[parts[i]] {
			return false
		}
		seen[parts[i]] = true
		i++
		count := 0
		for i < len(parts) && len(parts[i]) >= 2 {
			i++
			count++
		}
		if count == 0 {
			return false
		}
	}
	// privateuse = "x" 1*("-" (1*8alphanum))
	if i < len(parts) && parts[i] == "x" {
		return i+1 < len(parts)
	}
	return i == len(parts)
}

var bcp47Grandfathered = map[string]bool{
	"en-gb-oed": true, "i-ami": true, "i-bnn": true, "i-default": true, "i-enochian": true,
	"i-hak": true, "i-klingon": true, "i-lux": true, "i-mingo": true, "i-navajo": true,
	"i-pwn": true, "i-tao": true, "i-tay": true, "i-tsu": true, "sgn-be-fr": true,
	"sgn-be-nl": true, "sgn-ch-de": true, "art-lojban": true, "cel-gaulish": true,
	"no-bok": true, "no-nyn": true, "zh-guoyu": true, "zh-hakka": true, "zh-min": true,
	"zh-min-nan": true, "zh-xiang": true,
}

func isASCIIAlnum(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

func isASCIIAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

func isASCIIDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
