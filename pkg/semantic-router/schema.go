package semanticrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// ParameterSchema defines the structure for function parameters.
type ParameterSchema struct {
	// Type is the parameter type (string, integer, number, boolean, array, object)
	Type string `json:"type"`

	// Description of what the parameter represents
	Description string `json:"description,omitempty"`

	// Required indicates if this parameter is mandatory
	Required bool `json:"required"`

	// Default value (optional)
	Default interface{} `json:"default,omitempty"`

	// Enum for enum validation (optional)
	Enum []interface{} `json:"enum,omitempty"`

	// Properties for object type (optional)
	Properties map[string]*ParameterSchema `json:"properties,omitempty"`

	// Items for array type (optional)
	Items *ParameterSchema `json:"items,omitempty"`

	// Pattern for regex validation (optional)
	Pattern string `json:"pattern,omitempty"`

	// Minimum for numeric types
	Minimum *float64 `json:"minimum,omitempty"`

	// Maximum for numeric types
	Maximum *float64 `json:"maximum,omitempty"`
}

// FunctionSchema defines a callable function with structured parameters.
type FunctionSchema struct {
	// Name is the function identifier
	Name string `json:"name"`

	// Description of what the function does
	Description string `json:"description"`

	// Parameters schema for the function
	Parameters map[string]*ParameterSchema `json:"parameters"`

	// Required parameter names
	Required []string `json:"required,omitempty"`

	// Handler is the function to execute
	Handler FunctionHandler `json:"-"`

	// Extractor for parameter extraction (optional, uses default if nil)
	Extractor ParameterExtractor `json:"-"`
}

// FunctionHandler executes a function with extracted parameters.
type FunctionHandler func(ctx context.Context, params map[string]interface{}) (interface{}, error)

// ParameterExtractor extracts parameters from text based on schema.
type ParameterExtractor interface {
	Extract(ctx context.Context, text string, schema *FunctionSchema) (map[string]interface{}, []string, error)
}

// FunctionCall represents a function call result.
type FunctionCall struct {
	// Function name
	Name string `json:"name"`

	// Extracted parameters
	Arguments map[string]interface{} `json:"arguments"`

	// Whether all required parameters were found
	Complete bool `json:"complete"`

	// Missing required parameters
	Missing []string `json:"missing,omitempty"`

	// Handler to execute
	Handler FunctionHandler `json:"-"`
}

// RouteWithFunction extends Route with function calling capability.
type RouteWithFunction struct {
	*Route
	FunctionSchema *FunctionSchema
}

// FunctionRouter handles routing with function calling.
type FunctionRouter struct {
	routes map[string]*RouteWithFunction
	mu     sync.RWMutex

	// Optional parameter extractor
	extractor ParameterExtractor

	// Fallback router for semantic matching
	router *Router
}

// NewFunctionRouter creates a new function router.
func NewFunctionRouter(baseRouter *Router) *FunctionRouter {
	return &FunctionRouter{
		routes:   make(map[string]*RouteWithFunction),
		extractor: NewRegexExtractor(),
		router:   baseRouter,
	}
}

// Add adds a route with function schema.
func (fr *FunctionRouter) Add(route *RouteWithFunction) error {
	if route == nil {
		return fmt.Errorf("route cannot be nil")
	}
	if route.FunctionSchema == nil {
		return fmt.Errorf("function schema cannot be nil")
	}
	if route.FunctionSchema.Name == "" {
		return fmt.Errorf("function name cannot be empty")
	}

	fr.mu.Lock()
	defer fr.mu.Unlock()

	fr.routes[route.FunctionSchema.Name] = route
	return nil
}

