package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
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

// ActionListRequest lists the action types on the active ontology.
type ActionListRequest struct{}

// ActionListResponse returns the callable action types. The full definitions
// are returned, not just their names: an agent reads this to work out how to
// call an action, so the parameters and criteria are the useful part.
type ActionListResponse struct {
	Actions []OntologyActionType `json:"actions"`
}

func (db *DB) ListActionTypes(ctx context.Context, _ ActionListRequest) (*ActionListResponse, error) {
	schema, err := db.loadActiveOntologySchema(ctx)
	if err != nil {
		return nil, err
	}
	if schema == nil {
		return &ActionListResponse{Actions: []OntologyActionType{}}, nil
	}
	return &ActionListResponse{Actions: schema.ActionTypes}, nil
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

// actionScope is everything a value source can read at apply time: the
// declared parameters, the values supplied for them, and the two ambient
// values (who is running this, and when).
type actionScope struct {
	compiled   *compiledOntology
	action     OntologyActionType
	parameters map[string]OntologyActionParameter
	values     map[string]string
	actor      string
	// timestamp is taken once for the whole action, so two current_time
	// sources in one action cannot disagree by a microsecond.
	timestamp string
}

func newActionScope(compiled *compiledOntology, action OntologyActionType, req ActionApplyRequest) *actionScope {
	scope := &actionScope{
		compiled:   compiled,
		action:     action,
		parameters: make(map[string]OntologyActionParameter, len(action.Parameters)),
		values:     make(map[string]string, len(req.Parameters)),
		actor:      req.Actor,
		timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
	for _, parameter := range action.Parameters {
		scope.parameters[ontologyAPIKey(parameter.APIName)] = parameter
	}
	for key, value := range req.Parameters {
		scope.values[ontologyAPIKey(key)] = value
	}
	return scope
}

func (s *actionScope) value(name string) string {
	return s.values[ontologyAPIKey(name)]
}

// applyActionRules executes an action's rules in order. Objects created by
// this action are tracked so that a later modify or delete rule targeting
// them can be refused, matching Foundry's restriction.
func (db *DB) applyActionRules(ctx context.Context, compiled *compiledOntology, action OntologyActionType, req ActionApplyRequest) ([]ActionEdit, error) {
	// The rules below write through the same tools strict_actions closes; the
	// marker is what tells the gate these are the governed writes it exists to
	// permit rather than the free-form ones it exists to refuse.
	ctx = withinActionApply(ctx)
	if err := db.graph.InitGraphSchema(ctx); err != nil {
		return nil, fmt.Errorf("init graph schema: %w", err)
	}

	scope := newActionScope(compiled, action, req)
	createdThisAction := make(map[string]struct{})
	edits := make([]ActionEdit, 0, len(action.Rules))

	for _, rule := range action.Rules {
		switch rule.Kind {
		case ActionRuleCreateObject, ActionRuleCreateOrModifyObject:
			edit, err := db.applyCreateObjectRule(ctx, scope, rule)
			if err != nil {
				return nil, err
			}
			createdThisAction[edit.ObjectID] = struct{}{}
			edits = append(edits, edit)

		case ActionRuleModifyObject, ActionRuleDeleteObject:
			objectID, err := db.actionTargetNodeID(scope, rule)
			if err != nil {
				return nil, err
			}
			// Foundry refuses to reference an object created in the same
			// action: the edit would be made against something this action
			// has not finished committing.
			if _, created := createdThisAction[objectID]; created {
				return nil, fmt.Errorf("%w: action %q %ss an object created in the same action, which is not allowed",
					ErrInvalidOntology, action.APIName, strings.TrimSuffix(string(rule.Kind), "_object"))
			}
			edit, err := db.applyObjectTargetRule(ctx, scope, rule, objectID)
			if err != nil {
				return nil, err
			}
			edits = append(edits, edit)

		case ActionRuleCreateLink, ActionRuleDeleteLink:
			edit, err := db.applyLinkRule(ctx, scope, rule)
			if err != nil {
				return nil, err
			}
			edits = append(edits, edit)

		default:
			return nil, fmt.Errorf("%w: action %q has unsupported rule kind %q", ErrInvalidOntology, action.APIName, rule.Kind)
		}
	}
	return edits, nil
}

func (db *DB) applyCreateObjectRule(ctx context.Context, scope *actionScope, rule OntologyActionRule) (ActionEdit, error) {
	objectType, ok := scope.compiled.objectType(rule.ObjectType)
	if !ok {
		return ActionEdit{}, fmt.Errorf("%w: ontology does not define object type %q", ErrInvalidOntology, rule.ObjectType)
	}

	properties := make(map[string]string, len(rule.PropertyValues))
	// Sorted so a rule with two broken sources always reports the same one.
	for _, propertyName := range sortedMapKeys(rule.PropertyValues) {
		property, ok := scope.compiled.property(objectType.APIName, propertyName)
		if !ok {
			return ActionEdit{}, fmt.Errorf("%w: object type %q has no property %q", ErrInvalidOntology, objectType.APIName, propertyName)
		}
		value, err := db.resolveActionValue(ctx, scope, rule.PropertyValues[propertyName])
		if err != nil {
			return ActionEdit{}, err
		}
		// An optional property whose source resolved to nothing is left out
		// rather than written as "": the ontology's own type check rejects an
		// empty value, so writing one would fail every optional property that
		// was simply not supplied.
		if strings.TrimSpace(value) == "" {
			continue
		}
		// Stored under the declared spelling, so an object written by an
		// action and one written by the generic path key their properties the
		// same way.
		properties[property.APIName] = value
	}

	entity := ToolEntityInput{Type: objectType.APIName, Metadata: properties}
	entity.Name = properties[objectType.TitleProperty]
	if entity.Name == "" {
		entity.Name = properties[objectType.PrimaryKey]
	}

	nodeID, err := ontologyEntityNodeID(scope.compiled, entity)
	if err != nil {
		return ActionEdit{}, err
	}
	// create_object is not an upsert. Foundry fails an action that would
	// create an object that already exists, which is what makes
	// create_or_modify_object a distinct rule kind rather than a synonym.
	// The check is here rather than in validation because validation is
	// documented as never consulting the graph.
	if rule.Kind == ActionRuleCreateObject {
		exists, err := db.ontologyNodeExists(ctx, nodeID)
		if err != nil {
			return ActionEdit{}, err
		}
		if exists {
			return ActionEdit{}, fmt.Errorf("%w: action %q creates %s %q, which already exists; use create_or_modify_object to update it",
				ErrInvalidOntology, scope.action.APIName, objectType.APIName, properties[objectType.PrimaryKey])
		}
	}

	if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{entity},
	}); err != nil {
		return ActionEdit{}, fmt.Errorf("action create object: %w", err)
	}
	return ActionEdit{Kind: string(rule.Kind), ObjectID: nodeID, ObjectType: objectType.APIName}, nil
}

