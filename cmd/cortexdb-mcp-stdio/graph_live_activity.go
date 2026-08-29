package main

import (
	"context"
	"github.com/liliang-cn/cortexdb/v2/pkg/liveview"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// liveActivityMiddleware reports every handled tool call to the live view.
//
// Receiving middleware is the one place both modes meet. Local mode registers
// its tools from the library and shared-brain mode forwards them over gRPC, so
// there is no shared handler to wrap — but both build an *mcp.Server, and every
// call arrives through its method handler. Hooking there means the view sees
// the same set of calls in either mode, and neither the library's tool surface
// nor the gRPC proxy has to know a view exists.
//
// It runs after the call, not before: an event says what the brain did, and
// whether it worked is part of that.
func liveActivityMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			if method != "tools/call" {
				return res, err
			}
			// No view open means nothing to tell. Checked per call rather than
			// installed conditionally, because the view is started later, by a
			// tool call arriving on this very handler.
			sv := liveview.Current()
			if sv == nil {
				return res, err
			}
			call, ok := req.(*mcp.CallToolRequest)
			if !ok || call.Params == nil {
				return res, err
			}
			failed := err != nil
			if out, ok := res.(*mcp.CallToolResult); ok && out != nil && out.IsError {
				failed = true
			}
			if ev, want := liveview.ClassifyToolCall(call.Params.Name, call.Params.Arguments, failed); want {
				sv.Observe(ev)
			}
			return res, err
		}
	}
}
