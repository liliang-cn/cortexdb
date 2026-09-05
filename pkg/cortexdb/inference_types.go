package cortexdb

// InferenceRule defines a deterministic two-hop relation composition rule.
type InferenceRule struct {
	RuleID             string            `json:"rule_id"`
	Description        string            `json:"description,omitempty"`
	LeftRelationType   string            `json:"left_relation_type"`
	RightRelationType  string            `json:"right_relation_type"`
	ResultRelationType string            `json:"result_relation_type"`
	Weight             float64           `json:"weight,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}

// ApplyInferenceRequest executes deterministic inference rules against stored relations.
type ApplyInferenceRequest struct {
	DocumentID     string          `json:"document_id,omitempty"`
	Rules          []InferenceRule `json:"rules"`
	DeleteExisting bool            `json:"delete_existing,omitempty"`
}

// ApplyInferenceResponse summarizes inferred edge materialization.
type ApplyInferenceResponse struct {
	CreatedEdgeIDs []string `json:"created_edge_ids"`
	DeletedEdgeIDs []string `json:"deleted_edge_ids,omitempty"`
	// UnchangedEdgeIDs are edges these rules re-derived that the same rule had
	// already written. Reported rather than rewritten, so a second run of the
	// same rules is a no-op that says so instead of a run that looks empty.
	UnchangedEdgeIDs []string `json:"unchanged_edge_ids,omitempty"`
}