// applyObjectTargetRule modifies or deletes the object a rule targets.
func (db *DB) applyObjectTargetRule(ctx context.Context, scope *actionScope, rule OntologyActionRule, objectID string) (ActionEdit, error) {
	if rule.Kind == ActionRuleDeleteObject {
		if err := db.graph.DeleteNode(ctx, objectID); err != nil {
			return ActionEdit{}, fmt.Errorf("%w: action %q delete object: %w", ErrInvalidOntology, scope.action.APIName, err)
		}
		return ActionEdit{Kind: string(rule.Kind), ObjectID: objectID, ObjectType: rule.ObjectType}, nil
	}

	node, err := db.graph.GetNode(ctx, objectID)
	if err != nil {
		return ActionEdit{}, fmt.Errorf("%w: action %q modify object %s: %w", ErrInvalidOntology, scope.action.APIName, objectID, err)
	}
	if node.Properties == nil {
		node.Properties = map[string]interface{}{}
	}
	for _, propertyName := range sortedMapKeys(rule.PropertyValues) {
		property, ok := scope.compiled.property(rule.ObjectType, propertyName)
		if !ok {
			return ActionEdit{}, fmt.Errorf("%w: object type %q has no property %q", ErrInvalidOntology, rule.ObjectType, propertyName)
		}
		value, err := db.resolveActionValue(ctx, scope, rule.PropertyValues[propertyName])
		if err != nil {
			return ActionEdit{}, err
		}
		if err := parseOntologyPropertyValue(property.DataType, value); err != nil {
			return ActionEdit{}, fmt.Errorf("%w: property %q: %w", ErrInvalidOntology, property.APIName, err)
		}
		node.Properties[property.APIName] = value
	}
	if err := db.graph.UpsertNode(ctx, node); err != nil {
		return ActionEdit{}, fmt.Errorf("action modify object: %w", err)
	}
	return ActionEdit{Kind: string(rule.Kind), ObjectID: objectID, ObjectType: rule.ObjectType}, nil
}

