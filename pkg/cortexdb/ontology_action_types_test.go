package cortexdb

import (
	"errors"
	"strings"
	"testing"
)

func aviationSchemaWithActions() OntologySchema {
	schema := validAviationSchema()
	schema.ActionTypes = []OntologyActionType{
		{
			APIName:     "registerAirport",
			DisplayName: "Register Airport",
			Parameters: []OntologyActionParameter{
				{APIName: "iataCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
				{APIName: "airportName", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
			},
			Rules: []OntologyActionRule{
				{
					Kind:       ActionRuleCreateObject,
					ObjectType: "Airport",
					PropertyValues: map[string]OntologyValueSource{
						"iataCode":    {Kind: ValueSourceParameter, Parameter: "iataCode"},
						"airportName": {Kind: ValueSourceParameter, Parameter: "airportName"},
					},
				},
			},
			SubmissionCriteria: []OntologySubmissionCriterion{
				{
					Parameter:      "iataCode",
					Regex:          "^[A-Z]{3}$",
					FailureMessage: "IATA code must be three uppercase letters.",
				},
			},
		},
	}
	return schema
}

func TestValidateSchemaAcceptsActionTypes(t *testing.T) {
	if err := validateOntologySchema(aviationSchemaWithActions()); err != nil {
		t.Fatalf("expected valid action types, got %v", err)
	}
}

// An action rejection has to reach a protocol boundary as "you sent something
// bad", the same as every other ontology rejection. Message text is checked
// elsewhere; this is the machine-readable half of the contract.
func TestValidateSchemaActionRejectionCarriesErrInvalidOntology(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules[0].ObjectType = "Spacecraft"

	err := validateOntologySchema(schema)
	if !errors.Is(err, ErrInvalidOntology) {
		t.Fatalf("expected ErrInvalidOntology, got %v", err)
	}
}

func TestValidateSchemaRejectsActionRuleOnUnknownObjectType(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules[0].ObjectType = "Spacecraft"

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "Spacecraft") {
		t.Fatalf("expected unknown object type to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsActionRuleOnUnknownProperty(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules[0].PropertyValues["runwayCount"] = OntologyValueSource{
		Kind: ValueSourceStatic, Static: "2",
	}

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "runwayCount") {
		t.Fatalf("expected unknown property to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsValueSourceOnUnknownParameter(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules[0].PropertyValues["iataCode"] = OntologyValueSource{
		Kind: ValueSourceParameter, Parameter: "nope",
	}

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected unknown parameter reference to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsUnknownValueSourceKind(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules[0].PropertyValues["airportName"] = OntologyValueSource{Kind: "whatever"}

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "whatever") {
		t.Fatalf("expected an unknown value source kind to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsStaticValueSourceWithoutValue(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules[0].PropertyValues["airportName"] = OntologyValueSource{Kind: ValueSourceStatic}

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "airportName") {
		t.Fatalf("expected an empty static value to be rejected, got %v", err)
	}
}

func TestValidateSchemaAcceptsCurrentUserAndCurrentTimeValueSources(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules[0].PropertyValues["airportName"] = OntologyValueSource{Kind: ValueSourceCurrentUser}

	if err := validateOntologySchema(schema); err != nil {
		t.Fatalf("expected current_user to need no further configuration, got %v", err)
	}

	schema.ActionTypes[0].Rules[0].PropertyValues["airportName"] = OntologyValueSource{Kind: ValueSourceCurrentTime}
	if err := validateOntologySchema(schema); err != nil {
		t.Fatalf("expected current_time to need no further configuration, got %v", err)
	}
}

func TestValidateSchemaRequiresCreateObjectRuleToSupplyPrimaryKey(t *testing.T) {
	schema := aviationSchemaWithActions()
	delete(schema.ActionTypes[0].Rules[0].PropertyValues, "iataCode")

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "iataCode") {
		t.Fatalf("expected a create rule without the primary key to be rejected, got %v", err)
	}
}

// create_or_modify_object creates when the object is absent, so it carries the
// same obligation to name the object it would create.
func TestValidateSchemaRequiresCreateOrModifyRuleToSupplyPrimaryKey(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules[0].Kind = ActionRuleCreateOrModifyObject
	delete(schema.ActionTypes[0].Rules[0].PropertyValues, "iataCode")

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "iataCode") {
		t.Fatalf("expected create_or_modify without the primary key to be rejected, got %v", err)
	}
}

// A modify rule does not create anything, so it is free to leave the primary
// key alone — the object it edits is named by its target parameter instead.
func TestValidateSchemaAllowsModifyRuleWithoutPrimaryKey(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules[0] = OntologyActionRule{
		Kind:       ActionRuleModifyObject,
		ObjectType: "Airport",
		Target:     "iataCode",
		PropertyValues: map[string]OntologyValueSource{
			"airportName": {Kind: ValueSourceParameter, Parameter: "airportName"},
		},
	}

	if err := validateOntologySchema(schema); err != nil {
		t.Fatalf("expected a modify rule to need no primary key, got %v", err)
	}
}

func TestValidateSchemaRequiresTargetOnModifyAndDeleteRules(t *testing.T) {
	for _, kind := range []ActionRuleKind{ActionRuleModifyObject, ActionRuleDeleteObject} {
		schema := aviationSchemaWithActions()
		schema.ActionTypes[0].Rules[0] = OntologyActionRule{Kind: kind, ObjectType: "Airport"}

		// Matched on the "needs target" wording specifically: an empty target
		// also fails the "is not a parameter" check below, and a looser match
		// on "target" would pass while the missing-target rule was gone.
		err := validateOntologySchema(schema)
		if err == nil || !strings.Contains(err.Error(), "needs target") {
			t.Fatalf("expected %s without a target to be rejected, got %v", kind, err)
		}
	}
}

func TestValidateSchemaRejectsTargetThatIsNotAParameter(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules[0] = OntologyActionRule{
		Kind: ActionRuleDeleteObject, ObjectType: "Airport", Target: "notAParameter",
	}

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "notAParameter") {
		t.Fatalf("expected an unknown target to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsCreateLinkOnNonManyToManyLink(t *testing.T) {
	schema := aviationSchemaWithActions()
	// flightDeparture is one-to-many (origin side is ONE), so Foundry
	// requires the foreign key to be modified instead of a link rule.
	schema.ActionTypes[0].Rules = append(schema.ActionTypes[0].Rules, OntologyActionRule{
		Kind:     ActionRuleCreateLink,
		LinkType: "flightDeparture",
		From:     OntologyValueSource{Kind: ValueSourceParameter, Parameter: "iataCode"},
		To:       OntologyValueSource{Kind: ValueSourceParameter, Parameter: "airportName"},
	})

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "many-to-many") {
		t.Fatalf("expected create_link on a one-to-many link to be rejected, got %v", err)
	}
}

// delete_link is the same foreign-key argument in reverse: unlinking a ONE
// side means clearing the foreign key property, not deleting an edge.
func TestValidateSchemaRejectsDeleteLinkOnNonManyToManyLink(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules = append(schema.ActionTypes[0].Rules, OntologyActionRule{
		Kind:     ActionRuleDeleteLink,
		LinkType: "flightDeparture",
		From:     OntologyValueSource{Kind: ValueSourceParameter, Parameter: "iataCode"},
		To:       OntologyValueSource{Kind: ValueSourceParameter, Parameter: "airportName"},
	})

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "many-to-many") {
		t.Fatalf("expected delete_link on a one-to-many link to be rejected, got %v", err)
	}
}

