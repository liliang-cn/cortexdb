// Command cortexdb is CortexDB's command line: every mode of
// cortexdb-mcp-stdio as a subcommand, and every MCP tool callable from a
// shell. Run `cortexdb --help`.
package main

import (
	"os"

	"github.com/liliang-cn/cortexdb/v2/internal/cli"
)

func main() {
	os.Exit(cli.Main(cli.ProgCortexDB, os.Args[1:]))
}