func (db *DB) applyLinkRule(ctx context.Context, scope *actionScope, rule OntologyActionRule) (ActionEdit, error) {
	linkType, ok := scope.compiled.linkType(rule.LinkType)
	if !ok {
		return ActionEdit{}, fmt.Errorf("%w: ontology does not define link type %q", ErrInvalidOntology, rule.LinkType)
	}
	fromID, err := db.resolveActionEndpoint(ctx, scope, rule.From)
	if err != nil {
		return ActionEdit{}, err
	}
	toID, err := db.resolveActionEndpoint(ctx, scope, rule.To)
	if err != nil {
		return ActionEdit{}, err
	}

	if rule.Kind == ActionRuleCreateLink {
		if _, err := db.GraphRAGTools().UpsertRelations(ctx, ToolUpsertRelationsRequest{
			Relations: []ToolRelationInput{{From: fromID, To: toID, Type: linkType.APIName}},
		}); err != nil {
			return ActionEdit{}, fmt.Errorf("action create link: %w", err)
		}
	} else {
		// A link type is bidirectional, so which endpoint an edge happens to
		// be stored from is an implementation detail of whoever wrote it;
		// unlinking has to find it either way round. The edge type is matched
		// case-insensitively for the same reason it is everywhere else — api
		// names resolve that way, so an exact match would leave a differently
		// spelled edge behind.
		if _, err := db.exec(ctx, `
			DELETE FROM graph_edges
			WHERE LOWER(edge_type) = LOWER(?)
			  AND ((from_node_id = ? AND to_node_id = ?) OR (from_node_id = ? AND to_node_id = ?))
		`, linkType.APIName, fromID, toID, toID, fromID); err != nil {
			return ActionEdit{}, fmt.Errorf("action delete link: %w", err)
		}
	}
	return ActionEdit{Kind: string(rule.Kind), LinkType: linkType.APIName, FromID: fromID, ToID: toID}, nil
}

// actionTargetNodeID resolves the object a modify or delete rule points at.
// The target parameter may carry the node ID, which is what an object set or
// an earlier action hands back, or the object's primary key, which is what a
// caller holding a business key has.
func (db *DB) actionTargetNodeID(scope *actionScope, rule OntologyActionRule) (string, error) {
	value := strings.TrimSpace(scope.value(rule.Target))
	if value == "" {
		return "", fmt.Errorf("%w: action %q rule %q has no value for target parameter %q",
			ErrInvalidOntology, scope.action.APIName, rule.Kind, rule.Target)
	}
	return ontologyReferenceNodeID(rule.ObjectType, value), nil
}

