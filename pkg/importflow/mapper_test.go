package importflow

import "testing"

func TestMapperRAGChunk(t *testing.T) {
	plan := TablePlan{RAG: &RAGPlan{
		ContentTmpl: "{title}\n\n{body}",
		IDColumn:    "id",
		Metadata:    []string{"author"},
	}}
	r := Record{Table: "docs", Values: map[string]string{
		"id": "7", "title": "Go", "body": "fast", "author": "Ada",
	}}
	chunk, ok := mapRAG(plan.RAG, r)
	if !ok {
		t.Fatal("mapRAG ok = false; want true")
	}
	if chunk.id != "7" || chunk.content != "Go\n\nfast" {
		t.Fatalf("chunk = %+v", chunk)
	}
	if chunk.metadata["author"] != "Ada" || chunk.metadata["_table"] != "docs" {
		t.Fatalf("metadata = %+v", chunk.metadata)
	}
}

func TestMapperStructuredTriples(t *testing.T) {
	plan := &KGPlan{
		Entities: []EntityMap{
			{Ref: "cust", Type: "Customer", IDTmpl: "{customer_id}", LabelTmpl: "{customer_id}"},
			{Ref: "prod", Type: "Product", IDTmpl: "{product_id}"},
		},
		Relations: []RelationMap{
			{Subject: "cust", Predicate: "purchased", Object: "prod"},
		},
	}
	r := Record{Table: "orders", Values: map[string]string{"customer_id": "c1", "product_id": "p9"}}
	triples := mapTriples(plan, r)
	// expect: cust rdf:type Customer, prod rdf:type Product, cust purchased prod,
	// plus the label triple for cust
	wantSubj := entityIRI("Customer", "c1")
	wantObj := entityIRI("Product", "p9")
	var rel bool
	for _, tr := range triples {
		if tr.Subject.Value == wantSubj &&
			tr.Predicate.Value == "urn:cortexdb:rel:purchased" &&
			tr.Object.Value == wantObj {
			rel = true
		}
	}
	if !rel {
		t.Fatalf("expected purchased relation in %+v", triples)
	}
}
