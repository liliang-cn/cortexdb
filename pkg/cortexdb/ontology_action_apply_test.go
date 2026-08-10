package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestApplyActionCreatesObject(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)
	ctx := context.Background()

	resp, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:      "registerAirport",
		Parameters:  map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
		ReturnEdits: true,
		Actor:       "tester",
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !resp.Applied {
		t.Fatalf("expected the action to apply, errors: %v", resp.Errors)
	}
	if len(resp.Edits) != 1 || resp.Edits[0].Kind != string(ActionRuleCreateObject) {
		t.Fatalf("expected one create edit, got %+v", resp.Edits)
	}
	if resp.Edits[0].ObjectID != ontologyNodeID("Airport", "LHR") || resp.Edits[0].ObjectType != "Airport" {
		t.Fatalf("edit does not identify what was written: %+v", resp.Edits[0])
	}

	node, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR"))
	if err != nil {
		t.Fatalf("expected the airport to exist: %v", err)
	}
	if node.NodeType != "Airport" {
		t.Fatalf("unexpected node type %q", node.NodeType)
	}
	if fmt.Sprintf("%v", node.Properties["airportName"]) != "London Heathrow" {
		t.Fatalf("expected the parameter to reach the property, got %v", node.Properties["airportName"])
	}
}

func TestApplyActionWithoutReturnEditsHidesEdits(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)
	ctx := context.Background()

	resp, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LGW", "airportName": "Gatwick"},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !resp.Applied {
		t.Fatal("expected the action to apply")
	}
	if len(resp.Edits) != 0 {
		t.Fatal("edits must be withheld unless return_edits is set")
	}

	// Withheld, not skipped: the write still happened.
	if _, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LGW")); err != nil {
		t.Fatalf("expected the airport to exist even with edits withheld: %v", err)
	}
}

// The strong form of the validate-only guarantee: one action is applied for
// real and another is only validated, and the graph shows exactly the first.
func TestApplyActionValidateOnlyWritesNothing(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)
	ctx := context.Background()

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LGW", "airportName": "Gatwick"},
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
		ValidateOnly: true,
	}); err != nil {
		t.Fatalf("validate: %v", err)
	}

	if _, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LGW")); err != nil {
		t.Fatalf("expected the applied airport to exist: %v", err)
	}
	if _, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR")); err == nil {
		t.Fatal("validate-only must not write to the graph")
	}
	// Nor may it leave a trail of something that never happened.
	if count := auditRowCount(t, db, "registerAirport"); count != 1 {
		t.Fatalf("expected only the applied action to be audited, got %d rows", count)
	}
}

