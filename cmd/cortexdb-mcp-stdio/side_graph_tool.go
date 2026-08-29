package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/liliang-cn/cortexdb/v2/internal/liveview"
)

// The side-graph tools.
//
// Registered locally in both modes, like the renderers, and never proxied. That
// is the design, not an implementation detail: a side graph is scratch work for
// this machine, and forwarding it would put one agent's working notes on the
// database everyone else reads.
//
// The tool descriptions say what the tools do and stay silent on what to put in
// them. An agent that wants to record steps, a plan, a hypothesis or a call
// graph should be able to read these and see a place to put it, without this
// server having decided in advance which of those is legitimate.

const sideGraphWriteDescription = "Write nodes and edges into a NAMED graph that is separate from the brain, " +
	"kept in its own local database. Use it for anything that is a graph but is not shared knowledge — a run's " +
	"steps and what each was expected to do, a plan, a dependency chase, any scratch structure you want to " +
	"traverse later or watch in 3D. Nodes are matched by name, so writing the same name twice updates rather " +
	"than duplicates, and edges may use any type including 'next' for sequences. Nothing here reaches the " +
	"shared brain; promote what turns out to be durable with knowledge_save instead."

const sideGraphReadDescription = "Read back a named side graph: its nodes and the edges between them. Use it to " +
	"walk what was recorded — what followed what, what an expectation was attached to — rather than to search " +
	"by similarity."

const sideGraphListDescription = "List the named side graphs that exist on this machine."

const sideGraphDropDescription = "Delete a named side graph and everything in it. Side graphs are meant to be " +
	"disposable; dropping one that does not exist is not an error."

// addSideGraphTools registers the side-graph surface.
func addSideGraphTools(server *mcp.Server, reg *sideGraphs) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "side_graph_write",
		Description: sideGraphWriteDescription,
		Annotations: &mcp.ToolAnnotations{Title: "Write to a side graph"},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sideGraphWriteIn) (*mcp.CallToolResult, sideGraphWriteOut, error) {
		name := strings.TrimSpace(in.Graph)
		if name == "" {
			return nil, sideGraphWriteOut{}, fmt.Errorf("graph is required: name the graph to write into")
		}
		out, err := reg.write(ctx, name, in)
		if err != nil {
			return nil, sideGraphWriteOut{}, err
		}
		// If someone is watching this graph, the write should show on the page
		// the moment it lands rather than on the next poll.
		if sv := liveview.CurrentFor(name); sv != nil {
			if ev, want := liveview.ClassifyToolCall("side_graph_write", nil, false); want {
				ev.Terms, ev.Links = sideGraphHighlights(in)
				sv.Observe(ev)
			}
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "side_graph_read",
		Description: sideGraphReadDescription,
		Annotations: &mcp.ToolAnnotations{Title: "Read a side graph", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Graph string `json:"graph"`
	}) (*mcp.CallToolResult, sideGraphReadOut, error) {
		name := strings.TrimSpace(in.Graph)
		if name == "" {
			return nil, sideGraphReadOut{}, fmt.Errorf("graph is required: name the graph to read")
		}
		out, err := reg.read(ctx, name)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "side_graph_list",
		Description: sideGraphListDescription,
		Annotations: &mcp.ToolAnnotations{Title: "List side graphs", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct {
		Graphs []string `json:"graphs"`
	}, error) {
		names, err := reg.list()
		return nil, struct {
			Graphs []string `json:"graphs"`
		}{Graphs: names}, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "side_graph_drop",
		Description: sideGraphDropDescription,
		Annotations: &mcp.ToolAnnotations{Title: "Delete a side graph", DestructiveHint: boolPtr(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Graph string `json:"graph"`
	}) (*mcp.CallToolResult, struct {
		Dropped string `json:"dropped"`
	}, error) {
		name := strings.TrimSpace(in.Graph)
		if name == "" {
			return nil, struct {
				Dropped string `json:"dropped"`
			}{}, fmt.Errorf("graph is required: name the graph to drop")
		}
		err := reg.drop(name)
		return nil, struct {
			Dropped string `json:"dropped"`
		}{Dropped: name}, err
	})
}

func boolPtr(b bool) *bool { return &b }

// sideGraphHighlights turns a write into the things the page should light up.
func sideGraphHighlights(in sideGraphWriteIn) ([]string, [][2]string) {
	terms := make([]string, 0, len(in.Nodes))
	for _, n := range in.Nodes {
		if n.Name != "" {
			terms = append(terms, n.Name)
		}
	}
	links := make([][2]string, 0, len(in.Edges))
	for _, e := range in.Edges {
		if e.From != "" && e.To != "" {
			links = append(links, [2]string{e.From, e.To})
		}
	}
	return terms, links
}

// sideGraphRoot is where side graphs live: beside the brain when there is one
// locally, and under the user's home when the brain is shared and there is no
// local database to sit next to.
func sideGraphRoot() string {
	if path := strings.TrimSpace(os.Getenv("CORTEXDB_PATH")); path != "" {
		return filepath.Dir(path)
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".cortexdb")
	}
	return "."
}
