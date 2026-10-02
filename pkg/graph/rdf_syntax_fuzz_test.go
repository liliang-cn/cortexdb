package graph

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzParseTurtle12 throws arbitrary text at the Turtle 1.2 parser (and the
// TriG, N-Triples and N-Quads modes that share it). Two properties:
//
//   - it never panics and never recurses without bound: a malformed document
//     is an error;
//   - whatever it accepts survives a round trip — written as canonical
//     N-Quads and read back, it is the same dataset. That one property holds
//     the parser, the canonical writer and the triple-term encoding to each
//     other: a triple term spelled wrong, an escape written wrong, or a blank
//     node lost inside a triple term all break it.
func FuzzParseTurtle12(f *testing.F) {
	for _, seed := range []string{
		"", "@prefix : <http://e/> .", "PREFIX : <http://e/> :a :b :c .",
		`PREFIX : <http://e/> :a :b :c ~ :r {| :q "x"@en--ltr ; :n 1.5e3 |} .`,
		`PREFIX : <http://e/> << :a :b << _:x :c [] ~ _:r >> >> :q ( 1 2 [ :p :o ] ) .`,
		`PREFIX : <http://e/> :s :p <<( :a :b <<( _:c :d "é\t" )>> )>> .`,
		`PREFIX : <http://e/> :g { :a :b :c {| :d :e |} } GRAPH _:h { [] :p "x" }`,
		`<http://a> <http://b> <<( <http://c> <http://d> "e"@ar--rtl )>> <http://g> .`,
		"@base <http://x/y/> . <../z> <a> <#b> .", `VERSION "1.2" <a> <b> 'c' .`,
		"<<( <a> <b> <c> )>> <p> <o> .", "[] <p> <<( [] <q> [] )>> .",
		strings.Repeat("<<( <a> <b> ", 70) + "<c>" + strings.Repeat(" )>>", 70),
		strings.Repeat("[ <p> ", 300) + strings.Repeat("]", 300),
	} {
		f.Add(seed)
	}
	// The W3C documents in testdata are good seeds: real syntax, every
	// RDF 1.2 production, and the negative cases right next to the positive.
	_ = filepath.WalkDir(filepath.Join("testdata", "w3c", "rdf"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (strings.HasSuffix(path, ".ttl") || strings.HasSuffix(path, ".trig") || strings.HasSuffix(path, ".nt")) {
			if data, err := os.ReadFile(path); err == nil && len(data) < 4096 {
				f.Add(string(data))
			}
		}
		return nil
	})

	f.Fuzz(func(t *testing.T, doc string) {
		for _, syntax := range []rdfSyntax{rdfSyntaxTurtle, rdfSyntaxTriG, rdfSyntaxNTriples, rdfSyntaxNQuads} {
			parsed, err := parseRDFDocument(doc, syntax, "http://example.org/base/")
			if err != nil {
				continue
			}
			if !utf8.ValidString(doc) {
				t.Fatalf("%s accepted invalid UTF-8", syntax)
			}
			var buf bytes.Buffer
			if err := writeCanonicalNQuads(&buf, parsed); err != nil {
				// The parser handed back a term canonical form cannot
				// spell, which only a parser bug can produce.
				t.Fatalf("%s accepted %q but its output cannot be written: %v", syntax, doc, err)
			}
			again, err := parseRDFDocument(buf.String(), rdfSyntaxNQuads, "")
			if err != nil {
				t.Fatalf("%s output is not valid N-Quads: %v\n%s", syntax, err, buf.String())
			}
			if ok, diff := isomorphicDatasets(again, parsed); !ok {
				t.Fatalf("%s round trip changed the data: %s", syntax, diff)
			}
		}
	})
}
