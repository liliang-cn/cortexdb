package cortexdb

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func toolNames(definitions []ToolDefinition) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	return names
}

func findTool(t *testing.T, definitions []ToolDefinition, name string) ToolDefinition {
	t.Helper()
	for i := range definitions {
		if definitions[i].Name == name {
			return definitions[i]
		}
	}
	t.Fatalf("expected a tool named %q, got %v", name, toolNames(definitions))
	return ToolDefinition{}
}

func toolProperties(t *testing.T, definition ToolDefinition) map[string]any {
	t.Helper()
	properties, ok := definition.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("tool %s has no properties: %+v", definition.Name, definition.InputSchema)
	}
	return properties
}

func TestGenerateTypedToolsForActions(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	definitions, err := db.GenerateOntologyTools(context.Background(), OntologyToolGenOptions{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	registerAirport := findTool(t, definitions, "action_register_airport")
	properties := toolProperties(t, registerAirport)
	if _, ok := properties["iataCode"]; !ok {
		t.Fatalf("expected iataCode as a first-class parameter, got %+v", properties)
	}
	if _, ok := properties["airportName"]; !ok {
		t.Fatalf("expected airportName as a first-class parameter, got %+v", properties)
	}

	required, ok := registerAirport.InputSchema["required"].([]string)
	if !ok {
		t.Fatalf("expected required to be []string, got %T (%v)", registerAirport.InputSchema["required"], registerAirport.InputSchema["required"])
	}
	if !reflect.DeepEqual(required, []string{"airportName", "iataCode"}) {
		t.Fatalf("expected both required parameters declared and sorted, got %v", required)
	}
}

// The generated surface has to distinguish required from optional parameters,
// not simply list every parameter it saw. aviationSchemaWithActions declares
// both of its parameters required, so a test built only on it would pass with
// the Required flag ignored entirely.
func TestGenerateToolsMarksOnlyRequiredParametersRequired(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Parameters = append(schema.ActionTypes[0].Parameters,
		OntologyActionParameter{APIName: "note", DataType: OntologyDataType{Kind: OntologyDataString}},
	)
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	definitions, err := db.GenerateOntologyTools(ctx, OntologyToolGenOptions{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	registerAirport := findTool(t, definitions, "action_register_airport")

	properties := toolProperties(t, registerAirport)
	if _, ok := properties["note"]; !ok {
		t.Fatalf("an optional parameter still belongs in properties, got %+v", properties)
	}

	required, _ := registerAirport.InputSchema["required"].([]string)
	if !reflect.DeepEqual(required, []string{"airportName", "iataCode"}) {
		t.Fatalf("expected only the required parameters in required, got %v", required)
	}
}

// An action with no required parameters must not emit "required": null, which
// strict JSON Schema validators reject.
func TestGenerateToolsOmitsRequiredWhenNoParameterIsRequired(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	for i := range schema.ActionTypes[0].Parameters {
		schema.ActionTypes[0].Parameters[i].Required = false
	}
	schema.ActionTypes[0].SubmissionCriteria = nil
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	definitions, err := db.GenerateOntologyTools(ctx, OntologyToolGenOptions{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	registerAirport := findTool(t, definitions, "action_register_airport")
	if raw, ok := registerAirport.InputSchema["required"]; ok {
		t.Fatalf("expected no required key at all, got %v", raw)
	}
}

func TestGenerateTypedToolsForObjectTypes(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	definitions, err := db.GenerateOntologyTools(context.Background(), OntologyToolGenOptions{IncludeObjectTypes: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	listAirport := findTool(t, definitions, "list_airport")
	properties := toolProperties(t, listAirport)
	if _, ok := properties["limit"]; !ok {
		t.Fatalf("expected a limit parameter on a list tool, got %+v", properties)
	}
	if _, ok := properties["where"]; !ok {
		t.Fatalf("expected a where parameter on a list tool, got %+v", properties)
	}
	findTool(t, definitions, "list_flight")
}

// IncludeObjectTypes is a flag, so it has to be able to be off: the default
// surface is one tool per action and nothing else.
func TestGenerateToolsOmitsObjectTypeToolsByDefault(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	definitions, err := db.GenerateOntologyTools(context.Background(), OntologyToolGenOptions{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got := toolNames(definitions); !reflect.DeepEqual(got, []string{"action_register_airport"}) {
		t.Fatalf("expected only the action tool without IncludeObjectTypes, got %v", got)
	}
}

func TestGenerateToolsMapsDataTypesToJSONSchemaTypes(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Parameters = append(schema.ActionTypes[0].Parameters,
		OntologyActionParameter{APIName: "elevation", DataType: OntologyDataType{Kind: OntologyDataInteger}},
		OntologyActionParameter{APIName: "latitude", DataType: OntologyDataType{Kind: OntologyDataDouble}},
		OntologyActionParameter{APIName: "isHub", DataType: OntologyDataType{Kind: OntologyDataBoolean}},
	)
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	definitions, err := db.GenerateOntologyTools(ctx, OntologyToolGenOptions{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	properties := toolProperties(t, findTool(t, definitions, "action_register_airport"))

	for parameter, want := range map[string]string{
		"iataCode":  "string",
		"elevation": "integer",
		"latitude":  "number",
		"isHub":     "boolean",
	} {
		got, ok := properties[parameter].(map[string]any)
		if !ok {
			t.Fatalf("parameter %s missing from %+v", parameter, properties)
		}
		if got["type"] != want {
			t.Errorf("parameter %s: expected type %q, got %v", parameter, want, got["type"])
		}
	}
}

// Every data kind is checked separately so that collapsing any one of them
// onto another — integer onto number, boolean onto string, the default onto
// everything — fails on its own line rather than being masked by a sibling.
func TestJSONSchemaForDataTypeCoversEveryKind(t *testing.T) {
	cases := []struct {
		kind OntologyDataKind
		want string
	}{
		{OntologyDataString, "string"},
		{OntologyDataInteger, "integer"},
		{OntologyDataLong, "integer"},
		{OntologyDataDouble, "number"},
		{OntologyDataDecimal, "number"},
		{OntologyDataBoolean, "boolean"},
		{OntologyDataDate, "string"},
		{OntologyDataTimestamp, "string"},
		{OntologyDataGeoPoint, "string"},
		{OntologyDataGeoShape, "string"},
		{OntologyDataMarking, "string"},
		{OntologyDataStruct, "object"},
	}
	for _, testCase := range cases {
		got := jsonSchemaForDataType(OntologyDataType{Kind: testCase.kind}, "desc")
		if got["type"] != testCase.want {
			t.Errorf("%s: expected %q, got %v", testCase.kind, testCase.want, got["type"])
		}
	}

	array := jsonSchemaForDataType(OntologyDataType{
		Kind:     OntologyDataArray,
		ItemType: &OntologyDataType{Kind: OntologyDataInteger},
	}, "desc")
	if array["type"] != "array" {
		t.Fatalf("expected array, got %v", array["type"])
	}
	items, ok := array["items"].(map[string]any)
	if !ok || items["type"] != "integer" {
		t.Fatalf("expected the element type to be carried through, got %+v", array["items"])
	}

	// The date and timestamp descriptions have to say which encoding is
	// expected, since both land on JSON Schema "string".
	date := jsonSchemaForDataType(OntologyDataType{Kind: OntologyDataDate}, "when")
	timestamp := jsonSchemaForDataType(OntologyDataType{Kind: OntologyDataTimestamp}, "when")
	if date["description"] == timestamp["description"] {
		t.Fatalf("date and timestamp must be distinguishable in the description, both said %v", date["description"])
	}
}

func TestGenerateToolsRespectsMaxTools(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	unbounded, err := db.GenerateOntologyTools(context.Background(), OntologyToolGenOptions{IncludeObjectTypes: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(unbounded) <= 1 {
		t.Fatalf("fixture must generate more than one tool for the cap to mean anything, got %v", toolNames(unbounded))
	}

	definitions, err := db.GenerateOntologyTools(context.Background(), OntologyToolGenOptions{
		IncludeObjectTypes: true,
		MaxTools:           1,
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(definitions) != 1 {
		t.Fatalf("expected the cap to be honoured, got %d tools: %v", len(definitions), toolNames(definitions))
	}
}

// The default cap is the whole point of the feature: an ontology that grows
// must not silently grow the agent's context window with it.
func TestGenerateToolsDefaultCapBoundsTheSurface(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := OntologySchema{SchemaID: "wide", Name: "Wide"}
	for i := 0; i < defaultMaxGeneratedTools+8; i++ {
		schema.ObjectTypes = append(schema.ObjectTypes, OntologyObjectType{
			APIName:    fmt.Sprintf("Widget%02d", i),
			PrimaryKey: "widgetID",
			Properties: []OntologyProperty{
				{APIName: "widgetID", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
			},
		})
	}
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	definitions, err := db.GenerateOntologyTools(ctx, OntologyToolGenOptions{IncludeObjectTypes: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(definitions) != defaultMaxGeneratedTools {
		t.Fatalf("expected the default cap of %d, got %d", defaultMaxGeneratedTools, len(definitions))
	}
}

func TestGenerateToolsWithoutActiveSchemaReturnsNothing(t *testing.T) {
	db := openOntologyTestDB(t)

	definitions, err := db.GenerateOntologyTools(context.Background(), OntologyToolGenOptions{IncludeObjectTypes: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(definitions) != 0 {
		t.Fatalf("expected no generated tools without an ontology, got %v", toolNames(definitions))
	}
}

// Generated tools are opt-in, not automatic. OSDK 1.x grew code with the
// ontology; the equivalent failure here is a tool list that grows with it and
// eats the context window of every request, whether or not the ontology is
// what the caller came for.
func TestGeneratedToolsAreNotRegisteredWithTheMCPServer(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)
	ctx := context.Background()

	generated, err := db.GenerateOntologyTools(ctx, OntologyToolGenOptions{IncludeObjectTypes: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(generated) == 0 {
		t.Fatal("fixture generated nothing, so the assertion below would pass vacuously")
	}

	declared := make(map[string]struct{})
	for _, definition := range db.GraphRAGTools().Definitions() {
		declared[definition.Name] = struct{}{}
	}

	server := db.NewMCPServer(MCPServerOptions{})
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "sdk-test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = clientSession.Close() }()

	listed, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	reachable := make(map[string]struct{}, len(listed.Tools))
	for _, tool := range listed.Tools {
		reachable[tool.Name] = struct{}{}
	}

	for _, definition := range generated {
		if _, ok := declared[definition.Name]; ok {
			t.Errorf("generated tool %s must not be in the static tool list", definition.Name)
		}
		if _, ok := reachable[definition.Name]; ok {
			t.Errorf("generated tool %s must not be auto-registered with the MCP server", definition.Name)
		}
	}
}
