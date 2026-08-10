package rpcserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

func iri(v string) *rpcv1.RdfTerm     { return &rpcv1.RdfTerm{Kind: "iri", Value: v} }
func literal(v string) *rpcv1.RdfTerm { return &rpcv1.RdfTerm{Kind: "literal", Value: v} }

func TestKnowledgeGraphRoundTrip(t *testing.T) {
	conn := newTestConn(t, false, "")
	client := rpcv1.NewKnowledgeGraphServiceClient(conn)
	ctx := context.Background()

	ns, err := client.UpsertNamespace(ctx, &rpcv1.UpsertNamespaceRequest{Prefix: "ex", Uri: "https://example.com/"})
	if err != nil || ns.GetNamespace().GetPrefix() != "ex" {
		t.Fatalf("namespace: %v", err)
	}
	nsList, err := client.ListNamespaces(ctx, &rpcv1.ListNamespacesRequest{})
	if err != nil || len(nsList.GetNamespaces()) == 0 {
		t.Fatalf("list namespaces: %v", err)
	}

	up, err := client.UpsertKnowledgeGraph(ctx, &rpcv1.UpsertKnowledgeGraphRequest{
		Triples: []*rpcv1.RdfTriple{
			{Subject: iri("ex:alice"), Predicate: iri("ex:knows"), Object: iri("ex:bob")},
			{Subject: iri("ex:alice"), Predicate: iri("schema:name"), Object: literal("Alice")},
		},
	})
	if err != nil || up.GetCount() != 2 {
		t.Fatalf("upsert: %v count=%d", err, up.GetCount())
	}

	found, err := client.FindKnowledgeGraph(ctx, &rpcv1.FindKnowledgeGraphRequest{
		Pattern: &rpcv1.TriplePattern{Subject: iri("ex:alice")},
	})
	if err != nil || len(found.GetTriples()) != 2 {
		t.Fatalf("find: %v n=%d", err, len(found.GetTriples()))
	}

	q, err := client.QuerySparql(ctx, &rpcv1.QuerySparqlRequest{
		Query: `PREFIX schema: <https://schema.org/>
SELECT ?name WHERE { <https://example.com/alice> schema:name ?name . }`,
	})
	if err != nil || q.GetResult().GetCount() != 1 {
		t.Fatalf("sparql: %v result=%+v", err, q.GetResult())
	}

	exp, err := client.ExportKnowledgeGraph(ctx, &rpcv1.ExportKnowledgeGraphRequest{Format: "ntriples"})
	if err != nil || !strings.Contains(exp.GetContent(), "<https://example.com/alice>") {
		t.Fatalf("export: %v content=%q", err, exp.GetContent())
	}

	imp, err := client.ImportKnowledgeGraph(ctx, &rpcv1.ImportKnowledgeGraphRequest{
		Format:  "ntriples",
		Content: "<https://example.com/carol> <https://schema.org/name> \"Carol\" .\n",
	})
	if err != nil || imp.GetCount() != 1 {
		t.Fatalf("import: %v count=%d", err, imp.GetCount())
	}

	shapeID := "https://example.com/PersonShape"
	shacl, err := client.ValidateShacl(ctx, &rpcv1.ValidateShaclRequest{
		Shapes: []*rpcv1.RdfTriple{
			{Subject: iri(shapeID), Predicate: iri("http://www.w3.org/1999/02/22-rdf-syntax-ns#type"), Object: iri("http://www.w3.org/ns/shacl#NodeShape")},
			{Subject: iri(shapeID), Predicate: iri("http://www.w3.org/ns/shacl#targetClass"), Object: iri("https://schema.org/Person")},
		},
	})
	if err != nil || !shacl.GetReport().GetConforms() {
		t.Fatalf("shacl: %v report=%+v", err, shacl.GetReport())
	}

	ref, err := client.RefreshInference(ctx, &rpcv1.RefreshInferenceRequest{})
	if err != nil {
		t.Fatalf("inference refresh: %v", err)
	}
	if ref.GetResult().GetExplicitCount() == 0 {
		t.Fatalf("inference refresh explicit_count = 0")
	}
	if _, err := client.SummarizeInference(ctx, &rpcv1.SummarizeInferenceRequest{}); err != nil {
		t.Fatalf("inference summary: %v", err)
	}

	del, err := client.DeleteKnowledgeGraph(ctx, &rpcv1.DeleteKnowledgeGraphRequest{
		Pattern: &rpcv1.TriplePattern{Subject: iri("ex:alice"), Predicate: iri("ex:knows")},
	})
	if err != nil || del.GetDeleted() != 1 {
		t.Fatalf("delete: %v deleted=%d", err, del.GetDeleted())
	}
}

