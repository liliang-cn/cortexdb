// Command cortexdb-mcp-stdio is the MCP stdio server MCP clients launch with
// no arguments, and the --<mode> one-shots the plugins and hooks call. It
// takes the same commands as cortexdb.
package main

import (
	"os"

	"github.com/liliang-cn/cortexdb/v2/internal/cli"
)

func main() {
	os.Exit(cli.Main(cli.ProgStdio, os.Args[1:]))
}
