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

	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = ".cortexdb/cortexdb.db"
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
