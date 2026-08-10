package cortexdb

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

func diffChangeKinds(diff OntologyDiff) []string {
	kinds := make([]string, 0, len(diff.Changes))
	for _, change := range diff.Changes {
		kinds = append(kinds, change.Kind+" "+change.Target)
	}
	sort.Strings(kinds)
	return kinds
}

// requireChange asserts on the individual change rather than on the diff's
// summary flag. HasBreakingChanges alone would pass with the rule under test
// deleted, as long as any other rule in the same fixture happened to fire.
func requireChange(t *testing.T, diff OntologyDiff, kind string, target string, breaking bool) OntologyChange {
	t.Helper()
	for _, change := range diff.Changes {
		if change.Kind == kind && change.Target == target {
			if change.Breaking != breaking {
				t.Fatalf("change %s on %s: expected breaking=%v, got %v (%s)", kind, target, breaking, change.Breaking, change.Detail)
			}
			if change.Detail == "" {
				t.Fatalf("change %s on %s has no detail; the diff is read by humans deciding whether to apply it", kind, target)
			}
			if breaking && !diff.HasBreakingChanges {
				t.Fatalf("change %s on %s is breaking but the diff does not say so", kind, target)
			}
			return change
		}
	}
	t.Fatalf("expected a %s change on %s, got %v", kind, target, diffChangeKinds(diff))
	return OntologyChange{}
}

func requireNoBreakingChanges(t *testing.T, diff OntologyDiff) {
	t.Helper()
	if diff.HasBreakingChanges {
		t.Fatalf("expected no breaking changes, got %+v", diff.Changes)
	}
	for _, change := range diff.Changes {
		if change.Breaking {
			t.Fatalf("change %s on %s should not be breaking", change.Kind, change.Target)
		}
	}
}

func TestDiffOnIdenticalSchemasIsEmpty(t *testing.T) {
	diff := DiffOntologySchemas(validAviationSchema(), validAviationSchema())
	if len(diff.Changes) != 0 {
		t.Fatalf("expected no changes, got %+v", diff.Changes)
	}
	if diff.HasBreakingChanges {
		t.Fatal("an unchanged schema cannot break anything")
	}
}

