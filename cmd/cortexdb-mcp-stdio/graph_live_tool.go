package main

import (
	"context"
	"fmt"
	"github.com/liliang-cn/cortexdb/v2/pkg/liveview"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serve_graph_3d — the brain as something you watch rather than something you
// re-render.
//
// Unlike render_graph_html, which writes a file and is finished, this holds a
// server open inside the MCP process for as long as that process lives. That
// placement is the feature: this is the process the agent's tool calls go
// through, so the page can light up on the same call the agent just made
// instead of waiting to find the result in a database poll.

type serveGraph3DIn struct {
	// Graph opens the view onto a named side graph instead of the brain. Each
	// graph gets its own view on its own port, so a run can be watched beside
	// the knowledge it will eventually contribute to.
	Graph string `json:"graph,omitempty"`

	// Open launches the browser. On by default: the reason to ask for a live
	// view is to look at it, and returning a URL nobody opens is a slower way
	// of doing nothing.
	Open *bool `json:"open,omitempty"`
}

type serveGraph3DOut struct {
	Graph  string `json:"graph,omitempty"`
	URL    string `json:"url"`
	Nodes  int    `json:"nodes"`
	Edges  int    `json:"edges"`
	Source string `json:"source"`
	// Activity reports whether tool calls are being watched, which is what
	// separates this from a page that only redraws when the graph changes.
	Activity bool   `json:"activity"`
	Opened   bool   `json:"opened"`
	Note     string `json:"note,omitempty"`
}

const serveGraph3DDescription = "Open a LIVE 3D view of the brain's knowledge graph in the browser and return " +
	"its URL. Use it when asked to watch, monitor or see the graph live, or for a 3D/rotating/glowing view. " +
	"Unlike render_graph_html, which writes a static file, this serves a page on 127.0.0.1 that updates by " +
	"itself: nodes and relations appear as they are written, and every query, save and relation this server " +
	"handles lights up the nodes it touched. Calling it again returns the same URL rather than starting a " +
	"second view. Read-only — it never writes to the graph."

// addServeGraph3DTool registers the live view on an MCP server and installs the
// middleware that feeds it. Both happen here, together, because a view without
// the middleware silently shows structure only — the failure would look like a
// working feature.
func addServeGraph3DTool(server *mcp.Server) {
	server.AddReceivingMiddleware(liveActivityMiddleware())

	mcp.AddTool(server, &mcp.Tool{
		Name:        "serve_graph_3d",
		Description: serveGraph3DDescription,
		Annotations: &mcp.ToolAnnotations{
			Title:        "Open the live 3D knowledge graph",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in serveGraph3DIn) (*mcp.CallToolResult, serveGraph3DOut, error) {
		name := strings.TrimSpace(in.Graph)
		openSource := liveview.OpenSource
		if name != "" {
			if verr := validSideGraphName(name); verr != nil {
				return nil, serveGraph3DOut{}, verr
			}
			openSource = func(ctx context.Context) (*liveview.Source, error) { return sideGraphs_.source(ctx, name) }
		}
		// A side graph's view watches calls too: side_graph_write reports to it
		// directly, so a trace lights up as it is written.
		sv, err := liveview.SharedFor(ctx, name, true, openSource)
		if err != nil {
			return nil, serveGraph3DOut{}, fmt.Errorf("serve live graph: %w", err)
		}
		snap := sv.Snapshot()
		out := serveGraph3DOut{
			URL:      sv.URL(),
			Nodes:    len(snap.Nodes),
			Edges:    len(snap.Edges),
			Source:   sv.SourceName(),
			Activity: sv.WatchesCalls(),
			Graph:    name,
		}
		open := in.Open == nil || *in.Open
		if open {
			if oerr := liveview.OpenInBrowser(sv.URL()); oerr != nil {
				out.Note = "could not open a browser (" + oerr.Error() + "); open the URL yourself"
			} else {
				out.Opened = true
			}
		}
		if out.Nodes == 0 && name != "" {
			out.Note = "this side graph is empty — write to it with side_graph_write"
		} else if out.Nodes == 0 {
			out.Note = "the graph is empty — it fills in as knowledge is saved with entities and relations"
		}
		return nil, out, nil
	})
}
