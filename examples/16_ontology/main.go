// Command ontology demonstrates the Palantir-style ontology end to end:
// typed object types with mandatory primary keys, link types with per-side
// cardinality, interfaces for polymorphic retrieval, the composable object
// set algebra, governed writes through action types, typed tool generation,
// schema diffing, and the strict_actions write gate.
//
// It needs no LLM and no embedder — every step below is lexical or structural.
//
// Run it with: go run ./examples/16_ontology
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func main() {
	dbPath := "ontology_example.db"
	cleanup := func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	}
	cleanup()
	defer cleanup()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	section("1. Activate the ontology")
	if _, err := db.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{
		Schema:   aviationSchema(),
		Activate: true,
	}); err != nil {
		log.Fatalf("save schema: %v", err)
	}
	fmt.Println("ontology 'aviation' is active and now validates every write")

	section("2. Governed writes: validate first, then apply")
	// Submission criteria see the parameters only, never the graph, so this
	// costs no queries and writes nothing.
	invalid, err := db.ApplyAction(ctx, cortexdb.ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "heathrow", "facilityName": "London Heathrow"},
		ValidateOnly: true,
	})
	if err != nil {
		log.Fatalf("validate: %v", err)
	}
	fmt.Printf("validate_only on a malformed code: valid=%v errors=%v\n", invalid.Valid, invalid.Errors)

	for _, airport := range [][3]string{
		{"LHR", "London Heathrow", "80000"},
		{"LGW", "Gatwick", "40000"},
		{"BRS", "Bristol", "9000"},
	} {
		applied, err := db.ApplyAction(ctx, cortexdb.ActionApplyRequest{
			Action: "registerAirport",
			Parameters: map[string]string{
				"iataCode": airport[0], "facilityName": airport[1], "capacity": airport[2],
			},
			ReturnEdits: true,
			Actor:       "example",
		})
		if err != nil {
			log.Fatalf("apply registerAirport: %v", err)
		}
		fmt.Printf("registered %s: applied=%v edits=%v\n", airport[0], applied.Applied, editSummary(applied.Edits))
	}

	// A modify rule reads its target through an object reference parameter,
	// given here as a primary key rather than a node ID.
	expanded, err := db.ApplyAction(ctx, cortexdb.ActionApplyRequest{
		Action:      "expandAirport",
		Parameters:  map[string]string{"airport": "BRS", "capacity": "12000"},
		ReturnEdits: true,
		Actor:       "example",
	})
	if err != nil {
		log.Fatalf("apply expandAirport: %v", err)
	}
	fmt.Printf("expanded BRS: applied=%v edits=%v\n", expanded.Applied, editSummary(expanded.Edits))

	section("3. Generic upsert, still validated against the schema")
	tools := db.GraphRAGTools()
	// strict_actions is off for now, so free-form upserts are allowed — but
	// they are validated: an unknown property or object type is refused.
	if _, err := tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{
		Entities: []cortexdb.ToolEntityInput{
			{Name: "Sunderland Plant", Type: "Plant", Metadata: map[string]string{
				"plantCode": "SUN", "facilityName": "Sunderland Plant", "capacity": "5000"}},
			{Name: "BA117", Type: "Flight", Metadata: map[string]string{
				"flightNumber": "BA117", "originIata": "LHR"}},
			{Name: "BA212", Type: "Flight", Metadata: map[string]string{
				"flightNumber": "BA212", "originIata": "LHR"}},
			{Name: "U28903", Type: "Flight", Metadata: map[string]string{
				"flightNumber": "U28903", "originIata": "LGW"}},
		},
	}); err != nil {
		log.Fatalf("upsert entities: %v", err)
	}
	_, err = tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{
		Entities: []cortexdb.ToolEntityInput{
			{Name: "Mystery Site", Type: "Spaceport", Metadata: map[string]string{"padCode": "X1"}},
		},
	})
	fmt.Printf("an object type the ontology does not declare: %v\n", oneLine(err))

	if _, err := tools.UpsertRelations(ctx, cortexdb.ToolUpsertRelationsRequest{
		Relations: []cortexdb.ToolRelationInput{
			{From: "London Heathrow", To: "BA117", Type: "flightDeparture"},
			{From: "London Heathrow", To: "BA212", Type: "flightDeparture"},
			{From: "Gatwick", To: "U28903", Type: "flightDeparture"},
		},
	}); err != nil {
		log.Fatalf("upsert relations: %v", err)
	}
	fmt.Println("linked 3 flights to their departure airports")

	section("4. Interfaces: query the abstraction, get every implementor")
	facilities, err := db.ResolveObjectSetObjects(ctx, cortexdb.ObjectSetResolveRequest{
		ObjectSet: cortexdb.ObjectSet{
			Kind:          cortexdb.ObjectSetInterfaceBase,
			InterfaceType: "Facility",
		},
	})
	if err != nil {
		log.Fatalf("resolve interface: %v", err)
	}
	fmt.Printf("Facility resolves across object types: %d objects %v\n", facilities.Total, titles(facilities))

	section("5. Object sets compose")
	// Filter over the interface, intersected with a concrete base set: the
	// large facilities that are airports.
	large := cortexdb.ObjectSet{
		Kind:   cortexdb.ObjectSetFilter,
		Source: &cortexdb.ObjectSet{Kind: cortexdb.ObjectSetInterfaceBase, InterfaceType: "Facility"},
		Where: &cortexdb.ObjectSetPredicate{
			Op: cortexdb.PredicateGte, Property: "capacity", Value: "20000",
		},
	}
	largeAirports, err := db.ResolveObjectSetObjects(ctx, cortexdb.ObjectSetResolveRequest{
		ObjectSet: cortexdb.ObjectSet{
			Kind: cortexdb.ObjectSetIntersect,
			Operands: []cortexdb.ObjectSet{
				large,
				{Kind: cortexdb.ObjectSetBase, ObjectType: "Airport"},
			},
		},
	})
	if err != nil {
		log.Fatalf("resolve intersect: %v", err)
	}
	fmt.Printf("large facilities ∩ airports: %v\n", titles(largeAirports))

	// search_around walks one named link side. "departures" starts at an
	// Airport and reaches Flights; "origin" would walk the same edges back.
	departures, err := db.ResolveObjectSetObjects(ctx, cortexdb.ObjectSetResolveRequest{
		ObjectSet: cortexdb.ObjectSet{
			Kind: cortexdb.ObjectSetSearchAround,
			Link: "departures",
			Source: &cortexdb.ObjectSet{
				Kind:   cortexdb.ObjectSetFilter,
				Source: &cortexdb.ObjectSet{Kind: cortexdb.ObjectSetBase, ObjectType: "Airport"},
				Where: &cortexdb.ObjectSetPredicate{
					Op: cortexdb.PredicateEq, Property: "iataCode", Value: "LHR",
				},
			},
		},
	})
	if err != nil {
		log.Fatalf("resolve search_around: %v", err)
	}
	fmt.Printf("search_around 'departures' from LHR: %v\n", titles(departures))

	// Full-text and set subtraction are peers of the two above, in one
	// expression rather than three APIs.
	named, err := db.ResolveObjectSetObjects(ctx, cortexdb.ObjectSetResolveRequest{
		ObjectSet: cortexdb.ObjectSet{
			Kind: cortexdb.ObjectSetSubtract,
			Operands: []cortexdb.ObjectSet{
				{Kind: cortexdb.ObjectSetInterfaceBase, InterfaceType: "Facility"},
				{
					Kind:   cortexdb.ObjectSetFilter,
					Source: &cortexdb.ObjectSet{Kind: cortexdb.ObjectSetInterfaceBase, InterfaceType: "Facility"},
					Where: &cortexdb.ObjectSetPredicate{
						Op: cortexdb.PredicateContainsAnyTerm, Property: "facilityName", Value: "plant",
					},
				},
			},
		},
	})
	if err != nil {
		log.Fatalf("resolve subtract: %v", err)
	}
	fmt.Printf("facilities minus anything named 'plant': %v\n", titles(named))

	section("6. Typed tools generated from the schema")
	// Not registered with the MCP server: the caller decides what to expose,
	// because every extra tool is paid for in the agent's context window on
	// every request. The count is capped for the same reason.
	generated, err := db.GenerateOntologyTools(ctx, cortexdb.OntologyToolGenOptions{IncludeObjectTypes: true})
	if err != nil {
		log.Fatalf("generate tools: %v", err)
	}
	fmt.Printf("generated %d typed tools (not auto-registered):\n", len(generated))
	for _, tool := range generated {
		fmt.Printf("  - %-24s %s\n", tool.Name, tool.Description)
	}
	if schema, ok := generated[0].InputSchema["properties"].(map[string]any); ok {
		fmt.Printf("  %s parameters: %s\n", generated[0].Name, describeProperties(schema))
	}

	section("7. Schema diff before changing anything")
	candidate := aviationSchema()
	// Three edits: one safe, two that would invalidate data already written.
	candidate.ObjectTypes[0].Properties = append(candidate.ObjectTypes[0].Properties,
		cortexdb.OntologyProperty{APIName: "terminalCount", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataInteger}})
	candidate.ObjectTypes[2].Properties[1].DataType = cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataInteger}
	candidate.LinkTypes[0].A.Cardinality = cortexdb.OntologyCardinalityOne

	diff, err := db.DiffOntologySchema(ctx, cortexdb.OntologyDiffRequest{SchemaID: "aviation", Candidate: candidate})
	if err != nil {
		log.Fatalf("diff: %v", err)
	}
	fmt.Printf("has_breaking_changes=%v\n", diff.Diff.HasBreakingChanges)
	for _, change := range diff.Diff.Changes {
		marker := "safe    "
		if change.Breaking {
			marker = "BREAKING"
		}
		fmt.Printf("  [%s] %-24s %-22s %s\n", marker, change.Kind, change.Target, change.Detail)
	}

	section("8. strict_actions closes the generic write path")
	strict := aviationSchema()
	strict.StrictActions = true
	if _, err := db.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{Schema: strict, Activate: true}); err != nil {
		log.Fatalf("activate strict: %v", err)
	}
	_, err = tools.UpsertEntities(ctx, cortexdb.ToolUpsertEntitiesRequest{
		Entities: []cortexdb.ToolEntityInput{
			{Name: "Stansted", Type: "Airport", Metadata: map[string]string{
				"iataCode": "STN", "facilityName": "Stansted"}},
		},
	})
	fmt.Printf("free-form upsert under strict_actions: %v\n", oneLine(err))

	governed, err := db.ApplyAction(ctx, cortexdb.ActionApplyRequest{
		Action:      "registerAirport",
		Parameters:  map[string]string{"iataCode": "STN", "facilityName": "Stansted", "capacity": "28000"},
		ReturnEdits: true,
		Actor:       "example",
	})
	if err != nil {
		log.Fatalf("apply under strict: %v", err)
	}
	fmt.Printf("the same write through an action: applied=%v edits=%v\n", governed.Applied, editSummary(governed.Edits))

	final, err := db.ResolveObjectSetObjects(ctx, cortexdb.ObjectSetResolveRequest{
		ObjectSet: cortexdb.ObjectSet{Kind: cortexdb.ObjectSetBase, ObjectType: "Airport"},
	})
	if err != nil {
		log.Fatalf("resolve final: %v", err)
	}
	fmt.Printf("airports on record: %v\n", titles(final))
}

