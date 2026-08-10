package cortexdb

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// ActionApplyRequest runs one action type.
type ActionApplyRequest struct {
	Action     string            `json:"action"`
	Parameters map[string]string `json:"parameters,omitempty"`
	// ValidateOnly checks parameters and submission criteria without
	// writing. Mutually exclusive with ReturnEdits, matching OSDK.
	ValidateOnly bool `json:"validate_only,omitempty"`
	// ReturnEdits includes the graph edits the action made.
	ReturnEdits bool `json:"return_edits,omitempty"`
	// Actor is recorded in the audit trail and resolves current_user value
	// sources.
	Actor string `json:"actor,omitempty"`
}

// ActionEdit is one graph change an action made.
type ActionEdit struct {
	Kind       string `json:"kind"`
	ObjectID   string `json:"object_id,omitempty"`
	ObjectType string `json:"object_type,omitempty"`
	LinkType   string `json:"link_type,omitempty"`
	FromID     string `json:"from_id,omitempty"`
	ToID       string `json:"to_id,omitempty"`
}

// ActionApplyResponse reports validity and, when asked, the edits applied.
type ActionApplyResponse struct {
	Action  string       `json:"action"`
	Valid   bool         `json:"valid"`
	Applied bool         `json:"applied"`
	Errors  []string     `json:"errors,omitempty"`
	Edits   []ActionEdit `json:"edits,omitempty"`
}

// ApplyAction runs one governed write.
//
// Two failure modes are deliberately different shapes. A request that does not
// name a runnable action is an error: nothing about it can be retried by
// fixing a value. A request whose parameters or submission criteria do not
// hold comes back as a normal response with Valid=false, because that is a
// verdict on the inputs, and validate-only callers ask for exactly that.
func (db *DB) ApplyAction(ctx context.Context, req ActionApplyRequest) (*ActionApplyResponse, error) {
	if req.ValidateOnly && req.ReturnEdits {
		return nil, fmt.Errorf("%w: validate_only and return_edits cannot both be true; validate-only writes nothing, so it has no edits to return",
			ErrInvalidOntology)
	}
	compiled, err := db.activeCompiledOntology(ctx)
	if err != nil {
		return nil, err
	}
	if compiled == nil {
		return nil, fmt.Errorf("%w: no active ontology defines any actions", ErrInvalidOntology)
	}

	action, ok := compiled.actionType(req.Action)
	if !ok {
		return nil, fmt.Errorf("%w: ontology defines no action type %q", ErrInvalidOntology, req.Action)
	}

	failures := validateActionParameters(action, req.Parameters)
	failures = append(failures, evaluateSubmissionCriteria(action, req.Parameters)...)

	// The declared spelling, not the caller's: action names resolve
	// case-insensitively, and the audit trail keys off one canonical name.
	response := &ActionApplyResponse{Action: action.APIName, Valid: len(failures) == 0, Errors: failures}
	if !response.Valid || req.ValidateOnly {
		return response, nil
	}

	edits, err := db.applyActionRules(ctx, compiled, action, req)
	if err != nil {
		return nil, err
	}
	response.Applied = true
	if req.ReturnEdits {
		response.Edits = edits
	}
	if err := db.recordActionAudit(ctx, action, req, edits); err != nil {
		return nil, err
	}
	return response, nil
}

