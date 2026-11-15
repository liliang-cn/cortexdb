package rpcserver

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/grpc/status"

	"github.com/liliang-cn/cortexdb/v2/pkg/authz"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
)

// Tool is a tool the server offers beside the facade's toolbox (Options.
// ExtraTools).
//
// The toolbox holds what pkg/cortexdb defines. The workflow packages built on
// the facade (importflow, graphflow) define tools of their own, which the
// facade cannot import and so cannot list; a local MCP server registers them
// next to the facade's, and without this a brain served over gRPC had no way
// to offer them at all. A Tool is listed by ListTools and called by CallTool
// exactly as a toolbox tool is, so the MCP bridge of a remote client needs no
// code for it.
type Tool struct {
	// Definition is what ListTools returns. Mutates is the tool's
	// authorization class, as it is for a toolbox tool: a tool that sets it
	// needs a read-write key.
	Definition cortexdb.ToolDefinition
	// Call runs the tool on the arguments as the caller sent them ("{}" when
	// it sent none). The context carries the calling key, so what the tool
	// writes through the facade is signed as any other write is. An error
	// that carries a gRPC status is returned as it is; any other is mapped
	// the way a facade error is.
	Call func(ctx context.Context, args json.RawMessage) (any, error)
	// ConfinedRefusal is why a key confined to a collection or to a memory
	// bucket may not call the tool, worded to follow "it " ("writes rows no
	// scope field confines"). Every extra tool is refused to a confined key:
	// the allowlist that checks a toolbox tool's arguments against a scope
	// has no line for it. Empty gives the allowlist's default reason.
	ConfinedRefusal string
}

// extraTools is Options.ExtraTools, checked and indexed.
type extraTools struct {
	order  []string
	byName map[string]Tool
}

// newExtraTools indexes the tools that can be served and reports the first
// that cannot: one with no name or no handler, one named twice, and one that
// takes the name of a toolbox tool, which would be listed twice and
// authorized as the toolbox's.
func newExtraTools(tools []Tool) (*extraTools, error) {
	out := &extraTools{byName: make(map[string]Tool, len(tools))}
	var first error
	reject := func(err error) {
		if first == nil {
			first = err
		}
	}
	for _, tool := range tools {
		name := tool.Definition.Name
		switch {
		case name == "":
			reject(fmt.Errorf("rpcserver: an extra tool has no name"))
		case tool.Call == nil:
			reject(fmt.Errorf("rpcserver: extra tool %s has no handler", name))
		case out.has(name):
			reject(fmt.Errorf("rpcserver: extra tool %s is registered twice", name))
		default:
			if _, core := authz.LookupTool(name); core {
				reject(fmt.Errorf("rpcserver: extra tool %s takes the name of a toolbox tool", name))
				continue
			}
			out.order = append(out.order, name)
			out.byName[name] = tool
		}
	}
	return out, first
}

func (e *extraTools) has(name string) bool {
	if e == nil {
		return false
	}
	_, ok := e.byName[name]
	return ok
}

func (e *extraTools) get(name string) (Tool, bool) {
	if e == nil {
		return Tool{}, false
	}
	tool, ok := e.byName[name]
	return tool, ok
}

func (e *extraTools) definitions() []cortexdb.ToolDefinition {
	if e == nil {
		return nil
	}
	out := make([]cortexdb.ToolDefinition, 0, len(e.order))
	for _, name := range e.order {
		out = append(out, e.byName[name].Definition)
	}
	return out
}

// policy is the authorization line of the extra tool a CallTool names; ok is
// false for every other request, which the toolbox's policy decides.
func (e *extraTools) policy(fullMethod string, req any) (authz.ExtensionTool, bool) {
	if e == nil || fullMethod != authz.CallToolMethod {
		return authz.ExtensionTool{}, false
	}
	call, ok := req.(*rpcv1.CallToolRequest)
	if !ok {
		return authz.ExtensionTool{}, false
	}
	tool, ok := e.byName[call.GetName()]
	if !ok {
		return authz.ExtensionTool{}, false
	}
	access := authz.Read
	if tool.Definition.Mutates {
		access = authz.Write
	}
	return authz.ExtensionTool{Name: tool.Definition.Name, Access: access, Refusal: tool.ConfinedRefusal}, true
}

// extraToolStatus maps an extra tool's error: its own status when it chose
// one, a facade error's otherwise.
func extraToolStatus(err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}
	return toStatus(err)
}