func TestValidateSchemaAcceptsLinkRuleOnManyToManyLink(t *testing.T) {
	schema := manyToManySchemaWithLinkAction()
	if err := validateOntologySchema(schema); err != nil {
		t.Fatalf("expected a many-to-many link rule to be accepted, got %v", err)
	}
}

func TestValidateSchemaRejectsLinkRuleOnUnknownLinkType(t *testing.T) {
	schema := manyToManySchemaWithLinkAction()
	schema.ActionTypes[0].Rules[0].LinkType = "nonesuch"

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "nonesuch") {
		t.Fatalf("expected an unknown link type to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsUnknownActionRuleKind(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules[0].Kind = "teleport_object"

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "teleport_object") {
		t.Fatalf("expected an unknown rule kind to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsActionWithoutRules(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules = nil

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "at least one rule") {
		t.Fatalf("expected a rule-less action to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsDuplicateActionAPINames(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes = append(schema.ActionTypes, schema.ActionTypes[0])

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("expected duplicate action api names to be rejected")
	}
}

func TestValidateSchemaRejectsInvalidActionAPIName(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].APIName = "register airport"

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "action type api name") {
		t.Fatalf("expected an invalid action api name to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsDuplicateActionParameters(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Parameters = append(schema.ActionTypes[0].Parameters,
		OntologyActionParameter{APIName: "IATACODE", DataType: OntologyDataType{Kind: OntologyDataString}})

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "IATACODE") {
		t.Fatalf("expected a duplicate parameter to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsParameterWithUnknownObjectType(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Parameters[0].ObjectType = "Spacecraft"

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "Spacecraft") {
		t.Fatalf("expected a parameter on an unknown object type to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsParameterWithUndeclaredDataType(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Parameters[0].DataType = OntologyDataType{}

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "iataCode") {
		t.Fatalf("expected a parameter without a data type to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsSubmissionCriterionOnUnknownParameter(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].SubmissionCriteria[0].Parameter = "notAParameter"

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "notAParameter") {
		t.Fatalf("expected a criterion on an unknown parameter to be rejected, got %v", err)
	}
}

// A criterion that states neither a regex nor an operator can never fail, so
// it is a modelling mistake rather than a permissive rule.
func TestValidateSchemaRejectsSubmissionCriterionWithNoCheck(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].SubmissionCriteria[0].Regex = ""

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "regex or op") {
		t.Fatalf("expected a criterion with no check to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsSubmissionCriterionWithBadRegex(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].SubmissionCriteria[0].Regex = "^[A-Z"

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "regex") {
		t.Fatalf("expected an uncompilable regex to be rejected at save time, got %v", err)
	}
}

// Criteria are evaluated against a single parameter value, so the set-valued
// and vector predicates have nothing to operate on.
func TestValidateSchemaRejectsNonScalarSubmissionCriterionOp(t *testing.T) {
	for _, op := range []PredicateOp{PredicateAnd, PredicateNearestNeighbors, PredicateContainsAllTerms} {
		schema := aviationSchemaWithActions()
		schema.ActionTypes[0].SubmissionCriteria[0] = OntologySubmissionCriterion{
			Parameter: "iataCode", Op: op, Value: "LHR",
		}

		err := validateOntologySchema(schema)
		if err == nil || !strings.Contains(err.Error(), string(op)) {
			t.Fatalf("expected %s to be rejected as a submission criterion, got %v", op, err)
		}
	}
}

func TestValidateSchemaRejectsSubmissionCriterionMissingOperand(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].SubmissionCriteria[0] = OntologySubmissionCriterion{
		Parameter: "iataCode", Op: PredicateIn,
	}

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "values") {
		t.Fatalf("expected an in criterion without values to be rejected, got %v", err)
	}
}

func TestValidateSchemaAcceptsScalarSubmissionCriterionOp(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].SubmissionCriteria[0] = OntologySubmissionCriterion{
		Parameter: "iataCode", Op: PredicateIn, Values: []string{"LHR", "LGW"},
	}

	if err := validateOntologySchema(schema); err != nil {
		t.Fatalf("expected an in criterion with values to be accepted, got %v", err)
	}
}

