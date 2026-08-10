package cortexdb

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func activateAviationActions(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.SaveOntologySchema(context.Background(), OntologySaveRequest{
		Schema:   aviationSchemaWithActions(),
		Activate: true,
	}); err != nil {
		t.Fatalf("activate: %v", err)
	}
}

func TestApplyActionValidateOnlyReportsValid(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)
	ctx := context.Background()

	// Created up front so the absence check below is answered by an empty
	// table rather than by a missing one.
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		t.Fatalf("init graph schema: %v", err)
	}

	resp, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !resp.Valid {
		t.Fatalf("expected VALID, got %v", resp.Errors)
	}
	if resp.Applied {
		t.Fatal("validate-only must not report the action as applied")
	}
	if len(resp.Edits) != 0 {
		t.Fatal("validate-only must not report edits")
	}

	// Nothing may have been written.
	if _, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR")); err == nil {
		t.Fatal("validate-only must not write to the graph")
	}
}

func TestApplyActionValidateOnlyFailsSubmissionCriterion(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	resp, err := db.ApplyAction(context.Background(), ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "heathrow", "airportName": "London Heathrow"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected INVALID for a lowercase IATA code")
	}
	if len(resp.Errors) == 0 || !strings.Contains(resp.Errors[0], "three uppercase") {
		t.Fatalf("expected the configured failure message, got %v", resp.Errors)
	}
}

// Without a failure message the criterion still has to say what went wrong,
// and it has to name the regex it was actually checking.
func TestApplyActionSubmissionCriterionFallbackMessage(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].SubmissionCriteria[0].FailureMessage = ""
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	resp, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "heathrow", "airportName": "London Heathrow"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected INVALID for a lowercase IATA code")
	}
	if len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], "^[A-Z]{3}$") {
		t.Fatalf("expected the fallback message to name the regex, got %v", resp.Errors)
	}
}

// The operator path is a different branch from the regex path, and a criterion
// may state both — in which case both have to hold.
func TestApplyActionSubmissionCriterionOperatorPath(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].SubmissionCriteria = []OntologySubmissionCriterion{
		{Parameter: "iataCode", Regex: "^[A-Z]{3}$", FailureMessage: "IATA code must be three uppercase letters."},
		{Parameter: "iataCode", Op: PredicateIn, Values: []string{"LHR", "LGW"}, FailureMessage: "only London airports may be registered"},
	}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	// Passes the regex, fails the operator: only the operator's message fires.
	resp, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "JFK", "airportName": "John F Kennedy"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected JFK to fail the in-criterion")
	}
	if len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], "only London airports") {
		t.Fatalf("expected only the operator criterion to fail, got %v", resp.Errors)
	}

	// Fails the regex, would also fail the operator: two criteria, two errors.
	resp, err = db.ApplyAction(ctx, ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "jfk", "airportName": "John F Kennedy"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(resp.Errors) != 2 {
		t.Fatalf("expected both criteria to report, got %v", resp.Errors)
	}

	// Passes both.
	resp, err = db.ApplyAction(ctx, ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "LGW", "airportName": "Gatwick"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !resp.Valid {
		t.Fatalf("expected LGW to satisfy both criteria, got %v", resp.Errors)
	}
}

// A criterion stating both a regex and an operator is a conjunction, so a
// value failing the regex is rejected before the operator is consulted.
func TestApplyActionSubmissionCriterionCombinesRegexAndOperator(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].SubmissionCriteria = []OntologySubmissionCriterion{
		{Parameter: "iataCode", Regex: "^[A-Z]{3}$", Op: PredicateStartsWith, Value: "L"},
	}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	// Regex holds, operator does not.
	resp, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "JFK", "airportName": "John F Kennedy"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected the operator half of the criterion to be enforced")
	}
	if len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], string(PredicateStartsWith)) {
		t.Fatalf("expected the starts_with check to be named, got %v", resp.Errors)
	}

	// Regex fails on a value that would fail the operator too, so a fall
	// through to the operator would show up as a second error rather than
	// being masked by a value that happens to satisfy it.
	resp, err = db.ApplyAction(ctx, ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "jfk", "airportName": "John F Kennedy"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], "^[A-Z]{3}$") {
		t.Fatalf("expected one regex failure, got %v", resp.Errors)
	}
}

func TestApplyActionRejectsMissingRequiredParameter(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	resp, err := db.ApplyAction(context.Background(), ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "LHR"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected a missing required parameter to be INVALID")
	}
	if len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], `"airportName" is required`) {
		t.Fatalf("expected the missing parameter to be named, got %v", resp.Errors)
	}
}

// A blank string is absence, not a value: otherwise a required parameter could
// be satisfied by sending nothing under its name.
func TestApplyActionTreatsBlankRequiredParameterAsMissing(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	resp, err := db.ApplyAction(context.Background(), ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "LHR", "airportName": "   "},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected a blank required parameter to be INVALID")
	}
	if len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], `"airportName" is required`) {
		t.Fatalf("expected the blank parameter to be reported as missing, got %v", resp.Errors)
	}
}

func TestApplyActionAllowsOmittedOptionalParameter(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Parameters = append(schema.ActionTypes[0].Parameters, OntologyActionParameter{
		APIName: "elevation", DataType: OntologyDataType{Kind: OntologyDataInteger},
	})
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	resp, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "LHR", "airportName": "Heathrow"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !resp.Valid {
		t.Fatalf("expected an omitted optional parameter to be fine, got %v", resp.Errors)
	}
}

