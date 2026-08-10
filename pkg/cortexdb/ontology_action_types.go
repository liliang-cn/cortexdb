package cortexdb

import (
	"fmt"
	"regexp"
	"strings"
)

// ActionRuleKind enumerates the ontology edits an action can make.
// Foundry also has function rules and side-effect rules (notification,
// webhook, schedule build); those need a runtime CortexDB deliberately does
// not have, so they are out of scope.
type ActionRuleKind string

const (
	ActionRuleCreateObject         ActionRuleKind = "create_object"
	ActionRuleModifyObject         ActionRuleKind = "modify_object"
	ActionRuleCreateOrModifyObject ActionRuleKind = "create_or_modify_object"
	ActionRuleDeleteObject         ActionRuleKind = "delete_object"
	ActionRuleCreateLink           ActionRuleKind = "create_link"
	ActionRuleDeleteLink           ActionRuleKind = "delete_link"
)

// ValueSourceKind is where a rule gets a value from.
type ValueSourceKind string

const (
	ValueSourceParameter      ValueSourceKind = "parameter"
	ValueSourceObjectProperty ValueSourceKind = "object_property"
	ValueSourceStatic         ValueSourceKind = "static"
	ValueSourceCurrentUser    ValueSourceKind = "current_user"
	ValueSourceCurrentTime    ValueSourceKind = "current_time"
)

// OntologyValueSource resolves to a concrete value at apply time.
type OntologyValueSource struct {
	Kind ValueSourceKind `json:"kind"`
	// parameter
	Parameter string `json:"parameter,omitempty"`
	// object_property: read Property off the object the named parameter points at
	Property string `json:"property,omitempty"`
	// static
	Static string `json:"static,omitempty"`
}

// OntologyActionParameter is one input to an action.
type OntologyActionParameter struct {
	APIName     string           `json:"api_name"`
	DisplayName string           `json:"display_name,omitempty"`
	Description string           `json:"description,omitempty"`
	DataType    OntologyDataType `json:"data_type"`
	Required    bool             `json:"required,omitempty"`
	// ObjectType marks this as an object reference parameter: its value is
	// the node ID, or the primary key, of an existing object of that type.
	ObjectType string `json:"object_type,omitempty"`
	// AllowedValues restricts the parameter to a fixed set.
	AllowedValues []string `json:"allowed_values,omitempty"`
}

// OntologyActionRule is one edit an action makes.
type OntologyActionRule struct {
	Kind ActionRuleKind `json:"kind"`
	// object rules
	ObjectType string `json:"object_type,omitempty"`
	// Target names the object reference parameter identifying which object
	// to modify or delete.
	Target         string                         `json:"target,omitempty"`
	PropertyValues map[string]OntologyValueSource `json:"property_values,omitempty"`
	// link rules
	LinkType string              `json:"link_type,omitempty"`
	From     OntologyValueSource `json:"from,omitempty"`
	To       OntologyValueSource `json:"to,omitempty"`
}

// OntologySubmissionCriterion gates whether an action may be submitted.
// Criteria see parameters only, never the graph — matching Foundry's
// Validate Action, which explicitly does not consult existing data.
type OntologySubmissionCriterion struct {
	Parameter      string      `json:"parameter"`
	Op             PredicateOp `json:"op,omitempty"`
	Value          string      `json:"value,omitempty"`
	Values         []string    `json:"values,omitempty"`
	Regex          string      `json:"regex,omitempty"`
	FailureMessage string      `json:"failure_message,omitempty"`
}

// OntologyActionType is a governed, auditable set of graph edits.
type OntologyActionType struct {
	APIName            string                        `json:"api_name"`
	DisplayName        string                        `json:"display_name,omitempty"`
	Description        string                        `json:"description,omitempty"`
	Status             OntologyStatus                `json:"status,omitempty"`
	Parameters         []OntologyActionParameter     `json:"parameters,omitempty"`
	Rules              []OntologyActionRule          `json:"rules,omitempty"`
	SubmissionCriteria []OntologySubmissionCriterion `json:"submission_criteria,omitempty"`
}

