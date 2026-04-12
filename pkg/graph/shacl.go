package graph

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
)

// SHACL IRIs
const (
	SHACLNamespace = "http://www.w3.org/ns/shacl#"
	RDFNamespace   = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	XSDNamespace   = "http://www.w3.org/2001/XMLSchema#"

	SHACLNodeShape     = SHACLNamespace + "NodeShape"
	SHACLPropertyShape = SHACLNamespace + "PropertyShape"
	SHACLProperty      = SHACLNamespace + "property"
	SHACLPath          = SHACLNamespace + "path"
	SHACLTargetClass   = SHACLNamespace + "targetClass"
	SHACLTargetNode    = SHACLNamespace + "targetNode"
	SHACLDatatype      = SHACLNamespace + "datatype"
	SHACLMinCount      = SHACLNamespace + "minCount"
	SHACLMaxCount      = SHACLNamespace + "maxCount"
	SHACLMinInclusive  = SHACLNamespace + "minInclusive"
	SHACLMaxInclusive  = SHACLNamespace + "maxInclusive"
	SHACLPattern       = SHACLNamespace + "pattern"
	SHACLIn            = SHACLNamespace + "in"
	SHACLNodeKind      = SHACLNamespace + "nodeKind"
	SHACLClass         = SHACLNamespace + "class"
	SHACLSeverity      = SHACLNamespace + "severity"
	SHACLMessage       = SHACLNamespace + "message"

	SHACLSeverityInfo      = SHACLNamespace + "Info"
	SHACLSeverityWarning   = SHACLNamespace + "Warning"
	SHACLSeverityViolation = SHACLNamespace + "Violation"

	SHACLIRI               = SHACLNamespace + "IRI"
	SHACLBlankNode         = SHACLNamespace + "BlankNode"
	SHACLLiteral           = SHACLNamespace + "Literal"
	SHACLBlankNodeOrIRI    = SHACLNamespace + "BlankNodeOrIRI"
	SHACLBlankNodeOrLiteral = SHACLNamespace + "BlankNodeOrLiteral"
	SHACLIRIOrLiteral      = SHACLNamespace + "IRIOrLiteral"

	RDFType = RDFNamespace + "type"
)

// SHACLValidationResult represents a single constraint violation.
type SHACLValidationResult struct {
	FocusNode RDFTerm `json:"focus_node"`
	Path      RDFTerm `json:"path"`
	Value     RDFTerm `json:"value,omitempty"`
	Message   string  `json:"message"`
	Severity  string  `json:"severity"`
	Source    RDFTerm `json:"source_shape"`
}

// SHACLReport contains the outcome of SHACL validation.
type SHACLReport struct {
	Conforms bool                    `json:"conforms"`
	Results  []SHACLValidationResult `json:"results,omitempty"`
}

// shaclPropertyShape represents a property constraint in SHACL.
type shaclPropertyShape struct {
	ID           RDFTerm
	Path         RDFTerm
	Datatype     string
	MinCount     *int
	MaxCount     *int
	MinInclusive *float64
	MaxInclusive *float64
	Pattern      *regexp.Regexp
	In           []RDFTerm
	NodeKind     string
	Class        *RDFTerm
	Severity     string
	Message      string
}

// shaclNodeShape represents a node constraint in SHACL.
type shaclNodeShape struct {
	ID          RDFTerm
	TargetClass []RDFTerm
	TargetNode  []RDFTerm
	Properties  []shaclPropertyShape
}

// ValidateSHACL runs SHACL validation against the graph store using the provided shapes.
func (g *GraphStore) ValidateSHACL(ctx context.Context, shapeTriples []RDFTriple) (*SHACLReport, error) {
	shapes, err := parseSHACLShapes(shapeTriples)
	if err != nil {
		return nil, fmt.Errorf("parse shapes: %v", err)
	}

	report := &SHACLReport{Conforms: true}

	for _, shape := range shapes {
		targets, err := g.findSHACLTargets(ctx, shape)
		if err != nil {
			return nil, err
		}

		for _, focusNode := range targets {
			for _, prop := range shape.Properties {
				results, err := g.validateSHACLProperty(ctx, focusNode, prop)
				if err != nil {
					return nil, err
				}
				if len(results) > 0 {
					report.Conforms = false
					report.Results = append(report.Results, results...)
				}
			}
		}
	}

	return report, nil
}

func parseSHACLShapes(triples []RDFTriple) ([]shaclNodeShape, error) {
	// Group triples by subject
	bySubject := make(map[string][]RDFTriple)
	for _, tr := range triples {
		bySubject[tr.Subject.String()] = append(bySubject[tr.Subject.String()], tr)
	}

	var nodeShapes []shaclNodeShape
	for _, subjectTriples := range bySubject {
		isNodeShape := false
		for _, tr := range subjectTriples {
			if tr.Predicate.Value == RDFType && tr.Object.Value == SHACLNodeShape {
				isNodeShape = true
				break
			}
		}

		if !isNodeShape {
			continue
		}

		shape := shaclNodeShape{ID: subjectTriples[0].Subject}
		for _, tr := range subjectTriples {
			switch tr.Predicate.Value {
			case SHACLTargetClass:
				shape.TargetClass = append(shape.TargetClass, tr.Object)
			case SHACLTargetNode:
				shape.TargetNode = append(shape.TargetNode, tr.Object)
			case SHACLProperty:
				propShape, err := parseSHACLPropertyShape(tr.Object, bySubject)
				if err != nil {
					return nil, err
				}
				shape.Properties = append(shape.Properties, propShape)
			}
		}
		nodeShapes = append(nodeShapes, shape)
	}

	return nodeShapes, nil
}

