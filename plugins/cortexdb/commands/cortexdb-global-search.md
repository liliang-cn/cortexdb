---
description: Answer a whole-corpus question over the CortexDB brain using GraphRAG global search
---

Answer a **whole-corpus / thematic** question — the kind local retrieval can't ("what are the main themes?", "summarize everything I know about X", "what areas does this brain cover?") — using GraphRAG-style **global search**: detect entity communities in the graph, write an LLM report for each (once, then reused), and map-reduce those reports into an answer.

Requires an LLM — set `CORTEXDB_LLM_BASE_URL` (e.g. a local Ollama `http://localhost:11434`) and `CORTEXDB_LLM_MODEL` (e.g. `qwen3.5`). Run in a shell, putting the question in `Q`:

```bash
bin=$(ls -t ~/.claude/plugins/data/cortexdb-cortexdb/bin/cortexdb-mcp-* 2>/dev/null | head -1)
[ -n "$bin" ] || bin="${CORTEXDB_MCP_BIN:-cortexdb-mcp}"
Q="${ARGUMENTS:-What are the main themes in this knowledge base?}"
"$bin" --global-search "$Q"
```

The first run builds community summaries (this can take a while on a large brain — one LLM call per community); later runs reuse them. Then relay the printed answer to the user. If it reports zero communities, the brain has too few linked entities yet — suggest importing/organizing more (`/cortexdb-import-memory`, `/cortexdb-graph`, `knowledge_save` with entities/relations).

$ARGUMENTS