func TestApplyActionRejectsWrongParameterDataType(t *testing.T) {
	db := openOntologyTestDB(t)

	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Parameters = append(schema.ActionTypes[0].Parameters, OntologyActionParameter{
		APIName: "elevation", DataType: OntologyDataType{Kind: OntologyDataInteger},
	})
	if _, err := db.SaveOntologySchema(context.Background(), OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	resp, err := db.ApplyAction(context.Background(), ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "LHR", "airportName": "Heathrow", "elevation": "high"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected a type violation to be INVALID")
	}
	if len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], "elevation") {
		t.Fatalf("expected the offending parameter to be named, got %v", resp.Errors)
	}
}

func TestApplyActionRejectsValueOutsideAllowedValues(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Parameters[0].AllowedValues = []string{"LHR", "LGW"}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	resp, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "JFK", "airportName": "John F Kennedy"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected a value outside allowed_values to be INVALID")
	}
	if len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], "allowed values") {
		t.Fatalf("expected the allowed-values complaint, got %v", resp.Errors)
	}

	resp, err = db.ApplyAction(ctx, ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "LGW", "airportName": "Gatwick"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !resp.Valid {
		t.Fatalf("expected an allowed value to pass, got %v", resp.Errors)
	}
}

// An undeclared parameter is rejected rather than ignored: a caller sending
// "airportname2" has misspelled something, and silently dropping it would
// apply the action with a value the caller believes they supplied.
func TestApplyActionRejectsUndeclaredParameter(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	resp, err := db.ApplyAction(context.Background(), ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "LHR", "airportName": "Heathrow", "runwayCount": "2"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected an undeclared parameter to be INVALID")
	}
	if len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], "runwayCount") {
		t.Fatalf("expected the undeclared parameter to be named as sent, got %v", resp.Errors)
	}
}

// Parameter names resolve case-insensitively, the same as every other api name
// in the ontology.
func TestApplyActionMatchesParameterNamesCaseInsensitively(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	resp, err := db.ApplyAction(context.Background(), ActionApplyRequest{
		Action:       "REGISTERairport",
		Parameters:   map[string]string{"IATACODE": "LHR", "AirportName": "Heathrow"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !resp.Valid {
		t.Fatalf("expected case-insensitive parameter names, got %v", resp.Errors)
	}
	if resp.Action != "registerAirport" {
		t.Fatalf("expected the response to echo the declared spelling, got %q", resp.Action)
	}
}

func TestApplyActionRejectsUnknownAction(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	_, err := db.ApplyAction(context.Background(), ActionApplyRequest{Action: "nope"})
	if err == nil {
		t.Fatal("expected an unknown action to be an error, not an INVALID result")
	}
	if !errors.Is(err, ErrInvalidOntology) {
		t.Fatalf("expected ErrInvalidOntology so the boundary can answer InvalidArgument, got %v", err)
	}
}

func TestApplyActionRejectsWithoutActiveOntology(t *testing.T) {
	db := openOntologyTestDB(t)

	_, err := db.ApplyAction(context.Background(), ActionApplyRequest{Action: "registerAirport"})
	if err == nil {
		t.Fatal("expected an action with no active ontology to be an error")
	}
	if !errors.Is(err, ErrInvalidOntology) {
		t.Fatalf("expected ErrInvalidOntology, got %v", err)
	}
}

// OSDK 2.0 makes these two mutually exclusive: validate-only writes nothing,
// so there are no edits for it to return.
func TestApplyActionRejectsValidateOnlyWithReturnEdits(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	_, err := db.ApplyAction(context.Background(), ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "LHR", "airportName": "Heathrow"},
		ValidateOnly: true,
		ReturnEdits:  true,
	})
	if err == nil || !strings.Contains(err.Error(), "return_edits") {
		t.Fatalf("expected validate_only and return_edits to be mutually exclusive, got %v", err)
	}
	if !errors.Is(err, ErrInvalidOntology) {
		t.Fatalf("expected ErrInvalidOntology, got %v", err)
	}
}

// Criteria see parameters only. Foundry's Validate Action is documented as not
// consulting existing data, so a duplicate primary key is not its business —
// it validates as fine and is resolved by the upsert at apply time.
func TestApplyActionValidationDoesNotCheckPrimaryKeyUniqueness(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)
	ctx := context.Background()

	first, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "Heathrow"},
	})
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if !first.Applied {
		t.Fatalf("expected a valid, non-validate-only action to report applied, errors: %v", first.Errors)
	}

	resp, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "LHR", "airportName": "Heathrow Again"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if !resp.Valid {
		t.Fatalf("validation must not consult the graph, got %v", resp.Errors)
	}
}

// A criterion whose regex will not compile can only arrive on a schema saved
// before that check existed, so evaluation still has to answer rather than
// panic — as a failure, not as a silent pass.
func TestEvaluateSubmissionCriteriaReportsUncompilableRegex(t *testing.T) {
	failures := evaluateSubmissionCriteria(OntologyActionType{
		APIName:            "registerAirport",
		SubmissionCriteria: []OntologySubmissionCriterion{{Parameter: "iataCode", Regex: "^[A-Z"}},
	}, map[string]string{"iataCode": "LHR"})

	if len(failures) != 1 || !strings.Contains(failures[0], "invalid regex") {
		t.Fatalf("expected an invalid regex to be reported, got %v", failures)
	}
}

// Likewise for an op no scalar matcher can answer: the criterion fails loudly
// rather than being skipped.
func TestEvaluateSubmissionCriteriaReportsUnusableOp(t *testing.T) {
	failures := evaluateSubmissionCriteria(OntologyActionType{
		APIName:            "registerAirport",
		SubmissionCriteria: []OntologySubmissionCriterion{{Parameter: "iataCode", Op: PredicateContainsAllTerms, Value: "x"}},
	}, map[string]string{"iataCode": "LHR"})

	if len(failures) != 1 || !strings.Contains(failures[0], string(PredicateContainsAllTerms)) {
		t.Fatalf("expected an unusable op to be reported, got %v", failures)
	}
}
