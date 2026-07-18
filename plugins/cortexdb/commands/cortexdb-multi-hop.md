---
description: Answer a complex, multi-step question over the CortexDB brain using iterative agentic retrieval
---

Answer a **complex, multi-step** question — the kind that needs chaining evidence across several lookups ("who leads the team that owns X?", "what does the tool my project depends on use under the hood?") — using **multi-hop retrieval**: retrieve → reason → retrieve. Each hop runs a GraphRAG search, accumulates the evidence, and the LLM decides whether it's enough or hands back a focused follow-up query to drive the next hop, until it can answer.

Requires an LLM — set `CORTEXDB_LLM_BASE_URL` (e.g. a local Ollama `http://localhost:11434`) and `CORTEXDB_LLM_MODEL` (e.g. `qwen3.5`). Run in a shell, putting the question in `Q`:

```bash
bin=$(ls -t ~/.claude/plugins/data/cortexdb-cortexdb/bin/cortexdb-mcp-* 2>/dev/null | head -1)
[ -n "$bin" ] || bin="${CORTEXDB_MCP_BIN:-cortexdb-mcp}"
Q="${ARGUMENTS:-}"
"$bin" --multi-hop "$Q"
```

The loop is bounded (a few hops) and prints its hop trace to stderr and the final answer to stdout. Relay the printed answer to the user. If the answer comes back empty, the brain has too little relevant content yet — suggest importing/organizing more (`/cortexdb-import-memory`, `/cortexdb-graph`, `knowledge_save`).

$ARGUMENTS
