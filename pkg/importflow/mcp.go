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
	// LocalFiles lets the tools read the file a call names under path; see
	// WithLocalFiles for when that is safe.
	LocalFiles bool
}

// NewMCPServer returns an MCP server that exposes the importflow tool surface
// (every tool of the Toolbox) backed by the given Importer.
func NewMCPServer(im *Importer, opts MCPServerOptions) (*mcp.Server, error) {
	if im == nil {
		return nil, fmt.Errorf("importflow: importer is required")
	}
	var toolboxOpts []ToolboxOption
	if opts.LocalFiles {
		toolboxOpts = append(toolboxOpts, WithLocalFiles())
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
	AddMCPTools(server, NewToolbox(im, toolboxOpts...))
	return server, nil
}

// AddMCPTools registers every tool of the toolbox on an MCP server. The
// cortexdb-mcp-stdio binary calls this beside the facade's tools, which
// cannot import this package; NewMCPServer calls it for the importflow
// surface. Each tool runs the code Toolbox.Call runs for it.
func AddMCPTools(server *mcp.Server, toolbox *Toolbox) {
	definitions := make(map[string]cortexdb.ToolDefinition)
	for _, definition := range toolbox.Definitions() {
		definitions[definition.Name] = definition
	}
	addImportflowMCPTool(server, definitions[planToolName], toolbox.plan)
	addImportflowMCPTool(server, definitions[runToolName], toolbox.run)
	addImportflowMCPTool(server, definitions[ddlPlanToolName], toolbox.ddlPlan)
	addImportflowMCPTool(server, definitions[ddlPlanAIToolName], toolbox.ddlPlanAI)
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
		Annotations: definition.MCPAnnotations(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		output, err := handler(ctx, input)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return &mcp.CallToolResult{}, output, nil
	})
}
