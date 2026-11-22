package cortexdb

import "github.com/modelcontextprotocol/go-sdk/mcp"

// MCPAnnotations is how a tool's effects are declared to an MCP client (#255):
// readOnlyHint when it does not write; for a write, idempotentHint and
// destructiveHint as the definition decides. A read is idempotent. Without
// annotations a client must assume every tool writes, destructively, and
// cannot resend a recall that a crash interrupted.
func (d ToolDefinition) MCPAnnotations() *mcp.ToolAnnotations {
	if !d.Mutates {
		return &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	}
	destructive := d.Destructive
	return &mcp.ToolAnnotations{IdempotentHint: d.Idempotent, DestructiveHint: &destructive}
}
