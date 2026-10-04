package graph

// The SPARQL 1.1 (and 1.2) query results formats: JSON, XML, CSV and TSV.
//
// SPARQLResult is the engine's own shape; these are the documents other
// SPARQL software reads. JSON and XML carry everything, triple terms and
// base directions included; CSV is lossy by design (values only, no types or
// term kinds) and TSV writes each term as in Turtle. A graph result
// (CONSTRUCT, DESCRIBE) is RDF, not a result set: write it with an RDF
// syntax instead.

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// SPARQLResultsFormat names a query results format.
type SPARQLResultsFormat string

const (
	// SPARQLResultsJSON is application/sparql-results+json.
	SPARQLResultsJSON SPARQLResultsFormat = "json"
	// SPARQLResultsXML is application/sparql-results+xml.
	SPARQLResultsXML SPARQLResultsFormat = "xml"
	// SPARQLResultsCSV is text/csv: values only, for SELECT.
	SPARQLResultsCSV SPARQLResultsFormat = "csv"
	// SPARQLResultsTSV is text/tab-separated-values, for SELECT.
	SPARQLResultsTSV SPARQLResultsFormat = "tsv"
)

// MediaType is the format's registered media type.
func (f SPARQLResultsFormat) MediaType() string {
	switch f {
	case SPARQLResultsJSON:
		return "application/sparql-results+json"
	case SPARQLResultsXML:
		return "application/sparql-results+xml"
	case SPARQLResultsCSV:
		return "text/csv; charset=utf-8"
	case SPARQLResultsTSV:
		return "text/tab-separated-values; charset=utf-8"
	}
	return ""
}

// WriteResults writes a SELECT or ASK result in the given results format.
func (r *SPARQLResult) WriteResults(w io.Writer, format SPARQLResultsFormat) error {
	if r == nil {
		return fmt.Errorf("no result to write")
	}
	switch r.QueryType {
	case SPARQLQueryConstruct, SPARQLQueryDescribe:
		return fmt.Errorf("a %s result is a graph: write it with an RDF syntax, not a results format", r.QueryType)
	}
	ask := r.QueryType == SPARQLQueryAsk
	vars := r.resultVars()
	switch format {
	case SPARQLResultsJSON:
		return writeResultsJSON(w, ask, r.Boolean, vars, r.Bindings)
	case SPARQLResultsXML:
		return writeResultsXML(w, ask, r.Boolean, vars, r.Bindings)
	case SPARQLResultsCSV, SPARQLResultsTSV:
		if ask {
			return fmt.Errorf("%s carries SELECT results only", strings.ToUpper(string(format)))
		}
		return writeResultsDelimited(w, format == SPARQLResultsTSV, vars, r.Bindings)
	}
	return fmt.Errorf("unsupported SPARQL results format %q", format)
}

// resultVars is Vars, or for a result built without them the variables the
// bindings use, in first-seen order.
func (r *SPARQLResult) resultVars() []string {
	if len(r.Vars) > 0 {
		return r.Vars
	}
	return collectBindingVars(r.Bindings)
}

// --- JSON ---------------------------------------------------------------------

type jsonResultTerm struct {
	Type     string `json:"type"`
	Value    any    `json:"value"`
	Lang     string `json:"xml:lang,omitempty"`
	Dir      string `json:"its:dir,omitempty"`
	Datatype string `json:"datatype,omitempty"`
}