// scalarPredicateOps is the set matchScalarPredicate can answer. A submission
// criterion tests one parameter value, so the set-valued, full-text and vector
// predicates have no operand to work on and are rejected at save time rather
// than surfacing as an apply-time error every caller would hit.
var scalarPredicateOps = map[PredicateOp]struct{}{
	PredicateEq:         {},
	PredicateLt:         {},
	PredicateLte:        {},
	PredicateGt:         {},
	PredicateGte:        {},
	PredicateIsNull:     {},
	PredicateIn:         {},
	PredicateContains:   {},
	PredicateStartsWith: {},
}

func validateOntologyActionTypes(schema OntologySchema, compiled *compiledOntology) error {
	seen := make(map[string]struct{}, len(schema.ActionTypes))
	for _, action := range schema.ActionTypes {
		if err := validateOntologyAPIName("action type", action.APIName); err != nil {
			return err
		}
		key := ontologyAPIKey(action.APIName)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate action type %q", action.APIName)
		}
		seen[key] = struct{}{}

		parameters := make(map[string]OntologyActionParameter, len(action.Parameters))
		for _, parameter := range action.Parameters {
			label := fmt.Sprintf("action %s parameter", action.APIName)
			if err := validateOntologyAPIName(label, parameter.APIName); err != nil {
				return err
			}
			parameterKey := ontologyAPIKey(parameter.APIName)
			if _, exists := parameters[parameterKey]; exists {
				return fmt.Errorf("action %q has duplicate parameter %q", action.APIName, parameter.APIName)
			}
			if err := validateOntologyDataType(label, parameter.APIName, parameter.DataType); err != nil {
				return err
			}
			if parameter.ObjectType != "" {
				if _, ok := compiled.objectType(parameter.ObjectType); !ok {
					return fmt.Errorf("action %q parameter %q references unknown object type %q",
						action.APIName, parameter.APIName, parameter.ObjectType)
				}
			}
			parameters[parameterKey] = parameter
		}

		if len(action.Rules) == 0 {
			return fmt.Errorf("action type %q must declare at least one rule", action.APIName)
		}
		for _, rule := range action.Rules {
			if err := validateOntologyActionRule(action, rule, parameters, compiled); err != nil {
				return err
			}
		}
		for _, criterion := range action.SubmissionCriteria {
			if err := validateOntologySubmissionCriterion(action, criterion, parameters); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateOntologySubmissionCriterion(action OntologyActionType, criterion OntologySubmissionCriterion, parameters map[string]OntologyActionParameter) error {
	if _, ok := parameters[ontologyAPIKey(criterion.Parameter)]; !ok {
		return fmt.Errorf("action %q submission criterion references unknown parameter %q",
			action.APIName, criterion.Parameter)
	}
	if criterion.Regex == "" && criterion.Op == "" {
		return fmt.Errorf("action %q submission criterion on %q must declare a regex or op, or it can never fail",
			action.APIName, criterion.Parameter)
	}
	if criterion.Regex != "" {
		if _, err := regexp.Compile(criterion.Regex); err != nil {
			return fmt.Errorf("action %q submission criterion on %q has an invalid regex: %w",
				action.APIName, criterion.Parameter, err)
		}
	}
	if criterion.Op == "" {
		return nil
	}
	if _, ok := scalarPredicateOps[criterion.Op]; !ok {
		return fmt.Errorf("action %q submission criterion on %q uses %q, which compares a parameter against nothing; use one of the scalar predicates",
			action.APIName, criterion.Parameter, criterion.Op)
	}
	// Reuses the object-set predicate arity rules so a criterion and a filter
	// agree on what "in" or "eq" needs supplied.
	if err := validateObjectSetPredicate(ObjectSetPredicate{
		Op:       criterion.Op,
		Property: criterion.Parameter,
		Value:    criterion.Value,
		Values:   criterion.Values,
	}); err != nil {
		return fmt.Errorf("action %q submission criterion on %q: %w", action.APIName, criterion.Parameter, err)
	}
	return nil
}

func validateOntologyActionRule(action OntologyActionType, rule OntologyActionRule, parameters map[string]OntologyActionParameter, compiled *compiledOntology) error {
	checkSource := func(source OntologyValueSource, label string) error {
		switch source.Kind {
		case ValueSourceParameter:
			if _, ok := parameters[ontologyAPIKey(source.Parameter)]; !ok {
				return fmt.Errorf("action %q %s references unknown parameter %q", action.APIName, label, source.Parameter)
			}
		case ValueSourceObjectProperty:
			parameter, ok := parameters[ontologyAPIKey(source.Parameter)]
			if !ok {
				return fmt.Errorf("action %q %s references unknown parameter %q", action.APIName, label, source.Parameter)
			}
			// Without an object type on the parameter there is no object to
			// read from, so the source would silently hand back the raw
			// parameter value as if it were the property.
			if parameter.ObjectType == "" {
				return fmt.Errorf("action %q %s reads a property off parameter %q, which is not an object reference parameter",
					action.APIName, label, parameter.APIName)
			}
			if strings.TrimSpace(source.Property) == "" {
				return fmt.Errorf("action %q %s must name the property to read off %q", action.APIName, label, parameter.APIName)
			}
			if _, ok := compiled.property(parameter.ObjectType, source.Property); !ok {
				return fmt.Errorf("action %q %s reads property %q which object type %q does not declare",
					action.APIName, label, source.Property, parameter.ObjectType)
			}
		case ValueSourceStatic:
			if source.Static == "" {
				return fmt.Errorf("action %q %s is static but has no value", action.APIName, label)
			}
		case ValueSourceCurrentUser, ValueSourceCurrentTime:
		default:
			return fmt.Errorf("action %q %s has unknown value source kind %q", action.APIName, label, source.Kind)
		}
		return nil
	}

	switch rule.Kind {
	case ActionRuleCreateObject, ActionRuleModifyObject, ActionRuleCreateOrModifyObject, ActionRuleDeleteObject:
		objectType, ok := compiled.objectType(rule.ObjectType)
		if !ok {
			return fmt.Errorf("action %q rule references unknown object type %q", action.APIName, rule.ObjectType)
		}
		// Sorted, because a rule breaking two property rules would otherwise
		// report whichever one map iteration reached first.
		for _, propertyName := range sortedMapKeys(rule.PropertyValues) {
			if _, ok := compiled.property(objectType.APIName, propertyName); !ok {
				return fmt.Errorf("action %q rule sets property %q which object type %q does not declare",
					action.APIName, propertyName, objectType.APIName)
			}
			if err := checkSource(rule.PropertyValues[propertyName], fmt.Sprintf("rule property %q", propertyName)); err != nil {
				return err
			}
		}
		if rule.Kind == ActionRuleCreateObject || rule.Kind == ActionRuleCreateOrModifyObject {
			found := false
			for propertyName := range rule.PropertyValues {
				if ontologyAPIKey(propertyName) == ontologyAPIKey(objectType.PrimaryKey) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("action %q creates %q but does not supply its primary key %q",
					action.APIName, objectType.APIName, objectType.PrimaryKey)
			}
		}
		if rule.Kind == ActionRuleModifyObject || rule.Kind == ActionRuleDeleteObject {
			if strings.TrimSpace(rule.Target) == "" {
				return fmt.Errorf("action %q rule %q needs target naming an object reference parameter", action.APIName, rule.Kind)
			}
			if _, ok := parameters[ontologyAPIKey(rule.Target)]; !ok {
				return fmt.Errorf("action %q rule target %q is not a parameter", action.APIName, rule.Target)
			}
		}
		return nil

	case ActionRuleCreateLink, ActionRuleDeleteLink:
		linkType, ok := compiled.linkType(rule.LinkType)
		if !ok {
			return fmt.Errorf("action %q rule references unknown link type %q", action.APIName, rule.LinkType)
		}
		// Foundry restricts link rules to many-to-many links: a link with a
		// ONE side is backed by a foreign key, so it must be edited through
		// modify_object instead.
		if linkType.A.Cardinality != OntologyCardinalityMany || linkType.B.Cardinality != OntologyCardinalityMany {
			return fmt.Errorf("action %q uses a link rule on %q, but link rules only apply to many-to-many links; modify the foreign key property instead",
				action.APIName, linkType.APIName)
		}
		if err := checkSource(rule.From, "link rule from"); err != nil {
			return err
		}
		return checkSource(rule.To, "link rule to")

	default:
		return fmt.Errorf("action %q has unknown rule kind %q", action.APIName, rule.Kind)
	}
}
