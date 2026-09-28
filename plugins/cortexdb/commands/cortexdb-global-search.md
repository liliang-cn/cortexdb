---
description: Answer a whole-corpus question over the CortexDB brain using GraphRAG global search
---

Answer a **whole-corpus / thematic** question — the kind local retrieval can't ("what are the main themes?", "summarize everything I know about X", "what areas does this brain cover?") — using GraphRAG-style **global search**: detect entity communities in the graph, write an LLM report for each (once, then reused), and map-reduce those reports into an answer.

If the cortexdb MCP server is connected, call its `global_search` tool with the question (add `level` for a finer granularity: 0 is the finest; omitted, the coarsest level is used). Otherwise run in a shell, putting the question in `Q` (add `--level N` before it for a specific level):

```bash
bin=$(ls -t ~/.claude/plugins/data/cortexdb-cortexdb/bin/cortexdb-mcp-* 2>/dev/null | head -1)
[ -n "$bin" ] || bin="${CORTEXDB_MCP_BIN:-cortexdb-mcp}"
Q="${ARGUMENTS:-What are the main themes in this knowledge base?}"
"$bin" --global-search "$Q"
```

With an LLM configured (`CORTEXDB_LLM_BASE_URL`, e.g. a local Ollama `http://localhost:11434`, and `CORTEXDB_LLM_MODEL`) the community reports and the answer are the model's. Without one it still runs: reports are assembled deterministically and the most relevant are printed without a synthesised answer — say so to the user rather than presenting them as an answer.

The first run builds the community hierarchy (one LLM call per community when a model is set — slow on a large brain); later runs reuse it. `build_community_hierarchy` rebuilds it after the graph has changed substantially. Then relay the answer to the user. If it reports zero communities, the brain has too few linked entities yet — suggest importing/organizing more (`/cortexdb-import-memory`, `/cortexdb-graph`, `knowledge_save` with entities/relations).

$ARGUMENTS
