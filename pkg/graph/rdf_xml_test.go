package graph

import (
	"context"
	"strings"
	"testing"
)

// RDF/XML through ImportRDF, and the limits that keep a hostile document
// from taking the process down. The grammar itself is covered by the W3C
// suites (TestTheW3CRDF11RDFXMLSuitePassesInFull and the testdata subset).

func TestImportRDFReadsRDFXML(t *testing.T) {
	_, store, cleanup := setupTestGraph(t)
	defer cleanup()
	ctx := context.Background()
	doc := `<?xml version="1.0"?>
<!DOCTYPE rdf:RDF [ <!ENTITY ex "http://example.org/"> ]>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
         xmlns:owl="http://www.w3.org/2002/07/owl#"
         xmlns:ex="http://example.org/">
  <owl:Class rdf:about="&ex;Dog">
    <ex:label xml:lang="en">dog</ex:label>
  </owl:Class>
  <ex:Animal rdf:about="&ex;rex">
    <ex:owner rdf:resource="&ex;ann"/>
    <ex:age rdf:datatype="http://www.w3.org/2001/XMLSchema#integer">3</ex:age>
  </ex:Animal>
</rdf:RDF>`
	n, err := store.ImportRDF(ctx, strings.NewReader(doc), RDFFormatRDFXML)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n != 5 {
		t.Fatalf("imported %d triples, want 5", n)
	}
	res, err := store.ExecuteSPARQL(ctx, `SELECT ?age WHERE { <http://example.org/rex> <http://example.org/age> ?age }`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Bindings) != 1 || res.Bindings[0]["age"].Value != "3" || res.Bindings[0]["age"].Datatype != XSDNamespace+"integer" {
		t.Fatalf("age = %+v", res.Bindings)
	}
}

func TestRDFXMLRefusesWhatWouldExhaustTheProcess(t *testing.T) {
	deep := `<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns:ex="http://example.org/">` +
		strings.Repeat(`<ex:a><ex:b>`, maxRDFXMLDepth) + strings.Repeat(`</ex:b></ex:a>`, maxRDFXMLDepth) + `</rdf:RDF>`
	if _, err := parseRDFXML(deep, "http://example.org/doc"); err == nil || !strings.Contains(err.Error(), "nest") {
		t.Errorf("a document nested %d deep was accepted: %v", 2*maxRDFXMLDepth, err)
	}
	bomb := `<!DOCTYPE rdf:RDF [ <!ENTITY e "` + strings.Repeat("x", 1<<16) + `"> ]>` +
		`<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns:ex="http://example.org/">` +
		`<rdf:Description rdf:about="http://example.org/s"><ex:p>` + strings.Repeat("&e;", 1<<12) + `</ex:p></rdf:Description></rdf:RDF>`
	if _, err := parseRDFXML(bomb, "http://example.org/doc"); err == nil || !strings.Contains(err.Error(), "expand") {
		t.Errorf("an entity expansion of 256MB was accepted: %v", err)
	}
	if _, err := parseRDFXML(`<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description rdf:about="x" rdf:ID="y"/></rdf:RDF>`, "http://example.org/doc"); err == nil {
		t.Error("rdf:about with rdf:ID was accepted")
	}
}
