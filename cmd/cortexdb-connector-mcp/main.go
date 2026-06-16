// cortexdb-connector-mcp runs the data-connector tools (connector_introspect,
// connector_plan, connector_run, connector_unmask) as an MCP stdio server.
//
// It connects to live Postgres/MySQL sources on demand (DSN supplied per tool
// call), classifies + desensitizes per a human-signed MaskingPlan, and imports
// the result into a CortexDB knowledge file (RAG + knowledge graph). Reversible
// pseudonyms live in a SEPARATE token vault keyed per tenant; connector_unmask
// is the only reverse path.
//
// Environment:
//
//	CORTEXDB_PATH          knowledge DB file (default: cortexdb.db)
//	CONNECTOR_VAULT_PATH   token vault file  (default: cortexdb.vault.db)
//	CONNECTOR_TENANT       tenant id         (default: default)
//	CONNECTOR_KEY_FILE     path to a 32-byte (or 64-hex) key file, OR
//	CONNECTOR_KEY_ENV_PREFIX  env prefix for per-tenant keys (default: CONNECTOR_KEY_)
//
// Without a key source the un-mask/pseudonymize paths are unavailable; the
// introspect/plan tools (which never touch reversible data) still work.
package main

import (
	"context"
	"log"
	"os"

	"github.com/liliang-cn/cortexdb/v2/pkg/connector"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	dbPath := getenv("CORTEXDB_PATH", "cortexdb.db")
	vaultPath := getenv("CONNECTOR_VAULT_PATH", "cortexdb.vault.db")
	tenant := getenv("CONNECTOR_TENANT", "default")

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatalf("open cortexdb: %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close cortexdb: %v", closeErr)
		}
	}()

	vault, err := connector.OpenSQLiteVault(vaultPath)
	if err != nil {
		log.Fatalf("open vault: %v", err)
	}
	defer func() { _ = vault.Close() }()

	// Pick a key provider: a key file takes precedence; otherwise per-tenant env.
	var kp connector.KeyProvider
	if keyFile := os.Getenv("CONNECTOR_KEY_FILE"); keyFile != "" {
		kp = connector.FileKeyProvider{Path: keyFile}
	} else {
		kp = connector.EnvKeyProvider{Prefix: getenv("CONNECTOR_KEY_ENV_PREFIX", "CONNECTOR_KEY_")}
	}

	tb := connector.NewToolbox(db, connector.ToolboxOptions{
		Vault:       vault,
		KeyProvider: kp,
		Tenant:      tenant,
	})

	if err := connector.RunMCPStdio(context.Background(), tb, connector.MCPServerOptions{}); err != nil {
		log.Fatalf("run connector mcp stdio server: %v", err)
	}
}
