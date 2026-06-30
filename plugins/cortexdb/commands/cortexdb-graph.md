---
description: Organize the CortexDB brain into a knowledge graph and open an interactive view
---

First **organize** the brain — extract entities and co-occurrence relations from stored memories and knowledge into the graph (deterministic, no LLM) — then render it to an interactive HTML page and open it. So free-text memories that were never tagged still show up as a navigable entity graph. Run this in a shell:

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
