package cortexdb

// OntologyNamedObjectSet is a saved, reusable object set definition.
// The ObjectSet algebra itself lands in phase 4.
type OntologyNamedObjectSet struct {
	APIName     string `json:"api_name"`
	Description string `json:"description,omitempty"`
}
