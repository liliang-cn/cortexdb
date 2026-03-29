package cortexdb

// RetrievalPlan is the structured search plan that an external LLM can produce
// before calling CortexDB search APIs or MCP tools.
type RetrievalPlan struct {
	Query            string            `json:"query,omitempty"`
	Keywords         []string          `json:"keywords,omitempty"`
	AlternateQueries []string          `json:"alternate_queries,omitempty"`
	EntityNames      []string          `json:"entity_names,omitempty"`
	RetrievalMode    string            `json:"retrieval_mode,omitempty"`
	Filters          *RetrievalFilters `json:"filters,omitempty"`
}

// RetrievalFilters captures optional structured constraints for search.
type RetrievalFilters struct {
	Collection  string   `json:"collection,omitempty"`
	DocumentIDs []string `json:"document_ids,omitempty"`
	UserID      string   `json:"user_id,omitempty"`
	SessionID   string   `json:"session_id,omitempty"`
	Scope       string   `json:"scope,omitempty"`
	Namespace   string   `json:"namespace,omitempty"`
}

// RetrievalDecision explains how CortexDB interpreted the caller's requested mode.
type RetrievalDecision struct {
	RequestedMode string `json:"requested_mode"`
	EffectiveMode string `json:"effective_mode"`
	UseGraph      bool   `json:"use_graph"`
	Reason        string `json:"reason,omitempty"`
}

type retrievalPlanInput struct {
	Query                    string
	Plan                     *RetrievalPlan
	Keywords                 []string
	AlternateQueries         []string
	EntityNames              []string
	RetrievalMode            string
	DisableGraph             bool
	Filters                  *RetrievalFilters
	SupportsGraph            bool
	EmptyQueryUsesGraph      bool
	UnsupportedEffectiveMode string
	UnsupportedReason        string
}

type retrievalPlanResolution struct {
	Plan     RetrievalPlan
	Decision RetrievalDecision
}
