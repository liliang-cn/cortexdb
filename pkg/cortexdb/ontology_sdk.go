package cortexdb

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// defaultMaxGeneratedTools bounds how many tools an ontology may expand to.
// OSDK 1.x generated a full client implementation per entity and paid for it
// in code size; here the same growth lands on the agent's context window
// instead, which is the scarcer budget. So the cap is the default rather than
// an option, and the generated tools are never registered automatically —
// callers ask for them, decide what to expose, and pay only for that.
const defaultMaxGeneratedTools = 32

// OntologyToolGenOptions controls tool generation from the active ontology.
type OntologyToolGenOptions struct {
	// IncludeObjectTypes also emits one list tool per object type.
	IncludeObjectTypes bool `json:"include_object_types,omitempty"`
	// MaxTools caps the number of generated tools. Zero uses the default.
	MaxTools int `json:"max_tools,omitempty"`
}

// GenerateOntologyTools turns the active ontology into typed tool
// definitions: one per action type, and optionally one list tool per object
// type. Typed tools beat a generic upsert because the parameter names, types
// and required-ness live in the schema the model is shown rather than in
// prose it has to be trusted to follow.
//
// The result is deliberately not wired into NewMCPServer. Exposing it is the
// caller's decision, because the cost of a larger tool list is paid on every
// request, including the ones that have nothing to do with the ontology.
func (db *DB) GenerateOntologyTools(ctx context.Context, options OntologyToolGenOptions) ([]ToolDefinition, error) {
	schema, err := db.loadActiveOntologySchema(ctx)
	if err != nil || schema == nil {
		return nil, err
	}
	// Storage keeps schemas as they were written, so a property declared only
	// as a shared-property reference still has an empty data type here.
	expanded := resolveSharedProperties(*schema)

	maxTools := options.MaxTools
	if maxTools <= 0 {
		maxTools = defaultMaxGeneratedTools
	}

	definitions := make([]ToolDefinition, 0, len(expanded.ActionTypes))
	for _, action := range expanded.ActionTypes {
		definitions = append(definitions, generateActionTool(action))
	}
	if options.IncludeObjectTypes {
		for _, objectType := range expanded.ObjectTypes {
			definitions = append(definitions, generateObjectTypeListTool(objectType))
		}
	}
	// Sorted before truncation so the cap keeps a stable prefix rather than
	// whichever tools the schema happened to list first.
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })

	if len(definitions) > maxTools {
		definitions = definitions[:maxTools]
	}
	return definitions, nil
}

func generateActionTool(action OntologyActionType) ToolDefinition {
	properties := make(map[string]any, len(action.Parameters))
	required := make([]string, 0, len(action.Parameters))

	for _, parameter := range action.Parameters {
		properties[parameter.APIName] = jsonSchemaForActionParameter(parameter)
		if parameter.Required {
			required = append(required, parameter.APIName)
		}
	}
	sort.Strings(required)

	description := action.Description
	if description == "" {
		description = fmt.Sprintf("Run the %s action.", firstNonEmpty(action.DisplayName, action.APIName))
	}
	if len(action.SubmissionCriteria) > 0 {
		description += " Submission criteria apply; call with validate_only first if unsure."
	}

	return ToolDefinition{
		Name:        "action_" + toOntologySnakeCase(action.APIName),
		Description: description,
		InputSchema: toolObjectSchema(required, properties),
	}
}

func generateObjectTypeListTool(objectType OntologyObjectType) ToolDefinition {
	description := objectType.Description
	if description == "" {
		description = "List " + firstNonEmpty(objectType.PluralDisplayName, objectType.APIName+" objects") + "."
	}
	return ToolDefinition{
		Name:        "list_" + toOntologySnakeCase(objectType.APIName),
		Description: description,
		InputSchema: toolObjectSchema(nil, map[string]any{
			"limit": toolIntegerSchema("Maximum objects to return."),
			"where": map[string]any{
				"type":        "object",
				"description": fmt.Sprintf("Optional filter predicate over %s properties: %s.", objectType.APIName, ontologyPropertyNameList(objectType)),
			},
		}),
	}
}

func jsonSchemaForActionParameter(parameter OntologyActionParameter) map[string]any {
	schema := jsonSchemaForDataType(parameter.DataType, ontologyParameterDescription(parameter))
	if len(parameter.AllowedValues) > 0 {
		allowed := make([]any, 0, len(parameter.AllowedValues))
		for _, value := range parameter.AllowedValues {
			allowed = append(allowed, value)
		}
		schema["enum"] = allowed
	}
	return schema
}

func ontologyParameterDescription(parameter OntologyActionParameter) string {
	if parameter.Description != "" {
		return parameter.Description
	}
	if parameter.ObjectType != "" {
		return fmt.Sprintf("Node ID, or primary key, of an existing %s object.", parameter.ObjectType)
	}
	return firstNonEmpty(parameter.DisplayName, parameter.APIName)
}

func ontologyPropertyNameList(objectType OntologyObjectType) string {
	names := make([]string, 0, len(objectType.Properties))
	for _, property := range objectType.Properties {
		names = append(names, property.APIName)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// jsonSchemaForDataType maps an ontology data type onto the JSON Schema the
// model is shown, so a typed ontology yields typed tool parameters. The kinds
// that have no JSON counterpart — date, timestamp, geopoint — become strings
// with the expected encoding spelled out, because "string" alone would let the
// model invent a format.
func jsonSchemaForDataType(dataType OntologyDataType, description string) map[string]any {
	switch dataType.Kind {
	case OntologyDataInteger, OntologyDataLong:
		return map[string]any{"type": "integer", "description": description}
	case OntologyDataDouble, OntologyDataDecimal:
		return map[string]any{"type": "number", "description": description}
	case OntologyDataBoolean:
		return map[string]any{"type": "boolean", "description": description}
	case OntologyDataDate:
		return map[string]any{"type": "string", "description": strings.TrimSpace(description) + " (YYYY-MM-DD)"}
	case OntologyDataTimestamp:
		return map[string]any{"type": "string", "description": strings.TrimSpace(description) + " (RFC3339)"}
	case OntologyDataGeoPoint:
		return map[string]any{"type": "string", "description": strings.TrimSpace(description) + " (\"lat,lon\")"}
	case OntologyDataVector:
		return map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "number"},
			"description": description,
		}
	case OntologyDataArray:
		items := map[string]any{"type": "string"}
		if dataType.ItemType != nil {
			items = jsonSchemaForDataType(*dataType.ItemType, "")
		}
		return map[string]any{"type": "array", "items": items, "description": description}
	case OntologyDataStruct:
		return map[string]any{"type": "object", "description": description}
	default:
		return map[string]any{"type": "string", "description": description}
	}
}

// toOntologySnakeCase turns an ontology API name into a tool name segment:
// registerAirport -> register_airport, Airport -> airport.
func toOntologySnakeCase(value string) string {
	var builder strings.Builder
	for i, r := range value {
		if unicode.IsUpper(r) {
			if i > 0 {
				builder.WriteRune('_')
			}
			builder.WriteRune(unicode.ToLower(r))
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}