// Route performs semantic routing and returns function call with extracted parameters.
func (fr *FunctionRouter) Route(ctx context.Context, text string) (*FunctionCall, error) {
	// First, find the matching route semantically
	result, err := fr.router.Route(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("semantic routing failed: %w", err)
	}

	if !result.Matched || result.RouteName == "" {
		return nil, nil // No match
	}

	fr.mu.RLock()
	route, ok := fr.routes[result.RouteName]
	fr.mu.RUnlock()

	if !ok {
		// Route exists in semantic router but not in function router
		return nil, fmt.Errorf("route %q not found in function router", result.RouteName)
	}

	// Extract parameters using the function schema
	params, missing, err := fr.extractor.Extract(ctx, text, route.FunctionSchema)
	if err != nil {
		return nil, fmt.Errorf("parameter extraction failed: %w", err)
	}

	return &FunctionCall{
		Name:      route.FunctionSchema.Name,
		Arguments: params,
		Complete:  len(missing) == 0,
		Missing:   missing,
		Handler:   route.FunctionSchema.Handler,
	}, nil
}

// Execute executes a function call.
func (fc *FunctionCall) Execute(ctx context.Context) (interface{}, error) {
	if fc.Handler == nil {
		return nil, fmt.Errorf("no handler for function %q", fc.Name)
	}

	if !fc.Complete {
		return nil, fmt.Errorf("incomplete call: missing parameters: %v", fc.Missing)
	}

	return fc.Handler(ctx, fc.Arguments)
}

// RegexExtractor extracts parameters using regex patterns.
type RegexExtractor struct {
	patterns map[string]map[string]string // param_name -> pattern
}

// NewRegexExtractor creates a new regex-based parameter extractor.
func NewRegexExtractor() *RegexExtractor {
	return &RegexExtractor{
		patterns: make(map[string]map[string]string),
	}
}

// RegisterPattern registers a regex pattern for a parameter.
func (r *RegexExtractor) RegisterPattern(functionName, paramName, pattern string) {
	if r.patterns[functionName] == nil {
		r.patterns[functionName] = make(map[string]string)
	}
	r.patterns[functionName][paramName] = pattern
}

// Extract extracts parameters from text using regex patterns.
func (r *RegexExtractor) Extract(ctx context.Context, text string, schema *FunctionSchema) (map[string]interface{}, []string, error) {
	params := make(map[string]interface{})
	var missing []string

	textLower := strings.ToLower(text)

	for paramName, paramSchema := range schema.Parameters {
		// Check for custom pattern
		if patterns, ok := r.patterns[schema.Name]; ok {
			if pattern, ok := patterns[paramName]; ok {
				re, err := regexp.Compile(pattern)
				if err != nil {
					continue
				}
				matches := re.FindStringSubmatch(text)
				if len(matches) > 1 {
					params[paramName] = r.coerceType(matches[1], paramSchema.Type)
					continue
				}
			}
		}

		// Default extraction based on type and keywords
		value := r.extractValue(text, textLower, paramName, paramSchema)
		if value != nil {
			params[paramName] = value
		} else if paramSchema.Required {
			missing = append(missing, paramName)
		}
	}

	return params, missing, nil
}

// extractValue extracts a value from text based on parameter schema.
func (r *RegexExtractor) extractValue(text, textLower, paramName string, schema *ParameterSchema) interface{} {
	// Check description for keywords
	keywords := r.extractKeywords(schema.Description, paramName)

	for _, keyword := range keywords {
		if strings.Contains(textLower, keyword) {
			// Try to extract value after the keyword
			pattern := keyword + `\s+[:是]?\s*["']?([^"'\s,。.!?]+)["']?`
			re := regexp.MustCompile(pattern)
			matches := re.FindStringSubmatch(textLower)
			if len(matches) > 1 {
				return r.coerceType(matches[1], schema.Type)
			}
		}
	}

	// Try enum values
	if len(schema.Enum) > 0 {
		textLower = strings.ToLower(text)
		for _, enumVal := range schema.Enum {
			enumStr := fmt.Sprintf("%v", enumVal)
			if strings.Contains(textLower, strings.ToLower(enumStr)) {
				return r.coerceType(enumStr, schema.Type)
			}
		}
	}

	// Use default value
	if schema.Default != nil {
		return schema.Default
	}

	return nil
}

