package cortexdb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The dispatch path decodes raw JSON into the request types by hand, so a
// typo'd case label or a mismatched request type would go unnoticed: the MCP
// server registers its handlers through a different mechanism, and the Go API
// tests never go through Call.
func TestOntologyToolDispatchRoundTrip(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	savePayload, err := json.Marshal(OntologySaveRequest{Schema: validAviationSchema(), Activate: true})
	if err != nil {
		t.Fatalf("marshal save payload: %v", err)
	}
	saved, err := tools.Call(ctx, "ontology_save", savePayload)
	if err != nil {
		t.Fatalf("dispatch ontology_save: %v", err)
	}
	saveResp, ok := saved.(*OntologySaveResponse)
	if !ok {
		t.Fatalf("unexpected ontology_save response type: %T", saved)
	}
	if saveResp.Schema.Version != 1 || !saveResp.Schema.Active {
		t.Fatalf("unexpected saved schema: version=%d active=%v", saveResp.Schema.Version, saveResp.Schema.Active)
	}

	getPayload, err := json.Marshal(OntologyGetRequest{SchemaID: "aviation"})
	if err != nil {
		t.Fatalf("marshal get payload: %v", err)
	}
	got, err := tools.Call(ctx, "ontology_get", getPayload)
	if err != nil {
		t.Fatalf("dispatch ontology_get: %v", err)
	}
	getResp, ok := got.(*OntologyGetResponse)
	if !ok {
		t.Fatalf("unexpected ontology_get response type: %T", got)
	}
	if len(getResp.Schema.ObjectTypes) != 2 {
		t.Fatalf("expected 2 object types through dispatch, got %d", len(getResp.Schema.ObjectTypes))
	}
	if getResp.Schema.ObjectTypes[0].APIName != "Airport" {
		t.Fatalf("casing lost through dispatch: %q", getResp.Schema.ObjectTypes[0].APIName)
	}
	if getResp.Schema.ObjectTypes[0].PrimaryKey != "iataCode" {
		t.Fatalf("primary key lost through dispatch: %q", getResp.Schema.ObjectTypes[0].PrimaryKey)
	}

	listed, err := tools.Call(ctx, "ontology_list", json.RawMessage(`{"active_only":true}`))
	if err != nil {
		t.Fatalf("dispatch ontology_list: %v", err)
	}
	listResp, ok := listed.(*OntologyListResponse)
	if !ok {
		t.Fatalf("unexpected ontology_list response type: %T", listed)
	}
	if len(listResp.Schemas) != 1 || listResp.Schemas[0].SchemaID != "aviation" {
		t.Fatalf("unexpected active list through dispatch: %+v", listResp.Schemas)
	}

	deletePayload, err := json.Marshal(OntologyDeleteRequest{SchemaID: "aviation"})
	if err != nil {
		t.Fatalf("marshal delete payload: %v", err)
	}
	deleted, err := tools.Call(ctx, "ontology_delete", deletePayload)
	if err != nil {
		t.Fatalf("dispatch ontology_delete: %v", err)
	}
	deleteResp, ok := deleted.(*OntologyDeleteResponse)
	if !ok {
		t.Fatalf("unexpected ontology_delete response type: %T", deleted)
	}
	if !deleteResp.Deleted {
		t.Fatal("expected ontology_delete to report deletion")
	}
}

func TestOntologyToolDispatchSurfacesValidationErrors(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	schema := validAviationSchema()
	schema.ObjectTypes[0].PrimaryKey = ""
	payload, err := json.Marshal(OntologySaveRequest{Schema: schema})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if _, err := tools.Call(ctx, "ontology_save", payload); err == nil {
		t.Fatal("expected an invalid schema to be rejected through dispatch")
	} else if !strings.Contains(err.Error(), "primary_key") {
		t.Fatalf("dispatch should surface the validation detail, got %v", err)
	}
}
