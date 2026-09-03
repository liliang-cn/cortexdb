package authz

import (
	"fmt"
	"sort"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// CallToolMethod is the generic tool entry point, and the one RPC in the table
// whose classification cannot be decided from its name alone: it dispatches on
// a tool name carried in the request, and the toolbox behind it holds both
// reads and writes.
const CallToolMethod = "/cortexdb.v1.ToolsService/CallTool"

// toolAccess is every tool the toolbox defines, mapped to what calling it does.
//
// It is derived from the tool definitions rather than written out here. A hand-
// kept list beside the policy would have to be synced with the definitions by
// hand, and the way that drifts is a newly added write sitting in nobody's list
// and being treated as a read. Deriving it means a tool that never declares
// anything is at least a tool the completeness test in pkg/cortexdb refuses to
// let exist.
var toolAccess = buildToolAccess()

func buildToolAccess() map[string]Access {
	defs := cortexdb.ToolDefinitions()
	out := make(map[string]Access, len(defs))
	for _, d := range defs {
		access := Read
		if d.Mutates {
			access = Write
		}
		out[d.Name] = access
	}
	return out
}

// LookupTool returns what calling the named tool does. The second result is
// false for a name the toolbox does not define.
func LookupTool(name string) (Access, bool) {
	a, ok := toolAccess[name]
	return a, ok
}

// ClassifiedTools lists every tool name the policy knows, sorted. Tests use it
// to compare the policy's view of the toolbox against the toolbox itself.
func ClassifiedTools() []string {
	out := make([]string, 0, len(toolAccess))
	for name := range toolAccess {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ToolNameLookup reports the tool a CallTool request names. The second result
// is false when the name could not be read at all — a request that is not the
// expected message, or one whose name field is missing.
type ToolNameLookup func() (string, bool)

// AuthorizeCall is AuthorizeMethod with the one refinement CallTool needs.
//
// Every other RPC is decided by the method table alone. CallTool is decided by
// the Mutates flag of the tool it names, because the table can only say one
// thing about a method that reaches the whole toolbox, and that one thing has
// to be "write" — which left a read-only key unable to call search. The MCP
// shared-brain client proxies every tool call through here, so that made
// read-only clearance useless for the client path it matters most on.
//
// Three ways this refuses, all of them closed:
//
//   - the name cannot be read: an authorization decision that cannot see what
//     it is deciding about is not a decision;
//   - the name is not a tool: allowing it would mean trusting that the handler
//     really does reject it a moment later, and the handler is not the policy;
//   - the tool writes and the key does not.
func (k Key) AuthorizeCall(fullMethod string, tool ToolNameLookup) error {
	if fullMethod != CallToolMethod {
		return k.AuthorizeMethod(fullMethod)
	}
	if tool == nil {
		return fmt.Errorf("%w: key %q called %s with a request the tool name could not be read from",
			ErrDenied, k.ID, fullMethod)
	}
	name, ok := tool()
	if !ok || name == "" {
		return fmt.Errorf("%w: key %q called %s without naming a tool", ErrDenied, k.ID, fullMethod)
	}
	access, known := LookupTool(name)
	if !known {
		return fmt.Errorf("%w: %s is not a tool this server defines", ErrDenied, name)
	}
	if access == Write && k.Clearance != ReadWrite {
		return fmt.Errorf("%w: key %q is %s and tool %s writes", ErrDenied, k.ID, k.Clearance, name)
	}
	return nil
}

// confinedCallToolDenial is why a confined key cannot use CallTool at all.
//
// The row checks work by reading scope fields off the request message. A
// CallTool request carries a tool name and a string of JSON: protoreflect sees
// a string, not the user_id or collection inside it, so there is nothing for a
// confinement to be compared against. Every tool in the toolbox is reachable
// through this one RPC, which makes it the worst possible place to let a scope
// quietly lapse — a key confined to one user could ask memory_search for
// everybody's. Parsing the arguments per tool would mean a second copy of every
// request shape living in the policy and going stale; refusing is honest and
// says so in the error.
func confinedCallToolDenial(k Key) error {
	c := k.Scope.constraints()[0]
	return fmt.Errorf("%w: key %q is confined to %s=%q and %s carries its arguments as opaque JSON, "+
		"which no scope can be checked against — use the typed RPCs instead",
		ErrDenied, k.ID, c.field, c.value, CallToolMethod)
}