// extractKeywords extracts search keywords from description and parameter name.
func (r *RegexExtractor) extractKeywords(description, paramName string) []string {
	keywords := []string{strings.ToLower(paramName)}

	// Extract words from description
	if description != "" {
		words := strings.Fields(strings.ToLower(description))
		keywords = append(keywords, words...)
	}

	// Common keyword mappings
	keywordMap := map[string][]string{
		"city":       {"城市", "地点", "在哪里"},
		"date":       {"日期", "几号", "哪天", "时间"},
		"amount":     {"金额", "钱", "元", "块"},
		"product":    {"产品", "商品", "东西"},
		"quantity":   {"数量", "几个", "多少"},
		"temperature": {"温度", "气温", "度"},
	}

	if maps, ok := keywordMap[strings.ToLower(paramName)]; ok {
		keywords = append(keywords, maps...)
	}

	return keywords
}

// coerceType converts a string value to the specified type.
func (r *RegexExtractor) coerceType(value string, typeStr string) interface{} {
	switch strings.ToLower(typeStr) {
	case "string":
		return value
	case "integer", "int":
		// Extract numeric part
		re := regexp.MustCompile(`\d+`)
		matches := re.FindStringSubmatch(value)
		if len(matches) > 0 {
			var result int
			fmt.Sscanf(matches[0], "%d", &result)
			return result
		}
	case "number", "float":
		re := regexp.MustCompile(`\d+\.?\d*`)
		matches := re.FindStringSubmatch(value)
		if len(matches) > 0 {
			var result float64
			fmt.Sscanf(matches[0], "%f", &result)
			return result
		}
	case "boolean", "bool":
		lower := strings.ToLower(value)
		return lower == "true" || lower == "yes" || lower == "是" || lower == "好的"
	case "array":
		return strings.Split(value, ",")
	}
	return value
}

// SchemaBuilder helps build function schemas.
type SchemaBuilder struct {
	schema *FunctionSchema
}

// NewSchemaBuilder creates a new schema builder.
func NewSchemaBuilder(name, description string) *SchemaBuilder {
	return &SchemaBuilder{
		schema: &FunctionSchema{
			Name:        name,
			Description: description,
			Parameters:  make(map[string]*ParameterSchema),
		},
	}
}

// AddParameter adds a parameter to the schema.
func (b *SchemaBuilder) AddParameter(name, paramType, description string, required bool) *SchemaBuilder {
	b.schema.Parameters[name] = &ParameterSchema{
		Type:        paramType,
		Description: description,
		Required:    required,
	}
	return b
}

// AddParameterWithDefault adds a parameter with a default value.
func (b *SchemaBuilder) AddParameterWithDefault(name, paramType, description string, required bool, defaultValue interface{}) *SchemaBuilder {
	b.schema.Parameters[name] = &ParameterSchema{
		Type:        paramType,
		Description: description,
		Required:    required,
		Default:     defaultValue,
	}
	return b
}

// AddParameterWithEnum adds an enum parameter.
func (b *SchemaBuilder) AddParameterWithEnum(name, description string, required bool, enumValues []interface{}) *SchemaBuilder {
	b.schema.Parameters[name] = &ParameterSchema{
		Type:        "string",
		Description: description,
		Required:    required,
		Enum:        enumValues,
	}
	return b
}

// SetHandler sets the function handler.
func (b *SchemaBuilder) SetHandler(handler FunctionHandler) *SchemaBuilder {
	b.schema.Handler = handler
	return b
}

// Build returns the constructed schema.
func (b *SchemaBuilder) Build() *FunctionSchema {
	// Build required list
	for name, param := range b.schema.Parameters {
		if param.Required {
			b.schema.Required = append(b.schema.Required, name)
		}
	}
	return b.schema
}

// ToJSON converts the schema to JSON.
func (s *FunctionSchema) ToJSON() (string, error) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ParseFunctionSchema parses a function schema from JSON.
func ParseFunctionSchema(jsonStr string) (*FunctionSchema, error) {
	var schema FunctionSchema
	err := json.Unmarshal([]byte(jsonStr), &schema)
	if err != nil {
		return nil, err
	}
	return &schema, nil
}

// CreateRouteWithFunction creates a route with function calling capability.
func CreateRouteWithFunction(routeName string, utterances []string, schema *FunctionSchema) *RouteWithFunction {
	return &RouteWithFunction{
		Route: &Route{
			Name:       routeName,
			Utterances: utterances,
		},
		FunctionSchema: schema,
	}
}