// ontologyReferenceNodeID turns an object reference into a node ID. A value
// that already is one is passed through: node IDs are derived from the primary
// key, so re-deriving one would mangle it.
func ontologyReferenceNodeID(objectTypeAPIName string, value string) string {
	if strings.HasPrefix(value, "entity:") {
		return value
	}
	return ontologyNodeID(objectTypeAPIName, value)
}

func (db *DB) ontologyNodeExists(ctx context.Context, nodeID string) (bool, error) {
	// COUNT rather than EXISTS. The two databases disagree about what EXISTS
	// returns — SQLite gives 0 or 1, PostgreSQL gives a boolean — so scanning
	// it into an int failed on PostgreSQL with a driver-level type error, and
	// took every ontology action down with it. COUNT(*) is an integer on both,
	// and over a primary key it costs the same lookup.
	var exists int
	err := db.queryRow(ctx,
		`SELECT COUNT(*) FROM graph_nodes WHERE id = ?`, nodeID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check object exists: %w", err)
	}
	return exists != 0, nil
}

// resolveActionEndpoint resolves a link rule endpoint to a node ID. An
// endpoint sourced from an object reference parameter may be given as a
// primary key, the same as a modify target.
func (db *DB) resolveActionEndpoint(ctx context.Context, scope *actionScope, source OntologyValueSource) (string, error) {
	value, err := db.resolveActionValue(ctx, scope, source)
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if source.Kind == ValueSourceParameter {
		if parameter, ok := scope.parameters[ontologyAPIKey(source.Parameter)]; ok && parameter.ObjectType != "" {
			return ontologyReferenceNodeID(parameter.ObjectType, value), nil
		}
	}
	return value, nil
}

func (db *DB) resolveActionValue(ctx context.Context, scope *actionScope, source OntologyValueSource) (string, error) {
	switch source.Kind {
	case ValueSourceParameter:
		return scope.value(source.Parameter), nil
	case ValueSourceStatic:
		return source.Static, nil
	case ValueSourceCurrentUser:
		return scope.actor, nil
	case ValueSourceCurrentTime:
		return scope.timestamp, nil
	case ValueSourceObjectProperty:
		return db.resolveActionObjectProperty(ctx, scope, source)
	default:
		return "", fmt.Errorf("%w: unknown value source kind %q", ErrInvalidOntology, source.Kind)
	}
}

// resolveActionObjectProperty reads a property off the object a reference
// parameter names. Schema validation has already established that the
// parameter is an object reference and that the object type declares the
// property, so what is left is the graph read.
func (db *DB) resolveActionObjectProperty(ctx context.Context, scope *actionScope, source OntologyValueSource) (string, error) {
	parameter, ok := scope.parameters[ontologyAPIKey(source.Parameter)]
	if !ok || parameter.ObjectType == "" {
		return "", fmt.Errorf("%w: action %q reads a property off %q, which is not an object reference parameter",
			ErrInvalidOntology, scope.action.APIName, source.Parameter)
	}
	value := strings.TrimSpace(scope.value(source.Parameter))
	if value == "" {
		return "", nil
	}

	node, err := db.graph.GetNode(ctx, ontologyReferenceNodeID(parameter.ObjectType, value))
	if err != nil {
		return "", fmt.Errorf("%w: action %q reads %s off %q: %w",
			ErrInvalidOntology, scope.action.APIName, source.Property, source.Parameter, err)
	}
	// Matched case-insensitively: properties written through the generic path
	// carry whatever spelling the caller sent.
	wanted := ontologyAPIKey(source.Property)
	for name, raw := range node.Properties {
		if ontologyAPIKey(name) == wanted {
			return fmt.Sprintf("%v", raw), nil
		}
	}
	// An absent property reads as absent, not as an error: the source is used
	// to copy a value across, and a copy of nothing is nothing.
	return "", nil
}