// validateActionParameters checks presence, allowed values and data types.
// Like Foundry's Validate Action it never consults the graph, so it cannot
// and does not check primary key uniqueness.
func validateActionParameters(action OntologyActionType, supplied map[string]string) []string {
	normalized := make(map[string]string, len(supplied))
	for key, value := range supplied {
		normalized[ontologyAPIKey(key)] = value
	}

	failures := make([]string, 0)
	declared := make(map[string]struct{}, len(action.Parameters))
	for _, parameter := range action.Parameters {
		key := ontologyAPIKey(parameter.APIName)
		declared[key] = struct{}{}

		value, present := normalized[key]
		// A blank value is absence. Treating it as supplied would let a
		// required parameter be satisfied by sending nothing under its name.
		if !present || strings.TrimSpace(value) == "" {
			if parameter.Required {
				failures = append(failures, fmt.Sprintf("parameter %q is required", parameter.APIName))
			}
			continue
		}
		if err := parseOntologyPropertyValue(parameter.DataType, value); err != nil {
			failures = append(failures, fmt.Sprintf("parameter %q: %v", parameter.APIName, err))
			continue
		}
		if len(parameter.AllowedValues) > 0 {
			allowed := false
			for _, candidate := range parameter.AllowedValues {
				if candidate == value {
					allowed = true
					break
				}
			}
			if !allowed {
				failures = append(failures, fmt.Sprintf("parameter %q value %q is not one of the allowed values", parameter.APIName, value))
			}
		}
	}

	// Reported with the caller's spelling rather than the lookup key, so the
	// name in the failure can be found in the payload that was sent. Sorted,
	// because map order would otherwise reorder the failures between runs.
	for _, name := range sortedMapKeys(supplied) {
		if _, ok := declared[ontologyAPIKey(name)]; !ok {
			failures = append(failures, fmt.Sprintf("action does not declare parameter %q", name))
		}
	}
	return failures
}

// evaluateSubmissionCriteria applies each criterion to the parameter it names.
// A criterion stating both a regex and an operator is a conjunction: the regex
// is checked first and, when it fails, the operator is skipped so one bad
// value is not reported twice.
func evaluateSubmissionCriteria(action OntologyActionType, supplied map[string]string) []string {
	normalized := make(map[string]string, len(supplied))
	for key, value := range supplied {
		normalized[ontologyAPIKey(key)] = value
	}

	failures := make([]string, 0)
	for _, criterion := range action.SubmissionCriteria {
		value, present := normalized[ontologyAPIKey(criterion.Parameter)]

		if criterion.Regex != "" {
			// Schema validation compiles this too, so an uncompilable pattern
			// can only reach here on a schema stored before that check. It is
			// reported as a failure rather than ignored: a criterion nobody
			// can evaluate must not read as a criterion everybody passes.
			pattern, err := regexp.Compile(criterion.Regex)
			if err != nil {
				failures = append(failures, fmt.Sprintf("submission criterion on %q has an invalid regex: %v", criterion.Parameter, err))
				continue
			}
			if !pattern.MatchString(value) {
				failures = append(failures, submissionFailureMessage(criterion,
					fmt.Sprintf("parameter %q does not match %s", criterion.Parameter, criterion.Regex)))
				continue
			}
		}
		if criterion.Op == "" {
			continue
		}
		matched, err := matchScalarPredicate(ObjectSetPredicate{
			Op: criterion.Op, Property: criterion.Parameter, Value: criterion.Value, Values: criterion.Values,
		}, value, present && strings.TrimSpace(value) != "")
		if err != nil {
			failures = append(failures, fmt.Sprintf("submission criterion on %q: %v", criterion.Parameter, err))
			continue
		}
		if !matched {
			failures = append(failures, submissionFailureMessage(criterion,
				fmt.Sprintf("parameter %q failed the %s check", criterion.Parameter, criterion.Op)))
		}
	}
	return failures
}

func submissionFailureMessage(criterion OntologySubmissionCriterion, fallback string) string {
	if strings.TrimSpace(criterion.FailureMessage) != "" {
		return criterion.FailureMessage
	}
	return fallback
}

// applyActionRules and recordActionAudit land in the next commit; validation
// is complete without them, and validate-only never reaches either.
func (db *DB) applyActionRules(ctx context.Context, compiled *compiledOntology, action OntologyActionType, req ActionApplyRequest) ([]ActionEdit, error) {
	return nil, nil
}

func (db *DB) recordActionAudit(ctx context.Context, action OntologyActionType, req ActionApplyRequest, edits []ActionEdit) error {
	return nil
}
