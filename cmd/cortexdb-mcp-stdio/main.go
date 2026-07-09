package main

import (
	"context"
	"log"
	"os"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func main() {
	// `--recall` is a one-shot mode for the UserPromptSubmit hook: read a hook
	// payload from stdin, retrieve matching memories, print additionalContext,
	// and exit. Everything else launches the long-running MCP stdio server.
	if len(os.Args) > 1 && os.Args[1] == "--recall" {
		runRecall()
		return
	}

	// `--graph-html [out]` is a one-shot mode that renders the brain's knowledge
	// graph to a self-contained, interactive HTML file and prints its path. Used
	// by the /cortexdb-graph command to give the memory a viewable graph.
	if len(os.Args) > 1 && os.Args[1] == "--graph-html" {
		out := ""
		if len(os.Args) > 2 {
			out = os.Args[2]
		}
		runGraphHTML(out)
		return
	}

	// `--import-agent-memory [roots...]` imports Claude Code / Codex memory into
	// the brain. Used by /cortexdb-import-memory.
	if len(os.Args) > 1 && os.Args[1] == "--import-agent-memory" {
		runImportAgentMemory(os.Args[2:])
		return
	}

	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = cortexdb.DefaultDBPath()
	}

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatalf("open cortexdb: %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close cortexdb: %v", closeErr)
		}
	}()

	if err := db.RunMCPStdio(context.Background(), cortexdb.MCPServerOptions{}); err != nil {
		log.Fatalf("run mcp stdio server: %v", err)
	}
}
