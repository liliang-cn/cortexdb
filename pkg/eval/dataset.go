package eval

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// Document is one corpus document to index.
type Document struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// Query is one labeled query: its text plus the ids of relevant documents.
type Query struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	Relevant []string `json:"relevant"`
}

// Dataset is a corpus plus a labeled query set.
type Dataset struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Documents   []Document `json:"documents"`
	Queries     []Query    `json:"queries"`
}

//go:embed testdata/dataset.json
var embeddedDataset []byte

// Builtin returns the bundled retrieval-quality dataset.
func Builtin() (*Dataset, error) {
	return Parse(embeddedDataset)
}

// Parse decodes a Dataset from JSON and validates referential integrity: every
// query's relevant ids must exist as documents.
func Parse(data []byte) (*Dataset, error) {
	var ds Dataset
	if err := json.Unmarshal(data, &ds); err != nil {
		return nil, fmt.Errorf("eval: parse dataset: %w", err)
	}
	docIDs := make(map[string]struct{}, len(ds.Documents))
	for _, d := range ds.Documents {
		if d.ID == "" {
			return nil, fmt.Errorf("eval: document with empty id")
		}
		docIDs[d.ID] = struct{}{}
	}
	for _, q := range ds.Queries {
		if len(q.Relevant) == 0 {
			return nil, fmt.Errorf("eval: query %q has no relevant documents", q.ID)
		}
		for _, id := range q.Relevant {
			if _, ok := docIDs[id]; !ok {
				return nil, fmt.Errorf("eval: query %q references unknown document %q", q.ID, id)
			}
		}
	}
	return &ds, nil
}
