package graph

import "strings"

// The SPARQL 1.2 additions to the function library (§17.4): the functions on
// triple terms, and the ones on a literal's base direction.

// registerSPARQL12Functions is called by the library's own init, after the map
// exists: init functions run in file-name order, and this file sorts first.
func registerSPARQL12Functions() {
	for name, fn := range map[string]*sparqlFunction{
		"TRIPLE":     {3, 3, sparqlFnTriple},
		"ISTRIPLE":   {1, 1, sparqlFnKindTest(RDFTermTriple)},
		"SUBJECT":    {1, 1, sparqlFnTriplePart("SUBJECT", 0)},
		"PREDICATE":  {1, 1, sparqlFnTriplePart("PREDICATE", 1)},
		"OBJECT":     {1, 1, sparqlFnTriplePart("OBJECT", 2)},
		"LANGDIR":    {1, 1, sparqlFnLangDir},
		"HASLANG":    {1, 1, sparqlFnHasLang},
		"HASLANGDIR": {1, 1, sparqlFnHasLangDir},
		"STRLANGDIR": {3, 3, sparqlFnStrLangDir},
	} {
		sparqlFunctions[name] = fn
	}
}

// sparqlFnTriple is TRIPLE(s, p, o): the triple term, or an error when the
// three cannot form an RDF triple — which in a BIND leaves the variable
// unbound rather than storing something RDF 1.2 forbids.
func sparqlFnTriple(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	term, err := NewTripleTerm(a[0], a[1], a[2])
	if err != nil {
		return RDFTerm{}, sparqlTypeErrorf("TRIPLE: %v", err)
	}
	return term, nil
}

func sparqlFnTriplePart(name string, index int) func(*sparqlRuntime, []RDFTerm) (RDFTerm, error) {
	return func(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
		if a[0].Kind != RDFTermTriple {
			return RDFTerm{}, sparqlTypeErrorf("%s requires a triple term", name)
		}
		s, p, o, err := a[0].TripleTermParts()
		if err != nil {
			return RDFTerm{}, sparqlTypeErrorf("%s: %v", name, err)
		}
		return [3]RDFTerm{s, p, o}[index], nil
	}
}

// sparqlFnLangDir is LANGDIR: the base direction, "" for a literal without
// one, an error for anything that is not a literal.
func sparqlFnLangDir(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	if a[0].Kind != RDFTermLiteral {
		return RDFTerm{}, sparqlTypeErrorf("LANGDIR requires a literal")
	}
	return NewLiteral(a[0].BaseDirection()), nil
}

func sparqlFnHasLang(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	return booleanTerm(a[0].Kind == RDFTermLiteral && a[0].Language != ""), nil
}

func sparqlFnHasLangDir(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	return booleanTerm(a[0].Kind == RDFTermLiteral && a[0].BaseDirection() != ""), nil
}

// sparqlFnStrLangDir is STRLANGDIR(lexical, tag, direction). The direction
// must be exactly "ltr" or "rtl": the spec's own table makes "LTR" an error.
func sparqlFnStrLangDir(_ *sparqlRuntime, a []RDFTerm) (RDFTerm, error) {
	for _, arg := range a {
		if err := requireSimpleString("STRLANGDIR", arg); err != nil {
			return RDFTerm{}, err
		}
	}
	tag, direction := strings.TrimSpace(a[1].Value), a[2].Value
	if tag == "" {
		return RDFTerm{}, sparqlTypeErrorf("STRLANGDIR requires a non-empty language tag")
	}
	if direction != "ltr" && direction != "rtl" {
		return RDFTerm{}, sparqlTypeErrorf("STRLANGDIR requires the direction ltr or rtl, got %q", direction)
	}
	if !wellFormedLanguageTag(tag) {
		return RDFTerm{}, sparqlTypeErrorf("STRLANGDIR: %q is not a well-formed language tag", tag)
	}
	return NewDirLangLiteral(a[0].Value, tag, direction), nil
}