func (db *DB) ensureActionAuditTable(ctx context.Context) error {
	db.actionAuditMu.Lock()
	defer db.actionAuditMu.Unlock()
	if db.actionAuditReady {
		return nil
	}

	// Two statements, run separately. Whether a driver accepts several in one
	// Exec depends on which protocol it picks, and an audit table that exists
	// on one backend and not the other is the kind of difference that is only
	// noticed when someone asks who changed something.
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS ontology_action_audit (
			id ` + db.Dialect().AutoIncrementPK() + `,
			action_api_name TEXT NOT NULL,
			actor TEXT,
			parameters TEXT NOT NULL,
			edits TEXT NOT NULL,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_action_audit_action ON ontology_action_audit(action_api_name)`,
	}
	for _, stmt := range stmts {
		if _, err := db.exec(ctx, stmt); err != nil {
			// Deliberately not latched, for the same reason as the ontology
			// schema table: one cancelled context must not disable the audit
			// trail for the rest of this DB's lifetime.
			return err
		}
	}
	db.actionAuditReady = true
	return nil
}

// actionApplyContextKey marks a context as belonging to an action's own
// execution.
type actionApplyContextKey struct{}

// withinActionApply marks a context as an action's own write path. The
// strict_actions gate sits on the generic upsert tools, and an action's rules
// go through those same tools — without the marker the gate would refuse the
// very writes it exists to permit. It rides on the context rather than on the
// DB so it scopes to one action's execution, and not to every other caller
// sharing the store while that action runs.
func withinActionApply(ctx context.Context) context.Context {
	return context.WithValue(ctx, actionApplyContextKey{}, true)
}

func isWithinActionApply(ctx context.Context) bool {
	value, _ := ctx.Value(actionApplyContextKey{}).(bool)
	return value
}

// guardStrictActions closes the generic upsert path when the active schema
// asks for it. Off by default, so existing callers keep working unchanged, and
// inert until the schema declares an action — closing the only way in without
// opening another would just make the store unwritable.
func (db *DB) guardStrictActions(ctx context.Context) error {
	if isWithinActionApply(ctx) {
		return nil
	}
	schema, err := db.loadActiveOntologySchema(ctx)
	if err != nil || schema == nil {
		return err
	}
	if !schema.StrictActions || len(schema.ActionTypes) == 0 {
		return nil
	}
	return fmt.Errorf("%w: ontology %q sets strict_actions: write through an action type (see ontology_action_list), not the generic upsert tools",
		ErrInvalidOntology, schema.SchemaID)
}

func (t *GraphRAGToolbox) ApplyAction(ctx context.Context, req ActionApplyRequest) (*ActionApplyResponse, error) {
	return t.db.ApplyAction(ctx, req)
}

func (t *GraphRAGToolbox) ListActionTypes(ctx context.Context, req ActionListRequest) (*ActionListResponse, error) {
	return t.db.ListActionTypes(ctx, req)
}

// recordActionAudit writes what was changed, by whom, with what inputs.
// This is the point of routing writes through actions rather than through a
// generic upsert: the trail exists whether or not anyone asked for it, so the
// edits are recorded even when return_edits withheld them from the caller.
func (db *DB) recordActionAudit(ctx context.Context, action OntologyActionType, req ActionApplyRequest, edits []ActionEdit) error {
	if err := db.ensureActionAuditTable(ctx); err != nil {
		return fmt.Errorf("init action audit table: %w", err)
	}
	parametersJSON, err := json.Marshal(req.Parameters)
	if err != nil {
		return fmt.Errorf("encode action parameters: %w", err)
	}
	editsJSON, err := json.Marshal(edits)
	if err != nil {
		return fmt.Errorf("encode action edits: %w", err)
	}
	if _, err := db.exec(ctx,
		`INSERT INTO ontology_action_audit (action_api_name, actor, parameters, edits) VALUES (?, ?, ?, ?)`,
		action.APIName, req.Actor, string(parametersJSON), string(editsJSON)); err != nil {
		return fmt.Errorf("record action audit: %w", err)
	}
	return nil
}
