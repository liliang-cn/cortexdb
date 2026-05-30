package graphflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Toolbox exposes graphflow as a tool-call surface.
type Toolbox struct {
	db        *cortexdb.DB
	detector  Detector
	extractor Extractor
}

// NewToolbox constructs a graphflow tool facade.
func NewToolbox(db *cortexdb.DB, detector Detector, extractor Extractor) (*Toolbox, error) {
	if db == nil {
		return nil, fmt.Errorf("db is required")
	}
	return &Toolbox{db: db, detector: detector, extractor: extractor}, nil
}

// Definitions returns JSON-schema-like graphflow tool definitions.
func (t *Toolbox) Definitions() []cortexdb.ToolDefinition {
	return []cortexdb.ToolDefinition{
		{
			Name:        "graphflow_build",
			Description: "Persist extraction results into the graph store.",
			InputSchema: gfObjectSchema(
				[]string{"extractions"},
				map[string]any{
					"extractions": gfExtractionResultArraySchema(),
					"build":       gfBuildOptionsSchema(),
				},
			),
		},
		{
			Name:        "graphflow_analyze",
			Description: "Analyze the persisted graphflow graph and return a deterministic summary.",
			InputSchema: gfObjectSchema(
				nil,
				map[string]any{
					"top_n": gfIntegerSchema("Optional top-node count."),
				},
			),
		},
		{
			Name:        "graphflow_report",
			Description: "Render a markdown report from a graphflow analysis report.",
			InputSchema: gfObjectSchema(
				[]string{"analysis"},
				map[string]any{
					"analysis": gfAnalysisSchema(),
				},
			),
		},
		{
			Name:        "graphflow_export",
			Description: "Export graphflow outputs to disk.",
			InputSchema: gfObjectSchema(
				[]string{"output_dir"},
				map[string]any{
					"output_dir": gfStringSchema("Output directory."),
					"analysis":   gfAnalysisSchema(),
					"report":     gfStringSchema("Optional pre-rendered report."),
				},
			),
		},
		{
			Name:        "graphflow_run",
			Description: "Run detect, extract, build, analyze, report, and export in one call.",
			InputSchema: gfObjectSchema(
				[]string{"root", "output_dir"},
				map[string]any{
					"root":       gfStringSchema("Corpus root directory."),
					"output_dir": gfStringSchema("Output directory."),
					"build":      gfBuildOptionsSchema(),
					"analyze":    gfObjectSchema(nil, map[string]any{"top_n": gfIntegerSchema("Optional top-node count.")}),
				},
			),
		},
	}
}

// Call dispatches a graphflow tool call.
func (t *Toolbox) Call(ctx context.Context, name string, input json.RawMessage) (any, error) {
	switch name {
	case "graphflow_build":
		var req struct {
			Extractions []ExtractionResult `json:"extractions"`
			Build       BuildOptions       `json:"build"`
		}
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return Build(ctx, t.db, req.Extractions, req.Build)
	case "graphflow_analyze":
		var req AnalyzeRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return Analyze(ctx, t.db, req)
	case "graphflow_report":
		var req struct {
			Analysis AnalysisReport `json:"analysis"`
		}
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return RenderReport(ctx, &req.Analysis)
	case "graphflow_export":
		var req ExportRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		return Export(ctx, t.db, req)
	case "graphflow_run":
		if t.detector == nil {
			return nil, fmt.Errorf("graphflow detector is required for graphflow_run")
		}
		if t.extractor == nil {
			return nil, fmt.Errorf("graphflow extractor is required for graphflow_run")
		}
		var req RunRequest
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		pipeline, err := NewPipeline(t.detector, t.extractor)
		if err != nil {
			return nil, err
		}
		return pipeline.Run(ctx, t.db, req)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func gfObjectSchema(required []string, properties map[string]any) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	// Omit "required" when empty; a nil slice serializes as "required": null,
	// which strict function-calling validators reject.
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func gfStringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func gfIntegerSchema(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func gfBooleanSchema(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func gfNumberSchema(description string) map[string]any {
	return map[string]any{"type": "number", "description": description}
}

func gfStringArraySchema(description string) map[string]any {
	return map[string]any{"type": "array", "description": description, "items": map[string]any{"type": "string"}}
}

func gfMapSchema(description string) map[string]any {
	return map[string]any{"type": "object", "description": description, "additionalProperties": true}
}

func gfExtractionNodeSchema() map[string]any {
	return gfObjectSchema(
		[]string{"id", "label"},
		map[string]any{
			"id":              gfStringSchema("Node ID."),
			"label":           gfStringSchema("Human-readable label."),
			"type":            gfStringSchema("Optional node type."),
			"summary":         gfStringSchema("Optional summary."),
			"source_file":     gfStringSchema("Optional source file."),
			"source_location": gfStringSchema("Optional source location."),
			"metadata":        gfMapSchema("Optional node metadata."),
		},
	)
}

func gfExtractionEdgeSchema() map[string]any {
	return gfObjectSchema(
		[]string{"source", "target", "relation", "confidence"},
		map[string]any{
			"source":          gfStringSchema("Source node ID."),
			"target":          gfStringSchema("Target node ID."),
			"relation":        gfStringSchema("Relation label."),
			"confidence":      map[string]any{"type": "string", "enum": []any{string(ConfidenceExtracted), string(ConfidenceInferred), string(ConfidenceAmbiguous)}},
			"directed":        gfBooleanSchema("Whether the edge is directed."),
			"source_file":     gfStringSchema("Optional source file."),
			"source_location": gfStringSchema("Optional source location."),
			"metadata":        gfMapSchema("Optional edge metadata."),
		},
	)
}

func gfExtractionResultSchema() map[string]any {
	return gfObjectSchema(
		[]string{"source_id", "nodes", "edges"},
		map[string]any{
			"source_id":   gfStringSchema("Source document ID."),
			"source_type": gfStringSchema("Source type."),
			"title":       gfStringSchema("Optional title."),
			"nodes":       map[string]any{"type": "array", "items": gfExtractionNodeSchema()},
			"edges":       map[string]any{"type": "array", "items": gfExtractionEdgeSchema()},
			"metadata":    gfMapSchema("Optional extraction metadata."),
		},
	)
}

func gfExtractionResultArraySchema() map[string]any {
	return map[string]any{"type": "array", "items": gfExtractionResultSchema()}
}

func gfBuildOptionsSchema() map[string]any {
	return gfObjectSchema(nil, map[string]any{
		"collection":    gfStringSchema("Optional collection name."),
		"replace_edges": gfBooleanSchema("Optional replace-edges behavior."),
	})
}

func gfAnalysisSchema() map[string]any {
	return gfObjectSchema(nil, map[string]any{
		"node_count":          gfIntegerSchema("Node count."),
		"edge_count":          gfIntegerSchema("Edge count."),
		"node_types":          gfMapSchema("Node type counts."),
		"relation_types":      gfMapSchema("Relation type counts."),
		"confidence":          gfMapSchema("Confidence counts."),
		"suggested_questions": gfStringArraySchema("Suggested questions."),
		"top_nodes": map[string]any{
			"type": "array",
			"items": gfObjectSchema(nil, map[string]any{
				"id":    gfStringSchema("Node ID."),
				"label": gfStringSchema("Node label."),
				"type":  gfStringSchema("Node type."),
				"score": gfNumberSchema("Node score."),
			}),
		},
	})
}
