---
description: Render all CortexDB memories to an interactive HTML dashboard and open it
---

Visualize the **memory records themselves** (not the entity graph) as a self-contained, interactive HTML dashboard: cards grouped by scope (global / user / session), newest first, each showing the content, importance, creation time, and expiry, with a live search box at the top. Run this in a shell:

```bash
bin=$(ls -t ~/.claude/plugins/data/cortexdb-cortexdb/bin/cortexdb-mcp-* 2>/dev/null | head -1)
[ -n "$bin" ] || bin="${CORTEXDB_MCP_BIN:-cortexdb-mcp}"
html=$("$bin" --memory-html)        # writes to ~/.cortexdb/memory-view/ and prints the path
echo "$html"
( command -v open >/dev/null && open "$html" ) || ( command -v xdg-open >/dev/null && xdg-open "$html" ) || true
```

Then tell the user where the HTML was written and that it is now open. Pass a directory as an argument to write elsewhere. This is complementary to `/cortexdb-graph` (which visualizes the entity **graph** derived from memory) and `/cortexdb-export-memory` (which exports memories as Markdown files).

$ARGUMENTS