func TestDiffDetectsAddedObjectType(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.ObjectTypes = append(after.ObjectTypes, OntologyObjectType{
		APIName: "Aircraft", PrimaryKey: "tailNumber",
		Properties: []OntologyProperty{
			{APIName: "tailNumber", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
		},
	})

	diff := DiffOntologySchemas(before, after)
	if got := diffChangeKinds(diff); !reflect.DeepEqual(got, []string{"object_type_added Aircraft"}) {
		t.Fatalf("expected exactly the addition, got %v", got)
	}
	requireNoBreakingChanges(t, diff)
}

func TestDiffDetectsRemovedObjectTypeAsBreaking(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.ObjectTypes = after.ObjectTypes[:1]
	// The link type referencing the removed object type goes too, so the only
	// change left standing is the removal itself.
	after.LinkTypes = nil

	diff := DiffOntologySchemas(before, after)
	requireChange(t, diff, "object_type_removed", "Flight", true)
}

func TestDiffDetectsPrimaryKeyChangeAsBreaking(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.ObjectTypes[0].PrimaryKey = "airportName"

	diff := DiffOntologySchemas(before, after)
	// Asserted as the only change: flipping the new key's Required flag at the
	// same time — as one naturally would, to keep the schema valid — would
	// raise a second breaking change and mask a missing primary-key rule.
	if got := diffChangeKinds(diff); !reflect.DeepEqual(got, []string{"primary_key_changed Airport"}) {
		t.Fatalf("expected only the primary key change, got %v", got)
	}
	requireChange(t, diff, "primary_key_changed", "Airport", true)
}

func TestDiffTreatsPrimaryKeyCaseChangeAsNoChange(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.ObjectTypes[0].PrimaryKey = "IATACODE"

	diff := DiffOntologySchemas(before, after)
	if len(diff.Changes) != 0 {
		t.Fatalf("api names are matched case-insensitively everywhere else, got %+v", diff.Changes)
	}
}

func TestDiffDetectsRemovedPropertyAsBreaking(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.ObjectTypes[0].Properties = after.ObjectTypes[0].Properties[:1]

	diff := DiffOntologySchemas(before, after)
	if got := diffChangeKinds(diff); !reflect.DeepEqual(got, []string{"property_removed Airport.airportName"}) {
		t.Fatalf("expected only the property removal, got %v", got)
	}
	requireChange(t, diff, "property_removed", "Airport.airportName", true)
}

func TestDiffDetectsPropertyTypeChangeAsBreaking(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.ObjectTypes[0].Properties[1].DataType = OntologyDataType{Kind: OntologyDataInteger}

	diff := DiffOntologySchemas(before, after)
	if got := diffChangeKinds(diff); !reflect.DeepEqual(got, []string{"property_type_changed Airport.airportName"}) {
		t.Fatalf("expected only the type change, got %v", got)
	}
	requireChange(t, diff, "property_type_changed", "Airport.airportName", true)
}

func TestDiffDetectsPropertyBecomingRequiredAsBreaking(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.ObjectTypes[0].Properties[1].Required = true

	diff := DiffOntologySchemas(before, after)
	if got := diffChangeKinds(diff); !reflect.DeepEqual(got, []string{"property_became_required Airport.airportName"}) {
		t.Fatalf("expected only the required flip, got %v", got)
	}
	requireChange(t, diff, "property_became_required", "Airport.airportName", true)
}

func TestDiffDetectsPropertyBecomingOptionalAsSafe(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	before.ObjectTypes[0].Properties[1].Required = true

	diff := DiffOntologySchemas(before, after)
	requireNoBreakingChanges(t, diff)
	if got := diffChangeKinds(diff); !reflect.DeepEqual(got, []string{"property_became_optional Airport.airportName"}) {
		t.Fatalf("relaxing a property is a reportable, non-breaking change, got %v", got)
	}
}

func TestDiffDetectsNewRequiredPropertyAsBreaking(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.ObjectTypes[0].Properties = append(after.ObjectTypes[0].Properties, OntologyProperty{
		APIName: "runwayCount", DataType: OntologyDataType{Kind: OntologyDataInteger}, Required: true,
	})

	diff := DiffOntologySchemas(before, after)
	if got := diffChangeKinds(diff); !reflect.DeepEqual(got, []string{"required_property_added Airport.runwayCount"}) {
		t.Fatalf("expected only the required addition, got %v", got)
	}
	requireChange(t, diff, "required_property_added", "Airport.runwayCount", true)
}

func TestDiffDetectsOptionalPropertyAdditionAsSafe(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.ObjectTypes[0].Properties = append(after.ObjectTypes[0].Properties, OntologyProperty{
		APIName: "runwayCount", DataType: OntologyDataType{Kind: OntologyDataInteger},
	})

	diff := DiffOntologySchemas(before, after)
	requireNoBreakingChanges(t, diff)
	if got := diffChangeKinds(diff); !reflect.DeepEqual(got, []string{"property_added Airport.runwayCount"}) {
		t.Fatalf("expected the addition to be reported once, got %v", got)
	}
}

func TestDiffDetectsCardinalityTighteningAsBreaking(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.LinkTypes[0].A.Cardinality = OntologyCardinalityOne

	diff := DiffOntologySchemas(before, after)
	if got := diffChangeKinds(diff); !reflect.DeepEqual(got, []string{"cardinality_tightened flightDeparture.a"}) {
		t.Fatalf("expected only the tightening, got %v", got)
	}
	requireChange(t, diff, "cardinality_tightened", "flightDeparture.a", true)
}

func TestDiffDetectsCardinalityRelaxationAsSafe(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.LinkTypes[0].B.Cardinality = OntologyCardinalityMany

	diff := DiffOntologySchemas(before, after)
	requireNoBreakingChanges(t, diff)
	if got := diffChangeKinds(diff); !reflect.DeepEqual(got, []string{"cardinality_relaxed flightDeparture.b"}) {
		t.Fatalf("expected only the relaxation, got %v", got)
	}
}

func TestDiffDetectsLinkSideRetargetAsBreaking(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.ObjectTypes = append(after.ObjectTypes, OntologyObjectType{
		APIName: "Heliport", PrimaryKey: "icaoCode",
		Properties: []OntologyProperty{
			{APIName: "icaoCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
		},
	})
	after.LinkTypes[0].A.ObjectTypeAPIName = "Heliport"

	diff := DiffOntologySchemas(before, after)
	requireChange(t, diff, "link_side_retargeted", "flightDeparture.a", true)
}

func TestDiffDetectsRemovedLinkTypeAsBreaking(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.LinkTypes = nil

	diff := DiffOntologySchemas(before, after)
	if got := diffChangeKinds(diff); !reflect.DeepEqual(got, []string{"link_type_removed flightDeparture"}) {
		t.Fatalf("expected only the link removal, got %v", got)
	}
	requireChange(t, diff, "link_type_removed", "flightDeparture", true)
}

func TestDiffDetectsAddedLinkTypeAsSafe(t *testing.T) {
	before := validAviationSchema()
	before.LinkTypes = nil
	after := validAviationSchema()

	diff := DiffOntologySchemas(before, after)
	requireNoBreakingChanges(t, diff)
	if got := diffChangeKinds(diff); !reflect.DeepEqual(got, []string{"link_type_added flightDeparture"}) {
		t.Fatalf("expected only the link addition, got %v", got)
	}
}

// Storage keeps a schema exactly as it was written, shared property
// references and all. Diffing the stored form directly compares two
// placeholders with no data type and finds them identical, so retyping a
// shared property — which retypes it on every object type that uses it — is
// the single most far-reaching change the diff could miss.
func TestDiffExpandsSharedPropertiesBeforeComparing(t *testing.T) {
	before := sharedPropertySchema()
	after := sharedPropertySchema()
	after.SharedProperties[0].DataType = OntologyDataType{Kind: OntologyDataString}

	diff := DiffOntologySchemas(before, after)
	requireChange(t, diff, "property_type_changed", "Airport.position", true)
	requireChange(t, diff, "property_type_changed", "Plant.position", true)
}

// The mirror of the test above: expanding only one side is worse than
// expanding neither, because a bare placeholder compared against a resolved
// type differs in every field and reports a schema that did not change as a
// breaking one.
func TestDiffExpandsBothSidesBeforeComparing(t *testing.T) {
	diff := DiffOntologySchemas(sharedPropertySchema(), sharedPropertySchema())
	if len(diff.Changes) != 0 {
		t.Fatalf("an unchanged shared-property schema has no differences, got %+v", diff.Changes)
	}
	if diff.HasBreakingChanges {
		t.Fatal("an unchanged schema cannot break anything")
	}
}

// Api names are matched case-insensitively everywhere else in the ontology, so
// a rename that only changes case must not read as a type removed and another
// added — which is the most alarming pair of changes the diff can report.
func TestDiffMatchesAPINamesCaseInsensitively(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.ObjectTypes[0].APIName = "AIRPORT"
	after.ObjectTypes[0].Properties[1].APIName = "AIRPORTNAME"
	after.LinkTypes[0].APIName = "FLIGHTDEPARTURE"

	diff := DiffOntologySchemas(before, after)
	if len(diff.Changes) != 0 {
		t.Fatalf("expected case alone to be no change, got %v", diffChangeKinds(diff))
	}
}

func TestDiffOntologySchemaComparesAgainstTheStoredSchema(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: sharedPropertySchema(), Activate: true}); err != nil {
		t.Fatalf("save: %v", err)
	}

	candidate := sharedPropertySchema()
	candidate.SharedProperties[0].DataType = OntologyDataType{Kind: OntologyDataString}

	resp, err := db.DiffOntologySchema(ctx, OntologyDiffRequest{SchemaID: "shared", Candidate: candidate})
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	requireChange(t, resp.Diff, "property_type_changed", "Airport.position", true)
	if !resp.Diff.HasBreakingChanges {
		t.Fatal("expected the stored comparison to report breaking changes")
	}
}

