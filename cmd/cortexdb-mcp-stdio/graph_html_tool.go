package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// render_graph_html — the knowledge graph as a file an agent can hand to
// someone.
//
// This one tool is registered locally rather than proxied to the shared brain,
// which is the opposite of every other tool here and is the whole point: the
// caller needs the *file* on its own machine, so it can attach it to a message
// or open it. A server-side render would write the HTML on the brain's host,
// where the agent asking for it cannot reach it.
//
// The graph still comes from the shared brain over gRPC — only the rendering
// and the write are local.

// renderGraphHTMLIn is the tool's input.
type renderGraphHTMLIn struct {
	// OutDir defaults to a per-user view directory. Callers that need the file
	// somewhere specific — a directory a chat channel can attach from, or one
	// that survives a failover — pass it explicitly.
	OutDir string `json:"out_dir,omitempty"`
	// Organize runs LLM distillation over memories and knowledge before
	// rendering. It WRITES to the graph, so it is off by default and ignored
	// against a shared brain: a read-only view of a brain several machines
	// share should not mutate it from whichever one drew the picture.
	Organize bool `json:"organize,omitempty"`
}

// renderGraphHTMLOut reports where the file landed and how much it shows.
type renderGraphHTMLOut struct {
	Path   string `json:"path"`
	Nodes  int    `json:"nodes"`
	Edges  int    `json:"edges"`
	Source string `json:"source"`
	// Bytes lets a caller check the file against a channel's attachment limit
	// before trying to send it.
	Bytes int64 `json:"bytes"`
}

const renderGraphHTMLDescription = "Render the brain's knowledge graph to a self-contained, interactive HTML " +
	"file on THIS machine and return its path. Use it when asked to see, show, visualise or send the knowledge " +
	"graph. The file is standalone — no server, no assets — so it can be attached to a message or opened " +
	"directly. Nothing is written to the graph unless organize is set, and organize is ignored against a shared " +
	"brain. This is the only tool here that acts locally: the graph is read from the shared brain, but the file " +
	"is written where this server runs, which is what makes it reachable to whatever asked for it."

// addRenderGraphHTMLTool registers the renderer on an MCP server.
//
// Registered in both local and shared-brain mode, from one place, so the two
// cannot drift into offering different contracts.
func addRenderGraphHTMLTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "render_graph_html",
		Description: renderGraphHTMLDescription,
		Annotations: &mcp.ToolAnnotations{
			Title:        "Render knowledge graph to HTML",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in renderGraphHTMLIn) (*mcp.CallToolResult, renderGraphHTMLOut, error) {
		res, err := renderGraphHTML(ctx, strings.TrimSpace(in.OutDir), in.Organize)
		if err != nil {
			return nil, renderGraphHTMLOut{}, fmt.Errorf("render graph html: %w", err)
		}
		out := renderGraphHTMLOut{
			Path:   res.Path,
			Nodes:  res.Nodes,
			Edges:  res.Edges,
			Source: res.Source,
		}
		if st, serr := os.Stat(res.Path); serr == nil {
			out.Bytes = st.Size()
		}
		return nil, out, nil
	})
}

// graphViewDir is where renders land when a caller does not choose.
//
// Under a shared brain the default follows the brain, not the user's home:
// several machines run this server, the service that renders can move between
// them, and a path under a floating volume is the one that is still there
// afterwards. CORTEXDB_VIEW_DIR overrides it.
func graphViewDir() string {
	if dir := strings.TrimSpace(os.Getenv("CORTEXDB_VIEW_DIR")); dir != "" {
		return dir
	}
	return defaultViewDir("graph")
}
