package graphflow

import (
	"context"
	"encoding/json"
	"fmt"
)

// JSONGenerator is the minimal interface required for an LLM-backed extractor.
type JSONGenerator interface {
	GenerateJSON(ctx context.Context, systemPrompt string, userPrompt string) ([]byte, error)
}

// LLMExtractor delegates extraction to a model that returns JSON matching the extraction schema.
type LLMExtractor struct {
	Client   JSONGenerator
	MaxChars int
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
	payload, err := e.Client.GenerateJSON(ctx, graphflowSystemPrompt, buildExtractionPrompt(doc, content))
	if err != nil {
		return nil, err
	}
	var decoded llmExtractionPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("decode llm extraction payload: %w", err)
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

func buildExtractionPrompt(doc SourceDocument, content string) string {
	return fmt.Sprintf(
		"source_id: %s\nsource_type: %s\ntitle: %s\npath: %s\n\nDocument:\n%s\n\nReturn JSON with:\n{\n  \"nodes\": [{\"id\":\"...\",\"label\":\"...\",\"type\":\"...\",\"summary\":\"...\",\"source_file\":\"...\",\"source_location\":\"...\"}],\n  \"edges\": [{\"source\":\"...\",\"target\":\"...\",\"relation\":\"...\",\"confidence\":\"EXTRACTED|INFERRED|AMBIGUOUS\",\"directed\":true,\"source_file\":\"...\",\"source_location\":\"...\"}]\n}",
		doc.ID,
		doc.Type,
		doc.Title,
		doc.Path,
		content,
	)
}
