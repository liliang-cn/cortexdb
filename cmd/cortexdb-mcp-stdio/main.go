package main

import (
	"context"
	"log"
	"os"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func main() {
	dbPath := os.Getenv("CORTEXDB_PATH")
	if dbPath == "" {
		dbPath = "cortexdb.db"
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
