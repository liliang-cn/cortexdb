package cortexdb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestStrictActionsClosesGenericUpsert(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.StrictActions = true
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	_, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
	}})
	if err == nil || !strings.Contains(err.Error(), "strict_actions") {
		t.Fatalf("expected generic upsert to be closed under strict_actions, got %v", err)
	}
	// Closed, not merely complained about.
	if _, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR")); err == nil {
		t.Fatal("the refused upsert still wrote to the graph")
	}

	// Actions must still work.
	resp, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
	})
	if err != nil {
		t.Fatalf("action under strict_actions: %v", err)
	}
	if !resp.Applied {
		t.Fatalf("expected the action to apply, errors: %v", resp.Errors)
	}
	if _, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR")); err != nil {
		t.Fatalf("expected the action's own write to go through: %v", err)
	}
}

// The gate has to let an action's link writes through too — those go via
// UpsertRelations, which is the other closed door.
func TestStrictActionsClosesGenericRelationUpsertButNotLinkRules(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	seedCrewSchema(t, db)
	schema, err := db.loadActiveOntologySchema(ctx)
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	schema.StrictActions = true
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: *schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	_, err = db.GraphRAGTools().UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
		{From: ontologyNodeID("Flight", "BA123"), To: ontologyNodeID("CrewMember", "C-1"), Type: "flightCrew"},
	}})
	if err == nil || !strings.Contains(err.Error(), "strict_actions") {
		t.Fatalf("expected generic relation upsert to be closed, got %v", err)
	}
	if count := crewLinkCount(t, db); count != 0 {
		t.Fatalf("the refused upsert still wrote %d edges", count)
	}

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "assignCrew",
		Parameters: map[string]string{"flight": "BA123", "crewMember": "C-1"},
	}); err != nil {
		t.Fatalf("link action under strict_actions: %v", err)
	}
	if count := crewLinkCount(t, db); count != 1 {
		t.Fatalf("expected the action's link to be written, got %d edges", count)
	}
}

func TestStrictActionsDefaultsOff(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{
		Schema: aviationSchemaWithActions(), Activate: true,
	}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
	}}); err != nil {
		t.Fatalf("generic upsert must stay open by default, got %v", err)
	}
}

// A schema that closes the generic path without offering an action would leave
// no way to write at all, so the gate only bites once actions exist.
func TestStrictActionsWithoutActionTypesLeavesUpsertOpen(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := validAviationSchema()
	schema.StrictActions = true
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
	}}); err != nil {
		t.Fatalf("expected the gate to stay open with no actions declared, got %v", err)
	}
}

// The marker that lets an action's own writes through is scoped to that
// action's context, not to the DB: a caller reaching the generic tools while
// an action runs elsewhere is still refused.
func TestStrictActionsGateIsNotDisabledGlobally(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.StrictActions = true
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "Gatwick", Type: "Airport", Metadata: map[string]string{"iataCode": "LGW"}},
	}}); err == nil {
		t.Fatal("expected the gate to stay closed after an action ran")
	}
}

func TestActionToolsAreReachable(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)
	ctx := context.Background()

	listed, err := db.GraphRAGTools().ListActionTypes(ctx, ActionListRequest{})
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if len(listed.Actions) != 1 || listed.Actions[0].APIName != "registerAirport" {
		t.Fatalf("unexpected action list: %+v", listed.Actions)
	}
	// The listing is what an agent reads to work out how to call the action,
	// so it has to carry the parameters and criteria, not just the names.
	if len(listed.Actions[0].Parameters) != 2 || len(listed.Actions[0].SubmissionCriteria) != 1 {
		t.Fatalf("expected the listing to describe how to call the action: %+v", listed.Actions[0])
	}

	applied, err := db.GraphRAGTools().ApplyAction(ctx, ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply via toolbox: %v", err)
	}
	if !applied.Valid {
		t.Fatalf("expected VALID, got %v", applied.Errors)
	}
}

func TestListActionTypesWithoutActiveOntology(t *testing.T) {
	db := openOntologyTestDB(t)

	listed, err := db.ListActionTypes(context.Background(), ActionListRequest{})
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if len(listed.Actions) != 0 {
		t.Fatalf("expected no actions without an active ontology, got %+v", listed.Actions)
	}
}

// The dispatch table is the path an MCP host actually takes, and it is kept by
// hand alongside the tool definitions.
func TestActionToolsDispatch(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)
	ctx := context.Background()

	raw, err := db.GraphRAGTools().Call(ctx, "ontology_action_list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("call ontology_action_list: %v", err)
	}
	listed, ok := raw.(*ActionListResponse)
	if !ok || len(listed.Actions) != 1 {
		t.Fatalf("unexpected ontology_action_list result: %#v", raw)
	}

	raw, err = db.GraphRAGTools().Call(ctx, "ontology_action_apply", json.RawMessage(
		`{"action":"registerAirport","parameters":{"iataCode":"LHR","airportName":"London Heathrow"},"return_edits":true,"actor":"agent"}`))
	if err != nil {
		t.Fatalf("call ontology_action_apply: %v", err)
	}
	applied, ok := raw.(*ActionApplyResponse)
	if !ok || !applied.Applied {
		t.Fatalf("unexpected ontology_action_apply result: %#v", raw)
	}
	if len(applied.Edits) != 1 {
		t.Fatalf("expected return_edits to survive decoding, got %+v", applied.Edits)
	}
	if actor := auditActor(t, db, "registerAirport"); actor != "agent" {
		t.Fatalf("expected the actor to survive decoding, got %q", actor)
	}
}

func TestActionToolDefinitionsDeclareTheirInputs(t *testing.T) {
	db := openOntologyTestDB(t)
	definitions := map[string]ToolDefinition{}
	for _, definition := range db.GraphRAGTools().Definitions() {
		definitions[definition.Name] = definition
	}

	apply, ok := definitions["ontology_action_apply"]
	if !ok {
		t.Fatal("ontology_action_apply is not defined")
	}
	properties, _ := apply.InputSchema["properties"].(map[string]any)
	for _, name := range []string{"action", "parameters", "validate_only", "return_edits", "actor"} {
		if _, present := properties[name]; !present {
			t.Fatalf("ontology_action_apply does not declare %q", name)
		}
	}
	required, _ := apply.InputSchema["required"].([]string)
	if len(required) != 1 || required[0] != "action" {
		t.Fatalf("expected action to be the only required input, got %v", required)
	}

	if _, ok := definitions["ontology_action_list"]; !ok {
		t.Fatal("ontology_action_list is not defined")
	}
}

func auditActor(t *testing.T, db *DB, action string) string {
	t.Helper()
	var actor string
	if err := db.store.GetDB().QueryRowContext(context.Background(),
		`SELECT COALESCE(actor, '') FROM ontology_action_audit WHERE action_api_name = ?`, action).Scan(&actor); err != nil {
		t.Fatalf("read audit actor: %v", err)
	}
	return actor
}