func TestOntologyRoundTrip(t *testing.T) {
	conn := newTestConn(t, false, "")
	client := rpcv1.NewKnowledgeGraphServiceClient(conn)
	ctx := context.Background()

	schemaJSON := `{
		"schema_id": "s1",
		"name": "test",
		"object_types": [{
			"api_name": "Person",
			"primary_key": "employeeId",
			"properties": [
				{"api_name": "employeeId", "data_type": {"kind": "string"}, "required": true},
				{"api_name": "fullName", "data_type": {"kind": "string"}}
			]
		}],
		"link_types": [{
			"api_name": "knows",
			"a": {"api_name": "knowsFrom", "object_type_api_name": "Person", "cardinality": "MANY"},
			"b": {"api_name": "knownBy", "object_type_api_name": "Person", "cardinality": "MANY"}
		}]
	}`

	saved, err := client.SaveOntologySchema(ctx, &rpcv1.SaveOntologySchemaRequest{
		SchemaId: "s1", Name: "test", Activate: true, SchemaJson: schemaJSON,
	})
	if err != nil || saved.GetSchema().GetSchemaId() != "s1" {
		t.Fatalf("save ontology: %v", err)
	}
	got, err := client.GetOntologySchema(ctx, &rpcv1.GetOntologySchemaRequest{SchemaId: "s1"})
	if err != nil || got.GetSchema().GetName() != "test" {
		t.Fatalf("get ontology: %v", err)
	}

	// The v2 schema survives the round trip only via schema_json.
	var decoded cortexdb.OntologySchema
	if err := json.Unmarshal([]byte(got.GetSchema().GetSchemaJson()), &decoded); err != nil {
		t.Fatalf("decode schema_json: %v", err)
	}
	if len(decoded.ObjectTypes) != 1 || decoded.ObjectTypes[0].PrimaryKey != "employeeId" {
		t.Fatalf("v2 object type lost over the wire: %+v", decoded.ObjectTypes)
	}
	if len(decoded.LinkTypes) != 1 || decoded.LinkTypes[0].A.Cardinality != cortexdb.OntologyCardinalityMany {
		t.Fatalf("v2 link type lost over the wire: %+v", decoded.LinkTypes)
	}
	list, err := client.ListOntologySchemas(ctx, &rpcv1.ListOntologySchemasRequest{})
	if err != nil || len(list.GetSchemas()) != 1 {
		t.Fatalf("list ontology: %v", err)
	}
	del, err := client.DeleteOntologySchema(ctx, &rpcv1.DeleteOntologySchemaRequest{SchemaId: "s1"})
	if err != nil || !del.GetDeleted() {
		t.Fatalf("delete ontology: %v", err)
	}
}

func TestOntologyRejectsPreV2Request(t *testing.T) {
	conn := newTestConn(t, false, "")
	client := rpcv1.NewKnowledgeGraphServiceClient(conn)
	ctx := context.Background()

	// A client built against the ontology-lite wire format. Its request must
	// fail loudly rather than be accepted and silently store nothing.
	_, err := client.SaveOntologySchema(ctx, &rpcv1.SaveOntologySchemaRequest{
		SchemaId:      "legacy",
		EntityTypes:   []*rpcv1.OntologyEntityType{{Name: "Person"}},
		RelationTypes: []*rpcv1.OntologyRelationType{{Name: "knows"}},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
	if !strings.Contains(err.Error(), "schema_json") {
		t.Fatalf("error should point the caller at schema_json, got %v", err)
	}
}

func TestOntologyInvalidSchemaIsInvalidArgument(t *testing.T) {
	conn := newTestConn(t, false, "")
	client := rpcv1.NewKnowledgeGraphServiceClient(conn)
	ctx := context.Background()

	// A schema whose object type has no primary key. This is the caller's
	// mistake, so it must not be reported as an internal server fault.
	_, err := client.SaveOntologySchema(ctx, &rpcv1.SaveOntologySchemaRequest{
		SchemaId: "broken",
		SchemaJson: `{
			"schema_id": "broken",
			"object_types": [{
				"api_name": "Person",
				"properties": [{"api_name": "name", "data_type": {"kind": "string"}, "required": true}]
			}]
		}`,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got code=%s err=%v", status.Code(err), err)
	}
	if !strings.Contains(err.Error(), "primary_key") {
		t.Fatalf("error should keep the validation detail, got %v", err)
	}
}
