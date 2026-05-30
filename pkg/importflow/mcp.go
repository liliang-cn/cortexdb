package importflow

import (
	"context"
	"fmt"
	"log/slog"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultMCPServerName  = "cortexdb-importflow"
	defaultMCPServerTitle = "CortexDB Importflow"
)

// MCPServerOptions configures the importflow MCP server wrapper.
type MCPServerOptions struct {
	Implementation *mcp.Implementation
	Instructions   string
	Logger         *slog.Logger
}

// NewMCPServer returns an MCP server that exposes the importflow tool surface
// (importflow_plan, importflow_run) backed by the given Importer.
func NewMCPServer(im *Importer, opts MCPServerOptions) (*mcp.Server, error) {
	if im == nil {
		return nil, fmt.Errorf("importflow: importer is required")
	}
	toolbox := NewToolbox(im)

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

	addImportflowMCPTool(server, definitions["importflow_plan"], func(ctx context.Context, in planInput) (MappingPlan, error) {
		src, err := sourceFromInput(in.Format, in.Table, in.Data)
		if err != nil {
			return MappingPlan{}, err
		}
		defer src.Close()
		return im.Plan(ctx, src, Goal{BuildRAG: in.BuildRAG, BuildKG: in.BuildKG, Hint: in.Hint})
	})
	addImportflowMCPTool(server, definitions["importflow_run"], func(ctx context.Context, in runInput) (*Report, error) {
		src, err := sourceFromInput(in.Format, in.Table, in.Data)
		if err != nil {
			return nil, err
		}
		defer src.Close()
		return im.Run(ctx, src, in.Plan)
	})

	return server, nil
}

// RunMCPStdio runs the importflow MCP server over stdio until the context is done.
func RunMCPStdio(ctx context.Context, im *Importer, opts MCPServerOptions) error {
	server, err := NewMCPServer(im, opts)
	if err != nil {
		return err
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

func addImportflowMCPTool[In any, Out any](server *mcp.Server, definition cortexdb.ToolDefinition, handler func(context.Context, In) (Out, error)) {
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
