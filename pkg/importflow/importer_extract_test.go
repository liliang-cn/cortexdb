package importflow

import (
	"context"
	"strings"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// fakeExtractor returns a fixed edge for any document, exercising the text_extract path.
type fakeExtractor struct{ edges []graphflow.ExtractionEdge }

func (f fakeExtractor) Extract(_ context.Context, doc graphflow.SourceDocument) (*graphflow.ExtractionResult, error) {
	return &graphflow.ExtractionResult{SourceID: doc.ID, Edges: f.edges}, nil
}

// TestImporter_TextExtractMintsIRIs verifies that free-text extraction produces valid
// RDF triples (IRI subjects, not literals) — a literal subject was previously rejected
// by the graph store with "rdf subject must be iri or blank node".
func TestImporter_TextExtractMintsIRIs(t *testing.T) {
	ctx := context.Background()
	db, err := cortexdb.Open(cortexdb.DefaultConfig(t.TempDir() + "/extract.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ext := fakeExtractor{edges: []graphflow.ExtractionEdge{
		{Source: "Headphones", Relation: "pairs_with", Target: "Phone"},
	}}
	imp := New(db, WithExtractor(ext))

	plan := MappingPlan{Tables: map[string]TablePlan{
		"reviews": {KG: &KGPlan{TextExtract: []TextExtract{{Column: "body"}}}},
	}}
	csv := "id,body\n1,The headphones pair with the phone.\n"
	src, err := NewCSVSource(strings.NewReader(csv), CSVOptions{Table: "reviews"})
	if err != nil {
		t.Fatalf("csv: %v", err)
	}
	defer src.Close()

	rep, err := imp.Run(ctx, src, plan)
	if err != nil {
		t.Fatalf("run must not error on text extraction, got: %v", err)
	}
	if rep.TriplesCreated == 0 {
		t.Fatal("expected triples from text extraction")
	}

	resp, err := db.QueryKnowledgeGraph(ctx, cortexdb.KnowledgeGraphQueryRequest{
		Query: "SELECT ?s ?p ?o WHERE { ?s ?p ?o }",
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	var sawRel, sawIRISubject bool
	for _, b := range resp.Result.Bindings {
		if strings.HasPrefix(b["s"].Value, "urn:cortexdb:text:") {
			sawIRISubject = true
		}
		if b["p"].Value == "urn:cortexdb:rel:pairs_with" {
			sawRel = true
		}
	}
	if !sawIRISubject {
		t.Fatal("expected minted urn:cortexdb:text: IRI subjects from extraction")
	}
	if !sawRel {
		t.Fatal("expected the extracted relation pairs_with in the graph")
	}
}
