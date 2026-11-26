// Package cli is the command line of CortexDB: the cortexdb binary and
// cortexdb-mcp-stdio, which share every command and differ only in what no
// arguments means.
package cli

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// serve runs the MCP stdio server: on a shared brain when CORTEXDB_REMOTE
// is set, on the local file otherwise. It is what cortexdb-mcp-stdio does
// with no arguments, the way every MCP client launches it.
func serve() int {
	// Shared-brain client mode: with CORTEXDB_REMOTE set, serve MCP on stdio but
	// forward every tool call to one central cortexdb-grpc instead of opening a
	// local file — so several machines share a single brain. Checked before the
	// local open, since in this mode there is no local database at all.
	if remote := strings.TrimSpace(os.Getenv("CORTEXDB_REMOTE")); remote != "" {
		if err := runRemoteMCPStdio(context.Background(), remote, os.Getenv("CORTEXDB_GRPC_TOKEN")); err != nil {
			log.Fatalf("run remote mcp stdio server: %v", err)
		}
		return exitOK
	}

	dbPath := localDBPath()
	db, err := openBrainDB(dbPath)
	if err != nil {
		log.Fatalf("open cortexdb: %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close cortexdb: %v", closeErr)
		}
	}()

	server := newLocalMCPServer(db, dbPath)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("run mcp stdio server: %v", err)
	}
	return exitOK
}

// localDBPath is the brain file local mode opens.
func localDBPath() string {
	if p := os.Getenv("CORTEXDB_PATH"); p != "" {
		return p
	}
	return cortexdb.DefaultDBPath()
}

// newLocalMCPServer is the MCP server of local mode: the facade's tools and
// the ones that live beside it.
//
// Built rather than run directly so the local renderer can be registered
// alongside the facade's tools — the same surface the shared-brain path
// offers, so an agent sees one contract in both modes.
func newLocalMCPServer(db *cortexdb.DB, dbPath string) *mcp.Server {
	server := db.NewMCPServer(cortexdb.MCPServerOptions{})
	addRenderGraphHTMLTool(server)
	addServeGraph3DTool(server)
	addSideGraphTools(server, sideGraphs_)
	addImportAgentMemoryTool(server, localBrain{db}, dbPath)
	llm := newOrganizeLLM()
	// global_search / build_community_hierarchy live in graphflow, which the
	// facade cannot import, so they are registered here. The model is the
	// same CORTEXDB_LLM_* one the CLI modes use; without it both tools run
	// in their no-model mode rather than disappearing.
	graphflow.AddGlobalSearchMCPTools(server, db, llm)
	// importflow sits above the facade too: importflow_plan, importflow_run
	// and the two DDL planners, which are how an imported table (the
	// imported_tables_* tools) comes to exist. Without a model the planners
	// answer in their no-model mode. This process is the caller's own, so a
	// call may name a file on this machine; a shared brain serves the same
	// tools (cmd/cortexdb-grpc) and takes the text inline only.
	importflow.AddMCPTools(server, importflow.NewToolbox(importflow.New(db, importflow.WithLLM(llm)), importflow.WithLocalFiles()))
	return server
}