// object_property reads a property off the object a reference parameter names,
// so it needs both halves declared: which parameter, and which property.
func TestValidateSchemaRejectsObjectPropertySourceWithoutProperty(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Parameters = append(schema.ActionTypes[0].Parameters, OntologyActionParameter{
		APIName: "flight", DataType: OntologyDataType{Kind: OntologyDataString}, ObjectType: "Flight",
	})
	schema.ActionTypes[0].Rules[0].PropertyValues["airportName"] = OntologyValueSource{
		Kind: ValueSourceObjectProperty, Parameter: "flight",
	}

	// An empty property name would also miss the "does not declare" lookup, so
	// the assertion names the missing-property complaint exactly.
	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "must name the property to read off") {
		t.Fatalf("expected object_property without a property to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsLinkRuleEndpointOnUnknownParameter(t *testing.T) {
	for _, endpoint := range []string{"from", "to"} {
		schema := manyToManySchemaWithLinkAction()
		bad := OntologyValueSource{Kind: ValueSourceParameter, Parameter: "notAParameter"}
		if endpoint == "from" {
			schema.ActionTypes[0].Rules[0].From = bad
		} else {
			schema.ActionTypes[0].Rules[0].To = bad
		}

		err := validateOntologySchema(schema)
		if err == nil || !strings.Contains(err.Error(), "link rule "+endpoint) {
			t.Fatalf("expected a bad %s endpoint to be rejected, got %v", endpoint, err)
		}
	}
}

func TestValidateSchemaRejectsObjectPropertySourceOnNonObjectParameter(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules[0].PropertyValues["airportName"] = OntologyValueSource{
		Kind: ValueSourceObjectProperty, Parameter: "airportName", Property: "flightNumber",
	}

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "object reference") {
		t.Fatalf("expected object_property on a scalar parameter to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsObjectPropertySourceOnUnknownProperty(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Parameters = append(schema.ActionTypes[0].Parameters, OntologyActionParameter{
		APIName: "flight", DataType: OntologyDataType{Kind: OntologyDataString}, ObjectType: "Flight",
	})
	schema.ActionTypes[0].Rules[0].PropertyValues["airportName"] = OntologyValueSource{
		Kind: ValueSourceObjectProperty, Parameter: "flight", Property: "tailNumber",
	}

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "tailNumber") {
		t.Fatalf("expected object_property on an undeclared property to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsDuplicateObjectSetAPINames(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ObjectSets = []OntologyNamedObjectSet{
		{APIName: "allAirports", Definition: ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"}},
		{APIName: "allairports", Definition: ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"}},
	}

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "duplicate object set") {
		t.Fatalf("expected duplicate object set names to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsMalformedNamedObjectSet(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ObjectSets = []OntologyNamedObjectSet{
		{APIName: "allAirports", Definition: ObjectSet{Kind: ObjectSetBase}},
	}

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "allAirports") {
		t.Fatalf("expected a malformed named object set to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsInvalidObjectSetAPIName(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ObjectSets = []OntologyNamedObjectSet{
		{APIName: "all airports", Definition: ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"}},
	}

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "object set api name") {
		t.Fatalf("expected an invalid object set api name to be rejected, got %v", err)
	}
}

func TestValidateSchemaAcceptsNamedObjectSets(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ObjectSets = []OntologyNamedObjectSet{
		{APIName: "allAirports", Definition: ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"}},
	}

	if err := validateOntologySchema(schema); err != nil {
		t.Fatalf("expected a well-formed named object set to be accepted, got %v", err)
	}
}

// manyToManySchemaWithLinkAction is the fixture for the link-rule path: the
// aviation schema has no many-to-many link, by design, because its one-to-many
// link is what proves the Foundry restriction fires.
func manyToManySchemaWithLinkAction() OntologySchema {
	return OntologySchema{
		SchemaID: "crew",
		Name:     "Crew",
		ObjectTypes: []OntologyObjectType{
			{
				APIName:    "Flight",
				PrimaryKey: "flightNumber",
				Properties: []OntologyProperty{
					{APIName: "flightNumber", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
				},
			},
			{
				APIName:    "CrewMember",
				PrimaryKey: "badgeId",
				Properties: []OntologyProperty{
					{APIName: "badgeId", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "crewName", DataType: OntologyDataType{Kind: OntologyDataString}},
				},
			},
		},
		LinkTypes: []OntologyLinkType{
			{
				APIName: "flightCrew",
				A:       OntologyLinkSide{APIName: "crew", ObjectTypeAPIName: "Flight", Cardinality: OntologyCardinalityMany},
				B:       OntologyLinkSide{APIName: "flights", ObjectTypeAPIName: "CrewMember", Cardinality: OntologyCardinalityMany},
			},
		},
		ActionTypes: []OntologyActionType{
			{
				APIName: "assignCrew",
				Parameters: []OntologyActionParameter{
					{APIName: "flight", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true, ObjectType: "Flight"},
					{APIName: "crewMember", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true, ObjectType: "CrewMember"},
				},
				Rules: []OntologyActionRule{
					{
						Kind:     ActionRuleCreateLink,
						LinkType: "flightCrew",
						From:     OntologyValueSource{Kind: ValueSourceParameter, Parameter: "flight"},
						To:       OntologyValueSource{Kind: ValueSourceParameter, Parameter: "crewMember"},
					},
				},
			},
		},
	}
}
