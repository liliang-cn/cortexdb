package graph

// Function calls by IRI: the XSD constructor casts, and everything else.
//
// SPARQL's grammar has iriOrFunction — an IRI followed by an argument list is
// a call — and the only such functions the spec defines are the casts of §17.5,
// xsd:string(?x), xsd:integer(?x) and the rest. The parser read an IRI in an
// expression as a constant and failed on the '(' after it, so every cast, and
// every query naming an extension function, was a syntax error.
//
// A cast follows the XPath casting table SPARQL adopts: from a string its
// lexical form must be valid for the target type; from a number, a value
// conversion; from a boolean, 1 or 0; anything else — and a language-tagged
// string, an IRI to a number, a blank node to anything — is an error, which a
// SELECT expression reports by leaving its variable unbound. An IRI this
// engine does not know is still a well-formed call: it evaluates to an error,
// as SPARQL says an unknown function must.

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// xsdIntegerLexical and xsdDecimalLexical are shared with SHACL's datatype
// checks (shacl_constraints.go); doubles and floats also take an exponent.
var castDoubleLexical = regexp.MustCompile(`^([+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)([eE][+-]?[0-9]+)?|[+-]?INF|NaN)$`)

// sparqlIRIFunctions are the functions called by IRI, keyed by full IRI.
var sparqlIRIFunctions = map[string]*sparqlFunction{
	XSDNamespace + "string":   {1, 1, castToString},
	XSDNamespace + "boolean":  {1, 1, castToBoolean},
	XSDNamespace + "integer":  {1, 1, castToNumber(sparqlNumInteger)},
	XSDNamespace + "decimal":  {1, 1, castToNumber(sparqlNumDecimal)},
	XSDNamespace + "float":    {1, 1, castToNumber(sparqlNumFloat)},
	XSDNamespace + "double":   {1, 1, castToNumber(sparqlNumDouble)},
	XSDNamespace + "dateTime": {1, 1, castToDateTime},
}

// unknownIRIFunction is any other IRI called as a function.
func unknownIRIFunction(iri string) *sparqlFunction {
	return &sparqlFunction{0, -1, func(*sparqlRuntime, []RDFTerm) (RDFTerm, error) {
		return RDFTerm{}, sparqlTypeErrorf("unknown function <%s>", iri)
	}}
}

// castSource is what a cast reads: the lexical form of a string, or the
// value of a typed literal.
func castLexical(term RDFTerm) (string, bool) {
	if term.Kind != RDFTermLiteral || term.Language != "" {
		return "", false
	}
	if term.Datatype == "" || term.Datatype == XSDNamespace+"string" {
		return strings.TrimSpace(term.Value), true
	}
	return "", false
}

func castToString(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	switch a[0].Kind {
	case RDFTermIRI:
		return NewTypedLiteral(a[0].Value, XSDNamespace+"string"), nil
	case RDFTermLiteral:
		if a[0].Language != "" {
			return RDFTerm{}, sparqlTypeErrorf("xsd:string cannot cast a language-tagged string")
		}
		if n, ok := strictNumber(a[0]); ok {
			return NewTypedLiteral(xpathNumberString(n), XSDNamespace+"string"), nil
		}
		if a[0].Datatype == XSDNamespace+"boolean" {
			b, err := castToBoolean(nil, a)
			if err != nil {
				return RDFTerm{}, err
			}
			return NewTypedLiteral(b.Value, XSDNamespace+"string"), nil
		}
		return NewTypedLiteral(a[0].Value, XSDNamespace+"string"), nil
	}
	return RDFTerm{}, sparqlTypeErrorf("xsd:string cannot cast %s", a[0].Kind)
}

func castToBoolean(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	t := a[0]
	if t.Kind == RDFTermLiteral && t.Datatype == XSDNamespace+"boolean" {
		switch strings.TrimSpace(t.Value) {
		case "true", "1":
			return booleanTerm(true), nil
		case "false", "0":
			return booleanTerm(false), nil
		}
		return RDFTerm{}, sparqlTypeErrorf("invalid xsd:boolean %q", t.Value)
	}
	if n, ok := strictNumber(t); ok {
		return booleanTerm(!(n.value == 0 || math.IsNaN(n.value))), nil
	}
	if lexical, ok := castLexical(t); ok {
		switch lexical {
		case "true", "1":
			return booleanTerm(true), nil
		case "false", "0":
			return booleanTerm(false), nil
		}
		return RDFTerm{}, sparqlTypeErrorf("cannot cast %q to xsd:boolean", lexical)
	}
	return RDFTerm{}, sparqlTypeErrorf("cannot cast to xsd:boolean")
}