// aviationSchema is the ontology this example governs everything with.
func aviationSchema() cortexdb.OntologySchema {
	stringType := cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}
	integerType := cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataInteger}

	return cortexdb.OntologySchema{
		SchemaID:    "aviation",
		Name:        "Aviation",
		Description: "Airports, plants and flights.",
		// A shared property is declared once and reused; an object type may
		// name it without restating its type.
		SharedProperties: []cortexdb.OntologyProperty{
			{APIName: "capacity", DisplayName: "Capacity", Description: "Passengers or units per year.", DataType: integerType},
		},
		InterfaceTypes: []cortexdb.OntologyInterfaceType{
			{
				APIName:     "Facility",
				Description: "Anything we operate as a site.",
				Properties: []cortexdb.OntologyProperty{
					{APIName: "facilityName", DataType: stringType, Required: true, Searchable: true},
					{APIName: "capacity"},
				},
			},
		},
		ObjectTypes: []cortexdb.OntologyObjectType{
			{
				APIName:           "Airport",
				PluralDisplayName: "Airports",
				PrimaryKey:        "iataCode",
				TitleProperty:     "facilityName",
				Implements:        []string{"Facility"},
				Properties: []cortexdb.OntologyProperty{
					{APIName: "iataCode", DataType: stringType, Required: true},
					{APIName: "facilityName", DataType: stringType, Required: true, Searchable: true},
					{APIName: "capacity"},
				},
			},
			{
				APIName:           "Plant",
				PluralDisplayName: "Plants",
				PrimaryKey:        "plantCode",
				TitleProperty:     "facilityName",
				Implements:        []string{"Facility"},
				Properties: []cortexdb.OntologyProperty{
					{APIName: "plantCode", DataType: stringType, Required: true},
					{APIName: "facilityName", DataType: stringType, Required: true, Searchable: true},
					{APIName: "capacity"},
				},
			},
			{
				APIName:           "Flight",
				PluralDisplayName: "Flights",
				PrimaryKey:        "flightNumber",
				TitleProperty:     "flightNumber",
				Properties: []cortexdb.OntologyProperty{
					{APIName: "flightNumber", DataType: stringType, Required: true},
					{APIName: "originIata", DataType: stringType},
				},
			},
		},
		// One link, two sides, each with its own name and multiplicity: an
		// airport has MANY departures, a flight has ONE origin. The ONE side
		// is the one that may carry the foreign key.
		LinkTypes: []cortexdb.OntologyLinkType{
			{
				APIName: "flightDeparture",
				A: cortexdb.OntologyLinkSide{
					APIName: "departures", DisplayName: "Departures",
					ObjectTypeAPIName: "Airport", Cardinality: cortexdb.OntologyCardinalityMany,
				},
				B: cortexdb.OntologyLinkSide{
					APIName: "origin", DisplayName: "Origin",
					ObjectTypeAPIName: "Flight", Cardinality: cortexdb.OntologyCardinalityOne,
					ForeignKeyProperty: "originIata",
				},
			},
		},
		ActionTypes: []cortexdb.OntologyActionType{
			{
				APIName:     "registerAirport",
				DisplayName: "Register Airport",
				Description: "Add an airport to the ontology.",
				Parameters: []cortexdb.OntologyActionParameter{
					{APIName: "iataCode", DataType: stringType, Required: true, Description: "Three-letter IATA code."},
					{APIName: "facilityName", DataType: stringType, Required: true},
					{APIName: "capacity", DataType: integerType},
				},
				Rules: []cortexdb.OntologyActionRule{
					{
						Kind:       cortexdb.ActionRuleCreateObject,
						ObjectType: "Airport",
						PropertyValues: map[string]cortexdb.OntologyValueSource{
							"iataCode":     {Kind: cortexdb.ValueSourceParameter, Parameter: "iataCode"},
							"facilityName": {Kind: cortexdb.ValueSourceParameter, Parameter: "facilityName"},
							"capacity":     {Kind: cortexdb.ValueSourceParameter, Parameter: "capacity"},
						},
					},
				},
				SubmissionCriteria: []cortexdb.OntologySubmissionCriterion{
					{Parameter: "iataCode", Regex: "^[A-Z]{3}$", FailureMessage: "IATA code must be three uppercase letters."},
				},
			},
			{
				APIName:     "expandAirport",
				DisplayName: "Expand Airport",
				Description: "Raise an existing airport's capacity.",
				Parameters: []cortexdb.OntologyActionParameter{
					{APIName: "airport", DataType: stringType, Required: true, ObjectType: "Airport"},
					{APIName: "capacity", DataType: integerType, Required: true},
				},
				Rules: []cortexdb.OntologyActionRule{
					{
						Kind:       cortexdb.ActionRuleModifyObject,
						ObjectType: "Airport",
						Target:     "airport",
						PropertyValues: map[string]cortexdb.OntologyValueSource{
							"capacity": {Kind: cortexdb.ValueSourceParameter, Parameter: "capacity"},
						},
					},
				},
			},
		},
	}
}

func section(title string) {
	fmt.Printf("\n=== %s ===\n", title)
}

func titles(resp *cortexdb.ObjectSetResolveResponse) []string {
	names := make([]string, 0, len(resp.Objects))
	for _, object := range resp.Objects {
		names = append(names, object.Title)
	}
	return names
}

func editSummary(edits []cortexdb.ActionEdit) []string {
	summary := make([]string, 0, len(edits))
	for _, edit := range edits {
		summary = append(summary, edit.Kind+" "+edit.ObjectID)
	}
	return summary
}

func describeProperties(properties map[string]any) string {
	parts := make([]string, 0, len(properties))
	for name, raw := range properties {
		schema, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:%v", name, schema["type"]))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

// oneLine keeps an expected rejection readable next to the line that caused it.
func oneLine(err error) string {
	if err == nil {
		return "accepted (no error)"
	}
	return "rejected: " + strings.Join(strings.Fields(err.Error()), " ")
}
