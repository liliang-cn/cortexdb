---
description: Build and open an interactive knowledge-graph view of the CortexDB brain
---

Render the CortexDB brain's knowledge graph (entities, relations, documents) to an interactive HTML page and open it in the browser. Run this in a shell:

```bash
bin=$(ls -t ~/.claude/plugins/data/cortexdb-cortexdb/bin/cortexdb-mcp-* 2>/dev/null | head -1)
[ -n "$bin" ] || bin="${CORTEXDB_MCP_BIN:-cortexdb-mcp}"
html=$("$bin" --graph-html)        # exports to ~/.cortexdb/graph/ and prints the path
echo "$html"
# Open it (macOS: open, Linux: xdg-open)
( command -v open >/dev/null && open "$html" ) || ( command -v xdg-open >/dev/null && xdg-open "$html" ) || true
```

Then tell the user where the HTML was written and that it is now open. If the graph looks empty, explain that the brain has few entity/relation links yet — they grow as knowledge is saved with entities/relations (via `knowledge_save`) or ingested through GraphRAG.

$ARGUMENTS
