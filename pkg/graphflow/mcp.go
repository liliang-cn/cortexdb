package graphflow

import (
	"context"
	"log/slog"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultMCPServerName  = "cortexdb-graphflow"
	defaultMCPServerTitle = "CortexDB Graphflow"
)

// MCPServerOptions configures the graphflow MCP server wrapper.
type MCPServerOptions struct {
	Implementation *mcp.Implementation
	Instructions   string
	Logger         *slog.Logger
}

// NewMCPServer returns an MCP server that exposes the graphflow tool surface.
func NewMCPServer(db *cortexdb.DB, detector Detector, extractor Extractor, opts MCPServerOptions) (*mcp.Server, error) {
	toolbox, err := NewToolbox(db, detector, extractor)
	if err != nil {
		return nil, err
	}

	impl := opts.Implementation
	if impl == nil {
		impl = &mcp.Implementation{
			Name:    defaultMCPServerName,
			Title:   defaultMCPServerTitle,
			Version: cortexdbroot.Version,
		}
	}

	server := mcp.NewServer(impl, &mcp.ServerOptions{
		Instructions: opts.Instructions,
		Logger:       opts.Logger,
	})

	definitions := make(map[string]cortexdb.ToolDefinition, len(toolbox.Definitions()))
	for _, definition := range toolbox.Definitions() {
		definitions[definition.Name] = definition
	}

	addGraphflowMCPTool(server, definitions["graphflow_build"], func(ctx context.Context, req struct {
		Extractions []ExtractionResult `json:"extractions"`
		Build       BuildOptions       `json:"build"`
	}) (*BuildResult, error) {
		return Build(ctx, db, req.Extractions, req.Build)
	})
	addGraphflowMCPTool(server, definitions["graphflow_analyze"], func(ctx context.Context, req AnalyzeRequest) (*AnalysisReport, error) {
		return Analyze(ctx, db, req)
	})
	addGraphflowMCPTool(server, definitions["graphflow_report"], func(ctx context.Context, req struct {
		Analysis AnalysisReport `json:"analysis"`
	}) (struct {
		Report string `json:"report"`
	}, error) {
		report, err := RenderReport(ctx, &req.Analysis)
		if err != nil {
			return struct {
				Report string `json:"report"`
			}{}, err
		}
		return struct {
			Report string `json:"report"`
		}{Report: report}, nil
	})
	addGraphflowMCPTool(server, definitions["graphflow_export"], func(ctx context.Context, req ExportRequest) (*ExportResult, error) {
		return Export(ctx, db, req)
	})
	addGraphflowMCPTool(server, definitions["graphflow_run"], func(ctx context.Context, req RunRequest) (*RunResult, error) {
		pipeline, err := NewPipeline(detector, extractor)
		if err != nil {
			return nil, err
		}
		return pipeline.Run(ctx, db, req)
	})

	return server, nil
}

func addGraphflowMCPTool[In any, Out any](server *mcp.Server, definition cortexdb.ToolDefinition, handler func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        definition.Name,
		Description: definition.Description,
		InputSchema: definition.InputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		output, err := handler(ctx, input)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return &mcp.CallToolResult{}, output, nil
	})
}
