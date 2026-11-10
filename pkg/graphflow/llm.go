package graphflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// JSONGenerator is the minimal interface required for an LLM-backed extractor.
type JSONGenerator interface {
	GenerateJSON(ctx context.Context, systemPrompt string, userPrompt string) ([]byte, error)
}

// LLMExtractor delegates extraction to a model that returns JSON matching the extraction schema.
type LLMExtractor struct {
	Client   JSONGenerator
	MaxChars int
	// Vocabulary is the active ontology's object types. When set, the model
	// is told to type each node with one of them or leave the type empty,
	// and a node type the schema does not declare is dropped. Pipeline.Run
	// fills it from the DB it builds into when the caller has not.
	Vocabulary cortexdb.ExtractorTypeVocabulary
}

// VocabularyExtractor is an Extractor that can be told the object types the
// active ontology declares, so its node types are the schema's spellings
// rather than ones it invents per document.
type VocabularyExtractor interface {
	Extractor
	WithVocabulary(vocab cortexdb.ExtractorTypeVocabulary) Extractor
}

// WithVocabulary returns the extractor with vocab, unless the caller already
// gave it one.
func (e LLMExtractor) WithVocabulary(vocab cortexdb.ExtractorTypeVocabulary) Extractor {
	if e.Vocabulary.Empty() {
		e.Vocabulary = vocab
	}
	return e
}

type llmExtractionPayload struct {
	Nodes []ExtractionNode `json:"nodes"`
	Edges []ExtractionEdge `json:"edges"`
}

// Extract calls the configured JSON generator and normalizes the returned payload.
func (e LLMExtractor) Extract(ctx context.Context, doc SourceDocument) (*ExtractionResult, error) {
	if e.Client == nil {
		return nil, fmt.Errorf("llm client is required")
	}
	if e.MaxChars <= 0 {
		e.MaxChars = 12000
	}
	content := doc.Content
	if len(content) > e.MaxChars {
		content = content[:e.MaxChars]
	}
	payload, err := e.Client.GenerateJSON(ctx, graphflowSystemPrompt, buildExtractionPrompt(doc, content, e.Vocabulary))
	if err != nil {
		return nil, err
	}
	var decoded llmExtractionPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("decode llm extraction payload: %w", err)
	}
	if !e.Vocabulary.Empty() {
		for i := range decoded.Nodes {
			// An undeclared type is left empty, which Build stores as the
			// generic graphflow_node rather than as a new spelling.
			apiName, _ := e.Vocabulary.Canonical(decoded.Nodes[i].Type)
			decoded.Nodes[i].Type = apiName
		}
	}
	result := &ExtractionResult{
		SourceID:   doc.ID,
		SourceType: doc.Type,
		Title:      doc.Title,
		Nodes:      decoded.Nodes,
		Edges:      decoded.Edges,
		Metadata:   cloneStringMap(doc.Metadata),
	}
	if err := ValidateExtraction(result); err != nil {
		return nil, err
	}
	return result, nil
}

const graphflowSystemPrompt = "Extract a graph from the provided document. Return JSON only with keys: nodes, edges."

// The extraction prompt is split at the reply shape so that a vocabulary can
// be inserted ahead of it while the prompt without one stays byte for byte
// what it always was (#153).
const (
	graphflowDocumentPrompt = "source_id: %s\nsource_type: %s\ntitle: %s\npath: %s\n\nDocument:\n%s"
	graphflowReplyPrompt    = "\n\nReturn JSON with:\n{\n  \"nodes\": [{\"id\":\"...\",\"label\":\"...\",\"type\":\"...\",\"summary\":\"...\",\"source_file\":\"...\",\"source_location\":\"...\"}],\n  \"edges\": [{\"source\":\"...\",\"target\":\"...\",\"relation\":\"...\",\"confidence\":\"EXTRACTED|INFERRED|AMBIGUOUS\",\"directed\":true,\"source_file\":\"...\",\"source_location\":\"...\"}]\n}"
)

func buildExtractionPrompt(doc SourceDocument, content string, vocab cortexdb.ExtractorTypeVocabulary) string {
	prompt := fmt.Sprintf(graphflowDocumentPrompt, doc.ID, doc.Type, doc.Title, doc.Path, content)
	if !vocab.Empty() {
		prompt += "\n\nNode types: a node's type is the api name of the OBJECT TYPE below that it is an instance of, written exactly as listed. When no listed type fits, leave type empty (\"\"). Never invent a type.\nOBJECT TYPES (the active ontology):\n" +
			vocab.PromptList()
	}
	return prompt + graphflowReplyPrompt
}