func TestApplyActionWritesAuditTrail(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)
	ctx := context.Background()

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
		Actor:      "tester",
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	var (
		count int
		actor string
	)
	row := db.store.GetDB().QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(MAX(actor), '') FROM ontology_action_audit WHERE action_api_name = ?`,
		"registerAirport")
	if err := row.Scan(&count, &actor); err != nil {
		t.Fatalf("scan audit: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 audit row, got %d", count)
	}
	if actor != "tester" {
		t.Fatalf("expected the actor to be recorded, got %q", actor)
	}
}

// The trail is the point of routing writes through actions, so it has to carry
// what was asked for and what that changed — not just that something happened.
func TestApplyActionAuditRecordsParametersAndEdits(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)
	ctx := context.Background()

	// return_edits is off: the trail must not depend on the caller asking.
	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
		Actor:      "tester",
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	var parametersJSON, editsJSON string
	row := db.store.GetDB().QueryRowContext(ctx,
		`SELECT parameters, edits FROM ontology_action_audit WHERE action_api_name = ?`, "registerAirport")
	if err := row.Scan(&parametersJSON, &editsJSON); err != nil {
		t.Fatalf("scan audit: %v", err)
	}

	var parameters map[string]string
	if err := json.Unmarshal([]byte(parametersJSON), &parameters); err != nil {
		t.Fatalf("decode audited parameters: %v", err)
	}
	if parameters["iataCode"] != "LHR" || parameters["airportName"] != "London Heathrow" {
		t.Fatalf("expected the inputs to be audited, got %v", parameters)
	}

	var edits []ActionEdit
	if err := json.Unmarshal([]byte(editsJSON), &edits); err != nil {
		t.Fatalf("decode audited edits: %v", err)
	}
	if len(edits) != 1 || edits[0].ObjectID != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected the edit to be audited, got %+v", edits)
	}
}

func TestApplyActionModifyObject(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	activateRenameAirport(t, db)

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "Heathrow"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action: "renameAirport",
		Parameters: map[string]string{
			"airport": ontologyNodeID("Airport", "LHR"),
			"newName": "London Heathrow",
		},
		ReturnEdits: true,
	})
	if err != nil {
		t.Fatalf("modify: %v", err)
	}
	if len(resp.Edits) != 1 || resp.Edits[0].Kind != string(ActionRuleModifyObject) {
		t.Fatalf("expected one modify edit, got %+v", resp.Edits)
	}

	node, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR"))
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if fmt.Sprintf("%v", node.Properties["airportName"]) != "London Heathrow" {
		t.Fatalf("expected the rename to apply, got %v", node.Properties["airportName"])
	}
	// Untouched properties survive: a modify rule edits, it does not replace.
	if fmt.Sprintf("%v", node.Properties["iataCode"]) != "LHR" {
		t.Fatalf("expected the primary key to survive the modify, got %v", node.Properties["iataCode"])
	}
}

// A target parameter may carry the object's primary key rather than its node
// ID, which is what a caller holding a business key has to hand.
func TestApplyActionModifyObjectByPrimaryKey(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	activateRenameAirport(t, db)

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "Heathrow"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "renameAirport",
		Parameters: map[string]string{"airport": "LHR", "newName": "London Heathrow"},
	}); err != nil {
		t.Fatalf("modify: %v", err)
	}

	node, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR"))
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if fmt.Sprintf("%v", node.Properties["airportName"]) != "London Heathrow" {
		t.Fatalf("expected the rename to apply, got %v", node.Properties["airportName"])
	}
}

func TestApplyActionModifyRejectsMissingObject(t *testing.T) {
	db := openOntologyTestDB(t)
	activateRenameAirport(t, db)

	_, err := db.ApplyAction(context.Background(), ActionApplyRequest{
		Action:     "renameAirport",
		Parameters: map[string]string{"airport": "LHR", "newName": "London Heathrow"},
	})
	if err == nil || !strings.Contains(err.Error(), "modify") {
		t.Fatalf("expected modifying an absent object to fail, got %v", err)
	}
}

func TestApplyActionModifyRejectsBadPropertyValue(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.ObjectTypes[0].Properties = append(schema.ObjectTypes[0].Properties,
		OntologyProperty{APIName: "runwayCount", DataType: OntologyDataType{Kind: OntologyDataInteger}})
	schema.ActionTypes = append(schema.ActionTypes, OntologyActionType{
		APIName: "setRunways",
		Parameters: []OntologyActionParameter{
			{APIName: "airport", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true, ObjectType: "Airport"},
			// Declared as a string so the bad value gets past parameter
			// validation and has to be caught against the property's type.
			{APIName: "runways", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
		},
		Rules: []OntologyActionRule{{
			Kind: ActionRuleModifyObject, ObjectType: "Airport", Target: "airport",
			PropertyValues: map[string]OntologyValueSource{
				"runwayCount": {Kind: ValueSourceParameter, Parameter: "runways"},
			},
		}},
	})
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "Heathrow"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "setRunways",
		Parameters: map[string]string{"airport": "LHR", "runways": "several"},
	})
	if err == nil || !strings.Contains(err.Error(), "runwayCount") {
		t.Fatalf("expected a value that is not an integer to be refused, got %v", err)
	}
}

func TestApplyActionDeletesObject(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	activateRetireAirport(t, db)

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "Heathrow"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:      "retireAirport",
		Parameters:  map[string]string{"airport": "LHR"},
		ReturnEdits: true,
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(resp.Edits) != 1 || resp.Edits[0].Kind != string(ActionRuleDeleteObject) {
		t.Fatalf("expected one delete edit, got %+v", resp.Edits)
	}
	if _, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR")); err == nil {
		t.Fatal("expected the airport to be gone")
	}
}

// Foundry refuses to let one action create an object and then edit it, because
// the edit would be written against an object the action has not committed.
func TestApplyActionRejectsModifyOfObjectCreatedInSameAction(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	// One action that both creates and then modifies the same object.
	schema.ActionTypes[0].Rules = append(schema.ActionTypes[0].Rules, OntologyActionRule{
		Kind:       ActionRuleModifyObject,
		ObjectType: "Airport",
		Target:     "iataCode",
		PropertyValues: map[string]OntologyValueSource{
			"airportName": {Kind: ValueSourceStatic, Static: "renamed"},
		},
	})
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	_, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "Heathrow"},
	})
	if err == nil || !strings.Contains(err.Error(), "created in the same action") {
		t.Fatalf("expected Foundry's same-action restriction to be enforced, got %v", err)
	}
}

func TestApplyActionRejectsDeleteOfObjectCreatedInSameAction(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules = append(schema.ActionTypes[0].Rules, OntologyActionRule{
		Kind: ActionRuleDeleteObject, ObjectType: "Airport", Target: "iataCode",
	})
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	_, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "Heathrow"},
	})
	if err == nil || !strings.Contains(err.Error(), "created in the same action") {
		t.Fatalf("expected the same-action restriction to cover delete too, got %v", err)
	}
}

// The restriction is about *this* action's creations, not about creation in
// general: an object an earlier action made is editable.
func TestApplyActionAllowsModifyOfObjectCreatedByAnEarlierAction(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	activateRenameAirport(t, db)

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "Heathrow"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "renameAirport",
		Parameters: map[string]string{"airport": "LHR", "newName": "London Heathrow"},
	}); err != nil {
		t.Fatalf("expected a separate action to be free to modify, got %v", err)
	}
}

// create_object is not an upsert: Foundry fails an action that would create an
// object that already exists, which is what makes create_or_modify_object a
// distinct rule kind rather than a synonym.
func TestApplyActionCreateObjectRefusesExistingObject(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)
	ctx := context.Background()

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "Heathrow"},
	}); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	_, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "Heathrow Again"},
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected a duplicate create to be refused at apply time, got %v", err)
	}

	// And the refusal left the original alone.
	node, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR"))
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if fmt.Sprintf("%v", node.Properties["airportName"]) != "Heathrow" {
		t.Fatalf("expected the refused create to change nothing, got %v", node.Properties["airportName"])
	}
}

func TestApplyActionCreateOrModifyObjectAcceptsExistingObject(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules[0].Kind = ActionRuleCreateOrModifyObject
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "Heathrow"},
	}); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
	}); err != nil {
		t.Fatalf("expected create_or_modify to accept an existing object, got %v", err)
	}

	node, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR"))
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if fmt.Sprintf("%v", node.Properties["airportName"]) != "London Heathrow" {
		t.Fatalf("expected the second apply to update, got %v", node.Properties["airportName"])
	}
}

func TestApplyActionCreatesAndDeletesManyToManyLinks(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	seedCrewSchema(t, db)

	resp, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action: "assignCrew",
		Parameters: map[string]string{
			"flight":     ontologyNodeID("Flight", "BA123"),
			"crewMember": ontologyNodeID("CrewMember", "C-1"),
		},
		ReturnEdits: true,
	})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if len(resp.Edits) != 1 || resp.Edits[0].Kind != string(ActionRuleCreateLink) {
		t.Fatalf("expected one create_link edit, got %+v", resp.Edits)
	}
	if resp.Edits[0].LinkType != "flightCrew" ||
		resp.Edits[0].FromID != ontologyNodeID("Flight", "BA123") ||
		resp.Edits[0].ToID != ontologyNodeID("CrewMember", "C-1") {
		t.Fatalf("edit does not identify the link written: %+v", resp.Edits[0])
	}
	if count := crewLinkCount(t, db); count != 1 {
		t.Fatalf("expected the link to exist, got %d edges", count)
	}

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action: "unassignCrew",
		Parameters: map[string]string{
			"flight":     ontologyNodeID("Flight", "BA123"),
			"crewMember": ontologyNodeID("CrewMember", "C-1"),
		},
	}); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if count := crewLinkCount(t, db); count != 0 {
		t.Fatalf("expected the link to be gone, got %d edges", count)
	}
}

// Link endpoints may be primary keys rather than node IDs, the same as a
// modify target.
func TestApplyActionCreatesLinkFromPrimaryKeys(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	seedCrewSchema(t, db)

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "assignCrew",
		Parameters: map[string]string{"flight": "BA123", "crewMember": "C-1"},
	}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if count := crewLinkCount(t, db); count != 1 {
		t.Fatalf("expected the link to exist, got %d edges", count)
	}
}

// A link type is bidirectional and edge type names fold case, so unlinking has
// to find the edge however it was written. Both were real bugs in earlier
// phases of this rewrite.
func TestApplyActionDeleteLinkIgnoresEdgeDirectionAndCase(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	seedCrewSchema(t, db)

	if _, err := db.store.GetDB().ExecContext(ctx,
		`INSERT INTO graph_edges (id, from_node_id, to_node_id, edge_type, weight) VALUES (?, ?, ?, ?, 1.0)`,
		"edge:manual:1", ontologyNodeID("CrewMember", "C-1"), ontologyNodeID("Flight", "BA123"), "FLIGHTCREW"); err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "unassignCrew",
		Parameters: map[string]string{"flight": "BA123", "crewMember": "C-1"},
	}); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if count := crewLinkCount(t, db); count != 0 {
		t.Fatalf("expected the reversed, differently-cased edge to be deleted, got %d edges", count)
	}
}

func TestApplyActionResolvesStaticCurrentUserAndCurrentTimeSources(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.ObjectTypes[0].Properties = append(schema.ObjectTypes[0].Properties,
		OntologyProperty{APIName: "registeredBy", DataType: OntologyDataType{Kind: OntologyDataString}},
		OntologyProperty{APIName: "registeredAt", DataType: OntologyDataType{Kind: OntologyDataString}},
		OntologyProperty{APIName: "region", DataType: OntologyDataType{Kind: OntologyDataString}},
	)
	values := schema.ActionTypes[0].Rules[0].PropertyValues
	values["registeredBy"] = OntologyValueSource{Kind: ValueSourceCurrentUser}
	values["registeredAt"] = OntologyValueSource{Kind: ValueSourceCurrentTime}
	values["region"] = OntologyValueSource{Kind: ValueSourceStatic, Static: "EMEA"}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "Heathrow"},
		Actor:      "ops-team",
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	node, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR"))
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if fmt.Sprintf("%v", node.Properties["registeredBy"]) != "ops-team" {
		t.Fatalf("expected current_user to resolve to the actor, got %v", node.Properties["registeredBy"])
	}
	if fmt.Sprintf("%v", node.Properties["region"]) != "EMEA" {
		t.Fatalf("expected the static value, got %v", node.Properties["region"])
	}
	// RFC3339 in UTC; checked by shape rather than by value so the test does
	// not depend on the clock.
	registeredAt := fmt.Sprintf("%v", node.Properties["registeredAt"])
	if !strings.HasSuffix(registeredAt, "Z") || len(registeredAt) != 20 {
		t.Fatalf("expected an RFC3339 UTC timestamp, got %q", registeredAt)
	}
}

// object_property reads the named property off the object a reference
// parameter points at. Reading back the parameter value instead — which is the
// node ID — would look plausible and be wrong.
func TestApplyActionResolvesObjectPropertySource(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.ActionTypes = append(schema.ActionTypes, OntologyActionType{
		APIName: "cloneAirportName",
		Parameters: []OntologyActionParameter{
			{APIName: "source", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true, ObjectType: "Airport"},
			{APIName: "iataCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
		},
		Rules: []OntologyActionRule{{
			Kind: ActionRuleCreateObject, ObjectType: "Airport",
			PropertyValues: map[string]OntologyValueSource{
				"iataCode":    {Kind: ValueSourceParameter, Parameter: "iataCode"},
				"airportName": {Kind: ValueSourceObjectProperty, Parameter: "source", Property: "airportName"},
			},
		}},
	})
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "cloneAirportName",
		Parameters: map[string]string{"source": "LHR", "iataCode": "LGW"},
	}); err != nil {
		t.Fatalf("clone: %v", err)
	}

	node, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LGW"))
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if fmt.Sprintf("%v", node.Properties["airportName"]) != "London Heathrow" {
		t.Fatalf("expected the property to be read off the referenced object, got %v", node.Properties["airportName"])
	}
}

// An action whose rules cannot be carried out must not leave an audit row
// claiming it was: the trail is written after the edits, not alongside them.
func TestApplyActionWritesNoAuditRowWhenRulesFail(t *testing.T) {
	db := openOntologyTestDB(t)
	activateRenameAirport(t, db)

	if _, err := db.ApplyAction(context.Background(), ActionApplyRequest{
		Action:     "renameAirport",
		Parameters: map[string]string{"airport": "LHR", "newName": "London Heathrow"},
	}); err == nil {
		t.Fatal("expected the modify to fail")
	}
	if count := auditRowCount(t, db, "renameAirport"); count != 0 {
		t.Fatalf("expected no audit row for a failed action, got %d", count)
	}
}

// The audit table is created lazily, and a cancelled first attempt must not
// latch: sync.Once would disable the trail for the rest of the process.
func TestEnsureActionAuditTableRecoversFromACancelledFirstCall(t *testing.T) {
	db := openOntologyTestDB(t)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := db.ensureActionAuditTable(cancelled); err == nil {
		t.Fatal("expected the cancelled context to fail")
	}
	if err := db.ensureActionAuditTable(context.Background()); err != nil {
		t.Fatalf("expected a later call to succeed, got %v", err)
	}
	// Asked of the table itself: a readiness flag latched before the create
	// ran would report success while leaving no table behind.
	if _, err := db.store.GetDB().ExecContext(context.Background(),
		`SELECT COUNT(*) FROM ontology_action_audit`); err != nil {
		t.Fatalf("expected the audit table to exist after the retry: %v", err)
	}
}

// A target parameter that is not required can arrive empty, and the complaint
// then has to name the parameter rather than a node ID nobody asked for.
func TestApplyActionRejectsEmptyTarget(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.ActionTypes = append(schema.ActionTypes, OntologyActionType{
		APIName: "renameAirportLoosely",
		Parameters: []OntologyActionParameter{
			{APIName: "airport", DataType: OntologyDataType{Kind: OntologyDataString}, ObjectType: "Airport"},
			{APIName: "newName", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
		},
		Rules: []OntologyActionRule{{
			Kind: ActionRuleModifyObject, ObjectType: "Airport", Target: "airport",
			PropertyValues: map[string]OntologyValueSource{
				"airportName": {Kind: ValueSourceParameter, Parameter: "newName"},
			},
		}},
	})
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	_, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "renameAirportLoosely",
		Parameters: map[string]string{"newName": "London Heathrow"},
	})
	if err == nil || !strings.Contains(err.Error(), `target parameter "airport"`) {
		t.Fatalf("expected the empty target to be named, got %v", err)
	}
}

// A rule may spell a property in any case, so what lands on the node has to be
// the declared spelling — otherwise one object ends up with airportName and
// AIRPORTNAME side by side and readers see whichever they guessed.
func TestApplyActionWritesPropertiesUnderTheDeclaredSpelling(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	create := schema.ActionTypes[0].Rules[0]
	delete(create.PropertyValues, "airportName")
	create.PropertyValues["AIRPORTNAME"] = OntologyValueSource{Kind: ValueSourceParameter, Parameter: "airportName"}
	schema.ActionTypes[0].Rules[0] = create
	schema.ActionTypes = append(schema.ActionTypes, OntologyActionType{
		APIName: "renameAirport",
		Parameters: []OntologyActionParameter{
			{APIName: "airport", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true, ObjectType: "Airport"},
			{APIName: "newName", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
		},
		Rules: []OntologyActionRule{{
			Kind: ActionRuleModifyObject, ObjectType: "Airport", Target: "airport",
			PropertyValues: map[string]OntologyValueSource{
				"AirportName": {Kind: ValueSourceParameter, Parameter: "newName"},
			},
		}},
	})
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "Heathrow"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	node, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR"))
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if fmt.Sprintf("%v", node.Properties["airportName"]) != "Heathrow" {
		t.Fatalf("create must use the declared spelling, got %v", node.Properties)
	}
	if _, shouted := node.Properties["AIRPORTNAME"]; shouted {
		t.Fatalf("create wrote the rule's spelling too: %v", node.Properties)
	}

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "renameAirport",
		Parameters: map[string]string{"airport": "LHR", "newName": "London Heathrow"},
	}); err != nil {
		t.Fatalf("modify: %v", err)
	}
	node, err = db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR"))
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if fmt.Sprintf("%v", node.Properties["airportName"]) != "London Heathrow" {
		t.Fatalf("modify must use the declared spelling, got %v", node.Properties)
	}
	if _, mixed := node.Properties["AirportName"]; mixed {
		t.Fatalf("modify wrote the rule's spelling too: %v", node.Properties)
	}
}

// An optional property whose source resolves to nothing is left off the object
// rather than written blank: the ontology's own type check rejects an empty
// value, so writing one would fail every action that omits an optional input.
func TestApplyActionOmitsPropertiesThatResolveToNothing(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.ObjectTypes[0].Properties = append(schema.ObjectTypes[0].Properties,
		OntologyProperty{APIName: "region", DataType: OntologyDataType{Kind: OntologyDataString}})
	schema.ActionTypes[0].Parameters = append(schema.ActionTypes[0].Parameters,
		OntologyActionParameter{APIName: "region", DataType: OntologyDataType{Kind: OntologyDataString}})
	schema.ActionTypes[0].Rules[0].PropertyValues["region"] = OntologyValueSource{
		Kind: ValueSourceParameter, Parameter: "region",
	}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "Heathrow"},
	}); err != nil {
		t.Fatalf("expected the omitted optional property to be skipped, got %v", err)
	}

	node, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR"))
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if _, present := node.Properties["region"]; present {
		t.Fatalf("expected no region property at all, got %v", node.Properties["region"])
	}
}

func activateRenameAirport(t *testing.T, db *DB) {
	t.Helper()
	schema := aviationSchemaWithActions()
	schema.ActionTypes = append(schema.ActionTypes, OntologyActionType{
		APIName: "renameAirport",
		Parameters: []OntologyActionParameter{
			{APIName: "airport", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true, ObjectType: "Airport"},
			{APIName: "newName", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
		},
		Rules: []OntologyActionRule{{
			Kind: ActionRuleModifyObject, ObjectType: "Airport", Target: "airport",
			PropertyValues: map[string]OntologyValueSource{
				"airportName": {Kind: ValueSourceParameter, Parameter: "newName"},
			},
		}},
	})
	if _, err := db.SaveOntologySchema(context.Background(), OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}
}

func activateRetireAirport(t *testing.T, db *DB) {
	t.Helper()
	schema := aviationSchemaWithActions()
	schema.ActionTypes = append(schema.ActionTypes, OntologyActionType{
		APIName: "retireAirport",
		Parameters: []OntologyActionParameter{
			{APIName: "airport", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true, ObjectType: "Airport"},
		},
		Rules: []OntologyActionRule{{Kind: ActionRuleDeleteObject, ObjectType: "Airport", Target: "airport"}},
	})
	if _, err := db.SaveOntologySchema(context.Background(), OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}
}

// seedCrewSchema activates a schema whose only link type is many-to-many, and
// writes the two objects the link rules connect.
func seedCrewSchema(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()

	schema := manyToManySchemaWithLinkAction()
	schema.ActionTypes = append(schema.ActionTypes, OntologyActionType{
		APIName: "unassignCrew",
		Parameters: []OntologyActionParameter{
			{APIName: "flight", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true, ObjectType: "Flight"},
			{APIName: "crewMember", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true, ObjectType: "CrewMember"},
		},
		Rules: []OntologyActionRule{{
			Kind:     ActionRuleDeleteLink,
			LinkType: "flightCrew",
			From:     OntologyValueSource{Kind: ValueSourceParameter, Parameter: "flight"},
			To:       OntologyValueSource{Kind: ValueSourceParameter, Parameter: "crewMember"},
		}},
	})
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "BA123", Type: "Flight", Metadata: map[string]string{"flightNumber": "BA123"}},
		{Name: "Ada Lovelace", Type: "CrewMember", Metadata: map[string]string{"badgeId": "C-1", "crewName": "Ada Lovelace"}},
	}}); err != nil {
		t.Fatalf("seed objects: %v", err)
	}
}

func crewLinkCount(t *testing.T, db *DB) int {
	t.Helper()
	var count int
	if err := db.store.GetDB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM graph_edges WHERE edge_type = ? COLLATE NOCASE`, "flightCrew").Scan(&count); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	return count
}

func auditRowCount(t *testing.T, db *DB, action string) int {
	t.Helper()
	// The table is created lazily, so an action that never applied leaves it
	// absent; created here so "no rows" and "no table" read the same.
	if err := db.ensureActionAuditTable(context.Background()); err != nil {
		t.Fatalf("init audit table: %v", err)
	}
	var count int
	if err := db.store.GetDB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM ontology_action_audit WHERE action_api_name = ?`, action).Scan(&count); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return count
}
