package cortexdb

// RetrievalPlan is the structured search plan that an external LLM can produce
// before calling CortexDB search APIs or MCP tools.
type RetrievalPlan struct {
	Query            string   `json:"query,omitempty"`
	Keywords         []string `json:"keywords,omitempty"`
	AlternateQueries []string `json:"alternate_queries,omitempty"`
	EntityNames      []string `json:"entity_names,omitempty"`
	RetrievalMode    string   `json:"retrieval_mode,omitempty"`
	// Collection is shorthand for Filters.Collection.
	//
	// `collection` is a top-level parameter of the same call and also lives at
	// `plan.filters.collection`, so a model reaches for `plan.collection` — and with
	// additionalProperties:false that was a hard schema rejection, costing a whole model round trip on
	// every scoped search before it guessed the longer spelling. Accepting it is cheaper than being
	// right about it. Filters.Collection is the more specific spelling and wins.
	Collection string            `json:"collection,omitempty"`
	Filters    *RetrievalFilters `json:"filters,omitempty"`
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
	// PreferSemantic keeps auto mode on a non-lexical (vector) path when the
	// query has no strong graph/entity signal — set it when an embedder is
	// available so semantic retrieval is actually used instead of silently
	// falling back to lexical.
	PreferSemantic bool
}

type retrievalPlanResolution struct {
	Plan     RetrievalPlan
	Decision RetrievalDecision
}