func jsonTerm(t RDFTerm) (jsonResultTerm, error) {
	switch t.Kind {
	case RDFTermIRI:
		return jsonResultTerm{Type: "uri", Value: t.Value}, nil
	case RDFTermBlankNode:
		return jsonResultTerm{Type: "bnode", Value: t.Value}, nil
	case RDFTermTriple:
		triple, err := decodeTripleTermValue(t.Value)
		if err != nil {
			return jsonResultTerm{}, err
		}
		parts := map[string]jsonResultTerm{}
		for name, part := range map[string]RDFTerm{"subject": triple.Subject, "predicate": triple.Predicate, "object": triple.Object} {
			if parts[name], err = jsonTerm(part); err != nil {
				return jsonResultTerm{}, err
			}
		}
		return jsonResultTerm{Type: "triple", Value: parts}, nil
	}
	out := jsonResultTerm{Type: "literal", Value: t.Value}
	switch {
	case t.Language != "":
		out.Lang, out.Dir = t.LanguageTag(), t.BaseDirection()
	case t.Datatype != "" && t.Datatype != XSDNamespace+"string":
		out.Datatype = t.Datatype
	}
	return out, nil
}

func writeResultsJSON(w io.Writer, ask, boolean bool, vars []string, rows []map[string]RDFTerm) error {
	type head struct {
		Vars []string `json:"vars,omitempty"`
	}
	if ask {
		return json.NewEncoder(w).Encode(struct {
			Head    head `json:"head"`
			Boolean bool `json:"boolean"`
		}{head{}, boolean})
	}
	bindings := make([]map[string]jsonResultTerm, len(rows))
	for i, row := range rows {
		b := make(map[string]jsonResultTerm, len(row))
		for _, v := range vars {
			term, ok := row[v]
			if !ok || term.Kind == "" {
				continue
			}
			jt, err := jsonTerm(term)
			if err != nil {
				return err
			}
			b[v] = jt
		}
		bindings[i] = b
	}
	if vars == nil {
		vars = []string{}
	}
	return json.NewEncoder(w).Encode(struct {
		Head struct {
			Vars []string `json:"vars"`
		} `json:"head"`
		Results struct {
			Bindings []map[string]jsonResultTerm `json:"bindings"`
		} `json:"results"`
	}{
		Head: struct {
			Vars []string `json:"vars"`
		}{vars},
		Results: struct {
			Bindings []map[string]jsonResultTerm `json:"bindings"`
		}{bindings},
	})
}

// --- XML ----------------------------------------------------------------------

func writeResultsXML(w io.Writer, ask, boolean bool, vars []string, rows []map[string]RDFTerm) error {
	b := bufio.NewWriter(w)
	b.WriteString(`<?xml version="1.0"?>` + "\n")
	b.WriteString(`<sparql xmlns="http://www.w3.org/2005/sparql-results#" xmlns:its="http://www.w3.org/2005/11/its">` + "\n")
	b.WriteString("  <head>\n")
	for _, v := range vars {
		if !ask {
			fmt.Fprintf(b, "    <variable name=\"%s\"/>\n", xmlEscape(v))
		}
	}
	b.WriteString("  </head>\n")
	if ask {
		fmt.Fprintf(b, "  <boolean>%t</boolean>\n", boolean)
	} else {
		b.WriteString("  <results>\n")
		for _, row := range rows {
			b.WriteString("    <result>\n")
			for _, v := range vars {
				term, ok := row[v]
				if !ok || term.Kind == "" {
					continue
				}
				fmt.Fprintf(b, "      <binding name=\"%s\">", xmlEscape(v))
				if err := writeXMLTerm(b, term); err != nil {
					return err
				}
				b.WriteString("</binding>\n")
			}
			b.WriteString("    </result>\n")
		}
		b.WriteString("  </results>\n")
	}
	b.WriteString("</sparql>\n")
	return b.Flush()
}