func parseSHACLPropertyShape(id RDFTerm, bySubject map[string][]RDFTriple) (shaclPropertyShape, error) {
	prop := shaclPropertyShape{ID: id, Severity: SHACLSeverityViolation}
	triples, ok := bySubject[id.String()]
	if !ok {
		return prop, nil // Minimal or external property shape
	}

	for _, tr := range triples {
		switch tr.Predicate.Value {
		case SHACLPath:
			prop.Path = tr.Object
		case SHACLDatatype:
			prop.Datatype = tr.Object.Value
		case SHACLMinCount:
			if count, err := strconv.Atoi(tr.Object.Value); err == nil {
				prop.MinCount = &count
			}
		case SHACLMaxCount:
			if count, err := strconv.Atoi(tr.Object.Value); err == nil {
				prop.MaxCount = &count
			}
		case SHACLMinInclusive:
			if val, err := strconv.ParseFloat(tr.Object.Value, 64); err == nil {
				prop.MinInclusive = &val
			}
		case SHACLMaxInclusive:
			if val, err := strconv.ParseFloat(tr.Object.Value, 64); err == nil {
				prop.MaxInclusive = &val
			}
		case SHACLPattern:
			if re, err := regexp.Compile(tr.Object.Value); err == nil {
				prop.Pattern = re
			}
		case SHACLSeverity:
			prop.Severity = tr.Object.Value
		case SHACLMessage:
			prop.Message = tr.Object.Value
		}
	}

	return prop, nil
}

func (g *GraphStore) findSHACLTargets(ctx context.Context, shape shaclNodeShape) ([]RDFTerm, error) {
	var targets []RDFTerm
	// Target Class
	for _, cls := range shape.TargetClass {
		pattern := TriplePattern{
			Predicate: &RDFTerm{Kind: RDFTermIRI, Value: RDFType},
			Object:    &cls,
		}
		triples, err := g.FindTriples(ctx, pattern)
		if err != nil {
			return nil, err
		}
		for _, tr := range triples {
			targets = append(targets, tr.Subject)
		}
	}

	// Target Node
	for _, node := range shape.TargetNode {
		targets = append(targets, node)
	}

	return targets, nil
}

func (g *GraphStore) validateSHACLProperty(ctx context.Context, focusNode RDFTerm, prop shaclPropertyShape) ([]SHACLValidationResult, error) {
	pattern := TriplePattern{
		Subject:   &focusNode,
		Predicate: &prop.Path,
	}
	values, err := g.FindTriples(ctx, pattern)
	if err != nil {
		return nil, err
	}

	var results []SHACLValidationResult

	// Count constraint
	count := len(values)
	if prop.MinCount != nil && count < *prop.MinCount {
		results = append(results, SHACLValidationResult{
			FocusNode: focusNode,
			Path:      prop.Path,
			Message:   fmt.Sprintf("Less than %d values", *prop.MinCount),
			Severity:  prop.Severity,
			Source:    prop.ID,
		})
	}
	if prop.MaxCount != nil && count > *prop.MaxCount {
		results = append(results, SHACLValidationResult{
			FocusNode: focusNode,
			Path:      prop.Path,
			Message:   fmt.Sprintf("More than %d values", *prop.MaxCount),
			Severity:  prop.Severity,
			Source:    prop.ID,
		})
	}

	// Value constraints
	for _, tr := range values {
		val := tr.Object

		// Datatype constraint
		if prop.Datatype != "" {
			if val.Kind != RDFTermLiteral || val.Datatype != prop.Datatype {
				results = append(results, SHACLValidationResult{
					FocusNode: focusNode,
					Path:      prop.Path,
					Value:     val,
					Message:   fmt.Sprintf("Value does not have datatype %s", prop.Datatype),
					Severity:  prop.Severity,
					Source:    prop.ID,
				})
			}
		}

		// Numeric constraints
		if prop.MinInclusive != nil || prop.MaxInclusive != nil {
			num, err := strconv.ParseFloat(val.Value, 64)
			if err != nil {
				results = append(results, SHACLValidationResult{
					FocusNode: focusNode,
					Path:      prop.Path,
					Value:     val,
					Message:   "Value is not a number",
					Severity:  prop.Severity,
					Source:    prop.ID,
				})
			} else {
				if prop.MinInclusive != nil && num < *prop.MinInclusive {
					results = append(results, SHACLValidationResult{
						FocusNode: focusNode,
						Path:      prop.Path,
						Value:     val,
						Message:   fmt.Sprintf("Value %f is less than %f", num, *prop.MinInclusive),
						Severity:  prop.Severity,
						Source:    prop.ID,
					})
				}
				if prop.MaxInclusive != nil && num > *prop.MaxInclusive {
					results = append(results, SHACLValidationResult{
						FocusNode: focusNode,
						Path:      prop.Path,
						Value:     val,
						Message:   fmt.Sprintf("Value %f is greater than %f", num, *prop.MaxInclusive),
						Severity:  prop.Severity,
						Source:    prop.ID,
					})
				}
			}
		}

		// Pattern constraint
		if prop.Pattern != nil {
			if !prop.Pattern.MatchString(val.Value) {
				results = append(results, SHACLValidationResult{
					FocusNode: focusNode,
					Path:      prop.Path,
					Value:     val,
					Message:   fmt.Sprintf("Value does not match pattern %s", prop.Pattern.String()),
					Severity:  prop.Severity,
					Source:    prop.ID,
				})
			}
		}
	}

	return results, nil
}
