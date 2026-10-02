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
	// Group is the coarser unit a document belongs to, when a benchmark scores
	// at two granularities: a LoCoMo dialog turn belongs to its session.
	Group string `json:"group,omitempty"`
}

// Query is one labeled query: its text plus the ids of relevant documents.
type Query struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	Relevant []string `json:"relevant"`
	// Category is the benchmark's own label for the kind of question
	// (LongMemEval's question_type, MuSiQue's hop count), kept so a score can
	// be broken down the way the benchmark's authors break it down.
	Category string `json:"category,omitempty"`
}

// Negative is a query the corpus holds nothing about. A retriever that returns
// anything for it is inventing an answer; Run does not score these, and a test
// asserts they come back empty.
type Negative struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Dataset is a corpus plus a labeled query set.
type Dataset struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Documents   []Document `json:"documents"`
	Queries     []Query    `json:"queries"`
	Negatives   []Negative `json:"negatives,omitempty"`
}

//go:embed testdata/dataset.json
var embeddedDataset []byte

//go:embed testdata/dataset_zh.json
var embeddedChineseDataset []byte

// Builtin returns the bundled retrieval-quality dataset.
func Builtin() (*Dataset, error) {
	return Parse(embeddedDataset)
}

// BuiltinChinese returns the bundled Chinese dataset: sentence queries written
// in different words from the notes that answer them, and negatives that share
// one word with a note at most.
func BuiltinChinese() (*Dataset, error) {
	return Parse(embeddedChineseDataset)
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
