package cortexdb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SweepExpiredToolRequest is the argument of sweep_expired.
type SweepExpiredToolRequest struct {
	DryRun bool `json:"dry_run,omitempty"`
}

// expiryToolDefinitions declares sweep_expired. The sweep is a tool, not
// only a subcommand, so that a client of a shared brain can run it where the
// brain is: cortexdb-mcp-stdio --sweep-expired calls it over gRPC when
// CORTEXDB_REMOTE is set.
func expiryToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "sweep_expired",
			Mutates:     true,
			Idempotent:  true,
			Destructive: true,
			Description: "Remove every document and memory whose expires_at has passed: the document with its chunks and graph " +
				"nodes, the memory with its node. Searches already leave expired rows out, so the sweep can run at any hour. " +
				"Run with dry_run first.",
			InputSchema: toolObjectSchema(
				nil,
				map[string]any{
					"dry_run": toolBooleanSchema("Report what would be removed without removing it."),
				},
			),
		},
	}
}

// SweepExpiredTool answers sweep_expired.
func (db *DB) SweepExpiredTool(ctx context.Context, req SweepExpiredToolRequest) (*ExpiredSweepReport, error) {
	return db.SweepExpired(ctx, time.Now(), req.DryRun)
}

// callExpiryTool dispatches sweep_expired from JSON, the shape
// callTemporalTool uses.
func (t *GraphRAGToolbox) callExpiryTool(ctx context.Context, name string, input json.RawMessage) (any, bool, error) {
	if name != "sweep_expired" {
		return nil, false, nil
	}
	var req SweepExpiredToolRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, true, fmt.Errorf("decode %s: %w", name, err)
	}
	resp, err := t.db.SweepExpiredTool(ctx, req)
	return resp, true, err
}

// addExpiryMCPTools registers sweep_expired on the MCP server, next to its
// definition, for the reason addTemporalMCPTools gives.
func addExpiryMCPTools(server *mcp.Server, definitions map[string]ToolDefinition, db *DB) {
	addGraphRAGMCPTool(server, definitions["sweep_expired"], func(ctx context.Context, req SweepExpiredToolRequest) (*ExpiredSweepReport, error) {
		return db.SweepExpiredTool(ctx, req)
	})
}