func writeXMLTerm(b *bufio.Writer, t RDFTerm) error {
	switch t.Kind {
	case RDFTermIRI:
		fmt.Fprintf(b, "<uri>%s</uri>", xmlEscape(t.Value))
	case RDFTermBlankNode:
		fmt.Fprintf(b, "<bnode>%s</bnode>", xmlEscape(t.Value))
	case RDFTermTriple:
		triple, err := decodeTripleTermValue(t.Value)
		if err != nil {
			return err
		}
		b.WriteString("<triple>")
		for _, part := range []struct {
			name string
			term RDFTerm
		}{{"subject", triple.Subject}, {"predicate", triple.Predicate}, {"object", triple.Object}} {
			fmt.Fprintf(b, "<%s>", part.name)
			if err := writeXMLTerm(b, part.term); err != nil {
				return err
			}
			fmt.Fprintf(b, "</%s>", part.name)
		}
		b.WriteString("</triple>")
	default:
		b.WriteString("<literal")
		switch {
		case t.Language != "":
			fmt.Fprintf(b, " xml:lang=\"%s\"", xmlEscape(t.LanguageTag()))
			if dir := t.BaseDirection(); dir != "" {
				fmt.Fprintf(b, " its:dir=\"%s\"", dir)
			}
		case t.Datatype != "" && t.Datatype != XSDNamespace+"string":
			fmt.Fprintf(b, " datatype=\"%s\"", xmlEscape(t.Datatype))
		}
		fmt.Fprintf(b, ">%s</literal>", xmlEscape(t.Value))
	}
	return nil
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// --- CSV and TSV --------------------------------------------------------------

// tsvBareNumber is the Turtle INTEGER, DECIMAL and DOUBLE grammar: a number
// TSV may write without quotes and datatype, as Turtle would.
var (
	tsvBareInteger = regexp.MustCompile(`^[+-]?[0-9]+$`)
	tsvBareDecimal = regexp.MustCompile(`^[+-]?[0-9]*\.[0-9]+$`)
	tsvBareDouble  = regexp.MustCompile(`^[+-]?([0-9]+\.[0-9]*|\.[0-9]+|[0-9]+)[eE][+-]?[0-9]+$`)
)

func writeResultsDelimited(w io.Writer, tsv bool, vars []string, rows []map[string]RDFTerm) error {
	b := bufio.NewWriter(w)
	sep, eol := ",", "\r\n"
	if tsv {
		sep, eol = "\t", "\n"
	}
	for i, v := range vars {
		if i > 0 {
			b.WriteString(sep)
		}
		if tsv {
			b.WriteString("?" + v)
		} else {
			b.WriteString(csvField(v))
		}
	}
	b.WriteString(eol)
	for _, row := range rows {
		for i, v := range vars {
			if i > 0 {
				b.WriteString(sep)
			}
			term, ok := row[v]
			if !ok || term.Kind == "" {
				continue
			}
			if tsv {
				text, err := tsvTerm(term)
				if err != nil {
					return err
				}
				b.WriteString(text)
				continue
			}
			text, err := csvValue(term)
			if err != nil {
				return err
			}
			b.WriteString(csvField(text))
		}
		b.WriteString(eol)
	}
	return b.Flush()
}

// csvValue is a term's CSV value: an IRI or literal's text, a blank node as
// _:label, a triple term in N-Triples form.
func csvValue(t RDFTerm) (string, error) {
	switch t.Kind {
	case RDFTermBlankNode:
		return "_:" + t.Value, nil
	case RDFTermTriple:
		return canonicalTerm(t)
	}
	return t.Value, nil
}

// csvField quotes a field per RFC 4180 when it holds a comma, a quote or a
// line break, or begins or ends with a space.
func csvField(s string) string {
	if s == "" || !strings.ContainsAny(s, ",\"\r\n") && strings.TrimSpace(s) == s {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func tsvTerm(t RDFTerm) (string, error) {
	if t.Kind == RDFTermLiteral && t.Language == "" {
		switch t.Datatype {
		case XSDNamespace + "integer":
			if tsvBareInteger.MatchString(t.Value) {
				return t.Value, nil
			}
		case XSDNamespace + "decimal":
			if tsvBareDecimal.MatchString(t.Value) {
				return t.Value, nil
			}
		case XSDNamespace + "double":
			if tsvBareDouble.MatchString(t.Value) {
				return t.Value, nil
			}
		case XSDNamespace + "boolean":
			if t.Value == "true" || t.Value == "false" {
				return t.Value, nil
			}
		}
	}
	return canonicalTerm(t)
}