// The stored schema is the `before` side and the candidate the `after` side.
// Swapping them inverts every asymmetric verdict the diff makes: making a
// property required would be reported as relaxing it, and the caller would be
// told a data-invalidating change is safe.
func TestDiffOntologySchemaTreatsTheStoredSchemaAsBefore(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: validAviationSchema(), Activate: true}); err != nil {
		t.Fatalf("save: %v", err)
	}

	candidate := validAviationSchema()
	candidate.ObjectTypes[0].Properties[1].Required = true

	resp, err := db.DiffOntologySchema(ctx, OntologyDiffRequest{SchemaID: "aviation", Candidate: candidate})
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if got := diffChangeKinds(resp.Diff); !reflect.DeepEqual(got, []string{"property_became_required Airport.airportName"}) {
		t.Fatalf("expected the stored schema to be the before side, got %v", got)
	}
	requireChange(t, resp.Diff, "property_became_required", "Airport.airportName", true)
}

func TestDiffOntologySchemaOnUnknownSchemaErrors(t *testing.T) {
	db := openOntologyTestDB(t)

	if _, err := db.DiffOntologySchema(context.Background(), OntologyDiffRequest{
		SchemaID:  "nope",
		Candidate: validAviationSchema(),
	}); err == nil {
		t.Fatal("expected an error for an unknown schema id")
	}
}

func TestDiffOntologySchemaIsReachableThroughTheToolbox(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: validAviationSchema(), Activate: true}); err != nil {
		t.Fatalf("save: %v", err)
	}

	input := []byte(`{
		"schema_id": "aviation",
		"candidate": {
			"schema_id": "aviation",
			"object_types": [
				{
					"api_name": "Airport",
					"primary_key": "iataCode",
					"properties": [
						{"api_name": "iataCode", "data_type": {"kind": "string"}, "required": true}
					]
				}
			]
		}
	}`)
	resp, err := db.GraphRAGTools().Call(ctx, "ontology_diff", input)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	diffResp, ok := resp.(*OntologyDiffResponse)
	if !ok {
		t.Fatalf("expected an *OntologyDiffResponse, got %T", resp)
	}
	requireChange(t, diffResp.Diff, "object_type_removed", "Flight", true)
	requireChange(t, diffResp.Diff, "property_removed", "Airport.airportName", true)
}
