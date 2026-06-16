package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultMCPServerName  = "cortexdb-connector"
	defaultMCPServerTitle = "CortexDB Data Connector"
)

// MCPServerOptions configures the connector MCP server wrapper.
type MCPServerOptions struct {
	Implementation *mcp.Implementation
	Instructions   string
	Logger         *slog.Logger
}

// sourceInput is the schema for connector_introspect / connector_plan.
type sourceInput struct {
	Driver     string `json:"driver"`
	DSN        string `json:"dsn"`
	Schema     string `json:"schema,omitempty"`
	SampleSize int    `json:"sample_size,omitempty"`
}

// runInput is the schema for connector_run: a live source + a SIGNED MaskingPlan
// + an importflow MappingPlan (which may route to RAG and/or the knowledge graph).
type runInput struct {
	Driver      string                 `json:"driver"`
	DSN         string                 `json:"dsn"`
	Schema      string                 `json:"schema,omitempty"`
	MaskingPlan MaskingPlan            `json:"masking_plan"`
	MappingPlan importflow.MappingPlan `json:"mapping_plan"`
}

// unmaskInput is the schema for connector_unmask.
type unmaskInput struct {
	Tokens []string `json:"tokens"`
}

// RegisterMCPTools adds the connector_* tools (introspect, plan, run, unmask)
// onto an existing MCP server, so the connector can "ride" another surface — e.g.
// register it onto the server returned by importflow.NewMCPServer to expose the
// import and desensitization tools together. Dispatch goes through Toolbox.Call,
// the single tested code path.
func RegisterMCPTools(server *mcp.Server, tb *Toolbox) {
	addConnectorMCPTool[sourceInput](server, tb, "connector_introspect")
	addConnectorMCPTool[sourceInput](server, tb, "connector_plan")
	addConnectorMCPTool[runInput](server, tb, "connector_run")
	addConnectorMCPTool[unmaskInput](server, tb, "connector_unmask")
}

// NewMCPServer returns a standalone MCP server exposing only the connector tools.
func NewMCPServer(tb *Toolbox, opts MCPServerOptions) (*mcp.Server, error) {
	if tb == nil {
		return nil, fmt.Errorf("connector: toolbox is required")
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
	RegisterMCPTools(server, tb)
	return server, nil
}

// RunMCPStdio runs the connector MCP server over stdio until the context is done.
func RunMCPStdio(ctx context.Context, tb *Toolbox, opts MCPServerOptions) error {
	server, err := NewMCPServer(tb, opts)
	if err != nil {
		return err
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

// addConnectorMCPTool wires one connector tool. The typed In gives MCP clients a
// concrete input schema; the handler re-marshals it and dispatches through
// Toolbox.Call so there is exactly one place that opens sources, builds plans,
// runs the desensitized import, and un-masks.
func addConnectorMCPTool[In any](server *mcp.Server, tb *Toolbox, name string) {
	def, ok := lookupDefinition(tb, name)
	if !ok {
		return
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        def.Name,
		Description: def.Description,
		InputSchema: def.InputSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input In) (*mcp.CallToolResult, any, error) {
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, nil, err
		}
		out, err := tb.Call(ctx, name, json.RawMessage(raw))
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})
}

func lookupDefinition(tb *Toolbox, name string) (def toolDef, ok bool) {
	for _, d := range tb.Definitions() {
		if d.Name == name {
			return toolDef{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema}, true
		}
	}
	return toolDef{}, false
}

type toolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
}