func castToNumber(kind int) func(*sparqlRuntime, []RDFTerm) (RDFTerm, error) {
	return func(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
		t := a[0]
		var value float64
		switch {
		case t.Kind == RDFTermLiteral && t.Datatype == XSDNamespace+"boolean":
			switch strings.TrimSpace(t.Value) {
			case "true", "1":
				value = 1
			case "false", "0":
				value = 0
			default:
				return RDFTerm{}, sparqlTypeErrorf("invalid xsd:boolean %q", t.Value)
			}
		default:
			if n, ok := strictNumber(t); ok {
				value = n.value
				break
			}
			lexical, ok := castLexical(t)
			if !ok {
				return RDFTerm{}, sparqlTypeErrorf("cannot cast to a number")
			}
			var parsed bool
			value, parsed = parseCastLexical(kind, lexical)
			if !parsed {
				return RDFTerm{}, sparqlTypeErrorf("%q is not a valid lexical form for the target type", lexical)
			}
		}
		if kind == sparqlNumInteger || kind == sparqlNumDecimal {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return RDFTerm{}, sparqlTypeErrorf("NaN and INF have no exact value")
			}
			if kind == sparqlNumInteger {
				value = math.Trunc(value)
			}
		}
		return NewTypedLiteral(canonicalNumberLexical(sparqlNumber{kind: kind, value: value}), numberDatatype(kind)), nil
	}
}

func parseCastLexical(kind int, lexical string) (float64, bool) {
	switch kind {
	case sparqlNumInteger:
		if !xsdIntegerLexical.MatchString(lexical) {
			return 0, false
		}
	case sparqlNumDecimal:
		if !xsdDecimalLexical.MatchString(lexical) {
			return 0, false
		}
	default:
		if !castDoubleLexical.MatchString(lexical) {
			return 0, false
		}
	}
	return parseSPARQLFloat(lexical)
}

func numberDatatype(kind int) string {
	switch kind {
	case sparqlNumDecimal:
		return xsdDecimalIRI
	case sparqlNumFloat:
		return xsdFloatIRI
	case sparqlNumDouble:
		return xsdDoubleIRI
	}
	return xsdIntegerIRI
}

// canonicalNumberLexical is the XSD canonical form: integers without a
// point, decimals with at least one fractional digit, floats and doubles in
// scientific notation.
func canonicalNumberLexical(n sparqlNumber) string {
	switch {
	case math.IsNaN(n.value):
		return "NaN"
	case math.IsInf(n.value, 1):
		return "INF"
	case math.IsInf(n.value, -1):
		return "-INF"
	}
	switch n.kind {
	case sparqlNumInteger:
		return strconv.FormatFloat(math.Trunc(n.value), 'f', -1, 64)
	case sparqlNumDecimal:
		s := strconv.FormatFloat(decimalValue(n.value), 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		if s == "-0.0" {
			s = "0.0"
		}
		return s
	}
	if n.value == 0 {
		return "0.0E0"
	}
	s := strconv.FormatFloat(n.value, 'E', -1, 64)
	mantissa, exp, _ := strings.Cut(s, "E")
	if !strings.Contains(mantissa, ".") {
		mantissa += ".0"
	}
	e, _ := strconv.Atoi(exp)
	return mantissa + "E" + strconv.Itoa(e)
}

// xpathNumberString is XPath's cast of a number to xs:string: no trailing
// ".0", and decimal notation for floats and doubles between 1e-6 and 1e6,
// scientific outside it.
func xpathNumberString(n sparqlNumber) string {
	switch {
	case math.IsNaN(n.value):
		return "NaN"
	case math.IsInf(n.value, 1):
		return "INF"
	case math.IsInf(n.value, -1):
		return "-INF"
	}
	if n.kind == sparqlNumFloat || n.kind == sparqlNumDouble {
		if a := math.Abs(n.value); a != 0 && (a < 1e-6 || a >= 1e6) {
			return canonicalNumberLexical(n)
		}
	}
	if n.kind == sparqlNumInteger {
		return canonicalNumberLexical(n)
	}
	value := n.value
	if n.kind == sparqlNumDecimal {
		value = decimalValue(value)
	}
	s := strconv.FormatFloat(value, 'f', -1, 64)
	if s == "-0" {
		s = "0"
	}
	return s
}

func castToDateTime(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	t := a[0]
	if t.Kind == RDFTermLiteral && t.Datatype == XSDNamespace+"dateTime" {
		return t, nil
	}
	lexical, ok := castLexical(t)
	if !ok {
		return RDFTerm{}, sparqlTypeErrorf("cannot cast to xsd:dateTime")
	}
	for _, layout := range []string{"2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05.999999999Z07:00", "2006-01-02T15:04:05", "2006-01-02T15:04:05.999999999"} {
		if _, err := time.Parse(layout, lexical); err == nil {
			return NewTypedLiteral(lexical, XSDNamespace+"dateTime"), nil
		}
	}
	return RDFTerm{}, sparqlTypeErrorf("%q is not an xsd:dateTime", lexical)
}
