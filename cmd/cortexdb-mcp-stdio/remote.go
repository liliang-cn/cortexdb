package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cortexdbroot "github.com/liliang-cn/cortexdb/v2"
	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// Shared-brain client mode.
//
// By default this binary opens a LOCAL SQLite file, which several processes on
// one machine can share safely (WAL). That does not extend across machines: a
// SQLite file on a network mount is a corruption hazard, not a shared brain.
//
// So for a brain shared by Claude Code, Codex, a Lima VM and an OpenClaw VM,
// exactly one host runs `cortexdb-grpc` over the real file, and every agent
// elsewhere runs this binary in REMOTE mode: it speaks MCP on stdio to its
// agent and forwards every tool call to that one server over gRPC.
//
//	CORTEXDB_REMOTE      host:port of cortexdb-grpc (enables remote mode)
//	CORTEXDB_GRPC_TOKEN  bearer token, must match the server's
//
// The tool surface is discovered at startup via ToolsService.ListTools and
// proxied generically through CallTool, so every current and future CortexDB
// tool is available remotely without per-tool code here.

// remoteDialTimeout bounds the initial connect + tool discovery.
const remoteDialTimeout = 15 * time.Second

// runRemoteMCPStdio serves MCP on stdio, backed by a remote CortexDB.
func runRemoteMCPStdio(ctx context.Context, addr, token string) error {
	conn, err := dialCortexDB(addr, token)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	client := rpcv1.NewToolsServiceClient(conn)

	discoverCtx, cancel := context.WithTimeout(ctx, remoteDialTimeout)
	defer cancel()
	list, err := client.ListTools(discoverCtx, &rpcv1.ListToolsRequest{})
	if err != nil {
		return fmt.Errorf("discover tools from %s: %w (is cortexdb-grpc running and is CORTEXDB_GRPC_TOKEN correct?)", addr, err)
	}
	if len(list.GetTools()) == 0 {
		return fmt.Errorf("remote %s exposed no tools", addr)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "cortexdb-graphrag",
		Title:   "CortexDB GraphRAG (shared brain)",
		Version: cortexdbroot.Version,
	}, &mcp.ServerOptions{
		Instructions: remoteMCPInstructions,
	})

	for _, def := range list.GetTools() {
		addRemoteTool(server, client, def)
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

// addRemoteTool registers one discovered remote tool, forwarding calls over
// gRPC. The server's own JSON schema is passed through unchanged so the agent
// sees exactly the same tool contract as in local mode.
func addRemoteTool(server *mcp.Server, client rpcv1.ToolsServiceClient, def *rpcv1.ToolDefinition) {
	name := def.GetName()
	tool := &mcp.Tool{
		Name:        name,
		Description: def.GetDescription(),
	}
	if schema := strings.TrimSpace(def.GetInputSchemaJson()); schema != "" {
		var parsed any
		if err := json.Unmarshal([]byte(schema), &parsed); err == nil {
			tool.InputSchema = parsed
		}
	}

	server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := "{}"
		if raw := req.Params.Arguments; len(raw) > 0 {
			args = string(raw)
		}
		resp, err := client.CallTool(ctx, &rpcv1.CallToolRequest{Name: name, ArgsJson: args})
		if err != nil {
			return nil, fmt.Errorf("remote tool %s: %w", name, err)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: resp.GetResultJson()}},
		}, nil
	})
}

// dialCortexDB opens a gRPC connection, attaching the bearer token to every
// call when one is configured.
//
// The connection is plaintext: it is meant to run over loopback or a private
// encrypted network (Tailscale), which is also the only place a shared brain
// should be reachable. The token is what stops other hosts on that network
// from reading the brain.
func dialCortexDB(addr, token string) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if token != "" {
		opts = append(opts, grpc.WithUnaryInterceptor(bearerInterceptor(token)))
	}
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("connect to cortexdb at %s: %w", addr, err)
	}
	return conn, nil
}

// bearerInterceptor attaches `authorization: Bearer <token>` to every call,
// matching the server's authInterceptor.
func bearerInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

const remoteMCPInstructions = "This CortexDB is a SHARED BRAIN: the same memory and knowledge graph is used by every agent and machine connected to it, so what you save here is visible to the others, and what you recall may have been written by them. " +
	"Prefer the high-level knowledge_memory_* tools for unified recall across episodic memory, durable knowledge, and graph expansion. Use knowledge_* and memory_* for direct control of those stores. " +
	"For search, send a structured plan with query, keywords, alternate_queries, entity_names, and retrieval_mode; expand the goal into many keywords, aliases, synonyms and multilingual variants first. " +
	"Supply entity_names when known so graph expansion can recover results even if lexical seeds are sparse."
