package cortexdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/liliang-cn/cortexdb/v2/pkg/observability"
)

// MCPRequestIDMetaKey is where a tool result's _meta carries the call's
// request id: the id the server's trace record has, so a client that cannot
// read a reply can name the record of it.
const MCPRequestIDMetaKey = "cortexdb/request_id"

// LocalClientInfo is what this process says about where its calls come
// from: the short host name, and the project — CORTEXDB_PROJECT, or the
// name of the working directory, which is the project an agent runs in.
func LocalClientInfo() observability.ClientInfo {
	var info observability.ClientInfo
	if host, err := os.Hostname(); err == nil {
		info.Host, _, _ = strings.Cut(host, ".")
	}
	if p := strings.TrimSpace(os.Getenv("CORTEXDB_PROJECT")); p != "" {
		info.Project = p
	} else if wd, err := os.Getwd(); err == nil {
		if base := filepath.Base(wd); base != "/" && base != "." {
			info.Project = base
		}
	}
	return info
}

// MCPCallMiddleware gives every tool call a request id and the client's
// description of itself (observability.Call), returns the id in the
// result's _meta, and names it in an error. Below it the id travels with
// the call: into the trace of a local brain, and over gRPC to a shared one,
// which adopts it (rpc.CallInterceptor).
func MCPCallMiddleware(client observability.ClientInfo) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			ctx, call := observability.WithCall(ctx, "")
			info := client
			if ss, ok := req.GetSession().(*mcp.ServerSession); ok && ss != nil {
				if p := ss.InitializeParams(); p != nil && p.ClientInfo != nil && info.Agent == "" {
					info.Agent = p.ClientInfo.Name
				}
			}
			call.SetClient(info)
			res, err := next(ctx, method, req)
			if err != nil {
				return res, fmt.Errorf("%w (request_id %s)", err, call.ID())
			}
			if out, ok := res.(*mcp.CallToolResult); ok && out != nil {
				if out.Meta == nil {
					out.Meta = mcp.Meta{}
				}
				out.Meta[MCPRequestIDMetaKey] = call.ID()
				// A failed tool's text is what a host shows, and hosts do
				// not all show _meta.
				if out.IsError {
					for _, c := range out.Content {
						if t, ok := c.(*mcp.TextContent); ok {
							t.Text += " (request_id " + call.ID() + ")"
							break
						}
					}
				}
			}
			return res, err
		}
	}
}
