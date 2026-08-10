package cortexdb

// OntologyActionType is the schema definition of a governed set of graph
// edits. The rule engine lands in phase 5.
type OntologyActionType struct {
	APIName     string `json:"api_name"`
	Description string `json:"description,omitempty"`
}
